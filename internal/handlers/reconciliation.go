package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/milossdjuric/rolling_update_service/internal/domain"
	"github.com/milossdjuric/rolling_update_service/internal/utils"
	"github.com/milossdjuric/rolling_update_service/internal/worker"
)

func (u *UpdateServiceGrpcHandler) Reconcile(ctx context.Context, d *domain.Deployment) {

	//check if context for gorotine is interrupted
	if u.IsContextInterrupted(ctx) {
		return
	}

	log.Printf("Starting reconcile for deployment %s", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))

	newRevision, oldRevisions, err := u.GetNewAndOldRevisions(d)
	if err != nil {
		return
	}

	// log.Printf("New revision: %v, Old revisions: %v", newRevision, oldRevisions)
	if u.IsContextInterrupted(ctx) {
		return
	}

	err = u.revisionRepo.Put(*newRevision)
	if err != nil {
		return
	}

	totalRevisions := append(oldRevisions, *newRevision)

	if u.IsContextInterrupted(ctx) {
		return
	}

	nodes, err := u.QueryNodes(ctx, d.OrgId, 100)
	if err != nil {
		log.Printf("Failed to query nodes: %v", err)
		return
	}

	nodeIds := make([]string, 0)
	for _, node := range nodes {
		nodeIds = append(nodeIds, node.Id)
	}

	if u.IsContextInterrupted(ctx) {
		return
	}

	totalApps, newApps, readyApps, availableApps, oldApps, err := u.GetApps(d, newRevision, oldRevisions, nodeIds...)
	if err != nil {
		return
	}
	log.Printf("Total apps: %v, New apps: %v, Ready apps: %v, Available apps: %v, Old apps: %v", int64(len(totalApps)), int64(len(newApps)), int64(len(readyApps)), int64(len(availableApps)), int64(len(oldApps)))

	d.Status.TotalAppCount = int64(len(totalApps))
	d.Status.UpdatedAppCount = int64(len(newApps))
	d.Status.ReadyAppCount = int64(len(readyApps))
	d.Status.AvailableAppCount = int64(len(availableApps))
	d.Status.UnavailableAppCount = d.Status.TotalAppCount - d.Status.AvailableAppCount

	if u.IsContextInterrupted(ctx) {
		return
	}

	activeRevisions, activeRevisionsAppCount, err := u.GetActiveRevisions(d, totalRevisions, totalApps)
	if err != nil {
		return
	}
	sort.Sort(sort.Reverse(domain.ByCreationTimestamp(activeRevisions)))

	// log.Printf("Active revisions: %v, Active revisions app count: %v", activeRevisions, activeRevisionsAppCount)
	log.Printf("New revison app count: %v", activeRevisionsAppCount[newRevision.Name])

	if u.IsContextInterrupted(ctx) {
		return
	}

	err = u.UpdateStatusStates(d, activeRevisionsAppCount[newRevision.Name])
	if err != nil {
		return
	}
	log.Printf("Deployment status states: Progress - %v, Available - %v, Failure - %v", d.Status.States[domain.DeploymentProgress].Active, d.Status.States[domain.DeploymentAvailable].Active, d.Status.States[domain.DeploymentFailure].Active)
	log.Printf("Deployment status states messages: Progress - %v, Available - %v, Failure - %v", d.Status.States[domain.DeploymentProgress].Message, d.Status.States[domain.DeploymentAvailable].Message, d.Status.States[domain.DeploymentFailure].Message)

	if u.IsContextInterrupted(ctx) {
		return
	}

	// if len(totalRevisions)%3 == 0 && len(totalRevisions) > 0 {
	// 	log.Println("TOO MANY REVS TRIGGERED")
	// 	log.Println("Automatic ROllback: ", d.Spec.AutomaticRollback)
	// 	d.Status.States[domain.DeploymentFailure] = domain.NewDeploymentState(domain.DeploymentFailure, true, "Too many revisions", time.Now().Unix(), time.Now().Unix())
	// }

	if d.Status.States[domain.DeploymentFailure].Active && d.Spec.AutomaticRollback {
		log.Printf("Deployment failed, automatically rolling back")
		err := u.Rollback(d, "")
		if err != nil {
			log.Printf("Failed to rollback deployment %s: %v", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), err)
		}
	}

	if u.IsContextInterrupted(ctx) {
		return
	}

	if d.Status.States[domain.DeploymentProgress].Active && !d.Status.States[domain.DeploymentFailure].Active && !d.Status.Paused {
		resChan := make(chan error, 1)
		go func() {
			log.Println("ROLLING deployment")
			err := u.Roll(d, newRevision, oldRevisions, activeRevisions, activeRevisionsAppCount, totalApps, availableApps)
			resChan <- err
		}()
		go func() {
			select {
			case err := <-resChan:
				if err != nil {
					log.Printf("Error rolling deployment %s: %v", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), err)
				} else {
					log.Printf("Deployment rolling operation successfull %s", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))
				}
			}
		}()
	}

	if u.IsContextInterrupted(ctx) {
		return
	}

	u.SaveDeployment(d)
}

func (u *UpdateServiceGrpcHandler) GetNewAndOldRevisions(d *domain.Deployment) (*domain.Revision, []domain.Revision, error) {

	allRevisions, err := u.revisionRepo.GetDeploymentOwnedRevisions(d.Spec.SelectorLabels, d.Namespace, d.OrgId)
	if err != nil {
		return nil, nil, err
	}

	// log.Println("All revisions:", allRevisions)

	newRevision, err := u.GetNewRevision(d, allRevisions)
	// log.Printf("New Revision: %v", newRevision)
	if err != nil {
		return nil, nil, err
	}
	oldRevisions := make([]domain.Revision, 0)
	for _, revision := range allRevisions {
		if newRevision == nil || !revision.CompareRevisions(*newRevision) {
			// log.Printf("Old Revision: %v", revision)
			oldRevisions = append(oldRevisions, revision)
		}
	}

	return newRevision, oldRevisions, nil
}

func (u *UpdateServiceGrpcHandler) GetNewRevision(d *domain.Deployment, revisions []domain.Revision) (*domain.Revision, error) {

	log.Println("Getting new revision")

	sort.Sort(sort.Reverse(domain.ByCreationTimestamp(revisions)))
	if len(revisions) != 0 && d.Spec.AppSpec.CompareAppSpecs(revisions[0].Spec.AppSpec) {
		log.Println("Returned existing revision")
		return &revisions[0], nil
	}
	//TODO: Should check something for new revision and old revisions
	log.Println("Returned new revision")
	newRevision := domain.NewRevisionFromDeployment(*d)
	if newRevision.Name == "" || newRevision.Namespace == "" || newRevision.OrgId == "" {
		return nil, errors.New("new revision is nil")
	}

	return &newRevision, nil
}

func (u *UpdateServiceGrpcHandler) Roll(d *domain.Deployment, newRevision *domain.Revision, oldRevisions, activeRevisions []domain.Revision, activeRevisionsAppCount map[string]int64, totalApps, availableApps []domain.App, extraArgs ...string) error {

	log.Println("Rolling deployment")

	newApps := make([]domain.App, 0)
	for _, app := range totalApps {
		if utils.MatchLabels(newRevision.Spec.SelectorLabels, app.SelectorLabels) {
			newApps = append(newApps, app)
		}
	}
	newAvailableAppCount := int64(0)
	for _, app := range availableApps {
		if utils.MatchLabels(newRevision.Spec.SelectorLabels, app.SelectorLabels) {
			newAvailableAppCount++
		}
	}

	log.Println("Reconciling new revision")

	err := u.ReconcileNewRevision(d, newRevision, oldRevisions, activeRevisions, activeRevisionsAppCount, newApps, extraArgs...)
	if err != nil {
		return err
	}

	log.Println("Reconciling old revisions")

	err = u.ReconcileOldRevisions(d, newRevision, oldRevisions, activeRevisions, activeRevisionsAppCount, newAvailableAppCount, totalApps, availableApps, extraArgs...)
	if err != nil {
		return err
	}

	return nil
}

func (u *UpdateServiceGrpcHandler) Rollback(d *domain.Deployment, revisionName string) error {

	newRevision, oldRevisions, err := u.GetNewAndOldRevisions(d)
	if err != nil {
		log.Printf("Failed to get new and old revisions: %v", err)
	}

	allRevisions := append(oldRevisions, *newRevision)
	var rollbackRevision *domain.Revision

	//if no revision is specififed, rollback to last revision
	if revisionName == "" {
		sort.Sort(sort.Reverse(domain.ByCreationTimestamp(allRevisions)))
		//if there is no revision to rollback to, return
		if len(allRevisions) < 2 {
			log.Printf("No revision to rollback to")
			return errors.New("no revision to rollback to")
		}
		rollbackRevision = &allRevisions[1]
	} else {
		rollbackRevision, err = u.revisionRepo.Get(revisionName, d.Namespace, d.OrgId)
		if err != nil {
			log.Printf("Failed to get rollback revision: %v", err)
			return errors.New("failed to get rollback revision")
		}
	}
	delete(rollbackRevision.Spec.SelectorLabels, "revision")
	delete(rollbackRevision.Spec.AppSpec.SelectorLabels, "revision")
	rollbackRevision.Name = utils.GenerateUniqueName(d.Name)
	rollbackRevision.Spec.SelectorLabels["revision"] = rollbackRevision.Name
	rollbackRevision.Spec.AppSpec.SelectorLabels["revision"] = rollbackRevision.Name
	rollbackRevision.CreationTimestamp = time.Now().Unix()

	u.revisionRepo.Put(*rollbackRevision)

	//set appSpec of deployment to that of rollback revision
	d.Spec.AppSpec = rollbackRevision.Spec.AppSpec

	// if revisions number surpasses revision limit, delete oldest revisions first
	if len(allRevisions)+1 > int(*d.Spec.RevisionLimit) {
		log.Println("Deleting oldest revisions")
		sufficientRevisions := len(allRevisions) + 1 - int(*d.Spec.RevisionLimit)
		if sufficientRevisions <= len(allRevisions) {
			oldestRevisions := allRevisions[len(allRevisions)-sufficientRevisions:]
			for _, oldestRevision := range oldestRevisions {
				u.revisionRepo.Delete(oldestRevision.Name, oldestRevision.Namespace, oldestRevision.OrgId)
			}
		}
	}

	//update
	d.Status.States[domain.DeploymentFailure] = domain.NewDeploymentState(domain.DeploymentFailure, false, "Deployment in rollback", time.Now().Unix(), time.Now().Unix())
	d.Status.States[domain.DeploymentProgress] = domain.NewDeploymentState(domain.DeploymentProgress, true, "Deployment in rollback", time.Now().Unix(), time.Now().Unix())
	d.Status.States[domain.DeploymentAvailable] = domain.NewDeploymentState(domain.DeploymentAvailable, d.Status.States[domain.DeploymentAvailable].Active, d.Status.States[domain.DeploymentProgress].Message, time.Now().Unix(), d.Status.States[domain.DeploymentAvailable].LastTransitionTimestamp)

	return nil
}

func (u *UpdateServiceGrpcHandler) ReconcileNewRevision(d *domain.Deployment, newRevision *domain.Revision, oldRevisions, activeRevisions []domain.Revision, activeRevisionsAppCount map[string]int64, newApps []domain.App, extraArgs ...string) error {

	log.Println("RECONCILE NEW REVISION: deployment app count:", d.Spec.AppCount)
	log.Println("RECONCILE NEW REVISION: new revision app count:", activeRevisionsAppCount[newRevision.Name])

	if d.Spec.AppCount == activeRevisionsAppCount[newRevision.Name] {
		log.Println("RECONCILE NEW REVISION: NO SCALE")
		return nil
	}
	if d.Spec.AppCount < activeRevisionsAppCount[newRevision.Name] {
		//Scale down
		log.Println("RECONCILE NEW REVISION: SCALE DOWN")
		err := u.ScaleRevision(d, newRevision, d.Spec.AppCount, activeRevisionsAppCount, newApps)
		if err != nil {
			return err
		}
		return nil
	}

	//Scale up
	log.Println("RECONCILE NEW REVISION: SCALE UP")
	newRevisionAppCount := int64(0)

	currentDeploymentAppCount := int64(0)
	maxSurge := *d.Spec.Strategy.RollingUpdate.MaxSurge
	for _, revision := range activeRevisions {
		currentDeploymentAppCount += activeRevisionsAppCount[revision.Name]
	}
	// currentDeploymentAppCount := int64(len(activeRevisionsAppCount))
	maxDeploymentAppCount := d.Spec.AppCount + maxSurge

	log.Println("RECONCILE NEW REVISION: current deployment app count:", currentDeploymentAppCount)
	log.Println("RECONCILE NEW REVISION: max deployment app count:", maxDeploymentAppCount)
	log.Println("RECONCILE NEW REVISION: max surge:", maxSurge)

	if currentDeploymentAppCount >= maxDeploymentAppCount {
		newRevisionAppCount = activeRevisionsAppCount[newRevision.Name]
	} else {
		scaleUpCount := maxDeploymentAppCount - int64(currentDeploymentAppCount)
		scaleUpCount = min(scaleUpCount, d.Spec.AppCount-activeRevisionsAppCount[newRevision.Name])
		newRevisionAppCount = activeRevisionsAppCount[newRevision.Name] + scaleUpCount
	}

	log.Println("RECONCILE NEW REVISION: scale up count:", newRevisionAppCount)
	log.Println("RECONCILE NEW REVISION: new revision app count:", newRevisionAppCount)

	err := u.ScaleRevision(d, newRevision, newRevisionAppCount, activeRevisionsAppCount, newApps)
	if err != nil {
		return err
	}
	return nil
}

func (u *UpdateServiceGrpcHandler) ReconcileOldRevisions(d *domain.Deployment, newRevision *domain.Revision, oldRevisions, activeRevisions []domain.Revision, activeRevisionsAppCount map[string]int64, newAvailableAppCount int64, totalApps, availableApps []domain.App, extraArgs ...string) error {

	//old apps count
	oldAppsCount := int64(0)
	oldApps := make([]domain.App, 0)
	for _, revision := range oldRevisions {
		oldAppsCount += activeRevisionsAppCount[revision.Name]
		for _, app := range totalApps {
			if utils.MatchLabels(revision.Spec.SelectorLabels, app.SelectorLabels) {
				oldApps = append(oldApps, app)
			}
		}
	}

	log.Println("RECONCILE OLD REVISIONS: old apps count:", oldAppsCount)

	allAppsCount := int64(0)
	allAppsCount += oldAppsCount
	allAppsCount += activeRevisionsAppCount[newRevision.Name]

	log.Println("RECONCILE OLD REVISIONS: all apps count:", allAppsCount)

	maxUnavailable := d.Spec.Strategy.RollingUpdate.MaxUnavailable
	minAvailable := d.Spec.AppCount - *maxUnavailable

	log.Println("RECONCILE OLD REVISIONS: max unavailable:", *maxUnavailable)
	log.Println("RECONCILE OLD REVISIONS: min available:", minAvailable)

	newRevisionUnavailableAppCount := activeRevisionsAppCount[newRevision.Name] - newAvailableAppCount
	maxScaledDown := allAppsCount - minAvailable - newRevisionUnavailableAppCount
	if maxScaledDown <= 0 {
		return nil
	}

	log.Println("RECONCILE OLD REVISIONS: new revision unavailable app count:")
	log.Println("RECONCILE OLD REVISIONS: max scaled down:", maxScaledDown)

	oldUnavailableApps := GetOldUnavailableApps(totalApps, availableApps, newRevision)

	log.Println("RECONCILE OLD REVISIONS: old unavailable apps:", len(oldUnavailableApps))

	log.Println("RECONCILE OLD REVISIONS: stop unavailable apps CHECKPOINT")

	//Scale down unavailable/unhealthy old apps
	err := u.StopApps(int(maxScaledDown), oldUnavailableApps, u.StopStarContainer, extraArgs...)
	if err != nil {
		log.Println("RECONCILE OLD REVISIONS: stop unavailable apps error:", err)
		return err
	}

	//Add downscale of available apps
	availableAppsCount := int64(len(availableApps))

	log.Println("RECONCILE OLD REVISIONS: available apps count:", availableAppsCount)

	totalScaledDown := int64(0)
	totalScaledDownCount := availableAppsCount - minAvailable
	for _, revision := range oldRevisions {
		if totalScaledDown >= totalScaledDownCount {
			log.Println("RECONCILE OLD REVISIONS: total scaled down reached on count")
			break
		}

		if activeRevisionsAppCount[revision.Name] == 0 {
			log.Println("RECONCILE OLD REVISIONS: active revision app count is 0, revision:", revision.Name)
			continue
		}

		scaleDownCount := min(activeRevisionsAppCount[revision.Name], totalScaledDownCount-totalScaledDown)
		newAppsCount := activeRevisionsAppCount[revision.Name] - scaleDownCount
		if newAppsCount > activeRevisionsAppCount[revision.Name] {
			return errors.New("invalid request to scale down")
		}

		log.Printf("RECONCILE OLD REVISIONS: scaling revision %s", revision.Name)
		log.Println("RECONCILE OLD REVISIONS: new apps count:", newAppsCount)
		err := u.ScaleRevision(d, &revision, newAppsCount, activeRevisionsAppCount, oldApps)
		if err != nil {
			return err
		}
		log.Println("RECONCILE OLD REVISIONS: scaled down count:", scaleDownCount)
		totalScaledDown += scaleDownCount
		log.Println("RECONCILE OLD REVISIONS: total scaled down:", totalScaledDown)
	}

	log.Println("RECONCILE OLD REVISIONS: final total scaled down:", totalScaledDown)
	totalScaledDown += int64(len(oldUnavailableApps))

	return nil
}

func (u *UpdateServiceGrpcHandler) ScaleRevision(d *domain.Deployment, revision *domain.Revision, newSize int64, activeRevisionsAppCount map[string]int64, apps []domain.App, extraArgs ...string) error {

	log.Printf("Scaling revision %s", revision.Name)

	if activeRevisionsAppCount[revision.Name] == newSize {
		//Nothing to scale
		return nil
	}

	oldSize := activeRevisionsAppCount[revision.Name]
	var scaleDirection string
	if activeRevisionsAppCount[revision.Name] < newSize {
		//Scale up
		scaleDirection = worker.ScaleUp
		log.Printf("Scaling %s %s, Old size: %d, New size: %d", revision.Name, scaleDirection, oldSize, newSize)
		successes, err := u.StartAppsBatch(newSize-oldSize, func(...string) error {
			// change method for start apps if needed
			// return u.StartDockerContainer(revision.Name, revision.Spec.AppSpec.SelectorLabels)
			return u.StartStarContainer(revision.Name, revision.Spec.AppSpec.SelectorLabels, extraArgs...)
		}, extraArgs...)
		if err != nil {
			log.Println("Start apps batch error:", err)
			return err
		}
		log.Println("Successes number:", successes)
	} else {
		//Scale down
		scaleDirection = worker.ScaleDown
		log.Printf("Scaling %s %s, Old size: %d, New size: %d", revision.Name, scaleDirection, oldSize, newSize)
		err := u.StopApps(int(oldSize-newSize), apps, u.StopStarContainer, extraArgs...)
		if err != nil {
			return err
		}
	}
	return nil
}

func (u *UpdateServiceGrpcHandler) StopApps(appCount int, apps []domain.App, fn func(name string, extraArgs ...string) error, extraArgs ...string) error {

	log.Printf("Apps len: %d", len(apps))

	appsToDelete := make([]domain.App, 0)
	if appCount >= len(apps) {
		appsToDelete = append(appsToDelete, apps...)
	} else {
		appsToDelete = append(appsToDelete, apps[len(apps)-appCount:]...)
	}

	log.Printf("Stopping %d apps", len(appsToDelete))

	errChan := make(chan error, len(appsToDelete))
	var waitGroup sync.WaitGroup

	waitGroup.Add(len(appsToDelete))
	for _, app := range appsToDelete {

		go func(targetApp domain.App) {
			defer waitGroup.Done()
			if err := fn(targetApp.Name); err != nil {
				errChan <- err
			}
		}(app)
	}
	waitGroup.Wait()
	select {
	case err := <-errChan:
		if err != nil {
			return err
		}
	default:
	}
	return nil
}

func (u *UpdateServiceGrpcHandler) StartAppsBatch(appCount int64, fn func(...string) error, extraArgs ...string) (int, error) {

	remaining := int(appCount)
	successes := 0
	batchSize := 1

	log.Println("START APPS BATCH: appCount ", appCount)

	errChan := make(chan error, batchSize)
	var waitGroup sync.WaitGroup

	for remaining > 0 {

		effectiveBatchSize := min(batchSize, remaining)
		log.Println("Effective Batch size:", effectiveBatchSize)

		waitGroup.Add(effectiveBatchSize)
		for i := 0; i < effectiveBatchSize; i++ {
			go func() {
				defer waitGroup.Done()
				if err := fn(); err != nil {
					errChan <- err
				}
			}()
		}

		waitGroup.Wait()
		currentSuccesses := batchSize - len(errChan)
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

func (u *UpdateServiceGrpcHandler) GetActiveRevisions(d *domain.Deployment, revisions []domain.Revision, apps []domain.App) ([]domain.Revision, map[string]int64, error) {
	// Get active revisions, those are revisions which contain selector labels that match the labels of apps
	log.Println("Getting active revisions")

	activeRevisionsAppCount := make(map[string]int64)
	appLabels := make([]*domain.App, 0)
	for _, app := range apps {
		appLabels = append(appLabels, &app)
	}

	// log.Printf("App labels: %v", appLabels)

	activeRevisions := make([]domain.Revision, 0)
	for _, revision := range revisions {

		appCount := CountMatchingAppsForRevisons(&revision, appLabels)
		if appCount > 0 {
			activeRevisions = append(activeRevisions, revision)
			activeRevisionsAppCount[revision.Name] = appCount
			// log.Printf("Active revision: %v, App count: %d", &revision, appCount)
		}
	}

	return activeRevisions, activeRevisionsAppCount, nil
}

func (u *UpdateServiceGrpcHandler) UpdateStatusStates(d *domain.Deployment, availableNewRevisionAppCount int64) error {
	//Should update status, based on its count, previous status, timestamps and other fields

	// d.Status.AvailableAppCount == d.Status.TotalAppCount &&

	if d.Status.AvailableAppCount >= d.Spec.AppCount-*d.Spec.Strategy.RollingUpdate.MaxUnavailable && d.Status.AvailableAppCount > 0 {
		d.Status.States[domain.DeploymentAvailable] = domain.NewDeploymentState(domain.DeploymentAvailable, true, "Deployment available", d.Status.States[domain.DeploymentAvailable].LastUpdateTimestamp, time.Now().Unix())
		log.Println("Deployment now available")
		if d.Status.AvailableAppCount == availableNewRevisionAppCount {
			d.Status.States[domain.DeploymentProgress] = domain.NewDeploymentState(domain.DeploymentProgress, false, "Deployment rollout completed", d.Status.States[domain.DeploymentProgress].LastUpdateTimestamp, time.Now().Unix())
		} else {
			d.Status.States[domain.DeploymentProgress] = domain.NewDeploymentState(domain.DeploymentProgress, true, "Deployment in progress", d.Status.States[domain.DeploymentProgress].LastUpdateTimestamp, time.Now().Unix())
		}
	} else {
		d.Status.States[domain.DeploymentAvailable] = domain.NewDeploymentState(domain.DeploymentAvailable, false, "Deployment not available", d.Status.States[domain.DeploymentAvailable].LastUpdateTimestamp, time.Now().Unix())
		d.Status.States[domain.DeploymentProgress] = domain.NewDeploymentState(domain.DeploymentProgress, true, "Deployment in progress", d.Status.States[domain.DeploymentProgress].LastUpdateTimestamp, time.Now().Unix())
	}

	if d.Status.States[domain.DeploymentProgress].Active {
		log.Println("Deployment in progress")
		if u.IsDeadlineExceeded(d, d.Status.States[domain.DeploymentProgress].LastTransitionTimestamp) && !d.Status.Paused {
			log.Println("Deadline now exceeded")
			d.Status.States[domain.DeploymentProgress] = domain.NewDeploymentState(domain.DeploymentProgress, false, "Deadline exceeded", d.Status.States[domain.DeploymentProgress].LastUpdateTimestamp, time.Now().Unix())
			d.Status.States[domain.DeploymentFailure] = domain.NewDeploymentState(domain.DeploymentFailure, true, "Deadline exceeded", d.Status.States[domain.DeploymentFailure].LastUpdateTimestamp, time.Now().Unix())
		} else {
			d.Status.States[domain.DeploymentFailure] = domain.NewDeploymentState(domain.DeploymentFailure, false, "Deployment has not failed", d.Status.States[domain.DeploymentFailure].LastUpdateTimestamp, time.Now().Unix())
		}
	}

	return nil
}

func (u *UpdateServiceGrpcHandler) GetRevisionOwnedApps(d *domain.Deployment, r *domain.Revision, fn func(name string, selectorLables map[string]string, extraArgs ...string) ([]domain.App, error), nodeIds ...string) ([]domain.App, error) {
	//Get all apps that are owned by the given revision
	namePrefix := r.Name

	// apps, err := fn(namePrefix, r.Spec.SelectorLabels)
	// if err != nil {
	// 	return nil, err
	// }

	apps := make([]domain.App, 0)
	var mu sync.Mutex
	var waitGroup sync.WaitGroup
	for _, nodeId := range nodeIds {
		waitGroup.Add(1)
		go func(nodeId string) {
			defer waitGroup.Done()
			nodeApps, err := fn(namePrefix, r.Spec.SelectorLabels, nodeId)
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

func (u *UpdateServiceGrpcHandler) GetReadyApps(d *domain.Deployment, apps []domain.App, fn func(string, ...string) (bool, error), extraArgs ...string) ([]domain.App, error) {
	//Get all apps that are ready, readiness could be registered by probing perhaps
	readyApps := make([]domain.App, 0)
	var mu sync.Mutex
	var waitGroup sync.WaitGroup

	for _, app := range apps {
		waitGroup.Add(1)
		go func(app domain.App) {
			defer waitGroup.Done()

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

func (u *UpdateServiceGrpcHandler) GetAvailableApps(d *domain.Deployment, apps []domain.App, fn func(string, int64, ...string) (bool, error), extraArgs ...string) ([]domain.App, error) {
	//Get all apps that are available, availability could be registered by min ready seconds passing, could perhaps need an
	//app registry of sorts
	availableApps := make([]domain.App, 0)
	var mu sync.Mutex
	var waitGroup sync.WaitGroup

	for _, app := range apps {
		waitGroup.Add(1)
		go func(app domain.App) {
			defer waitGroup.Done()

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

func (u *UpdateServiceGrpcHandler) GetApps(d *domain.Deployment, newRevision *domain.Revision, oldRevisions []domain.Revision, nodeIds ...string) ([]domain.App,
	[]domain.App, []domain.App, []domain.App, []domain.App, error) {

	totalApps := make([]domain.App, 0)
	newApps := make([]domain.App, 0)
	oldApps := make([]domain.App, 0)

	var mu sync.Mutex
	var waitGroup sync.WaitGroup

	for _, revision := range append(oldRevisions, *newRevision) {
		waitGroup.Add(1)
		go func(revision domain.Revision) {
			defer waitGroup.Done()
			revisionApps, err := u.GetRevisionOwnedApps(d, &revision, u.QueryStarContainers, nodeIds...)
			if err != nil {
				return
			}
			mu.Lock()
			totalApps = append(totalApps, revisionApps...)
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

	// newApps, err := u.GetRevisionOwnedApps(d, newRevision, u.QueryDockerContainer)
	// if err != nil {
	// 	return nil, nil, nil, nil, nil, err
	// }

	// totalApps = append(totalApps, newApps...)

	// for _, revision := range oldRevisions {
	// 	oldRevisionApps, err := u.GetRevisionOwnedApps(d, &revision, u.QueryDockerContainer)
	// 	if err != nil {
	// 		return nil, nil, nil, nil, nil, err
	// 	}
	// 	oldApps = append(oldApps, oldRevisionApps...)
	// 	totalApps = append(totalApps, oldRevisionApps...)
	// }

	readyApps, err := u.GetReadyApps(d, totalApps, u.HealthCheckStarContainer)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	availableApps, err := u.GetAvailableApps(d, readyApps, u.AvailabilityCheckStarContainer)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	return totalApps, newApps, readyApps, availableApps, oldApps, nil
}

// func (u *UpdateServiceGrpcHandler) Scale(d *domain.Deployment, newRevision *domain.Revision, activeRevisions []domain.Revision, activeRevisionsAppCount map[*domain.Revision]int64) error {

// isNewRevisionActive := false
// for _, revision := range activeRevisions {
// 	if newRevision.CompareRevisions(revision) {
// 		isNewRevisionActive = true
// 		break
// 	}
// }

// //If no active revisions or only new revision is active, scale
// if len(activeRevisions) == 0 || len(activeRevisions) == 1 && isNewRevisionActive {
// 	//Total app count matches specified app count
// 	if d.Spec.AppCount == d.Status.TotalAppCount {
// 		return nil
// 	}
// 	//Scale
// }

// //If new revision is active, updated apps match the spec number of apps, but total apps are more than spec number of apps
// if isNewRevisionActive && d.Status.TotalAppCount > d.Spec.AppCount && d.Status.UpdatedAppCount == d.Spec.AppCount {

// 	for _, revision := range activeRevisions {
// 		//skip scaling for new revision, should downscale others
// 		if revision.Name == newRevision.Name && revision.Spec.AppSpec.CompareAppSpecs(newRevision.Spec.AppSpec) {
// 			continue
// 		}
// 		//Scale
// 	}
// }

// if d.Spec.Strategy.Type == domain.RollingUpdateStrategy {

// 	allowedSize := int64(0)

// 	if d.Spec.AppCount > 0 {
// 		allowedSize = d.Spec.AppCount + *d.Spec.Strategy.RollingUpdate.MaxSurge
// 	}

// 	appsToAdd := allowedSize - d.Status.TotalAppCount

// 	var scaleDirection string
// 	if appsToAdd > 0 {
// 		scaleDirection = worker.ScaleUp
// 	}
// 	if appsToAdd < 0 {
// 		scaleDirection = worker.ScaleDown
// 	}
// 	log.Printf("Scaling %s", scaleDirection)

// 	appsAdded := int64(0)
// 	revisionNameToSize := make(map[string]int64)

// 	for i := range activeRevisions {
// 		revision := activeRevisions[i]

// 		if appsToAdd != 0 {
// 			revisionProportion, err := u.GetRevisionProportion(d, &revision, appsToAdd, appsAdded, activeRevisionsAppCount[&revision])
// 			if err != nil {
// 				return err
// 			}

// 			revisionNameToSize[revision.Name] = activeRevisionsAppCount[&revision] + revisionProportion
// 			appsAdded += revisionProportion
// 		} else {
// 			revisionNameToSize[revision.Name] = activeRevisionsAppCount[&revision]
// 		}
// 	}

// 	for i := range activeRevisions {
// 		revision := activeRevisions[i]

// 		if i == 0 && appsToAdd != 0 {
// 			leftover := appsToAdd - appsAdded
// 			revisionNameToSize[revision.Name] += leftover
// 			if revisionNameToSize[revision.Name] < 0 {
// 				revisionNameToSize[revision.Name] = 0
// 			}
// 		}

// 		//Scale
// 	}
// }

// 	return nil
// }

// func (u *UpdateServiceGrpcHandler) GetRevisionProportion(d *domain.Deployment, revision *domain.Revision, appsToAdd, appsAdded, revisionAppCount int64) (int64, error) {

// 	if revision == nil || appsToAdd == 0 {
// 		return 0, nil
// 	}

// 	allowed := appsToAdd - appsAdded
// 	revisionProportion := u.CalculateRevisionProportion(d, revision, revisionAppCount)

// 	if appsToAdd > 0 {
// 		return min(allowed, revisionProportion), nil
// 	}

// 	return max(allowed, revisionProportion), nil
// }

// func (u *UpdateServiceGrpcHandler) CalculateRevisionProportion(d *domain.Deployment, revision *domain.Revision, revisionAppCount int64) int64 {

// 	if d.Spec.AppCount == int64(0) {
// 		return -revisionAppCount
// 	}

// 	apps := d.Spec.AppCount + *d.Spec.Strategy.RollingUpdate.MaxSurge

// 	newRevisionAppCount := (float64(revisionAppCount) * float64(apps)) / float64(d.Status.TotalAppCount)
// 	return int64(math.Round(newRevisionAppCount)) - revisionAppCount
// }
