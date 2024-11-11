package proto

import (
	"update-service/internal/domain"
	mapper "update-service/internal/mappers/proto"
	"update-service/pkg/api"

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
