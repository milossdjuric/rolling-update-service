#!/bin/bash

# Generate code for the update-service-model.proto
protoc --proto_path=./ \
    --go_out=../ \
    --go_opt=paths=source_relative \
    update-service-model.proto

# Generate code for the update-service.proto with gRPC
protoc --proto_path=./ \
    --go_out=../ \
    --go_opt=paths=source_relative \
    --go-grpc_out=../ \
    --go-grpc_opt=paths=source_relative \
    update-service.proto
