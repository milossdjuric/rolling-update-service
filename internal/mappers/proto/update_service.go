package proto

import (
	"log"
	"update-service/internal/domain"
	"update-service/internal/utils"
	"update-service/pkg/api"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func AddDeploymentReqToDomain(req *api.AddDeploymentReq) (domain.Deployment, error) {

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
	)

	deploymentStatus := domain.NewDeploymentStatus()
	log.Println("Add Deployment Request to Domain, Deployment status:", deploymentStatus)

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
