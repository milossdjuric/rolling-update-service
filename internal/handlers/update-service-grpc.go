package handlers

import (
	"context"
	"log"
	"time"

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
}

func NewUpdateServiceGrpcHandler(deploymentRepo domain.DeploymentRepo, revisionRepo domain.RevisionRepo, natsConn *nats.Conn, dockerClient *client.Client) api.UpdateServiceServer {
	return &UpdateServiceGrpcHandler{
		deploymentRepo: deploymentRepo,
		revisionRepo:   revisionRepo,
		natsConn:       natsConn,
		workerMap:      worker.NewWorkerMap(),
		dockerClient:   dockerClient,
	}
}

func (u UpdateServiceGrpcHandler) PingFunction(ctx context.Context, ping *api.Ping) (*api.Pong, error) {
	return &api.Pong{Message: "Pong"}, nil
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
		deployment.Status.States[domain.DeploymentAvailable] = domain.NewDeploymentState(domain.DeploymentAvailable, deployment.Status.States[domain.DeploymentAvailable].Active,
			deployment.Status.States[domain.DeploymentAvailable].Message, time.Now().Unix(), deployment.Status.States[domain.DeploymentAvailable].LastUpdateTimestamp)
		deployment.Status.States[domain.DeploymentProgress] = domain.NewDeploymentState(domain.DeploymentProgress, deployment.Status.States[domain.DeploymentProgress].Active,
			deployment.Status.States[domain.DeploymentProgress].Message, time.Now().Unix(), time.Now().Unix())
		deployment.Status.States[domain.DeploymentFailure] = domain.NewDeploymentState(domain.DeploymentFailure, deployment.Status.States[domain.DeploymentFailure].Active,
			deployment.Status.States[domain.DeploymentFailure].Message, time.Now().Unix(), deployment.Status.States[domain.DeploymentFailure].LastUpdateTimestamp)
	}

	err = u.deploymentRepo.Put(deployment)
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

// func (u UpdateServiceGrpcHandler) GetDeployment(ctx context.Context, req *api.GetDeploymentReq) (*api.GetDeploymentResp, error) {

// 	task := worker.WorkerTask{
// 		TaskType:            worker.TaskTypeGetDeployment,
// 		DeploymentName:      req.Name,
// 		DeploymentNamespace: req.Namespace,
// 		DeploymentOrgId:     req.OrgId,
// 		Payload: map[string]interface{}{
// 			"GetDeploymentReq": req,
// 		},
// 	}

// 	resp, err := u.sendTaskAndSubscribe(task)
// 	if err != nil {
// 		return nil, err
// 	}

// 	if resp.ErrorMsg != "" {
// 		return nil, utils.TaskResponseToGrpcError(resp)
// 	}

// 	deploymentData, ok := resp.Payload["Deployment"].(map[string]interface{})
// 	if !ok {
// 		return nil, status.Error(codes.Internal, "Failed to cast deployment data to map")
// 	}

// 	var deployment domain.Deployment
// 	deploymentJSON, err := json.Marshal(deploymentData)
// 	if err != nil {
// 		return nil, status.Error(codes.Internal, err.Error())
// 	}

// 	err = json.Unmarshal(deploymentJSON, &deployment)
// 	if err != nil {
// 		return nil, status.Error(codes.Internal, err.Error())
// 	}

// 	protoResp, err := proto.GetDeploymentRespFromDomain(deployment)
// 	if err != nil {
// 		return nil, status.Error(codes.Internal, err.Error())
// 	}

// 	return protoResp, nil
// }

// func (u UpdateServiceGrpcHandler) GetDeploymentOwnedRevisions(ctx context.Context, req *api.GetDeploymentOwnedRevisionsReq) (*api.GetDeploymentOwnedRevisionsResp, error) {

// 	task := worker.WorkerTask{
// 		TaskType:            worker.TaskTypeGetDeploymentOwnedRevisions,
// 		DeploymentName:      req.Name,
// 		DeploymentNamespace: req.Namespace,
// 		DeploymentOrgId:     req.OrgId,
// 		Payload: map[string]interface{}{
// 			"GetDeploymentOwnedRevisionsReq": req,
// 		},
// 	}

// 	resp, err := u.sendTaskAndSubscribe(task)
// 	if err != nil {
// 		return nil, err
// 	}

// 	if resp.ErrorMsg != "" {
// 		return nil, utils.TaskResponseToGrpcError(resp)
// 	}

// 	revisionsData, ok := resp.Payload["Revisions"].([]interface{})
// 	if !ok {
// 		return nil, status.Error(codes.Internal, "Failed to cast revisions data to map")
// 	}

// 	var revisions []domain.Revision
// 	revisionsJSON, err := json.Marshal(revisionsData)
// 	if err != nil {
// 		return nil, status.Error(codes.Internal, err.Error())
// 	}

// 	err = json.Unmarshal(revisionsJSON, &revisions)
// 	if err != nil {
// 		return nil, status.Error(codes.Internal, err.Error())
// 	}

// 	protoResp, err := proto.GetDeploymentOwnedRevisionsRespFromDomain(revisions)
// 	if err != nil {
// 		return nil, status.Error(codes.Internal, err.Error())
// 	}

// 	return protoResp, nil
// }
