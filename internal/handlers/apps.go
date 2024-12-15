package handlers

import (
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/milossdjuric/rolling_update_service/internal/domain"
	"github.com/milossdjuric/rolling_update_service/internal/utils"
)

func (u *UpdateServiceGrpcHandler) StartApps(d *domain.Deployment, r *domain.Revision, appCount int64, nodeIds ...string) error {
	// see how should we start the apps based on the deployment mode, direct is for local machine, indirect should be
	// for remote machines
	if d.Spec.Mode == domain.DirectDockerDaemon {
		successes, err := u.StartAppsDirect(appCount, func(extraArgs ...string) error {
			return u.StartDockerContainer(r.Name, r.Spec.AppSpec.SelectorLabels)
		})
		if err != nil {
			return fmt.Errorf("start apps batch error: %w", err)
		}
		log.Println("Successesfuly started apps:", successes)

	} else if d.Spec.Mode == domain.NodeAgentDirectDockerDaemon {
		randomNode, err := GetRandomNodeId(nodeIds)
		if err != nil {
			return err
		}
		startAppsArgs := PrepareAppOperationArgs(d.OrgId, d.Namespace, randomNode)

		successes, err := u.StartAppsDirect(appCount, func(extraArgs ...string) error {
			return u.StartNodeContainer(r.Name, r.Spec.AppSpec.SelectorLabels, extraArgs...)
		}, startAppsArgs...)

		if err != nil {
			return fmt.Errorf("start apps batch error: %w", err)
		}
		log.Println("Successesfuly started apps:", successes)

	} else if d.Spec.Mode == domain.NodeAgentIndirectDockerDaemon {
		startAppsArgs := PrepareAppOperationArgs(d.OrgId, d.Namespace, nodeIds...)

		successes, err := u.StartAppsIndirect(appCount, func(extraArgs ...string) error {
			return u.StartNodeContainer(r.Name, r.Spec.AppSpec.SelectorLabels, extraArgs...)
		}, startAppsArgs...)

		if err != nil {
			return fmt.Errorf("start apps batch error: %w", err)
		}
		log.Println("Successesfuly started apps:", successes)
	} else {
		return fmt.Errorf("invalid deployment mode for starting apps: %s", d.Spec.Mode)
	}
	return nil
}

func (u *UpdateServiceGrpcHandler) StopApps(d *domain.Deployment, appCount int, apps []domain.App, nodeIds ...string) error {
	var err error

	// see how should we stop the apps based on the deployment mode
	if d.Spec.Mode == domain.DirectDockerDaemon {
		err = u.StopAppsDirect(appCount, apps, u.StopDockerContainer)

	} else if d.Spec.Mode == domain.NodeAgentDirectDockerDaemon {
		randomNode, err := GetRandomNodeId(nodeIds)
		if err != nil {
			return err
		}
		stopAppsArgs := PrepareAppOperationArgs(d.OrgId, d.Namespace, randomNode)

		err = u.StopAppsDirect(appCount, apps, u.StopNodeContainer, stopAppsArgs...)

	} else if d.Spec.Mode == domain.NodeAgentIndirectDockerDaemon {
		stopAppsArgs := PrepareAppOperationArgs(d.OrgId, d.Namespace, nodeIds...)

		err = u.StopAppsIndirect(appCount, apps, u.StopNodeContainer, stopAppsArgs...)

	} else {
		return fmt.Errorf("invalid deployment mode: %s", d.Spec.Mode)
	}

	if err != nil {
		return err
	}
	return nil
}

func (u *UpdateServiceGrpcHandler) StartAppsIndirect(appCount int64, fn func(...string) error, extraArgs ...string) (int, error) {
	if len(extraArgs) < 2 {
		return 0, errors.New("not enough extra args")
	}
	orgId, namespace := extraArgs[0], extraArgs[1]

	nodeIds := extraArgs[2:]

	if len(nodeIds) < 1 {
		return 0, errors.New("no nodes available")
	}

	remaining := int(appCount)
	successes := 0
	batchSize := 1

	errChan := make(chan error, batchSize)
	var waitGroup sync.WaitGroup
	for remaining > 0 {
		effectiveBatchSize := min(batchSize, remaining)

		waitGroup.Add(effectiveBatchSize)
		for i := 0; i < effectiveBatchSize; i++ {
			// Select a random nodeId for each goroutine

			randomNode, err := GetRandomNodeId(nodeIds)
			if err != nil {
				errChan <- err
			}

			go func(nodeId string) {
				defer waitGroup.Done()
				// Pass orgId, namespace, and random nodeId as arguments to the function
				if err := fn(orgId, namespace, randomNode); err != nil {
					errChan <- err
				}
			}(randomNode)
		}

		waitGroup.Wait()
		currentSuccesses := effectiveBatchSize - len(errChan)
		successes += currentSuccesses

		if len(errChan) > 0 {
			return successes, <-errChan
		}

		batchSize *= 2
		remaining -= effectiveBatchSize
		errChan = make(chan error, batchSize)
	}
	return successes, nil
}

func (u *UpdateServiceGrpcHandler) StopAppsIndirect(appCount int, apps []domain.App, fn func(name string, extraArgs ...string) error, extraArgs ...string) error {

	if len(extraArgs) < 2 {
		return fmt.Errorf("not enough extra args")
	}
	orgId, namespace := extraArgs[0], extraArgs[1]
	nodeIds := extraArgs[2:]
	if len(nodeIds) == 0 {
		return fmt.Errorf("no nodes available")
	}

	appsToDelete := make([]domain.App, 0)
	if appCount >= len(apps) {
		appsToDelete = append(appsToDelete, apps...)
	} else {
		appsToDelete = append(appsToDelete, apps[len(apps)-appCount:]...)
	}
	if len(appsToDelete) == 0 {
		return nil
	}

	var waitGroup sync.WaitGroup
	var mu sync.Mutex

	deletedApps := 0
	// errChan := make(chan error, len(appsToDelete)*len(nodeIds))
	waitGroup.Add(len(appsToDelete) * len(nodeIds))
	for _, app := range appsToDelete {
		for _, nodeId := range nodeIds {
			go func(targetApp domain.App, nodeId string) {
				defer waitGroup.Done()

				// for !u.rateLimiter.Allow() {
				// 	time.Sleep(5 * time.Millisecond)
				// }

				stopAppArgs := PrepareAppOperationArgs(orgId, namespace, nodeId)
				err := fn(targetApp.Name, stopAppArgs...)
				if err != nil {
					log.Printf("Error stopping app: %v", err)
					// errChan <- err
					return
				}

				// Count only one successful stop per app
				mu.Lock()
				if deletedApps < appCount {
					deletedApps++
				}
				mu.Unlock()
			}(app, nodeId)
		}
	}

	// Wait for all stop attempts
	waitGroup.Wait()

	if deletedApps >= appCount {
		log.Printf("Successfully stopped %d apps", deletedApps)
		return nil
	}
	return fmt.Errorf("failed to stop %d apps", appCount)
}

func (u *UpdateServiceGrpcHandler) GetAppsIndirect(d *domain.Deployment, newRevision *domain.Revision, oldRevisions []domain.Revision, nodeIds ...string) ([]domain.App,
	[]domain.App, []domain.App, []domain.App, []domain.App, error) {

	totalApps := make([]domain.App, 0)
	newApps := make([]domain.App, 0)
	oldApps := make([]domain.App, 0)
	readyApps := make([]domain.App, 0)
	availableApps := make([]domain.App, 0)

	allRevisions := append(oldRevisions, *newRevision)

	var mu sync.Mutex
	var waitGroup sync.WaitGroup

	for _, revision := range allRevisions {
		waitGroup.Add(1)
		go func(revision domain.Revision) {
			defer waitGroup.Done()

			totalRevisionApps, readyRevisionApps, availableRevisionApps, err := u.GetRevisionOwnedAllAppsIndirect(d, &revision, u.QueryNodeAllContainer, nodeIds...)
			if err != nil {
				return
			}
			mu.Lock()
			totalApps = append(totalApps, totalRevisionApps...)
			readyApps = append(readyApps, readyRevisionApps...)
			availableApps = append(availableApps, availableRevisionApps...)
			mu.Unlock()
		}(revision)
	}
	waitGroup.Wait()

	for _, app := range totalApps {
		if utils.MatchLabels(newRevision.Spec.SelectorLabels, app.SelectorLabels) {
			newApps = append(newApps, app)
		} else {
			oldApps = append(oldApps, app)
		}
	}

	return totalApps, newApps, readyApps, availableApps, oldApps, nil
}

func (u *UpdateServiceGrpcHandler) GetReadyAppsIndirect(d *domain.Deployment, apps []domain.App, fn func(string, ...string) (bool, error), nodeIds ...string) ([]domain.App, error) {
	//Get all apps that are ready, readiness is checked by health check, if container is running and healthy
	// it is considered ready
	readyApps := make([]domain.App, 0)
	var mu sync.Mutex
	var waitGroup sync.WaitGroup

	for _, app := range apps {
		for _, nodeId := range nodeIds {
			waitGroup.Add(1)
			go func(app domain.App) {
				defer waitGroup.Done()

				isReady, err := fn(app.Name, d.OrgId, d.Namespace, nodeId)
				if err != nil {
					return
				}
				if isReady {
					mu.Lock()
					readyApps = append(readyApps, app)
					mu.Unlock()
				}
			}(app)
		}
	}
	waitGroup.Wait()

	return readyApps, nil
}

func (u *UpdateServiceGrpcHandler) GetAvailableAppsIndirect(d *domain.Deployment, apps []domain.App, fn func(string, int64, ...string) (bool, error), nodeIds ...string) ([]domain.App, error) {
	//Get all apps that are available, availability is checked by min ready seconds in deployment spec,
	// if app is running for min ready seconds it is considered available
	availableApps := make([]domain.App, 0)
	var mu sync.Mutex
	var waitGroup sync.WaitGroup

	for _, app := range apps {
		for _, nodeId := range nodeIds {
			waitGroup.Add(1)
			go func(app domain.App) {
				defer waitGroup.Done()

				isAvailable, err := fn(app.Name, d.Spec.MinReadySeconds, d.OrgId, d.Namespace, nodeId)
				if err != nil {
					return
				}
				if isAvailable {
					mu.Lock()
					availableApps = append(availableApps, app)
					mu.Unlock()
				}
			}(app)
		}
	}
	waitGroup.Wait()

	return availableApps, nil
}

func (u *UpdateServiceGrpcHandler) GetRevisionOwnedAppsIndirect(d *domain.Deployment, r *domain.Revision, fn func(name string, selectorLables map[string]string, extraArgs ...string) ([]domain.App, error), nodeIds ...string) ([]domain.App, error) {
	//Get all apps that are owned by the given revision
	namePrefix := r.Name
	apps := make([]domain.App, 0)
	var mu sync.Mutex
	var waitGroup sync.WaitGroup

	for _, nodeId := range nodeIds {
		waitGroup.Add(1)
		go func(nodeId string) {
			defer waitGroup.Done()
			nodeApps, err := fn(namePrefix, r.Spec.SelectorLabels, d.OrgId, d.Namespace, nodeId)
			if err != nil {
				return
			}
			mu.Lock()
			apps = append(apps, nodeApps...)
			mu.Unlock()
		}(nodeId)
	}
	waitGroup.Wait()

	return apps, nil
}

func (u *UpdateServiceGrpcHandler) GetRevisionOwnedAvailableAppsIndirect(d *domain.Deployment, r *domain.Revision, fn func(name string, minReadySeconds int64, selectorLables map[string]string, extraArgs ...string) ([]domain.App, error), nodeIds ...string) ([]domain.App, error) {
	//Get all apps that are owned by the given revision
	namePrefix := r.Name
	apps := make([]domain.App, 0)
	var mu sync.Mutex
	var waitGroup sync.WaitGroup

	for _, nodeId := range nodeIds {
		waitGroup.Add(1)
		go func(nodeId string) {
			defer waitGroup.Done()
			nodeApps, err := fn(namePrefix, d.Spec.MinReadySeconds, r.Spec.SelectorLabels, d.OrgId, d.Namespace, nodeId)
			if err != nil {
				return
			}
			mu.Lock()
			apps = append(apps, nodeApps...)
			mu.Unlock()
		}(nodeId)
	}
	waitGroup.Wait()

	return apps, nil
}

func (u *UpdateServiceGrpcHandler) StartAppsDirect(appCount int64, fn func(...string) error, extraArgs ...string) (int, error) {
	remaining := int(appCount)
	successes := 0
	batchSize := 1

	log.Println("Starting apps with batch size:", batchSize)

	errChan := make(chan error, batchSize)
	var waitGroup sync.WaitGroup

	for remaining > 0 {
		effectiveBatchSize := min(batchSize, remaining)

		waitGroup.Add(effectiveBatchSize)
		for i := 0; i < effectiveBatchSize; i++ {
			go func() {
				defer waitGroup.Done()
				if err := fn(extraArgs...); err != nil {
					errChan <- err
				}
			}()
		}

		waitGroup.Wait()
		currentSuccesses := effectiveBatchSize - len(errChan)
		successes += currentSuccesses

		if len(errChan) > 0 {
			return successes, <-errChan
		}

		batchSize *= 2
		remaining -= effectiveBatchSize
		errChan = make(chan error, batchSize)
	}
	return successes, nil
}

func (u *UpdateServiceGrpcHandler) StopAppsDirect(appCount int, apps []domain.App, fn func(name string, extraArgs ...string) error, extraArgs ...string) error {
	appsToDelete := make([]domain.App, 0)
	if appCount >= len(apps) {
		appsToDelete = append(appsToDelete, apps...)
	} else {
		appsToDelete = append(appsToDelete, apps[len(apps)-appCount:]...)
	}

	errChan := make(chan error, len(appsToDelete))
	var waitGroup sync.WaitGroup
	waitGroup.Add(len(appsToDelete))
	for _, app := range appsToDelete {
		go func(targetApp domain.App) {
			defer waitGroup.Done()

			// for !u.rateLimiter.Allow() {
			// 	time.Sleep(5 * time.Millisecond)
			// }

			if err := fn(targetApp.Name, extraArgs...); err != nil {
				errChan <- err
			}
		}(app)
	}
	waitGroup.Wait()
	select {
	case err := <-errChan:
		if err != nil {
			log.Println("Error stopping app:", err)
			return err
		}
	default:
	}
	return nil
}

// get apps, should be used on local machine since its direct, calls either docker daemon on machine
// or calls agent nodes on local machine, in future should be replaced to work with gravity and dissemination
func (u *UpdateServiceGrpcHandler) GetAppsDirect(d *domain.Deployment, newRevision *domain.Revision, oldRevisions []domain.Revision, nodeIds ...string) ([]domain.App,
	[]domain.App, []domain.App, []domain.App, []domain.App, error) {

	totalApps := make([]domain.App, 0)
	newApps := make([]domain.App, 0)
	oldApps := make([]domain.App, 0)
	readyApps := make([]domain.App, 0)
	availableApps := make([]domain.App, 0)

	var err error
	var mu sync.Mutex
	var waitGroup sync.WaitGroup

	withNodeAgent := IsWithNodeAgent(d)
	if withNodeAgent && len(nodeIds) < 1 {
		return nil, nil, nil, nil, nil, fmt.Errorf("no nodes available")
	}
	for _, revision := range append(oldRevisions, *newRevision) {
		waitGroup.Add(1)
		go func(revision domain.Revision) {
			defer waitGroup.Done()

			// for !u.rateLimiter.Allow() {
			// 	time.Sleep(5 * time.Millisecond)
			// }
			totalRevisionApps := make([]domain.App, 0)
			readyRevisionApps := make([]domain.App, 0)
			availableRevisionApps := make([]domain.App, 0)
			if withNodeAgent {
				randomNode, _ := GetRandomNodeId(nodeIds)
				totalRevisionApps, readyRevisionApps, availableRevisionApps, err = GetRevisionOwnedAllAppsDirect(d, &revision, u.QueryNodeAllContainer, randomNode)
			} else {
				totalRevisionApps, readyRevisionApps, availableRevisionApps, err = GetRevisionOwnedAllAppsDirect(d, &revision, u.QueryDockerAllContainer)
			}
			if err != nil {
				return
			}

			mu.Lock()
			totalApps = append(totalApps, totalRevisionApps...)
			readyApps = append(readyApps, readyRevisionApps...)
			availableApps = append(availableApps, availableRevisionApps...)
			mu.Unlock()
		}(revision)
	}
	waitGroup.Wait()

	for _, app := range totalApps {
		if utils.MatchLabels(newRevision.Spec.SelectorLabels, app.SelectorLabels) {
			newApps = append(newApps, app)
		} else {
			oldApps = append(oldApps, app)
		}
	}

	return totalApps, newApps, readyApps, availableApps, oldApps, nil
}

func (u *UpdateServiceGrpcHandler) GetReadyAppsDirect(d *domain.Deployment, apps []domain.App, fn func(string, ...string) (bool, error), nodeIds ...string) ([]domain.App, error) {
	//Get all apps that are ready, readiness could be registered by probing perhaps
	readyApps := make([]domain.App, 0)
	var mu sync.Mutex
	var waitGroup sync.WaitGroup

	for _, app := range apps {
		waitGroup.Add(1)
		go func(app domain.App) {
			defer waitGroup.Done()

			randomNode, err := GetRandomNodeId(nodeIds)
			if err != nil {
				log.Println("Get Ready Apps Direct: get random node id error:", err)
				return
			}
			extraArgs := PrepareAppOperationArgs(d.OrgId, d.Namespace, randomNode)

			// for !u.rateLimiter.Allow() {
			// 	time.Sleep(5 * time.Millisecond)
			// }

			isReady, err := fn(app.Name, extraArgs...)
			if err != nil {
				return
			}
			if isReady {
				mu.Lock()
				readyApps = append(readyApps, app)
				mu.Unlock()
			}
		}(app)
	}
	waitGroup.Wait()

	return readyApps, nil
}

func (u *UpdateServiceGrpcHandler) GetAvailableAppsDirect(d *domain.Deployment, apps []domain.App, fn func(string, int64, ...string) (bool, error), nodeIds ...string) ([]domain.App, error) {
	// gets available apps, calls either docker daemon on machine or calls node agents on local machine
	availableApps := make([]domain.App, 0)
	var mu sync.Mutex
	var waitGroup sync.WaitGroup

	for _, app := range apps {
		waitGroup.Add(1)
		go func(app domain.App) {
			defer waitGroup.Done()

			randomNode, err := GetRandomNodeId(nodeIds)
			if err != nil {
				log.Println("Get Available Apps Direct: get random node id error:", err)
				return
			}
			extraArgs := PrepareAppOperationArgs(d.OrgId, d.Namespace, randomNode)

			// for !u.rateLimiter.Allow() {
			// 	time.Sleep(5 * time.Millisecond)
			// }

			isAvailable, err := fn(app.Name, d.Spec.MinReadySeconds, extraArgs...)
			if err != nil {
				return
			}
			if isAvailable {
				mu.Lock()
				availableApps = append(availableApps, app)
				mu.Unlock()
			}
		}(app)
	}
	waitGroup.Wait()

	return availableApps, nil
}

func GetRevisionOwnedAppsDirect(d *domain.Deployment, r *domain.Revision, fn func(name string, selectorLables map[string]string, extraArgs ...string) ([]domain.App, error), nodeIds ...string) ([]domain.App, error) {
	//Get all apps that are owned by the given revision
	namePrefix := r.Name
	//additional extra args if needed for star node agent method
	getAppsArgs := PrepareAppOperationArgs(d.OrgId, d.Namespace, nodeIds...)

	apps, err := fn(namePrefix, r.Spec.SelectorLabels, getAppsArgs...)
	if err != nil {
		return nil, err
	}
	return apps, nil
}

func GetRevisionOwnedAvailableAppsDirect(d *domain.Deployment, r *domain.Revision, fn func(name string, minReadySeconds int64, selectorLables map[string]string, extraArgs ...string) ([]domain.App, error), nodeIds ...string) ([]domain.App, error) {
	//Get all apps that are owned by the given revision
	namePrefix := r.Name
	//additional extra args if needed for star node agent method
	getAppsArgs := PrepareAppOperationArgs(d.OrgId, d.Namespace, nodeIds...)

	availableApps, err := fn(namePrefix, d.Spec.MinReadySeconds, r.Spec.SelectorLabels, getAppsArgs...)
	if err != nil {
		return nil, err
	}
	return availableApps, nil
}

func GetRevisionOwnedAllAppsDirect(d *domain.Deployment, r *domain.Revision, fn func(name string, minReadySeconds int64, selectorLables map[string]string, extraArgs ...string) ([]domain.App, []domain.App, []domain.App, error), nodeIds ...string) ([]domain.App, []domain.App, []domain.App, error) {
	//Get all apps that are owned by the given revision
	namePrefix := r.Name
	//additional extra args if needed for star node agent method
	getAppsArgs := PrepareAppOperationArgs(d.OrgId, d.Namespace, nodeIds...)

	totalApps, readyApps, availableApps, err := fn(namePrefix, d.Spec.MinReadySeconds, r.Spec.SelectorLabels, getAppsArgs...)
	if err != nil {
		return nil, nil, nil, err
	}
	return totalApps, readyApps, availableApps, nil
}

func (u *UpdateServiceGrpcHandler) GetRevisionOwnedAllAppsIndirect(d *domain.Deployment, r *domain.Revision, fn func(name string, minReadySeconds int64, selectorLables map[string]string, extraArgs ...string) ([]domain.App, []domain.App, []domain.App, error), nodeIds ...string) ([]domain.App, []domain.App, []domain.App, error) {
	//Get all apps that are owned by the given revision
	namePrefix := r.Name
	totalApps, readyApps, availableApps := make([]domain.App, 0), make([]domain.App, 0), make([]domain.App, 0)

	var mu sync.Mutex
	var waitGroup sync.WaitGroup

	for _, nodeId := range nodeIds {
		waitGroup.Add(1)
		go func(nodeId string) {
			defer waitGroup.Done()

			getAppsArgs := PrepareAppOperationArgs(d.OrgId, d.Namespace, nodeId)
			nodeTotalApps, nodeReadyApps, nodeAvailableApps, err := fn(namePrefix, d.Spec.MinReadySeconds, r.Spec.SelectorLabels, getAppsArgs...)
			if err != nil {
				return
			}

			mu.Lock()
			totalApps = append(totalApps, nodeTotalApps...)
			readyApps = append(readyApps, nodeReadyApps...)
			availableApps = append(availableApps, nodeAvailableApps...)
			mu.Unlock()
		}(nodeId)
	}
	waitGroup.Wait()

	return totalApps, readyApps, availableApps, nil
}
