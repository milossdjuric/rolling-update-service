package handlers

import (
	"context"
	"fmt"
	"log"
	"math"
	"time"

	magnetarapi "github.com/c12s/magnetar/pkg/api"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/milossdjuric/rolling_update_service/internal/domain"
	"github.com/milossdjuric/rolling_update_service/internal/utils"
	"github.com/milossdjuric/rolling_update_service/pkg/api"
	"github.com/milossdjuric/rolling_update_service/pkg/messaging/nats"
	natsgo "github.com/nats-io/nats.go"
	"golang.org/x/exp/rand"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

func CountMatchingAppsForRevisons(revision *domain.Revision, apps []*domain.App) int64 {
	appCount := int64(0)
	for _, app := range apps {
		// log.Printf("Checking Count Matching Apps For Revisions, revision name: %s, app name: %s", revision.Name, app.Name)
		// log.Printf("Revision Selector Labels: %v, App Selector Labels: %v", revision.Spec.SelectorLabels, app.SelectorLabels)
		if utils.MatchLabels(revision.Spec.SelectorLabels, app.SelectorLabels) {
			// log.Printf("INCREMENTED")
			appCount++
		}
	}
	return appCount
}

func (u *UpdateServiceGrpcHandler) IsDeadlineExceeded(d *domain.Deployment, timestamp int64) bool {
	deadline := d.Spec.DeadlineExceeded + timestamp
	return time.Now().Unix() > deadline
}

func GetOldUnavailableApps(totalApps, availableApps []domain.App, newRevision *domain.Revision) []domain.App {

	availableAppsMap := make(map[string]domain.App)
	for _, app := range availableApps {
		availableAppsMap[app.Name] = app
	}

	unavailableApps := make([]domain.App, 0)
	for _, app := range totalApps {
		if _, exists := availableAppsMap[app.Name]; !exists {
			if !utils.MatchLabels(newRevision.Spec.SelectorLabels, app.SelectorLabels) {
				unavailableApps = append(unavailableApps, app)
			}
		}
	}
	return unavailableApps
}

func (u *UpdateServiceGrpcHandler) SaveDeployment(d *domain.Deployment) error {
	return u.deploymentRepo.Put(*d)
}

func (u *UpdateServiceGrpcHandler) IsContextInterrupted(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		log.Println("Reconcile interrupted before reconciling deployment")
		return true
	default:
		log.Println("Reconcile not interrupted")
		return false
	}
}

func (u *UpdateServiceGrpcHandler) StartDockerContainer(revisionName string, selectorLabels map[string]string) error {

	log.Println("Starting container for revision:", revisionName)

	containerLabels := make(map[string]string)
	for k, v := range selectorLabels {
		containerLabels[k] = v
	}
	containerLabels["revision"] = revisionName
	uniqueContainerName := utils.GenerateUniqueName(revisionName)

	log.Printf("Container labels: %v", containerLabels)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// containerConfig := &container.Config{
	// 	Image:  os.Getenv("DOCKER_CONTAINER_IMAGE"),
	// 	Cmd:    []string{os.Getenv("DOCKER_CONTAINER_CMD"), os.Getenv("DOCKER_CONTAINER_CMD_ARG")},
	// 	Labels: selectorLabels,
	// }

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

	// _, errChan := u.dockerClient.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
	// select {
	// case err := <-errChan:
	// 	if err != nil {
	// 		log.Printf("Failed to wait for container: %v", err)
	// 		return err
	// 	}
	// }

	log.Println("Container started:", resp.ID)
	return nil
}

func (u *UpdateServiceGrpcHandler) StopDockerContainer(name string, extraArgs ...string) error {

	log.Println("Stopping container:", name)

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	if err := u.dockerClient.ContainerStop(ctx, name, container.StopOptions{}); err != nil {
		log.Printf("Failed to stop container: %v", err)
		return err
	}

	_, errChan := u.dockerClient.ContainerWait(ctx, name, container.WaitConditionNotRunning)
	select {
	case err := <-errChan:
		log.Println("Container err chan received")
		if err != nil {
			log.Printf("Failed to wait for stop container: %v", err)
			return err
		}
	}

	log.Println("Container stopped:", name)
	return nil
}

func (u *UpdateServiceGrpcHandler) QueryDockerContainer(prefix string, selectorLabels map[string]string, extraArgs ...string) ([]domain.App, error) {

	log.Println("Querying container for prefix:", prefix)
	log.Println("Querying container for selector labels:", selectorLabels)

	keyValues := make([]filters.KeyValuePair, 0)
	keyValues = append(keyValues, filters.KeyValuePair{Key: "label", Value: "revision=" + prefix})
	for key, value := range selectorLabels {
		keyValue := key + "=" + value
		keyValues = append(keyValues, filters.KeyValuePair{Key: "label", Value: keyValue})
	}
	args := filters.NewArgs(keyValues...)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	log.Println("Filters args:", args)

	containers, err := u.dockerClient.ContainerList(ctx, container.ListOptions{Filters: args})
	if err != nil {
		log.Printf("Failed to list containers: %v", err)
		return nil, err
	}

	log.Println("Containers len:", len(containers))

	apps := make([]domain.App, 0)
	for _, container := range containers {
		// if strings.HasPrefix(container.Names[0], prefix) {
		// 	log.Printf("Container: %v", container)
		// 	apps = append(apps, domain.App{Name: container.Names[0]})
		// }
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

	// if containerInfo.State.Running || containerInfo.State.Health == nil || containerInfo.State.Health.Status != "healthy" {
	// 	log.Printf("----------------")
	// 	log.Printf("Container %s is not healthy", name)
	// 	if containerInfo.State.Health != nil {
	// 		log.Printf("Container Info State Health: %v", containerInfo.State.Health)
	// 		if containerInfo.State.Health.Status != "" {
	// 			log.Printf("Container Info State Health Status: %v", containerInfo.State.Health.Status)
	// 		} else {
	// 			log.Println("Container Info State Health Status is empty")
	// 		}
	// 	} else {
	// 		log.Println("Container Info State Health is nil")
	// 	}
	// 	return false, nil
	// }
	if !containerInfo.State.Running {
		log.Printf("Container %s is not running", name)
		return false, nil
	}

	log.Printf("Container %s is running", name)
	return true, nil
}

func (u *UpdateServiceGrpcHandler) AvailabilityCheckDockerContainer(name string, minReadySeconds int64, extraArgs ...string) (bool, error) {
	log.Println("Checking container availability for name:", name)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	containerInfo, err := u.dockerClient.ContainerInspect(ctx, name)
	if err != nil {
		log.Printf("Failed to inspect container: %v", err)
		return false, err
	}

	// if containerInfo.State.Status == "running" && (containerInfo.State.Health == nil || containerInfo.State.Health.Status == "healthy") {
	// 	startedAt := containerInfo.State.StartedAt
	// 	startTime, err := time.Parse(time.RFC3339Nano, startedAt)
	// 	if err != nil {
	// 		log.Printf("Failed to parse time: %v", err)
	// 		return false, err
	// 	}

	// 	if time.Since(startTime).Seconds() >= float64(minReadySeconds) {
	// 		log.Printf("Container %s is available", name)
	// 		return true, nil
	// 	}
	// }
	if containerInfo.State.Running {
		startedAt := containerInfo.State.StartedAt
		startTime, err := time.Parse(time.RFC3339Nano, startedAt)
		if err != nil {
			log.Printf("Failed to parse time: %v", err)
			return false, err
		}

		if time.Since(startTime).Seconds() >= float64(minReadySeconds) {
			log.Printf("Container %s is available", name)
			return true, nil
		}
	}

	log.Printf("Container %s is not available", name)
	return false, nil
}

func (u *UpdateServiceGrpcHandler) StartStarContainer(revisionName string, selectorLabels map[string]string, extraArgs ...string) error {
	containerLabels := make(map[string]string)
	for k, v := range selectorLabels {
		containerLabels[k] = v
	}
	containerLabels["revision"] = revisionName
	uniqueName := utils.GenerateUniqueName(revisionName)
	log.Printf("Container labels: %v", containerLabels)

	if len(extraArgs) < 3 {
		return fmt.Errorf("not enough arguments for starting star container")
	}
	orgId := extraArgs[0]
	namespace := extraArgs[1]
	nodeId := extraArgs[2]

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
	respChan := make(chan *natsgo.Msg, 1)
	err = sub.ChannelSubscribe(respChan)
	if err != nil {
		log.Printf("Failed to subscribe to channel: %v", err)
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case msg := <-respChan:
		var resp api.StartAppResp
		log.Printf("Received message: %v", msg)
		if err := proto.Unmarshal(msg.Data, &resp); err != nil {
			log.Printf("Failed to unmarshal response: %v", err)
			return err
		}
		if len(resp.ErrorMessages) > 0 {
			errorMessages := ""
			for _, errorMsg := range resp.ErrorMessages {
				errorMessages += "|" + errorMsg
			}
			return fmt.Errorf("response error messages: %v", err)
		}
		if resp.Success {
			log.Printf("Star container started: %s, %s", nodeId, uniqueName)
			return nil
		} else {
			log.Printf("Star container failed to start, reason unknown: %s, %s", nodeId, uniqueName)
			return fmt.Errorf("star container failed to start, reason unknown")
		}
	case <-ctx.Done():
		log.Printf("Timeout while waiting for star container to start: %s, %s", nodeId, uniqueName)
		return fmt.Errorf("timeout while waiting for star container to start")
	}
}

func (u *UpdateServiceGrpcHandler) StopStarContainer(name string, extraArgs ...string) error {

	if len(extraArgs) < 3 {
		return fmt.Errorf("not enough arguments for stopping star container")
	}
	orgId := extraArgs[0]
	namespace := extraArgs[1]
	nodeId := extraArgs[2]

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
	respChan := make(chan *natsgo.Msg, 1)
	err = sub.ChannelSubscribe(respChan)
	if err != nil {
		log.Printf("Failed to subscribe to channel: %v", err)
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case msg := <-respChan:
		var resp api.StopAppResp
		log.Printf("Received message: %v", msg)
		if err := proto.Unmarshal(msg.Data, &resp); err != nil {
			log.Printf("Failed to unmarshal response: %v", err)
			return err
		}
		if len(resp.ErrorMessages) > 0 {
			errorMessages := ""
			for _, errorMsg := range resp.ErrorMessages {
				errorMessages += "|" + errorMsg
			}
			return fmt.Errorf("response error messages: %v", err)
		}
		if resp.Success {
			log.Printf("Star container stopped: %s, %s", nodeId, name)
			return nil
		} else {
			log.Printf("Star container failed to stop, reason unknown: %s, %s", nodeId, name)
			return fmt.Errorf("star container failed to stop, reason unknown")
		}
	case <-ctx.Done():
		log.Printf("Timeout while waiting for star container to stop:%s, %s", nodeId, name)
		return fmt.Errorf("timeout while waiting for star container to stop")
	}
}

func (u *UpdateServiceGrpcHandler) QueryStarContainers(prefix string, selectorLabels map[string]string, extraArgs ...string) ([]domain.App, error) {

	log.Println("Querying container for prefix:", prefix)
	log.Println("Querying container for selector labels:", selectorLabels)

	if len(extraArgs) < 3 {
		return nil, fmt.Errorf("not enough arguments for stopping star container")
	}
	orgId := extraArgs[0]
	namespace := extraArgs[1]
	nodeId := extraArgs[2]

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
		log.Printf("Failed to marshal command: %v", err)
		return nil, err
	}

	err = u.natsConn.Publish(api.Subject(nodeId), data)
	if err != nil {
		log.Printf("Failed to publish command: %v", err)
		return nil, err
	}
	sub, err := nats.NewSubscriber(u.natsConn, nodeId+".app_operation.query_app."+prefix, "")
	if err != nil {
		log.Printf("Failed to create subscriber: %v", err)
		return nil, err
	}
	respChan := make(chan *natsgo.Msg, 1)
	err = sub.ChannelSubscribe(respChan)
	if err != nil {
		log.Printf("Failed to subscribe to channel: %v", err)
		return nil, err
	}

	apps := make([]domain.App, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case msg := <-respChan:
		var resp api.QueryAppResp
		log.Printf("Received message: %v", msg)
		if err := proto.Unmarshal(msg.Data, &resp); err != nil {
			log.Printf("Failed to unmarshal response: %v", err)
			return nil, err
		}
		if len(resp.ErrorMessages) > 0 {
			errorMessages := ""
			for _, errorMsg := range resp.ErrorMessages {
				errorMessages += "|" + errorMsg
			}
			return nil, fmt.Errorf("response error messages: %v", err)
		}
		if resp.Success {
			log.Printf("Star agent queried: %s, %s", nodeId, prefix)
			for _, app := range resp.Apps {
				apps = append(apps, domain.App{Name: app.Name, SelectorLabels: app.SelectorLabels})
			}
			return apps, nil
		} else {
			log.Printf("Star agent failed to query, reason unknown: %s, %s", nodeId, prefix)
			return nil, fmt.Errorf("star container failed to query, reason unknown")
		}
	case <-ctx.Done():
		log.Printf("Timeout while waiting for star agent to query:%s, %s", nodeId, prefix)
		return nil, fmt.Errorf("timeout while waiting for star agent to query")
	}
}

func (u *UpdateServiceGrpcHandler) HealthCheckStarContainer(name string, extraArgs ...string) (bool, error) {

	if len(extraArgs) < 3 {
		return false, fmt.Errorf("not enough arguments for stopping star container")
	}
	orgId := extraArgs[0]
	namespace := extraArgs[1]
	nodeId := extraArgs[2]

	cmd := api.ApplyAppOperationCommand{
		OrgId:           orgId,
		Namespace:       namespace,
		Name:            name,
		Operation:       "health",
		SelectorLabels:  make(map[string]string),
		MinReadySeconds: 0,
	}
	data, err := proto.Marshal(&cmd)
	if err != nil {
		log.Printf("Failed to marshal command: %v", err)
		return false, fmt.Errorf("failed to marshal command")
	}

	err = u.natsConn.Publish(api.Subject(nodeId), data)
	if err != nil {
		log.Printf("Failed to publish command: %v", err)
		return false, fmt.Errorf("failed to publish command")
	}
	sub, err := nats.NewSubscriber(u.natsConn, nodeId+".app_operation.healthcheck_app."+name, "")
	if err != nil {
		log.Printf("Failed to create subscriber: %v", err)
		return false, err
	}
	respChan := make(chan *natsgo.Msg, 1)
	err = sub.ChannelSubscribe(respChan)
	if err != nil {
		log.Printf("Failed to subscribe to channel: %v", err)
		return false, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case msg := <-respChan:
		var resp api.HealthCheckAppResp
		log.Printf("Received message: %v", msg)
		if err := proto.Unmarshal(msg.Data, &resp); err != nil {
			log.Printf("Failed to unmarshal response: %v", err)
			return false, err
		}
		if len(resp.ErrorMessages) > 0 {
			errorMessages := ""
			for _, errorMsg := range resp.ErrorMessages {
				errorMessages += "|" + errorMsg
			}
			return false, fmt.Errorf("response error messages: %v", err)
		}
		if resp.Success {
			log.Printf("Star container healthchecked: %s, %s", nodeId, name)
			return resp.Healthy, nil
		} else {
			log.Printf("Star container failed to healthcheck, reason unknown: %s, %s", nodeId, name)
			return false, fmt.Errorf("star container failed to healthcheck, reason unknown")
		}
	case <-ctx.Done():
		log.Printf("Timeout while waiting for star container to healthcheck: %s, %s", nodeId, name)
		return false, fmt.Errorf("timeout while waiting for star container to healthcheck")
	}
}

func (u *UpdateServiceGrpcHandler) AvailabilityCheckStarContainer(name string, minReadySeconds int64, extraArgs ...string) (bool, error) {

	if len(extraArgs) < 3 {
		return false, fmt.Errorf("not enough arguments for stopping star container")
	}
	orgId := extraArgs[0]
	namespace := extraArgs[1]
	nodeId := extraArgs[2]

	cmd := api.ApplyAppOperationCommand{
		OrgId:           orgId,
		Namespace:       namespace,
		Name:            name,
		Operation:       "availabilitycheck",
		SelectorLabels:  make(map[string]string),
		MinReadySeconds: 0,
	}
	data, err := proto.Marshal(&cmd)
	if err != nil {
		log.Printf("Failed to marshal command: %v", err)
		return false, fmt.Errorf("failed to marshal command")
	}

	err = u.natsConn.Publish(api.Subject(nodeId), data)
	if err != nil {
		log.Printf("Failed to publish command: %v", err)
		return false, fmt.Errorf("failed to publish command")
	}
	sub, err := nats.NewSubscriber(u.natsConn, nodeId+".app_operation.healthcheck_app."+name, "")
	if err != nil {
		log.Printf("Failed to create subscriber: %v", err)
		return false, err
	}
	respChan := make(chan *natsgo.Msg, 1)
	err = sub.ChannelSubscribe(respChan)
	if err != nil {
		log.Printf("Failed to subscribe to channel: %v", err)
		return false, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case msg := <-respChan:
		var resp api.AvailabilityCheckAppResp
		log.Printf("Received message: %v", msg)
		if err := proto.Unmarshal(msg.Data, &resp); err != nil {
			log.Printf("Failed to unmarshal response: %v", err)
			return false, err
		}
		if len(resp.ErrorMessages) > 0 {
			errorMessages := ""
			for _, errorMsg := range resp.ErrorMessages {
				errorMessages += "|" + errorMsg
			}
			return false, fmt.Errorf("response error messages: %v", err)
		}
		if resp.Success {
			log.Printf("Star container availability checked: %s, %s", nodeId, name)
			return resp.Available, nil
		} else {
			log.Printf("Star container failed to availability check, reason unknown: %s, %s", nodeId, name)
			return false, fmt.Errorf("star container failed to  availability check, reason unknown")
		}
	case <-ctx.Done():
		log.Printf("Timeout while waiting for star container to  availability check: %s, %s", nodeId, name)
		return false, fmt.Errorf("timeout while waiting for star container to  availability check")
	}
}

func (u *UpdateServiceGrpcHandler) QueryNodes(ctx context.Context, orgId string, percentage int32) ([]*magnetarapi.NodeStringified, error) {

	queryReq := &magnetarapi.ListOrgOwnedNodesReq{
		Org: orgId,
	}
	ctx = setOutgoingContext(ctx)
	queryResp, err := u.magnetar.ListOrgOwnedNodes(ctx, queryReq)
	if err != nil {
		log.Printf("Failed to list nodes: %v", err)
		return nil, err
	}

	log.Printf("query.Resp.Nodes: %v", queryResp.Nodes)

	nodes := selectRandomNodes(queryResp.Nodes, percentage)
	return nodes, nil
}

func selectRandomNodes(nodes []*magnetarapi.NodeStringified, percentage int32) []*magnetarapi.NodeStringified {
	totalNodes := len(nodes)
	numberOfNodesToSelect := int(math.Ceil(float64(totalNodes) * float64(percentage) / 100))

	r := rand.New(rand.NewSource(uint64(time.Now().Unix())))

	selectedNodes := make([]*magnetarapi.NodeStringified, 0)

	for i := 0; i < numberOfNodesToSelect; i++ {
		index := r.Intn(len(nodes))
		selectedNodes = append(selectedNodes, nodes[index])
		nodes = append(nodes[:index], nodes[index+1:]...)
	}

	return selectedNodes
}

func setOutgoingContext(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		log.Println("[WARN] no metadata in ctx when sending req")
		return ctx
	}
	return metadata.NewOutgoingContext(ctx, md)
}

func (u *UpdateServiceGrpcHandler) QueryNodesNoAuth(ctx context.Context, orgId string, percentage int32) ([]*magnetarapi.NodeStringified, error) {

	queryReq := &magnetarapi.ListOrgOwnedNodesNoAuthReq{
		Org: orgId,
	}
	ctx = setOutgoingContext(ctx)
	queryResp, err := u.magnetar.ListOrgOwnedNodesNoAuth(ctx, queryReq)
	if err != nil {
		log.Printf("Failed to list nodes: %v", err)
		return nil, err
	}

	log.Printf("query.Resp.Nodes: %v", queryResp.Nodes)

	nodes := selectRandomNodes(queryResp.Nodes, percentage)
	return nodes, nil
}
