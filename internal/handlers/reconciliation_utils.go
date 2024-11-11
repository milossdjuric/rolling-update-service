package handlers

import (
	"context"
	"log"
	"time"
	"update-service/internal/domain"
	"update-service/internal/utils"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
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

func (u *UpdateServiceGrpcHandler) StopDockerContainer(name string) error {

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

func (u *UpdateServiceGrpcHandler) QueryDockerContainer(prefix string, selectorLabels map[string]string) ([]domain.App, error) {

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

func (u *UpdateServiceGrpcHandler) HealthCheckDockerContainer(name string) (bool, error) {

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

func (u *UpdateServiceGrpcHandler) AvailabilityCheckDockerContainer(name string, minReadySeconds int64) (bool, error) {
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
