package proto

import (
	"rolling_update_service/internal/domain"
	mapper "rolling_update_service/internal/mappers/proto"
	"rolling_update_service/pkg/api"

	"google.golang.org/protobuf/proto"
)

type protoDeploymentMarshaller struct {
}

func NewProtoDeploymentMarshaller() *protoDeploymentMarshaller {
	return &protoDeploymentMarshaller{}
}

func (p protoDeploymentMarshaller) Marshal(deployment domain.Deployment) ([]byte, error) {

	protoDeployment, err := mapper.DeploymentFromDomain(deployment)
	if err != nil {
		return nil, err
	}

	return proto.Marshal(protoDeployment)
}

func (p protoDeploymentMarshaller) Unmarshal(deploymentMarshalled []byte) (*domain.Deployment, error) {
	protoDeployment := &api.Deployment{}
	err := proto.Unmarshal(deploymentMarshalled, protoDeployment)
	if err != nil {
		return nil, err
	}
	return mapper.DeploymentToDomain(protoDeployment)
}
