package worker

import (
	"errors"
	"sync"
)

const (
	TaskTypeRollback = "Rollback"
	TaskTypePause    = "Pause"
	TaskTypeUnpause  = "Unpause"
)

type WorkerTask struct {
	TaskType            string
	DeploymentName      string
	DeploymentNamespace string
	DeploymentOrgId     string
	Payload             map[string]interface{}
}

type TaskResponse struct {
	Payload   map[string]interface{}
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

func NewWorkerMap() *WorkerMap {
	return &WorkerMap{
		mapping: make(map[string]bool),
	}
}

func (wm *WorkerMap) Add(topic string) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if _, exists := wm.mapping[topic]; exists {
		return errors.New("worker already exists for this topic")
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
