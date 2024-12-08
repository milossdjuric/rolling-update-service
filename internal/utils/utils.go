package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/milossdjuric/rolling_update_service/internal/worker"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// calculates the total resource quotas for all apps for a deployment
func CalculateResourceQuotas(appCount int64, appQuotas map[string]float64) map[string]float64 {
	calculatedQuotas := make(map[string]float64)
	for resource, quota := range appQuotas {
		calculatedQuotas[resource] = quota * float64(appCount)
	}
	return calculatedQuotas
}

// generates a unique name by appending a short hash to the input name
func GenerateUniqueName(name string) string {
	uuidValue := uuid.New()
	shortUUID := strings.ReplaceAll(uuidValue.String(), "-", "")

	hash := sha256.Sum256([]byte(shortUUID + name))
	shortHash := hex.EncodeToString(hash[:])[:10]

	uniqueName := name + "-" + shortHash

	return uniqueName
}

func MatchLabels(childLabels, parentLables map[string]string) bool {
	for key, value := range parentLables {
		if childValue, exists := childLabels[key]; !exists || childValue != value {
			return false
		}
	}
	return true
}

func CompareStringSlices(slice1, slice2 []string) bool {
	if len(slice1) != len(slice2) {
		return false
	}
	for i := range slice1 {
		if slice1[i] != slice2[i] {
			return false
		}
	}
	return true
}

func CompareFloatMaps(map1, map2 map[string]float64) bool {
	if len(map1) != len(map2) {
		return false
	}
	for key, value := range map1 {
		if map2[key] != value {
			return false
		}
	}
	return true
}

func CompareStringMaps(map1, map2 map[string]string) bool {
	if len(map1) != len(map2) {
		return false
	}
	for key, value := range map1 {
		if map2[key] != value {
			return false
		}
	}
	return true
}

// converts a worker task response to a gRPC error
func TaskResponseToGrpcError(resp *worker.TaskResponse) error {
	switch resp.ErrorType {
	case "NotFound":
		return status.Error(codes.NotFound, resp.ErrorMsg)
	default:
		return status.Error(codes.Internal, resp.ErrorMsg)
	}
}

// sets default value for rolling update, for max surge and max unavailable
func CalculateDefaultRollingValue(value *int64, appCount int64) {
	//Sets default of 1/4 of total app count, if app count < 0, return 1
	*value = appCount / 4
	if *value == 0 {
		*value = 1
	}
}

// retrieves a new instance of the registered type by its type URL, used for mapping for payload for worker tasks
func GetRegisteredType(typeURL string) (proto.Message, error) {
	constructor, ok := worker.TypeRegistry[typeURL]
	if !ok {
		return nil, fmt.Errorf("type URL %s not registered", typeURL)
	}
	return constructor(), nil
}
