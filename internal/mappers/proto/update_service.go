package proto

import (
	"fmt"
	"log"

	"github.com/milossdjuric/rolling_update_service/internal/domain"
	"github.com/milossdjuric/rolling_update_service/internal/utils"
	"github.com/milossdjuric/rolling_update_service/internal/worker"
	"github.com/milossdjuric/rolling_update_service/pkg/api"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func PutDeploymentReqToDomain(req *api.PutDeploymentReq) (domain.Deployment, error) {

	seccompProfile := domain.SeccompProfile{
		Version:       req.Spec.App.Profile.Version,
		DefaultAction: req.Spec.App.Profile.DefaultAction,
		Syscalls:      []domain.SyscallRule{},
	}

	for _, syscall := range req.Spec.App.Profile.Syscalls {
		rule := domain.SyscallRule{
			Names:  syscall.Names,
			Action: syscall.Action,
		}
		seccompProfile.Syscalls = append(seccompProfile.Syscalls, rule)
	}

	appSpec := domain.NewAppSpec(
		req.Spec.App.Name,
		req.Namespace,
		req.OrgId,
		req.Spec.SelectorLabels,
		seccompProfile,
		req.Spec.App.SeccompDefinitionStrategy,
	)
	appSpec.SelectorLabels["deployment"] = req.Name

	for resource, quota := range req.Spec.App.Quotas {
		err := appSpec.AddResourceQuota(resource, quota)
		if err != nil {
			log.Println(err)
			return domain.Deployment{}, status.Error(codes.InvalidArgument, err.Error())
		}
	}

	if req.Spec.Strategy.RollingUpdate == nil && req.Spec.Strategy.Type == string(domain.RollingUpdateStrategy) {
		req.Spec.Strategy.RollingUpdate = &api.RollingUpdate{
			MaxUnavailable: nil,
			MaxSurge:       nil,
		}
	}

	//if maxSurge and maxUnavailable are nil, set them to default values
	if req.Spec.Strategy.RollingUpdate != nil && req.Spec.Strategy.RollingUpdate.MaxUnavailable == nil {
		utils.CalculateDefaultRollingValue(req.Spec.Strategy.RollingUpdate.MaxUnavailable, req.Spec.AppCount)
	}

	if req.Spec.Strategy.RollingUpdate != nil && req.Spec.Strategy.RollingUpdate.MaxSurge == nil {
		utils.CalculateDefaultRollingValue(req.Spec.Strategy.RollingUpdate.MaxSurge, req.Spec.AppCount)
	}

	automaticRollback := true
	if req.Spec.AutomaticRollback != nil {
		automaticRollback = *req.Spec.AutomaticRollback
	}

	if req.Spec.Mode != string(domain.NodeAgentDirectDockerDaemon) &&
		req.Spec.Mode != string(domain.NodeAgentIndirectDockerDaemon) &&
		req.Spec.Mode != string(domain.DirectDockerDaemon) {
		return domain.Deployment{}, status.Error(codes.InvalidArgument, "Invalid deployment mode")
	}

	deploymentSpec := domain.NewDeploymentSpec(
		req.Spec.SelectorLabels,
		req.Spec.AppCount,
		req.Spec.RevisionLimit,
		domain.DeploymentStrategy{
			Type: domain.DeploymentStategyType(req.Spec.Strategy.Type),
			RollingUpdate: &domain.RollingUpdate{
				MaxUnavailable: req.Spec.Strategy.RollingUpdate.MaxUnavailable,
				MaxSurge:       req.Spec.Strategy.RollingUpdate.MaxSurge,
			},
		},
		appSpec,
		req.Spec.MinReadySeconds,
		req.Spec.DeadlineExceeded,
		automaticRollback,
		domain.DeploymentMode(req.Spec.Mode),
	)

	deploymentStatus := domain.NewDeploymentStatus()

	deployment := domain.NewDeployment(req.Name, req.Namespace, req.OrgId, req.Labels, deploymentSpec, deploymentStatus)

	return deployment, nil
}

func GetDeploymentRespFromDomain(deployment domain.Deployment) (*api.GetDeploymentResp, error) {

	deploymentSpec, err := DeploymentSpecFromDomain(deployment.Spec)
	if err != nil {
		return nil, err
	}

	deploymentStatus, err := DeploymentStatusFromDomain(deployment.Status)
	if err != nil {
		return nil, err
	}

	deploymentProto := &api.Deployment{
		Name:      deployment.Name,
		Namespace: deployment.Namespace,
		OrgId:     deployment.OrgId,
		Labels:    deployment.Labels,
		Spec:      deploymentSpec,
		Status:    deploymentStatus,
	}

	resp := &api.GetDeploymentResp{
		Deployment: deploymentProto,
	}

	return resp, nil
}

func GetDeploymentOwnedRevisionsRespFromDomain(revisions []domain.Revision) (*api.GetDeploymentOwnedRevisionsResp, error) {

	var revisionsProto []*api.Revision

	for _, revision := range revisions {
		revisionProto, err := RevisionFromDomain(revision)
		if err != nil {
			return nil, err
		}
		revisionsProto = append(revisionsProto, revisionProto)
	}

	return &api.GetDeploymentOwnedRevisionsResp{
		Revisions: revisionsProto,
	}, nil
}

func GetNewestRevisionRespFromDomain(revision domain.Revision) (*api.GetNewRevisionResp, error) {

	revisionProto, err := RevisionFromDomain(revision)
	if err != nil {
		return nil, err
	}

	return &api.GetNewRevisionResp{
		Revision: revisionProto,
	}, nil
}

func WorkerTaskFromDomain(workerTask worker.WorkerTask) (*api.WorkerTask, error) {

	protoPayload := make(map[string]*anypb.Any)

	// map payload key-values to proto.Message
	for key, value := range workerTask.Payload {
		switch v := value.(type) {
		case string:
			protoValue := wrapperspb.String(v)
			anyValue, err := anypb.New(protoValue)
			if err != nil {
				return nil, fmt.Errorf("failed to pack payload value for key %s: %w", key, err)
			}
			protoPayload[key] = anyValue
		case proto.Message:

			protoValue, ok := value.(proto.Message)
			if !ok {
				return nil, fmt.Errorf("payload value for key %s is not a proto.Message", key)
			}
			anyValue, err := anypb.New(protoValue)
			if err != nil {
				return nil, fmt.Errorf("failed to pack payload value for key %s: %w", key, err)
			}
			protoPayload[key] = anyValue
		default:
			return nil, fmt.Errorf("payload value for key %s is not a proto.Message or string", key)
		}
	}

	workerTaskProto := &api.WorkerTask{
		TaskType:            workerTask.TaskType,
		DeploymentName:      workerTask.DeploymentName,
		DeploymentNamespace: workerTask.DeploymentNamespace,
		DeploymentOrgId:     workerTask.DeploymentOrgId,
		Payload:             protoPayload,
	}

	return workerTaskProto, nil
}

func WorkerTaskToDomain(workerTask *api.WorkerTask) (*worker.WorkerTask, error) {
	domainPayload := make(map[string]interface{})

	for key, value := range workerTask.Payload {

		//string value used for "RevisionName" payload key-value
		if value.TypeUrl == "type.googleapis.com/google.protobuf.StringValue" {
			var strValue wrapperspb.StringValue
			if err := value.UnmarshalTo(&strValue); err != nil {
				return nil, fmt.Errorf("failed to unmarshal string payload for key %s: %w", key, err)
			}
			domainPayload[key] = strValue.GetValue()
		} else {
			//other values are stored as proto.Message, use type URL to get registered type
			messageType, err := utils.GetRegisteredType(value.GetTypeUrl())
			if err != nil {
				return nil, fmt.Errorf("failed to get registered type for key %s: %w", key, err)
			}

			if err := value.UnmarshalTo(messageType); err != nil {
				return nil, fmt.Errorf("failed to unmarshal payload for key %s: %w", key, err)
			}
			domainPayload[key] = messageType
		}
	}

	return &worker.WorkerTask{
		TaskType:            workerTask.TaskType,
		DeploymentName:      workerTask.DeploymentName,
		DeploymentNamespace: workerTask.DeploymentNamespace,
		DeploymentOrgId:     workerTask.DeploymentOrgId,
		Payload:             domainPayload,
	}, nil
}

func TaskResponseFromDomain(taskResponse worker.TaskResponse) (*api.TaskResponse, error) {
	return &api.TaskResponse{
		ErrorMsg:  taskResponse.ErrorMsg,
		ErrorType: taskResponse.ErrorType,
	}, nil
}

func TaskResponseToDomain(taskResponse *api.TaskResponse) (*worker.TaskResponse, error) {
	return &worker.TaskResponse{
		ErrorMsg:  taskResponse.ErrorMsg,
		ErrorType: taskResponse.ErrorType,
	}, nil
}
