package proto

import (
	"rolling_update_service/internal/domain"
	mapper "rolling_update_service/internal/mappers/proto"
	"rolling_update_service/pkg/api"

	"google.golang.org/protobuf/proto"
)

type protoRevisionMarshaller struct {
}

func NewProtoRevisionMarshaller() *protoRevisionMarshaller {
	return &protoRevisionMarshaller{}
}

func (p protoRevisionMarshaller) Marshal(revision domain.Revision) ([]byte, error) {

	protoRevision, err := mapper.RevisionFromDomain(revision)
	if err != nil {
		return nil, err
	}

	return proto.Marshal(protoRevision)
}

func (p protoRevisionMarshaller) Unmarshal(revisionMarshalled []byte) (*domain.Revision, error) {
	protoRevision := &api.Revision{}
	err := proto.Unmarshal(revisionMarshalled, protoRevision)
	if err != nil {
		return nil, err
	}
	return mapper.RevisionToDomain(protoRevision)
}
