package handlers

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/milossdjuric/rolling_update_service/internal/domain"
	mapper "github.com/milossdjuric/rolling_update_service/internal/mappers/proto"
	"github.com/milossdjuric/rolling_update_service/internal/worker"
	"github.com/milossdjuric/rolling_update_service/pkg/api"
	"github.com/milossdjuric/rolling_update_service/pkg/messaging/nats"
	natsgo "github.com/nats-io/nats.go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func (u UpdateServiceGrpcHandler) StartWorker(d *domain.Deployment) {

	topic := fmt.Sprintf("deployments/%s/%s/%s", d.OrgId, d.Namespace, d.Name)

	// if worker already exists for topic, return
	if err := u.workerMap.Add(topic); err != nil {
		log.Printf("DEPLOYMENT %s: Worker already exists for topic: %s", topic, topic)
		return
	}

	// save deployment, if worker is started for the first time
	u.SaveDeployment(d)

	log.Printf("DEPLOYMENT %s: Starting worker for topic: %s", topic, topic)
	defer u.workerMap.Remove(topic)

	// create message channel, interrupt channel and stop channel, on messsage channel we send tasks,
	// on reconcile channel we send signal for reconcile, if there is ongoing reconcile, it is interrupted and
	// new one is started, stop channel is used to stop worker, stop apps, clean up resources
	msgChan := make(chan *natsgo.Msg, 100)
	reconcileChan := make(chan struct{})
	stopChan := make(chan struct{})
	interruptChan := make(chan struct{}, 10)

	sub, err := nats.NewSubscriber(u.natsConn, topic, "")
	if err != nil {
		log.Printf("DEPLOYMENT %s: Failed to create subscriber for topic %s: %v", topic, topic, err)
		return
	}
	defer sub.Unsubscribe()

	if err := sub.ChannelSubscribe(msgChan); err != nil {
		log.Printf("DEPLOYMENT %s: Failed to subscribe to topic %s: %v", topic, topic, err)
		return
	}

	// parent context is used for worker, when we stop worker it cancels helper goroutine
	parentCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cooldownTimer := time.NewTimer(100 * time.Millisecond)

	// helper goroutine, listens for messages, interrupts and cooldowns
	go func() {
		for {
			select {
			// if parent context is cancelled, stop helper goroutine
			case <-parentCtx.Done():
				log.Println("Parent context cancelled, stopping worker handler goroutine")
				return
			// new task received, interrupt ongoing reconcile, start new one, use deployment from repo
			case msg := <-msgChan:
				log.Println("Received message, triggering reconcile")
				// close(reconcileChan)
				// reconcileChan = make(chan struct{})
				interruptChan <- struct{}{}

				updatedDeployment, err := u.deploymentRepo.Get(d.Name, d.Namespace, d.OrgId)
				if err != nil {
					log.Printf("DEPLYOMENT %s:Failed to fetch deployment: %v", topic, err)
					continue
				}
				u.HandleMessage(updatedDeployment, msg)

				// reset cooldown timer
				if !cooldownTimer.Stop() {
					select {
					case <-cooldownTimer.C:
					default:
					}
				}
				cooldownTimer.Reset(100 * time.Millisecond)

			// cooldown time passed, trigger periodic reconcile
			case <-cooldownTimer.C:

				deployment, err := u.deploymentRepo.Get(d.Name, d.Namespace, d.OrgId)
				if err != nil {
					// if deployment could not be read, try again
					log.Printf("DEPLOYMENT %s: Failed to fetch deployment for periodic reconcile: %v", topic, err)
					cooldownTimer.Reset(100 * time.Millisecond)
					continue
				}
				log.Printf("DEPLOYMENT %s: Cooldown elapsed, triggering periodic reconcile", topic)

				reconcilePeriod := time.Duration(*deployment.Spec.ReconcilePeriod) * time.Second
				cooldownTimer.Reset(reconcilePeriod)
				close(reconcileChan)
				reconcileChan = make(chan struct{})
			}
		}
	}()

	for {
		select {
		case <-stopChan:
			log.Printf("DEPLOYMENT %s: Stopping worker for topic: %s", topic, topic)
			return
		case <-parentCtx.Done():
			log.Println("DEPLOYMENT %s: Parent context cancelled, stopping worker", topic)
			return
		// receive reconcile signal, start new reconcile, also listen for new reconcile signal, if it arrives,
		// cancel current context to stop Reconcile()
		case <-reconcileChan:
			log.Printf("DEPLOYMENT %s: Reconcile received, starting reconciliation for topic: %s", topic, topic)
			currentCtx := context.Background()

			updatedDeployment, err := u.deploymentRepo.Get(d.Name, d.Namespace, d.OrgId)
			if err != nil {
				log.Printf("DEPLOYMENT %s: Failed to fetch deployment for reconcile: %v", topic, err)
				continue
			}
			u.Reconcile(currentCtx, updatedDeployment, stopChan, interruptChan)
		}
	}
}

func (u *UpdateServiceGrpcHandler) HandleMessage(d *domain.Deployment, msg *natsgo.Msg) {

	log.Printf("DEPLOYMENT %s: Worker received message...", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))

	var protoTask api.WorkerTask

	err := proto.Unmarshal(msg.Data, &protoTask)
	if err != nil {
		log.Printf("DEPLOYMENT %s: Failed to unmarshal message: %v", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), err)
	}
	task, err := mapper.WorkerTaskToDomain(&protoTask)
	if err != nil {
		log.Printf("DEPLOYMENT %s: Failed to map task from proto to domain: %v", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), err)
	} else {
		replySubject := msg.Reply

		switch task.TaskType {
		case worker.TaskTypePause:
			u.HandlePauseDeploymentTask(d, *task, replySubject)
		case worker.TaskTypeUnpause:
			u.HandleUnpauseDeploymentTask(d, *task, replySubject)
		case worker.TaskTypeRollback:
			u.HandleRollbackTask(d, *task, replySubject)
		case worker.TaskTypeStop:
			u.HandleStopTask(d, *task, replySubject)
		case worker.TaskTypeDelete:
			u.HandleDeleteTask(d, *task, replySubject)
		case worker.TaskTypePut:
			u.HandlePutTask(*task, replySubject)
		default:
			log.Printf("DEPLOYMENT %s: Unknown task type: %s", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), task.TaskType)
			return
		}
	}
}

func (u *UpdateServiceGrpcHandler) SendTaskAndSubscribe(ctx context.Context, task worker.WorkerTask) (*worker.TaskResponse, error) {

	protoTask, err := mapper.WorkerTaskFromDomain(task)
	if err != nil {
		return nil, status.Error(codes.Internal, "Failed to map task domain to proto")
	}

	taskMarshalled, err := proto.Marshal(protoTask)
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

		protoResp := &api.TaskResponse{}
		err := proto.Unmarshal(msg.Data, protoResp)
		if err != nil {
			return nil, status.Error(codes.Internal, "Failed to unmarshal data")
		}

		resp, err := mapper.TaskResponseToDomain(protoResp)
		if err != nil {
			return nil, status.Error(codes.Internal, "Failed to map resp from proto to domain")
		}
		if resp.ErrorMsg != "" {
			return nil, status.Error(codes.Internal, resp.ErrorMsg)
		}
		return resp, nil
	case <-ctx.Done():
		return nil, status.Error(codes.DeadlineExceeded, "Request timed out")
	}
}

func (u *UpdateServiceGrpcHandler) SendTaskResponse(replySubject string, resp worker.TaskResponse) error {
	protoResp, err := mapper.TaskResponseFromDomain(resp)
	if err != nil {
		return err
	}

	respData, err := proto.Marshal(protoResp)
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
	err = u.SendTaskResponse(replySubject, resp)
	if err != nil {
		log.Printf("DEPLOYMENT %s: Failed to send task response: %v", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), err)
	}
}

func (u *UpdateServiceGrpcHandler) HandleUnpauseDeploymentTask(d *domain.Deployment, task worker.WorkerTask, replySubject string) {

	var resp worker.TaskResponse
	d.Status.Paused = false

	err := u.SaveDeployment(d)
	if err != nil {
		resp.ErrorType = worker.ErrorTypeInternal
		resp.ErrorMsg = "Failed to save deployment"
	}
	err = u.SendTaskResponse(replySubject, resp)
	if err != nil {
		log.Printf("DEPLOYMENT %s: Failed to send task response: %v", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), err)
	}
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
	err = u.SendTaskResponse(replySubject, resp)
	if err != nil {
		log.Printf("DEPLOYMENT %s: Failed to send task response: %v", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), err)
	}
}

func (u *UpdateServiceGrpcHandler) HandleStopTask(d *domain.Deployment, task worker.WorkerTask, replySubject string) {

	var resp worker.TaskResponse

	d.Status.Stopped = true

	err := u.SaveDeployment(d)
	if err != nil {
		resp.ErrorType = worker.ErrorTypeInternal
		resp.ErrorMsg = "Failed to mark stop deployment"
	}

	err = u.SendTaskResponse(replySubject, resp)
	if err != nil {
		log.Printf("DEPLOYMENT %s: Failed to send task response: %v", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), err)
	}
}

func (u *UpdateServiceGrpcHandler) HandleDeleteTask(d *domain.Deployment, task worker.WorkerTask, replySubject string) {

	var resp worker.TaskResponse

	d.Status.Stopped = true
	d.Status.Deleted = true
	err := u.SaveDeployment(d)
	if err != nil {
		resp.ErrorType = worker.ErrorTypeInternal
		resp.ErrorMsg = "Failed to mark delete deployment"
	}

	err = u.SendTaskResponse(replySubject, resp)
	if err != nil {
		log.Printf("DEPLOYMETN %s: Failed to send task response: %v", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), err)
	}
}

func (u *UpdateServiceGrpcHandler) HandlePutTask(task worker.WorkerTask, replySubject string) {

	var resp worker.TaskResponse
	var deployment *domain.Deployment

	protoDeployment := task.Payload["Deployment"].(*api.Deployment)

	deployment, err := mapper.DeploymentToDomain(protoDeployment)
	if err != nil {
		resp.ErrorType = worker.ErrorTypeInternal
		resp.ErrorMsg = "Failed to map to domain deployment"
		u.SendTaskResponse(replySubject, resp)
		return
	}

	err = u.SaveDeployment(deployment)
	if err != nil {
		resp.ErrorType = worker.ErrorTypeInternal
		resp.ErrorMsg = "Failed to save deployment"
	}

	err = u.SendTaskResponse(replySubject, resp)
	if err != nil {
		log.Printf("DEPLOYMENT %s: Failed to send task response: %v", fmt.Sprintf("%s/%s/%s", deployment.OrgId, deployment.Namespace, deployment.Name), err)
	}
}
