package proto

import (
	"github.com/milossdjuric/rolling_update_service/internal/domain"
	"github.com/milossdjuric/rolling_update_service/internal/utils"
	"github.com/milossdjuric/rolling_update_service/pkg/api"
)

func DeploymentToDomain(deployment *api.Deployment) (*domain.Deployment, error) {

	deploymentSpec, err := DeploymentSpecToDomain(deployment.Spec)
	if err != nil {
		return nil, err
	}

	deploymentStatus, err := DeploymentStatusToDomain(deployment.Status)
	if err != nil {
		return nil, err
	}

	resp := &domain.Deployment{
		Name:      deployment.Name,
		Namespace: deployment.Namespace,
		OrgId:     deployment.OrgId,
		Labels:    deployment.Labels,
		Spec:      deploymentSpec,
		Status:    deploymentStatus,
	}
	resp.Spec.AppSpec.SelectorLabels["deployment"] = deployment.Name
	return resp, nil
}

func DeploymentFromDomain(deployment domain.Deployment) (*api.Deployment, error) {

	var resp *api.Deployment
	deploymentSpec, err := DeploymentSpecFromDomain(deployment.Spec)
	if err != nil {
		return nil, err
	}

	deploymentStatus, err := DeploymentStatusFromDomain(deployment.Status)
	if err != nil {
		return nil, err
	}

	resp = &api.Deployment{
		Name:      deployment.Name,
		Namespace: deployment.Namespace,
		OrgId:     deployment.OrgId,
		Labels:    deployment.Labels,
		Spec:      deploymentSpec,
		Status:    deploymentStatus,
	}
	resp.Spec.AppSpec.SelectorLabels["deployment"] = deployment.Name
	return resp, nil
}

func DeploymentSpecToDomain(deploymentSpec *api.DeploymentSpec) (domain.DeploymentSpec, error) {

	var resp domain.DeploymentSpec
	appSpec, err := AppSpecToDomain(deploymentSpec.AppSpec)
	if err != nil {
		return resp, err
	}

	var rollingUpdate *domain.RollingUpdate
	if deploymentSpec.Strategy.RollingUpdate != nil {
		rollingUpdate = &domain.RollingUpdate{
			MaxUnavailable: deploymentSpec.Strategy.RollingUpdate.MaxUnavailable,
			MaxSurge:       deploymentSpec.Strategy.RollingUpdate.MaxSurge,
		}
	}

	//if maxSurge and maxUnavailable are nil, set them to default values
	if rollingUpdate.MaxSurge == nil {
		utils.CalculateDefaultRollingValue(rollingUpdate.MaxSurge, deploymentSpec.AppCount)
	}
	if rollingUpdate.MaxUnavailable == nil {
		utils.CalculateDefaultRollingValue(rollingUpdate.MaxUnavailable, deploymentSpec.AppCount)
	}

	resp = domain.DeploymentSpec{
		SelectorLabels: deploymentSpec.SelectorLabels,
		AppCount:       deploymentSpec.AppCount,
		RevisionLimit:  &deploymentSpec.RevisionLimit,
		ResourceQuotas: deploymentSpec.ResourceQuotas,
		Strategy: domain.DeploymentStrategy{
			Type:          domain.DeploymentStategyType(deploymentSpec.Strategy.Type),
			RollingUpdate: rollingUpdate,
		},
		AppSpec:           appSpec,
		MinReadySeconds:   deploymentSpec.MinReadySeconds,
		DeadlineExceeded:  deploymentSpec.DeadlineExceeded,
		AutomaticRollback: deploymentSpec.AutomaticRollback,
	}

	return resp, nil
}

func DeploymentSpecFromDomain(deploymentSpec domain.DeploymentSpec) (*api.DeploymentSpec, error) {

	var resp *api.DeploymentSpec
	appSpec, err := AppSpecFromDomain(deploymentSpec.AppSpec)
	if err != nil {
		return nil, err
	}

	var rollingUpdate *api.RollingUpdate
	if deploymentSpec.Strategy.RollingUpdate != nil {
		rollingUpdate = &api.RollingUpdate{
			MaxUnavailable: deploymentSpec.Strategy.RollingUpdate.MaxUnavailable,
			MaxSurge:       deploymentSpec.Strategy.RollingUpdate.MaxSurge,
		}
	}

	//if maxSurge and maxUnavailable are nil, set them to default values
	if rollingUpdate.MaxSurge == nil {
		utils.CalculateDefaultRollingValue(rollingUpdate.MaxSurge, deploymentSpec.AppCount)
	}
	if rollingUpdate.MaxUnavailable == nil {
		utils.CalculateDefaultRollingValue(rollingUpdate.MaxUnavailable, deploymentSpec.AppCount)
	}

	resp = &api.DeploymentSpec{
		SelectorLabels: deploymentSpec.SelectorLabels,
		AppCount:       deploymentSpec.AppCount,
		RevisionLimit:  *deploymentSpec.RevisionLimit,
		ResourceQuotas: deploymentSpec.ResourceQuotas,
		Strategy: &api.DeploymentStrategy{
			Type: string(deploymentSpec.Strategy.Type),
			RollingUpdate: &api.RollingUpdate{
				MaxUnavailable: deploymentSpec.Strategy.RollingUpdate.MaxUnavailable,
				MaxSurge:       deploymentSpec.Strategy.RollingUpdate.MaxSurge,
			},
		},
		AppSpec:           appSpec,
		MinReadySeconds:   deploymentSpec.MinReadySeconds,
		DeadlineExceeded:  deploymentSpec.DeadlineExceeded,
		AutomaticRollback: deploymentSpec.AutomaticRollback,
	}

	return resp, nil
}

func DeploymentStatusToDomain(status *api.DeploymentStatus) (domain.DeploymentStatus, error) {

	var resp domain.DeploymentStatus
	var err error

	resp = domain.DeploymentStatus{
		TotalAppCount:       status.TotalAppCount,
		UpdatedAppCount:     status.UpdatedAppCount,
		ReadyAppCount:       status.ReadyAppCount,
		AvailableAppCount:   status.AvailableAppCount,
		UnavailableAppCount: status.UnavailableAppCount,
		States:              make(map[domain.DeploymentStateType]domain.DeploymentState),
		Paused:              status.Paused,
	}

	for _, state := range status.States {
		resp.States[domain.DeploymentStateType(state.Type)], err = DeploymentStateToDomain(state)
		if err != nil {
			return resp, err
		}
	}

	return resp, nil
}

func DeploymentStatusFromDomain(deploymentStatus domain.DeploymentStatus) (*api.DeploymentStatus, error) {

	var resp *api.DeploymentStatus
	var err error

	resp = &api.DeploymentStatus{
		TotalAppCount:       deploymentStatus.TotalAppCount,
		UpdatedAppCount:     deploymentStatus.UpdatedAppCount,
		ReadyAppCount:       deploymentStatus.ReadyAppCount,
		AvailableAppCount:   deploymentStatus.AvailableAppCount,
		UnavailableAppCount: deploymentStatus.UnavailableAppCount,
		States:              make(map[string]*api.DeploymentState),
		Paused:              deploymentStatus.Paused,
	}

	for _, state := range deploymentStatus.States {
		resp.States[string(state.Type)], err = DepoymentStateFromDomain(state)
		if err != nil {
			return resp, err
		}
	}

	return resp, nil
}

func DeploymentStateToDomain(state *api.DeploymentState) (domain.DeploymentState, error) {

	resp := domain.DeploymentState{
		Type:                    domain.DeploymentStateType(state.Type),
		Active:                  state.Active,
		LastUpdateTimestamp:     state.LastUpdateTimestamp,
		LastTransitionTimestamp: state.LastTransitionTimestamp,
		Message:                 state.Message,
	}

	return resp, nil
}

func DepoymentStateFromDomain(deploymentState domain.DeploymentState) (*api.DeploymentState, error) {

	resp := &api.DeploymentState{
		Type:                    string(deploymentState.Type),
		LastUpdateTimestamp:     deploymentState.LastUpdateTimestamp,
		LastTransitionTimestamp: deploymentState.LastTransitionTimestamp,
		Message:                 deploymentState.Message,
	}

	return resp, nil
}

func RevisionToDomain(revision *api.Revision) (*domain.Revision, error) {

	var resp *domain.Revision
	revisionSpec, err := RevisionSpecToDomain(revision.Spec)
	if err != nil {
		return resp, err
	}

	resp = &domain.Revision{
		Name:              revision.Name,
		Namespace:         revision.Namespace,
		OrgId:             revision.OrgId,
		CreationTimestamp: revision.CreationTimestamp,
		Labels:            revision.Labels,
		Spec:              revisionSpec,
	}
	// resp.Spec.AppSpec.SelectorLabels["revision"] = revision.Name

	return resp, nil
}

func RevisionFromDomain(revision domain.Revision) (*api.Revision, error) {

	var resp *api.Revision
	revisionSpec, err := RevisionSpecFromDomain(revision.Spec)
	if err != nil {
		return nil, err
	}

	resp = &api.Revision{
		Name:              revision.Name,
		Namespace:         revision.Namespace,
		OrgId:             revision.OrgId,
		CreationTimestamp: revision.CreationTimestamp,
		Labels:            revision.Labels,
		Spec:              revisionSpec,
	}
	// resp.Spec.AppSpec.SelectorLabels["revision"] = revision.Name

	return resp, nil
}

func RevisionSpecFromDomain(revisionSpec domain.RevisionSpec) (*api.RevisionSpec, error) {

	var resp *api.RevisionSpec
	appSpec, err := AppSpecFromDomain(revisionSpec.AppSpec)
	if err != nil {
		return resp, err
	}

	resp = &api.RevisionSpec{
		SelectorLabels: revisionSpec.SelectorLabels,
		AppSpec:        appSpec,
	}
	return resp, nil
}

func RevisionSpecToDomain(revisionSpec *api.RevisionSpec) (domain.RevisionSpec, error) {

	var resp domain.RevisionSpec
	appSpec, err := AppSpecToDomain(revisionSpec.AppSpec)
	if err != nil {
		return resp, err
	}

	resp = domain.RevisionSpec{
		SelectorLabels: revisionSpec.SelectorLabels,
		AppSpec:        appSpec,
	}
	return resp, nil
}

func AppSpecToDomain(appSpec *api.AppSpec) (domain.AppSpec, error) {

	var resp domain.AppSpec
	seccompProfile, err := ProfileToDomain(appSpec.SeccompProfile)
	if err != nil {
		return resp, err
	}

	resp = domain.NewAppSpec(
		appSpec.Name,
		appSpec.Namespace,
		appSpec.OrgId,
		appSpec.SelectorLabels,
		seccompProfile,
		appSpec.SeccompDefinitionStrategy,
	)

	for resource, quota := range appSpec.Quotas {
		err := resp.AddResourceQuota(resource, quota)
		if err != nil {
			return resp, err
		}
	}

	return resp, nil
}

func AppSpecFromDomain(appSpec domain.AppSpec) (*api.AppSpec, error) {

	var resp *api.AppSpec
	seccompProfile, err := ProfileFromDomain(appSpec.SeccompProfile)
	if err != nil {
		return resp, err
	}

	resp = &api.AppSpec{
		Name:                      appSpec.Name,
		Namespace:                 appSpec.Namespace,
		OrgId:                     appSpec.OrgId,
		Quotas:                    appSpec.Quotas,
		SelectorLabels:            appSpec.SelectorLabels,
		SeccompProfile:            seccompProfile,
		SeccompDefinitionStrategy: appSpec.SeccompDefintionStrategy,
	}

	return resp, nil
}

func SyscallRuleToDomain(syscallRule *api.SyscallRules) (domain.SyscallRule, error) {

	resp := domain.SyscallRule{
		Names:  make([]string, len(syscallRule.Names)),
		Action: syscallRule.Action,
	}
	copy(resp.Names, syscallRule.Names)

	return resp, nil
}

func SyscallRuleFromDomain(syscallRule domain.SyscallRule) (*api.SyscallRules, error) {

	resp := &api.SyscallRules{
		Names:  make([]string, len(syscallRule.Names)),
		Action: syscallRule.Action,
	}
	copy(resp.Names, syscallRule.Names)

	return resp, nil
}

func ProfileToDomain(profile *api.SeccompProfile) (domain.SeccompProfile, error) {

	resp := domain.SeccompProfile{
		Version:       profile.Version,
		DefaultAction: profile.DefaultAction,
		Syscalls:      make([]domain.SyscallRule, len(profile.Syscalls)),
	}
	for i, protoSyscall := range profile.Syscalls {
		syscall, err := SyscallRuleToDomain(protoSyscall)
		if err != nil {
			return resp, err
		}
		resp.Syscalls[i] = syscall
	}

	return resp, nil
}

func ProfileFromDomain(profile domain.SeccompProfile) (*api.SeccompProfile, error) {

	resp := &api.SeccompProfile{
		Version:       profile.Version,
		DefaultAction: profile.DefaultAction,
		Syscalls:      make([]*api.SyscallRules, len(profile.Syscalls)),
	}
	for i, domainSyscall := range profile.Syscalls {
		syscall, err := SyscallRuleFromDomain(domainSyscall)
		if err != nil {
			return resp, err
		}
		resp.Syscalls[i] = syscall
	}

	return resp, nil
}
