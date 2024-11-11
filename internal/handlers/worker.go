package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/milossdjuric/rolling_update_service/internal/domain"
	"github.com/milossdjuric/rolling_update_service/internal/worker"
	"github.com/milossdjuric/rolling_update_service/pkg/messaging/nats"
	natsgo "github.com/nats-io/nats.go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (u UpdateServiceGrpcHandler) StartWorker(d *domain.Deployment) {

	topic := fmt.Sprintf("deployments/%s/%s/%s", d.OrgId, d.Namespace, d.Name)

	err := u.workerMap.Add(topic)
	if err != nil {
		log.Printf("Worker already exists for topic: %s", topic)
		//Send a message via NATS to trigger immidieate reconcile
		publisher, err := nats.NewPublisher(u.natsConn)
		if err != nil {
			log.Printf("Error with publisher for running worker on topic: %s", topic)
		}
		publisher.Publish([]byte("reconcile"), topic)
		return
	}
	defer u.workerMap.Remove(topic)

	log.Printf("Creating NATS topic: %s", topic)
	log.Printf("Starting worker for topic: %s", topic)

	msgChan := make(chan *natsgo.Msg, 100)
	interruptChan := make(chan struct{})

	sub, err := nats.NewSubscriber(u.natsConn, topic, "")
	if err != nil {
		log.Printf("Failed to create subscriber: %v", err)
		return
	}

	err = sub.ChannelSubscribe(msgChan)
	if err != nil {
		log.Printf("Failed to subscribe to topic %s: %v", topic, err)
		return
	}
	defer sub.Unsubscribe()

	cooldown := time.NewTicker(3 * time.Second)
	defer cooldown.Stop()

	for {
		select {
		case msg := <-msgChan:
			log.Println("Worker handling message: ", msg)
			close(interruptChan)
			interruptChan = make(chan struct{})
			d, err := u.deploymentRepo.Get(d.Name, d.Namespace, d.OrgId)
			if err != nil {
				log.Printf("Failed to get deployment: %v", err)
			} else {
				u.HandleMessage(d, msg)
				// reset cooldown
				cooldown = time.NewTicker(1 * time.Second)
			}
		case <-cooldown.C:
			cooldown = time.NewTicker(10 * time.Second)
			ctx, cancel := context.WithCancel(context.Background())
			go func() {
				<-interruptChan
				cancel()
			}()
			//Get version from repo
			d, err := u.deploymentRepo.Get(d.Name, d.Namespace, d.OrgId)
			if err != nil {
				log.Printf("Failed to get deployment: %v", err)
			} else {
				u.Reconcile(ctx, d)
			}
			log.Printf("Worker reconcile cooldown for 10 seconds")
		}
	}
}

func (u *UpdateServiceGrpcHandler) HandleMessage(d *domain.Deployment, msg *natsgo.Msg) {
	log.Printf("Received message on topic %s: %s", msg.Subject, string(msg.Data))

	var task worker.WorkerTask
	err := json.Unmarshal(msg.Data, &task)
	if err != nil {
		log.Printf("Failed to unmarshal message: %v", err)
	} else {
		replySubject := msg.Reply

		switch task.TaskType {
		case worker.TaskTypeGetDeployment:
			// u.HandleGetDeploymentTask(task, replySubject)
		case worker.TaskTypeGetDeploymentOwnedRevisions:
			// u.HandleGetDeploymentOwnedRevisionsTask(task, replySubject)
		case worker.TaskTypePause:
			u.HandlePauseDeploymentTask(d, task, replySubject)
		case worker.TaskTypeUnpause:
			u.HandleUnpauseDeploymentTask(d, task, replySubject)
		case worker.TaskTypeRollback:
			u.HandleRollbackTask(d, task, replySubject)
		default:
			log.Printf("Unknown task type: %s", task.TaskType)
			return
		}
	}
}

func (u *UpdateServiceGrpcHandler) sendTaskAndSubscribe(ctx context.Context, task worker.WorkerTask) (*worker.TaskResponse, error) {

	log.Printf("Sending task: %v", task)

	taskMarshalled, err := json.Marshal(task)
	if err != nil {
		return nil, status.Error(codes.Internal, "Failed to marshal task")
	}

	publisher, err := nats.NewPublisher(u.natsConn)
	if err != nil {
		return nil, status.Error(codes.Internal, "Failed to create publisher")
	}

	replySubject := publisher.GenerateReplySubject()

	replySubscriber, err := nats.NewSubscriber(u.natsConn, replySubject, "")
	if err != nil {
		return nil, status.Error(codes.Internal, "Failed to create reply subscriber")
	}
	defer replySubscriber.Unsubscribe()

	replyChan := make(chan *natsgo.Msg)

	err = replySubscriber.ChannelSubscribe(replyChan)
	if err != nil {
		return nil, status.Error(codes.Internal, "Failed to subscribe to reply channel")
	}

	topic := fmt.Sprintf("deployments/%s/%s/%s", task.DeploymentOrgId, task.DeploymentNamespace, task.DeploymentName)
	err = publisher.Subscribe(taskMarshalled, topic, replySubject)
	if err != nil {
		return nil, status.Error(codes.Internal, "Failed to subscribe to topic")
	}

	select {
	case msg := <-replyChan:
		var resp worker.TaskResponse
		err := json.Unmarshal(msg.Data, &resp)
		if err != nil {
			return nil, status.Error(codes.Internal, "Failed to unmarshal data")
		}
		if resp.ErrorMsg != "" {
			return nil, status.Error(codes.Internal, resp.ErrorMsg)
		}
		return &resp, nil
	case <-ctx.Done():
		return nil, status.Error(codes.DeadlineExceeded, "Request timed out")
	}
}

func (u *UpdateServiceGrpcHandler) SendTaskResponse(replySubject string, resp worker.TaskResponse) error {
	respData, err := json.Marshal(resp)
	if err != nil {
		return err
	}

	publisher, err := nats.NewPublisher(u.natsConn)
	if err != nil {
		return err
	}
	err = publisher.Publish(respData, replySubject)
	if err != nil {
		return err
	}

	log.Printf("Response sent on topic: %s", replySubject)

	return nil
}

func (u *UpdateServiceGrpcHandler) HandlePauseDeploymentTask(d *domain.Deployment, task worker.WorkerTask, replySubject string) {

	var resp worker.TaskResponse

	d.Status.Paused = true
	err := u.SaveDeployment(d)
	if err != nil {
		resp.ErrorType = worker.ErrorTypeInternal
		resp.ErrorMsg = "Failed to save deployment"
	}
	u.SendTaskResponse(replySubject, resp)
}

func (u *UpdateServiceGrpcHandler) HandleUnpauseDeploymentTask(d *domain.Deployment, task worker.WorkerTask, replySubject string) {

	var resp worker.TaskResponse

	d.Status.Paused = false
	err := u.SaveDeployment(d)
	if err != nil {
		resp.ErrorType = worker.ErrorTypeInternal
		resp.ErrorMsg = "Failed to save deployment"
	}
	u.SendTaskResponse(replySubject, resp)
}

func (u *UpdateServiceGrpcHandler) HandleRollbackTask(d *domain.Deployment, task worker.WorkerTask, replySubject string) {

	var resp worker.TaskResponse

	payload, ok := task.Payload["RollbackRevisionName"]
	if !ok {
		log.Println("Payload does not contain required fields")
		resp.ErrorType = worker.ErrorTypeInternal
		u.SendTaskResponse(replySubject, resp)
		return
	}
	revisionName, ok := payload.(string)
	if !ok {
		log.Println("Payload format is invalid")
		resp.ErrorType = worker.ErrorTypeInternal
		u.SendTaskResponse(replySubject, resp)
		return
	}

	err := u.Rollback(d, revisionName)
	if err != nil {
		resp.ErrorType = worker.ErrorTypeInternal
		resp.ErrorMsg = err.Error()
		u.SendTaskResponse(replySubject, resp)
		return
	}
	// save deployment in case appSpec has been changed in rollback revision
	err = u.SaveDeployment(d)
	if err != nil {
		resp.ErrorType = worker.ErrorTypeInternal
		resp.ErrorMsg = "Failed to save deployment"
	}
	// return response to publisher via NATS
	u.SendTaskResponse(replySubject, resp)
}

// func (u *UpdateServiceGrpcHandler) HandleGetDeploymentTask(task worker.WorkerTask, replySubject string) {

// 	var resp worker.TaskResponse

// 	payload, ok := task.Payload["GetDeploymentReq"]
// 	if !ok {
// 		resp.ErrorMsg = "Payload does not contain required fields"
// 		resp.ErrorType = worker.ErrorTypeInternal
// 		u.SendTaskResponse(replySubject, resp)
// 		return
// 	}

// 	getDeploymentReq, ok := payload.(map[string]interface{})
// 	if !ok {
// 		resp.ErrorMsg = "Payload format is invalid"
// 		resp.ErrorType = worker.ErrorTypeInternal
// 		u.SendTaskResponse(replySubject, resp)
// 		return
// 	}

// 	name, namespace, orgId, err := utils.ValidateGetDeploymentReqTask(getDeploymentReq)
// 	if err != nil {
// 		resp.ErrorMsg = "Payload must contain valid fields: Name, Namespace and OrgId"
// 		resp.ErrorType = worker.ErrorTypeInternal
// 		u.SendTaskResponse(replySubject, resp)
// 		return
// 	}

// 	deployment, err := u.deploymentRepo.Get(name, namespace, orgId)
// 	if err != nil {
// 		resp.ErrorMsg = err.Error()
// 		if resp.ErrorMsg == "deployment not found" {
// 			resp.ErrorType = worker.ErrorTypeNotFound
// 		} else {
// 			resp.ErrorType = worker.ErrorTypeInternal
// 		}
// 		u.SendTaskResponse(replySubject, resp)
// 		return
// 	}

// 	resp.Payload = map[string]interface{}{
// 		"Deployment": deployment,
// 	}
// 	u.SendTaskResponse(replySubject, resp)
// }

// func (u *UpdateServiceGrpcHandler) HandleGetDeploymentOwnedRevisionsTask(task worker.WorkerTask, replySubject string) {

// 	var resp worker.TaskResponse

// 	payload, ok := task.Payload["GetDeploymentOwnedRevisionsReq"]
// 	if !ok {
// 		resp.ErrorMsg = "Payload does not contain required fields"
// 		resp.ErrorType = worker.ErrorTypeInternal
// 		u.SendTaskResponse(replySubject, resp)
// 		return
// 	}

// 	getDeploymentReq, ok := payload.(map[string]interface{})
// 	if !ok {
// 		resp.ErrorMsg = "Payload format is invalid"
// 		u.SendTaskResponse(replySubject, resp)
// 		return
// 	}

// 	name, namespace, orgId, err := utils.ValidateGetDeploymentOwnedRevisionsReqTask(getDeploymentReq)
// 	if err != nil {
// 		resp.ErrorMsg = err.Error()
// 		resp.ErrorType = worker.ErrorTypeInternal
// 		u.SendTaskResponse(replySubject, resp)
// 		return
// 	}

// 	deployment, err := u.deploymentRepo.Get(name, namespace, orgId)
// 	if err != nil {
// 		resp.ErrorMsg = err.Error()
// 		if resp.ErrorMsg == "deployment not found" {
// 			resp.ErrorType = worker.ErrorTypeNotFound
// 		} else {
// 			resp.ErrorType = worker.ErrorTypeInternal
// 		}
// 		u.SendTaskResponse(replySubject, resp)
// 		return
// 	}

// 	revisions, err := u.revisionRepo.GetDeploymentOwnedRevisions(deployment.Spec.SelectorLabels, namespace, orgId)
// 	if err != nil {
// 		resp.ErrorMsg = err.Error()
// 		resp.ErrorType = worker.ErrorTypeInternal
// 		u.SendTaskResponse(replySubject, resp)
// 		return
// 	}

// 	if revisions == nil {
// 		resp.Payload = map[string]interface{}{
// 			"Revisions": make([]domain.Revision, 0),
// 		}
// 	} else {
// 		resp.Payload = map[string]interface{}{
// 			"Revisions": revisions,
// 		}
// 	}

// 	u.SendTaskResponse(replySubject, resp)
// }
