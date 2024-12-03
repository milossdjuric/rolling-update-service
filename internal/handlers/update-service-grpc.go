package handlers

import (
	"context"
	"fmt"
	"log"
	"time"

	magnetarapi "github.com/c12s/magnetar/pkg/api"
	"github.com/docker/docker/client"
	"github.com/milossdjuric/rolling_update_service/internal/domain"
	"github.com/milossdjuric/rolling_update_service/internal/mappers/proto"
	"github.com/milossdjuric/rolling_update_service/internal/utils"
	"github.com/milossdjuric/rolling_update_service/internal/worker"
	"github.com/milossdjuric/rolling_update_service/pkg/api"
	"github.com/nats-io/nats.go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type UpdateServiceGrpcHandler struct {
	api.UnimplementedUpdateServiceServer
	deploymentRepo domain.DeploymentRepo
	revisionRepo   domain.RevisionRepo
	natsConn       *nats.Conn
	workerMap      *worker.WorkerMap
	dockerClient   *client.Client
	magnetar       magnetarapi.MagnetarClient
	rateLimiter    *LeakyBucketRateLimiter
}

func NewUpdateServiceGrpcHandler(deploymentRepo domain.DeploymentRepo, revisionRepo domain.RevisionRepo, natsConn *nats.Conn, dockerClient *client.Client, magnetarClient magnetarapi.MagnetarClient, rateLimiter *LeakyBucketRateLimiter) api.UpdateServiceServer {
	return &UpdateServiceGrpcHandler{
		deploymentRepo: deploymentRepo,
		revisionRepo:   revisionRepo,
		natsConn:       natsConn,
		workerMap:      worker.NewWorkerMap(),
		dockerClient:   dockerClient,
		magnetar:       magnetarClient,
		rateLimiter:    rateLimiter,
	}
}

func (u UpdateServiceGrpcHandler) AddDeployment(ctx context.Context, req *api.AddDeploymentReq) (*api.AddDeploymentResp, error) {
	// get the deployment from the request
	deployment, err := proto.AddDeploymentReqToDomain(req)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// get the new revision, if it exists
	newRevision, _, err := u.GetNewAndOldRevisions(&deployment)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	err = u.revisionRepo.Put(*newRevision)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	// check if deployment already exists in etcd, if it does, assign existing status, still change spec and labels
	existingDeployment, err := u.deploymentRepo.Get(deployment.Name, deployment.Namespace, deployment.OrgId)
	if err == nil && existingDeployment != nil {
		deployment.Status = domain.SetDeploymentStatus(existingDeployment.Status)
		// update the existing deployment states update timestamp
		// deployment.Status.States[domain.DeploymentAvailable] = domain.NewDeploymentState(domain.DeploymentAvailable, deployment.Status.States[domain.DeploymentAvailable].Active,
		// 	deployment.Status.States[domain.DeploymentAvailable].Message, time.Now().Unix(), deployment.Status.States[domain.DeploymentAvailable].LastUpdateTimestamp)
		deployment.Status.States[domain.DeploymentAvailable] = domain.NewDeploymentState(domain.DeploymentAvailable, deployment.Status.States[domain.DeploymentAvailable].Active,
			deployment.Status.States[domain.DeploymentAvailable].Message, time.Now().Unix(), time.Now().Unix())

		deployment.Status.States[domain.DeploymentProgress] = domain.NewDeploymentState(domain.DeploymentProgress, deployment.Status.States[domain.DeploymentProgress].Active,
			deployment.Status.States[domain.DeploymentProgress].Message, time.Now().Unix(), time.Now().Unix())

		// deployment.Status.States[domain.DeploymentFailure] = domain.NewDeploymentState(domain.DeploymentFailure, deployment.Status.States[domain.DeploymentFailure].Active,
		// 	deployment.Status.States[domain.DeploymentFailure].Message, time.Now().Unix(), deployment.Status.States[domain.DeploymentFailure].LastUpdateTimestamp)
		deployment.Status.States[domain.DeploymentFailure] = domain.NewDeploymentState(domain.DeploymentFailure, deployment.Status.States[domain.DeploymentFailure].Active,
			deployment.Status.States[domain.DeploymentFailure].Message, time.Now().Unix(), time.Now().Unix())

		deployment.Status.Stopped = false
		deployment.Status.Deleted = false
	}

	log.Println("ADD DEPLOYMENT MOMENT")
	err = u.SaveDeployment(&deployment)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	// start worker to loop the deployment
	go u.StartWorker(&deployment)

	log.Printf("Deployment %s added successfully", deployment.Name)

	return &api.AddDeploymentResp{}, nil
}

func (u UpdateServiceGrpcHandler) PauseDeployment(ctx context.Context, req *api.PauseDeploymentReq) (*api.PauseDeploymentResp, error) {

	task := worker.WorkerTask{
		TaskType:            worker.TaskTypePause,
		DeploymentName:      req.Name,
		DeploymentNamespace: req.Namespace,
		DeploymentOrgId:     req.OrgId,
		Payload:             map[string]interface{}{}}

	resp, err := u.sendTaskAndSubscribe(ctx, task)
	if err != nil {
		return nil, err
	}
	if resp.ErrorMsg != "" {
		return nil, utils.TaskResponseToGrpcError(resp)
	}

	return &api.PauseDeploymentResp{}, nil
}

func (u UpdateServiceGrpcHandler) UnpauseDeployment(ctx context.Context, req *api.UnpauseDeploymentReq) (*api.UnpauseDeploymentResp, error) {

	task := worker.WorkerTask{
		TaskType:            worker.TaskTypeUnpause,
		DeploymentName:      req.Name,
		DeploymentNamespace: req.Namespace,
		DeploymentOrgId:     req.OrgId,
		Payload:             map[string]interface{}{}}

	resp, err := u.sendTaskAndSubscribe(ctx, task)
	if err != nil {
		return nil, err
	}
	if resp.ErrorMsg != "" {
		return nil, utils.TaskResponseToGrpcError(resp)
	}

	return &api.UnpauseDeploymentResp{}, nil
}

func (u UpdateServiceGrpcHandler) RollbackRevision(ctx context.Context, req *api.RollbackRevisionReq) (*api.RollbackRevisionResp, error) {

	task := worker.WorkerTask{
		TaskType:            worker.TaskTypeRollback,
		DeploymentName:      req.Name,
		DeploymentNamespace: req.Namespace,
		DeploymentOrgId:     req.OrgId,
		Payload: map[string]interface{}{
			"RollbackRevisionName": req.RevisionName,
		}}

	resp, err := u.sendTaskAndSubscribe(ctx, task)
	if err != nil {
		return nil, err
	}
	if resp.ErrorMsg != "" {
		return nil, utils.TaskResponseToGrpcError(resp)
	}

	return &api.RollbackRevisionResp{}, nil
}

func (u UpdateServiceGrpcHandler) GetDeployment(ctx context.Context, req *api.GetDeploymentReq) (*api.GetDeploymentResp, error) {
	deployment, err := u.deploymentRepo.Get(req.Name, req.Namespace, req.OrgId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	protoResp, err := proto.GetDeploymentRespFromDomain(*deployment)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return protoResp, nil
}

func (u UpdateServiceGrpcHandler) GetDeploymentOwnedRevisions(ctx context.Context, req *api.GetDeploymentOwnedRevisionsReq) (*api.GetDeploymentOwnedRevisionsResp, error) {
	deployment, err := u.deploymentRepo.Get(req.Name, req.Namespace, req.OrgId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	revisions, err := u.revisionRepo.GetDeploymentOwnedRevisions(deployment.Spec.SelectorLabels, req.Namespace, req.OrgId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	protoResp, err := proto.GetDeploymentOwnedRevisionsRespFromDomain(revisions)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return protoResp, nil
}

func (u UpdateServiceGrpcHandler) DeleteDeployment(ctx context.Context, req *api.DeleteDeploymentReq) (*api.DeleteDeploymentResp, error) {

	d, err := u.deploymentRepo.Get(req.Name, req.Namespace, req.OrgId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	topic := fmt.Sprintf("deployments/%s/%s/%s", d.OrgId, d.Namespace, d.Name)
	if u.workerMap.Exists(topic) {
		task := worker.WorkerTask{
			TaskType:            worker.TaskTypeDelete,
			DeploymentName:      req.Name,
			DeploymentNamespace: req.Namespace,
			DeploymentOrgId:     req.OrgId,
			Payload:             map[string]interface{}{}}

		resp, err := u.sendTaskAndSubscribe(ctx, task)
		if err != nil {
			return nil, err
		}
		if resp.ErrorMsg != "" {
			return nil, utils.TaskResponseToGrpcError(resp)
		}
	} else {
		err := u.revisionRepo.DeleteDeploymentOwnedRevisions(map[string]string{}, req.Namespace, req.OrgId)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		err = u.deploymentRepo.Delete(req.Name, req.Namespace, req.OrgId)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	return &api.DeleteDeploymentResp{}, nil
}

func (u UpdateServiceGrpcHandler) StopDeployment(ctx context.Context, req *api.StopDeploymentReq) (*api.StopDeploymentResp, error) {

	d, err := u.deploymentRepo.Get(req.Name, req.Namespace, req.OrgId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	topic := fmt.Sprintf("deployments/%s/%s/%s", d.OrgId, d.Namespace, d.Name)
	if u.workerMap.Exists(topic) {

		task := worker.WorkerTask{
			TaskType:            worker.TaskTypeStop,
			DeploymentName:      req.Name,
			DeploymentNamespace: req.Namespace,
			DeploymentOrgId:     req.OrgId,
			Payload:             map[string]interface{}{}}

		resp, err := u.sendTaskAndSubscribe(ctx, task)
		if err != nil {
			return nil, err
		}
		if resp.ErrorMsg != "" {
			return nil, utils.TaskResponseToGrpcError(resp)
		}
	} else {
		fmt.Println("Worker not found, deployment is already not running")
	}

	return &api.StopDeploymentResp{}, nil
}
