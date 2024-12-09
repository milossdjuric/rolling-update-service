package handlers

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	magnetarapi "github.com/c12s/magnetar/pkg/api"
	"github.com/docker/docker/client"
	"github.com/milossdjuric/rolling_update_service/internal/domain"
	mapper "github.com/milossdjuric/rolling_update_service/internal/mappers/proto"
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

func (u UpdateServiceGrpcHandler) PutDeployment(ctx context.Context, req *api.PutDeploymentReq) (*api.PutDeploymentResp, error) {
	// get the deployment from the request
	deployment, err := mapper.PutDeploymentReqToDomain(req)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// get the new revision, if it exists
	// newRevision, _, err := u.GetNewAndOldRevisions(&deployment)
	// if err != nil {
	// 	return nil, status.Error(codes.Internal, err.Error())
	// }

	// log.Printf("[PUT DEPLOYMENT] New revision: %v", newRevision)

	// err = u.revisionRepo.Put(*newRevision)
	// if err != nil {
	// 	return nil, status.Error(codes.Internal, err.Error())
	// }

	// check if deployment already exists in etcd, if it does, assign existing status, still change spec and labels
	existingDeployment, err := u.deploymentRepo.Get(deployment.Name, deployment.Namespace, deployment.OrgId)
	if err == nil && existingDeployment != nil {
		deployment.Status = domain.SetDeploymentStatus(existingDeployment.Status)
		// update the existing deployment status states, mostly update their timestamps
		deployment.Status.States[domain.DeploymentAvailable] = domain.NewDeploymentState(domain.DeploymentAvailable, deployment.Status.States[domain.DeploymentAvailable].Active,
			deployment.Status.States[domain.DeploymentAvailable].Message, time.Now().Unix(), time.Now().Unix())

		deployment.Status.States[domain.DeploymentProgress] = domain.NewDeploymentState(domain.DeploymentProgress, deployment.Status.States[domain.DeploymentProgress].Active,
			deployment.Status.States[domain.DeploymentProgress].Message, time.Now().Unix(), time.Now().Unix())

		deployment.Status.States[domain.DeploymentFailure] = domain.NewDeploymentState(domain.DeploymentFailure, deployment.Status.States[domain.DeploymentFailure].Active,
			deployment.Status.States[domain.DeploymentFailure].Message, time.Now().Unix(), time.Now().Unix())

		deployment.Status.Stopped = false
		deployment.Status.Deleted = false
	}

	// if worker exists, send the task to the worker to put the deployment in repo
	topic := fmt.Sprintf("deployments/%s/%s/%s", deployment.OrgId, deployment.Namespace, deployment.Name)
	if u.workerMap.Exists(topic) {
		protoDeployment, err := mapper.DeploymentFromDomain(deployment)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		payload := map[string]interface{}{
			"Deployment": protoDeployment,
		}
		task := worker.NewWorkerTask(worker.TaskTypeAdd, deployment.Name, deployment.Namespace, deployment.OrgId, payload)
		resp, err := u.SendTaskAndSubscribe(ctx, task)
		if err != nil {
			return nil, err
		}
		if resp.ErrorMsg != "" {
			return nil, utils.TaskResponseToGrpcError(resp)
		}
	}

	// start worker to loop the deployment, if worker already exists, it will be ignored and returned
	go u.StartWorker(&deployment)

	log.Printf("Deployment %s added successfully", deployment.Name)

	return &api.PutDeploymentResp{}, nil
}

func (u UpdateServiceGrpcHandler) PauseDeployment(ctx context.Context, req *api.PauseDeploymentReq) (*api.PauseDeploymentResp, error) {

	task := worker.NewWorkerTask(worker.TaskTypePause, req.Name, req.Namespace, req.OrgId, map[string]interface{}{})
	// send task to worker to pause the deployment
	resp, err := u.SendTaskAndSubscribe(ctx, task)
	if err != nil {
		return nil, err
	}
	if resp.ErrorMsg != "" {
		return nil, utils.TaskResponseToGrpcError(resp)
	}

	return &api.PauseDeploymentResp{}, nil
}

func (u UpdateServiceGrpcHandler) UnpauseDeployment(ctx context.Context, req *api.UnpauseDeploymentReq) (*api.UnpauseDeploymentResp, error) {

	task := worker.NewWorkerTask(worker.TaskTypeUnpause, req.Name, req.Namespace, req.OrgId, map[string]interface{}{})
	// send task to worker to unpause the deployment
	resp, err := u.SendTaskAndSubscribe(ctx, task)
	if err != nil {
		return nil, err
	}
	if resp.ErrorMsg != "" {
		return nil, utils.TaskResponseToGrpcError(resp)
	}

	return &api.UnpauseDeploymentResp{}, nil
}

func (u UpdateServiceGrpcHandler) RollbackRevision(ctx context.Context, req *api.RollbackRevisionReq) (*api.RollbackRevisionResp, error) {

	// send task to worker to rollback the deployment
	task := worker.NewWorkerTask(worker.TaskTypeRollback, req.Name, req.Namespace, req.OrgId, map[string]interface{}{"RollbackRevisionName": req.RevisionName})
	resp, err := u.SendTaskAndSubscribe(ctx, task)
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

	protoResp, err := mapper.GetDeploymentRespFromDomain(*deployment)
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

	revisions, err := u.revisionRepo.GetDeploymentOwned(deployment.Spec.SelectorLabels, req.Namespace, req.OrgId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	protoResp, err := mapper.GetDeploymentOwnedRevisionsRespFromDomain(revisions)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return protoResp, nil
}

func (u UpdateServiceGrpcHandler) GetNewRevision(ctx context.Context, req *api.GetNewRevisionReq) (*api.GetNewRevisionResp, error) {
	// method returns current newest revision by timestamp for deployment, if new revision is not adjusted
	// to deployment spec yet, it will not return it, but only the latest in repo
	deployment, err := u.deploymentRepo.Get(req.Name, req.Namespace, req.OrgId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	revisions, err := u.revisionRepo.GetDeploymentOwned(deployment.Spec.SelectorLabels, req.Namespace, req.OrgId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	sort.Sort(sort.Reverse(domain.ByCreationTimestamp(revisions)))
	newRevision := revisions[0]
	protoResp, err := mapper.GetNewestRevisionRespFromDomain(newRevision)
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

	// if worker exists, send the task to the worker to stop and delete the deployment
	topic := fmt.Sprintf("deployments/%s/%s/%s", d.OrgId, d.Namespace, d.Name)
	if u.workerMap.Exists(topic) {
		task := worker.NewWorkerTask(worker.TaskTypeDelete, req.Name, req.Namespace, req.OrgId, map[string]interface{}{})
		resp, err := u.SendTaskAndSubscribe(ctx, task)
		if err != nil {
			return nil, err
		}
		if resp.ErrorMsg != "" {
			return nil, utils.TaskResponseToGrpcError(resp)
		}
	} else {
		err := u.revisionRepo.DeleteDeploymentOwned(map[string]string{}, req.Namespace, req.OrgId)
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

	// if worker exists, send the task to the worker to stop the deployment
	topic := fmt.Sprintf("deployments/%s/%s/%s", d.OrgId, d.Namespace, d.Name)
	if u.workerMap.Exists(topic) {
		task := worker.NewWorkerTask(worker.TaskTypeStop, req.Name, req.Namespace, req.OrgId, map[string]interface{}{})
		resp, err := u.SendTaskAndSubscribe(ctx, task)
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
