package handlers

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/milossdjuric/rolling_update_service/internal/domain"
	"github.com/milossdjuric/rolling_update_service/internal/utils"
	"github.com/milossdjuric/rolling_update_service/pkg/api"
	"github.com/milossdjuric/rolling_update_service/pkg/messaging/nats"
	natsgo "github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

// methods for container operations, docker for using docker daemon on local machine,
// node for using node agents for container operations, can be on local machine or remote

func (u *UpdateServiceGrpcHandler) StartDockerContainer(revisionName string, selectorLabels map[string]string) error {

	containerLabels := make(map[string]string)
	for k, v := range selectorLabels {
		containerLabels[k] = v
	}
	containerLabels["revision"] = revisionName
	uniqueContainerName := utils.GenerateUniqueName(revisionName)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	containerConfig := &container.Config{
		Image:  "alpine:latest",
		Cmd:    []string{"sh", "-c", "while true; do sleep 1000; done"},
		Labels: containerLabels,
	}

	resp, err := u.dockerClient.ContainerCreate(ctx, containerConfig, nil, nil, nil, uniqueContainerName)
	if err != nil {
		log.Printf("Failed to create container: %v", err)
		return err
	}
	if err := u.dockerClient.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		log.Printf("Failed to start container: %v", err)
		return err
	}

	log.Println("Container started:", resp.ID)
	return nil
}

func (u *UpdateServiceGrpcHandler) StopDockerContainer(name string, extraArgs ...string) error {

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := u.dockerClient.ContainerStop(ctx, name, container.StopOptions{}); err != nil {
		log.Printf("Failed to stop container: %v", err)
		return err
	}

	_, errChan := u.dockerClient.ContainerWait(ctx, name, container.WaitConditionNotRunning)
	if err := <-errChan; err != nil {
		log.Printf("Failed to wait for stop container: %v", err)
		return err
	}

	log.Println("Container stopped:", name)
	return nil
}

func (u *UpdateServiceGrpcHandler) QueryDockerContainer(prefix string, selectorLabels map[string]string, extraArgs ...string) ([]domain.App, error) {

	// log.Println("Querying container for prefix:", prefix)
	// log.Println("Querying container for selector labels:", selectorLabels)

	keyValues := make([]filters.KeyValuePair, 0)
	keyValues = append(keyValues, filters.KeyValuePair{Key: "label", Value: "revision=" + prefix})
	for key, value := range selectorLabels {
		keyValue := key + "=" + value
		keyValues = append(keyValues, filters.KeyValuePair{Key: "label", Value: keyValue})
	}
	args := filters.NewArgs(keyValues...)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	containers, err := u.dockerClient.ContainerList(ctx, container.ListOptions{Filters: args})
	if err != nil {
		log.Printf("Failed to list containers: %v", err)
		return nil, err
	}

	apps := make([]domain.App, 0)
	for _, container := range containers {
		apps = append(apps, domain.App{Name: container.Names[0], SelectorLabels: container.Labels})
		log.Printf("App: %v", apps[len(apps)-1].Name)
	}

	return apps, nil
}

func (u *UpdateServiceGrpcHandler) HealthCheckDockerContainer(name string, extraArgs ...string) (bool, error) {

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	containerInfo, err := u.dockerClient.ContainerInspect(ctx, name)
	if err != nil {
		log.Printf("Failed to inspect container: %v", err)
		return false, err
	}

	if !containerInfo.State.Running {
		log.Printf("Container %s is not running", name)
		return false, nil
	}

	// log.Printf("Container %s is running", name)
	return true, nil
}

func (u *UpdateServiceGrpcHandler) AvailabilityCheckDockerContainer(name string, minReadySeconds int64, extraArgs ...string) (bool, error) {

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	containerInfo, err := u.dockerClient.ContainerInspect(ctx, name)
	if err != nil {
		log.Printf("Failed to inspect container: %v", err)
		return false, fmt.Errorf("failed to inspect container: %v", err)
	}

	if containerInfo.State.Running {
		startedAt := containerInfo.State.StartedAt
		startTime, err := time.Parse(time.RFC3339Nano, startedAt)
		if err != nil {
			log.Printf("Failed to parse time: %v", err)
			return false, err
		}

		if time.Since(startTime).Seconds() >= float64(minReadySeconds) {
			// log.Printf("Container %s is available", name)
			return true, nil
		}
	}

	// log.Printf("Container %s is not available", name)
	return false, nil
}

func (u *UpdateServiceGrpcHandler) StartNodeContainer(revisionName string, selectorLabels map[string]string, extraArgs ...string) error {
	containerLabels := make(map[string]string)
	for k, v := range selectorLabels {
		containerLabels[k] = v
	}
	containerLabels["revision"] = revisionName
	uniqueName := utils.GenerateUniqueName(revisionName)

	if len(extraArgs) < 3 {
		return fmt.Errorf("not enough arguments for starting node container")
	}
	orgId, namespace, nodeId := extraArgs[0], extraArgs[1], extraArgs[2]

	cmd := api.ApplyAppOperationCommand{
		OrgId:           orgId,
		Namespace:       namespace,
		Name:            uniqueName,
		Operation:       "start",
		SelectorLabels:  selectorLabels,
		MinReadySeconds: 0,
	}
	data, err := proto.Marshal(&cmd)
	if err != nil {
		log.Printf("Failed to marshal command: %v", err)
		return err
	}

	err = u.natsConn.Publish(api.Subject(nodeId), data)
	if err != nil {
		log.Printf("Failed to publish command: %v", err)
		return err
	}

	sub, err := nats.NewSubscriber(u.natsConn, nodeId+".app_operation.start_app."+uniqueName, "")
	if err != nil {
		log.Printf("Failed to create subscriber: %v", err)
		return err
	}
	defer sub.Unsubscribe()

	respChan := make(chan *natsgo.Msg, 1)
	err = sub.ChannelSubscribe(respChan)
	if err != nil {
		log.Printf("Failed to subscribe to channel: %v", err)
		return err
	}
	defer close(respChan)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	select {
	case msg := <-respChan:
		var resp api.StartAppResp
		if err := proto.Unmarshal(msg.Data, &resp); err != nil {
			log.Printf("Failed to unmarshal response: %v", err)
			return err
		}
		//writing down error messages by | separator
		if len(resp.ErrorMessages) > 0 {
			errorMessages := ""
			for _, errorMsg := range resp.ErrorMessages {
				errorMessages += "|" + errorMsg
			}
			return fmt.Errorf("response error messages: %v", errorMessages)
		}
		if resp.Success {
			log.Printf("Node container started: %s, %s", nodeId, uniqueName)
			return nil
		} else {
			log.Printf("Node container failed to start, reason unknown: %s, %s", nodeId, uniqueName)
			return fmt.Errorf("node container failed to start, reason unknown")
		}
	case <-ctx.Done():
		log.Printf("Timeout while waiting for node container to start: %s, %s", nodeId, uniqueName)
		return fmt.Errorf("timeout while waiting for node container to start")
	}
}

func (u *UpdateServiceGrpcHandler) StopNodeContainer(name string, extraArgs ...string) error {

	if len(extraArgs) < 3 {
		return fmt.Errorf("not enough arguments for stopping node container")
	}
	orgId, namespace, nodeId := extraArgs[0], extraArgs[1], extraArgs[2]

	cmd := api.ApplyAppOperationCommand{
		OrgId:           orgId,
		Namespace:       namespace,
		Name:            name,
		Operation:       "stop",
		SelectorLabels:  make(map[string]string),
		MinReadySeconds: 0,
	}
	data, err := proto.Marshal(&cmd)
	if err != nil {
		log.Printf("Failed to marshal command: %v", err)
		return fmt.Errorf("failed to marshal command")
	}

	err = u.natsConn.Publish(api.Subject(nodeId), data)
	if err != nil {
		log.Printf("Failed to publish command: %v", err)
		return fmt.Errorf("failed to publish command")
	}
	sub, err := nats.NewSubscriber(u.natsConn, nodeId+".app_operation.stop_app."+name, "")
	if err != nil {
		log.Printf("Failed to create subscriber: %v", err)
		return err
	}
	defer sub.Unsubscribe()

	respChan := make(chan *natsgo.Msg, 1)
	err = sub.ChannelSubscribe(respChan)
	if err != nil {
		log.Printf("Failed to subscribe to channel: %v", err)
		return err
	}
	defer close(respChan)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	select {
	case msg := <-respChan:
		var resp api.StopAppResp
		if err := proto.Unmarshal(msg.Data, &resp); err != nil {
			log.Printf("Failed to unmarshal response: %v", err)
			return err
		}
		//writing down error messages by | separator
		if len(resp.ErrorMessages) > 0 {
			errorMessages := ""
			for _, errorMsg := range resp.ErrorMessages {
				errorMessages += "|" + errorMsg
			}
			return fmt.Errorf("response error messages: %v", errorMessages)
		}
		if resp.Success {
			log.Printf("Node container stopped: %s, %s", nodeId, name)
			return nil
		} else {
			log.Printf("Node container failed to stop, reason unknown: %s, %s", nodeId, name)
			return fmt.Errorf("node container failed to stop, reason unknown")
		}
	case <-ctx.Done():
		log.Printf("Timeout while waiting for node container to stop:%s, %s", nodeId, name)
		return fmt.Errorf("timeout while waiting for node container to stop")
	}
}

func (u *UpdateServiceGrpcHandler) QueryNodeContainers(prefix string, selectorLabels map[string]string, extraArgs ...string) ([]domain.App, error) {
	// log.Println("Querying container for prefix:", prefix)
	// log.Println("Querying container for selector labels:", selectorLabels)

	if len(extraArgs) < 3 {
		return nil, fmt.Errorf("not enough arguments for stopping node container")
	}
	orgId, namespace, nodeId := extraArgs[0], extraArgs[1], extraArgs[2]

	cmd := api.ApplyAppOperationCommand{
		OrgId:           orgId,
		Namespace:       namespace,
		Name:            prefix,
		Operation:       "query",
		SelectorLabels:  selectorLabels,
		MinReadySeconds: 0,
	}
	data, err := proto.Marshal(&cmd)
	if err != nil {
		return nil, err
	}

	err = u.natsConn.Publish(api.Subject(nodeId), data)
	if err != nil {
		return nil, err
	}
	sub, err := nats.NewSubscriber(u.natsConn, nodeId+".app_operation.query_app."+prefix, "")
	if err != nil {
		return nil, err
	}
	defer sub.Unsubscribe()

	respChan := make(chan *natsgo.Msg, 1)
	err = sub.ChannelSubscribe(respChan)
	if err != nil {
		return nil, err
	}
	defer close(respChan)

	apps := make([]domain.App, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	select {
	case msg := <-respChan:
		var resp api.QueryAppResp
		if err := proto.Unmarshal(msg.Data, &resp); err != nil {
			return nil, err
		}
		//writing down error messages by | separator
		if len(resp.ErrorMessages) > 0 {
			errorMessages := ""
			for _, errorMsg := range resp.ErrorMessages {
				errorMessages += "|" + errorMsg
			}
			return nil, fmt.Errorf("response error messages: %v", errorMessages)
		}
		if resp.Success {
			// log.Printf("Node agent queried: %s, %s", nodeId, prefix)
			for _, app := range resp.Apps {
				apps = append(apps, domain.App{Name: app.Name, SelectorLabels: app.SelectorLabels})
			}
			return apps, nil
		} else {
			return nil, fmt.Errorf("node container failed to query, reason unknown")
		}
	case <-ctx.Done():
		return nil, fmt.Errorf("timeout while waiting for node agent to query")
	}
}

func (u *UpdateServiceGrpcHandler) HealthCheckNodeContainer(name string, extraArgs ...string) (bool, error) {

	if len(extraArgs) < 3 {
		return false, fmt.Errorf("not enough arguments for stopping node container")
	}
	orgId, namespace, nodeId := extraArgs[0], extraArgs[1], extraArgs[2]

	// log.Println("Healthcheck node container: ", name)

	cmd := api.ApplyAppOperationCommand{
		OrgId:           orgId,
		Namespace:       namespace,
		Name:            name,
		Operation:       "healthcheck",
		SelectorLabels:  make(map[string]string),
		MinReadySeconds: 0,
	}
	data, err := proto.Marshal(&cmd)
	if err != nil {
		return false, fmt.Errorf("failed to marshal command")
	}

	err = u.natsConn.Publish(api.Subject(nodeId), data)
	if err != nil {
		return false, fmt.Errorf("failed to publish command")
	}
	sub, err := nats.NewSubscriber(u.natsConn, nodeId+".app_operation.healthcheck_app."+name, "")
	if err != nil {
		return false, err
	}
	defer sub.Unsubscribe()

	respChan := make(chan *natsgo.Msg, 1)
	err = sub.ChannelSubscribe(respChan)
	if err != nil {
		return false, err
	}
	defer close(respChan)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	select {
	case msg := <-respChan:
		var resp api.HealthCheckAppResp
		if err := proto.Unmarshal(msg.Data, &resp); err != nil {
			log.Printf("Failed to unmarshal response: %v", err)
			return false, err
		}
		//writing down error messages by | separator
		if len(resp.ErrorMessages) > 0 {
			errorMessages := ""
			for _, errorMsg := range resp.ErrorMessages {
				errorMessages += "|" + errorMsg
			}
			return false, fmt.Errorf("response error messages: %v", errorMessages)
		}
		if resp.Success {
			// log.Printf("Node container healthchecked: %s, %s", nodeId, name)
			return resp.Healthy, nil
		} else {
			return false, fmt.Errorf("node container failed to healthcheck, reason unknown")
		}
	case <-ctx.Done():
		return false, fmt.Errorf("timeout while waiting for node container to healthcheck")
	}
}

func (u *UpdateServiceGrpcHandler) AvailabilityCheckNodeContainer(name string, minReadySeconds int64, extraArgs ...string) (bool, error) {

	if len(extraArgs) < 3 {
		return false, fmt.Errorf("not enough arguments for stopping node container")
	}
	orgId, namespace, nodeId := extraArgs[0], extraArgs[1], extraArgs[2]

	cmd := api.ApplyAppOperationCommand{
		OrgId:           orgId,
		Namespace:       namespace,
		Name:            name,
		Operation:       "availabilitycheck",
		SelectorLabels:  make(map[string]string),
		MinReadySeconds: minReadySeconds,
	}
	data, err := proto.Marshal(&cmd)
	if err != nil {
		return false, fmt.Errorf("failed to marshal command")
	}

	err = u.natsConn.Publish(api.Subject(nodeId), data)
	if err != nil {
		return false, fmt.Errorf("failed to publish command")
	}
	sub, err := nats.NewSubscriber(u.natsConn, nodeId+".app_operation.availabilitycheck_app."+name, "")
	if err != nil {
		return false, err
	}
	defer sub.Unsubscribe()

	respChan := make(chan *natsgo.Msg, 1)
	err = sub.ChannelSubscribe(respChan)
	if err != nil {
		return false, err
	}
	defer close(respChan)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	select {
	case msg := <-respChan:
		var resp api.AvailabilityCheckAppResp
		if err := proto.Unmarshal(msg.Data, &resp); err != nil {
			return false, err
		}
		//writing down error messages by | separator
		if len(resp.ErrorMessages) > 0 {
			errorMessages := ""
			for _, errorMsg := range resp.ErrorMessages {
				errorMessages += "|" + errorMsg
			}
			return false, fmt.Errorf("response error messages: %v", errorMessages)
		}
		if resp.Success {
			return resp.Available, nil
		} else {
			return false, fmt.Errorf("node container failed to  availability check, reason unknown")
		}
	case <-ctx.Done():
		return false, fmt.Errorf("timeout while waiting for node container to  availability check")
	}
}
