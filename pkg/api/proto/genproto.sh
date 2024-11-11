#!/bin/bash

# Specify absolute paths for plugins
PROTOC_GEN_GO="/home/milossdjuric/go/bin/protoc-gen-go"
PROTOC_GEN_GO_GRPC="/home/milossdjuric/go/bin/protoc-gen-go-grpc"

# Generate code for the update-service-model.proto
protoc --proto_path=./ \
    --go_out=../ \
    --go_opt=paths=source_relative \
    --plugin=protoc-gen-go=$PROTOC_GEN_GO \
    update-service-model.proto

# Generate code for the update-service.proto with gRPC
protoc -I=. \
    --proto_path=./ \
    --go_out=../ \
    --go_opt=paths=source_relative \
    --go-grpc_out=../ \
    --go-grpc_opt=paths=source_relative \
    --plugin=protoc-gen-go=$PROTOC_GEN_GO \
    --plugin=protoc-gen-go-grpc=$PROTOC_GEN_GO_GRPC \
    update-service.proto
