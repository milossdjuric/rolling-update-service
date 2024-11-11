package domain

import (
	"rolling_update_service/internal/utils"
	"time"
)

type Deployment struct {
	Name      string
	Namespace string
	OrgId     string
	Labels    map[string]string
	Spec      DeploymentSpec
	Status    DeploymentStatus
}

type DeploymentSpec struct {
	SelectorLabels    map[string]string
	AppCount          int64
	RevisionLimit     *int64
	ResourceQuotas    map[string]float64
	Strategy          DeploymentStrategy
	AppSpec           AppSpec
	MinReadySeconds   int64
	DeadlineExceeded  int64
	AutomaticRollback bool
}

type DeploymentRepo interface {
	Put(deployment Deployment) error
	Get(name, namespace, orgId string) (*Deployment, error)
}

type DeploymentMarshaller interface {
	Marshal(deplyoment Deployment) ([]byte, error)
	Unmarshal(data []byte) (*Deployment, error)
}

type DeploymentStatus struct {
	TotalAppCount       int64
	UpdatedAppCount     int64
	ReadyAppCount       int64
	AvailableAppCount   int64
	UnavailableAppCount int64
	States              map[DeploymentStateType]DeploymentState
	Paused              bool
}

type DeploymentStateType string

const (
	DeploymentAvailable DeploymentStateType = "Available"
	DeploymentProgress  DeploymentStateType = "Progress"
	DeploymentFailure   DeploymentStateType = "Failure"
)

type DeploymentState struct {
	Type DeploymentStateType

	Active bool

	LastUpdateTimestamp int64

	LastTransitionTimestamp int64

	Message string
}

type DeploymentStrategy struct {
	Type DeploymentStategyType

	RollingUpdate *RollingUpdate
}

type DeploymentStategyType string

const (
	RollingUpdateStrategy DeploymentStategyType = "RollingUpdate"
)

type RollingUpdate struct {
	MaxUnavailable *int64
	MaxSurge       *int64
}

func NewDeployment(name string, namespace string, orgId string, labels map[string]string, deploymentSpec DeploymentSpec, deploymentStatus DeploymentStatus) Deployment {

	return Deployment{
		Name:      name,
		Namespace: namespace,
		OrgId:     orgId,
		Labels:    labels,
		Spec:      deploymentSpec,
		Status:    deploymentStatus,
	}
}

func NewDeploymentSpec(selectorLabels map[string]string, appCount int64, revisionLimit *int64, strategy DeploymentStrategy, appSpec AppSpec, minReadySeconds int64, deadlineExceeded int64, automaticRollback bool) DeploymentSpec {

	calculatedQuotas := utils.CalculateResourceQuotas(appCount, appSpec.Quotas)

	if revisionLimit == nil {
		revisionLimit = new(int64)
		*revisionLimit = 10
	}

	return DeploymentSpec{
		SelectorLabels:    selectorLabels,
		AppCount:          appCount,
		RevisionLimit:     revisionLimit,
		ResourceQuotas:    calculatedQuotas,
		Strategy:          strategy,
		AppSpec:           appSpec,
		MinReadySeconds:   minReadySeconds,
		DeadlineExceeded:  deadlineExceeded,
		AutomaticRollback: automaticRollback,
	}
}

func NewDeploymentStatus() DeploymentStatus {

	res := DeploymentStatus{
		TotalAppCount:       0,
		UpdatedAppCount:     0,
		ReadyAppCount:       0,
		AvailableAppCount:   0,
		UnavailableAppCount: 0,
		States:              make(map[DeploymentStateType]DeploymentState),
		Paused:              false,
	}

	res.States[DeploymentProgress] = DeploymentState{
		Type:                    DeploymentProgress,
		Active:                  true,
		LastUpdateTimestamp:     time.Now().Unix(),
		LastTransitionTimestamp: time.Now().Unix(),
		Message:                 "Deployment started",
	}
	res.States[DeploymentAvailable] = DeploymentState{
		Type:                    DeploymentAvailable,
		Active:                  false,
		LastUpdateTimestamp:     time.Now().Unix(),
		LastTransitionTimestamp: time.Now().Unix(),
		Message:                 "Deployment not available yetr",
	}
	res.States[DeploymentFailure] = DeploymentState{
		Type:                    DeploymentFailure,
		Active:                  false,
		LastUpdateTimestamp:     time.Now().Unix(),
		LastTransitionTimestamp: time.Now().Unix(),
		Message:                 "Deployment has not failed",
	}

	return res
}

func SetDeploymentStatus(status DeploymentStatus) DeploymentStatus {

	res := DeploymentStatus{
		TotalAppCount:       status.TotalAppCount,
		UpdatedAppCount:     status.UpdatedAppCount,
		ReadyAppCount:       status.ReadyAppCount,
		AvailableAppCount:   status.AvailableAppCount,
		UnavailableAppCount: status.UnavailableAppCount,
		States:              make(map[DeploymentStateType]DeploymentState),
		Paused:              status.Paused,
	}

	for k, v := range status.States {
		res.States[k] = v
	}
	return res
}

func NewDeploymentState(stateType DeploymentStateType, active bool, message string, updateTimestamp, transitionTimestamp int64) DeploymentState {

	return DeploymentState{
		Type:                    stateType,
		Active:                  active,
		LastUpdateTimestamp:     updateTimestamp,
		LastTransitionTimestamp: transitionTimestamp,
		Message:                 message,
	}
}
