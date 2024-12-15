package worker

import (
	"fmt"
	"sync"

	"github.com/milossdjuric/rolling_update_service/pkg/api"
	"google.golang.org/protobuf/proto"
)

const (
	TaskTypeRollback = "Rollback"
	TaskTypePause    = "Pause"
	TaskTypeUnpause  = "Unpause"
	TaskTypeStop     = "Stop"
	TaskTypeDelete   = "Delete"
	TaskTypePut      = "Put"
)

type WorkerTask struct {
	TaskType            string
	DeploymentName      string
	DeploymentNamespace string
	DeploymentOrgId     string
	Payload             map[string]interface{}
}

type TaskResponse struct {
	ErrorMsg  string
	ErrorType string
}

const (
	ErrorTypeInternal = "Internal"
	ErrorTypeNotFound = "NotFound"
)

const (
	ScaleUp   = "Up"
	ScaleDown = "Down"
)

type WorkerMap struct {
	mapping map[string]bool
	mu      sync.RWMutex
}

// Registry of payload types for protobuf messages, used for worker tasks
var TypeRegistry = map[string]func() proto.Message{
	"type.googleapis.com/proto.Deployment": func() proto.Message { return &api.Deployment{} },
}

func NewWorkerTask(taskType, deploymentName, deploymentNamespace, deploymentOrgId string, payload map[string]interface{}) WorkerTask {
	return WorkerTask{
		TaskType:            taskType,
		DeploymentName:      deploymentName,
		DeploymentNamespace: deploymentNamespace,
		DeploymentOrgId:     deploymentOrgId,
		Payload:             payload,
	}
}

func NewWorkerMap() *WorkerMap {
	return &WorkerMap{
		mapping: make(map[string]bool),
	}
}

func (wm *WorkerMap) Add(topic string) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if _, exists := wm.mapping[topic]; exists {
		return fmt.Errorf("worker for topic %s already exists", topic)
	}

	wm.mapping[topic] = true
	return nil
}

func (wm *WorkerMap) Remove(topic string) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	delete(wm.mapping, topic)
}

func (wm *WorkerMap) Exists(topic string) bool {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	_, exists := wm.mapping[topic]
	return exists
}
