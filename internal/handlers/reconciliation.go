package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/milossdjuric/rolling_update_service/internal/domain"
	"github.com/milossdjuric/rolling_update_service/internal/utils"
	"github.com/milossdjuric/rolling_update_service/internal/worker"
)

func (u *UpdateServiceGrpcHandler) Reconcile(ctx context.Context, d *domain.Deployment, stopChan chan struct{}) {

	//check if context for gorotine is interrupted
	if IsContextInterrupted(ctx) {
		return
	}

	log.Printf("Starting reconcile for deployment %s", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))

	newRevision, oldRevisions, err := u.GetNewAndOldRevisions(d)
	if err != nil {
		return
	}

	// log.Printf("New revision: %v, Old revisions: %v", newRevision, oldRevisions)
	if IsContextInterrupted(ctx) {
		return
	}

	err = u.revisionRepo.Put(*newRevision)
	if err != nil {
		return
	}

	totalRevisions := append(oldRevisions, *newRevision)

	if IsContextInterrupted(ctx) {
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

	if IsContextInterrupted(ctx) {
		return
	}

	totalApps := make([]domain.App, 0)
	newApps := make([]domain.App, 0)
	readyApps := make([]domain.App, 0)
	availableApps := make([]domain.App, 0)
	oldApps := make([]domain.App, 0)

	// if direct docker daemon, call direct methods, use func for docker containers
	// if node agent direct docker daemon, call direct methods, use func for star containers
	// if node agent indirect docker daemon, call indirect methods, use func for star containers

	if d.Spec.Mode == domain.DirectDockerDaemon || d.Spec.Mode == domain.NodeAgentDirectDockerDaemon {
		totalApps, newApps, readyApps, availableApps, oldApps, err = u.GetAppsDirect(d, newRevision, oldRevisions, nodeIds...)
	} else if d.Spec.Mode == domain.NodeAgentDirectDockerDaemon {
		totalApps, newApps, readyApps, availableApps, oldApps, err = u.GetAppsIndirect(d, newRevision, oldRevisions, nodeIds...)
	}
	if err != nil {
		log.Println("Failed to get apps:", err)
		return
	}

	log.Printf("Total apps: %v, New apps: %v, Ready apps: %v, Available apps: %v, Old apps: %v", int64(len(totalApps)), int64(len(newApps)), int64(len(readyApps)), int64(len(availableApps)), int64(len(oldApps)))

	d.Status.TotalAppCount = int64(len(totalApps))
	d.Status.UpdatedAppCount = int64(len(newApps))
	d.Status.ReadyAppCount = int64(len(readyApps))
	d.Status.AvailableAppCount = int64(len(availableApps))
	d.Status.UnavailableAppCount = d.Status.TotalAppCount - d.Status.AvailableAppCount

	if IsContextInterrupted(ctx) {
		return
	}

	activeRevisions, activeRevisionsAppCount, err := u.GetActiveRevisions(d, totalRevisions, totalApps)
	if err != nil {
		return
	}
	sort.Sort(sort.Reverse(domain.ByCreationTimestamp(activeRevisions)))

	// log.Printf("Active revisions: %v, Active revisions app count: %v", activeRevisions, activeRevisionsAppCount)
	log.Printf("New revison app count: %v", activeRevisionsAppCount[newRevision.Name])

	if IsContextInterrupted(ctx) {
		return
	}

	if d.Status.Stopped {
		stopResChan := make(chan error, 1)
		go func() {
			log.Println("STOPPING deployment")
			err := u.Stop(d, newRevision, oldRevisions, activeRevisions, activeRevisionsAppCount, totalApps, availableApps, nodeIds...)
			stopResChan <- err
			close(stopResChan)
		}()
		go func() {
			for err := range stopResChan {
				if err != nil {
					log.Printf("Error stopping deployment %s: %v", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), err)
				} else {
					log.Printf("Deployment stopping operation successful %s", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))
				}
			}
		}()

		log.Println("AFTER STOPPING GOROUTINES")
		log.Println("Len of total apps:", len(totalApps))
		if len(totalApps) > 0 {
			// Interrupt stopping, so it doesnt send signal to stopChan
			log.Println("Apps still exist, interrupting stop signal")
			return
		}
		//if not marked for deletion, send stop signal, if it is proceed
		if !d.Status.Deleted {
			log.Println("Stop signal sent")
			go func() {
				stopChan <- struct{}{}
			}()
			log.Println("Stop signal sent x2")
			return
		}
	}
	if d.Status.Deleted {
		if len(totalApps) != 0 {
			//interrupt roll
			return
		}
		err := u.revisionRepo.DeleteDeploymentOwnedRevisions(d.Spec.SelectorLabels, d.Namespace, d.OrgId)
		if err != nil {
			log.Printf("Failed to delete deployment owned revisions: %v", err)
		}
		err = u.deploymentRepo.Delete(d.Name, d.Namespace, d.OrgId)
		if err != nil {
			log.Printf("Failed to delete deployment %s: %v", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), err)
		}

		go func() {
			stopChan <- struct{}{}
		}()
		return
	}

	if IsContextInterrupted(ctx) {
		return
	}

	err = UpdateStatusStates(d, activeRevisionsAppCount[newRevision.Name])
	if err != nil {
		return
	}
	log.Printf("Deployment status states: Progress - %v, Available - %v, Failure - %v", d.Status.States[domain.DeploymentProgress].Active, d.Status.States[domain.DeploymentAvailable].Active, d.Status.States[domain.DeploymentFailure].Active)
	log.Printf("Deployment status states messages: Progress - %v, Available - %v, Failure - %v", d.Status.States[domain.DeploymentProgress].Message, d.Status.States[domain.DeploymentAvailable].Message, d.Status.States[domain.DeploymentFailure].Message)

	if IsContextInterrupted(ctx) {
		return
	}

	if d.Status.States[domain.DeploymentFailure].Active && d.Spec.AutomaticRollback {
		log.Printf("Deployment failed, automatically rolling back")
		err := u.Rollback(d, "")
		if err != nil {
			log.Printf("Failed to rollback deployment %s: %v", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), err)
		}
	}

	if IsContextInterrupted(ctx) {
		return
	}

	if d.Status.States[domain.DeploymentProgress].Active &&
		!d.Status.States[domain.DeploymentFailure].Active && !d.Status.Paused {
		go func() {
			log.Println("ROLLING deployment")
			err := u.Roll(d, newRevision, oldRevisions, activeRevisions, activeRevisionsAppCount, totalApps, availableApps, nodeIds...)
			if err != nil {
				log.Printf("Error rolling deployment %s: %v", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), err)
			} else {
				log.Printf("Deployment rolling operation successful %s", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))
			}
		}()
	}

	// if d.Status.States[domain.DeploymentProgress].Active && !d.Status.States[domain.DeploymentFailure].Active && !d.Status.Paused {
	// 	resChan := make(chan error, 1)
	// 	go func() {
	// 		log.Println("ROLLING deployment")
	// 		err := u.Roll(d, newRevision, oldRevisions, activeRevisions, activeRevisionsAppCount, totalApps, availableApps, nodeIds...)
	// 		resChan <- err
	// 	}()
	// 	go func() {
	// 		select {
	// 		case err := <-resChan:
	// 			if err != nil {
	// 				log.Printf("Error rolling deployment %s: %v", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), err)
	// 			} else {
	// 				log.Printf("Deployment rolling operation successfull %s", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))
	// 			}
	// 		}
	// 	}()
	// }

	if IsContextInterrupted(ctx) {
		return
	}

	log.Println("RECONCILE SAVE MOMENT")
	u.SaveDeployment(d)
}

func (u *UpdateServiceGrpcHandler) Stop(d *domain.Deployment, newRevision *domain.Revision, oldRevisions, activeRevisions []domain.Revision, activeRevisionsAppCount map[string]int64, totalApps, availableApps []domain.App, nodeIds ...string) error {
	err := u.StopApps(d, int(len(totalApps)), totalApps, nodeIds...)
	if err != nil {
		log.Println("STOP DEPLOYMENT: stop unavailable apps error:", err)
		return err
	}

	return nil
}

func (u *UpdateServiceGrpcHandler) GetNewAndOldRevisions(d *domain.Deployment) (*domain.Revision, []domain.Revision, error) {

	allRevisions, err := u.revisionRepo.GetDeploymentOwnedRevisions(d.Spec.SelectorLabels, d.Namespace, d.OrgId)
	if err != nil {
		return nil, nil, err
	}

	// log.Println("All revisions:", allRevisions)

	newRevision, err := GetNewRevision(d, allRevisions)
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

func GetNewRevision(d *domain.Deployment, revisions []domain.Revision) (*domain.Revision, error) {

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

func (u *UpdateServiceGrpcHandler) Roll(d *domain.Deployment, newRevision *domain.Revision, oldRevisions, activeRevisions []domain.Revision, activeRevisionsAppCount map[string]int64, totalApps, availableApps []domain.App, nodeIds ...string) error {

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

	err := u.ReconcileNewRevision(d, newRevision, oldRevisions, activeRevisions, activeRevisionsAppCount, newApps, nodeIds...)
	if err != nil {
		return err
	}

	log.Println("Reconciling old revisions")

	err = u.ReconcileOldRevisions(d, newRevision, oldRevisions, activeRevisions, activeRevisionsAppCount, newAvailableAppCount, totalApps, availableApps, nodeIds...)
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

	//update status states
	d.Status.States[domain.DeploymentFailure] = domain.NewDeploymentState(domain.DeploymentFailure, false, "Deployment in rollback", time.Now().Unix(), time.Now().Unix())
	d.Status.States[domain.DeploymentProgress] = domain.NewDeploymentState(domain.DeploymentProgress, true, "Deployment in rollback", time.Now().Unix(), time.Now().Unix())
	d.Status.States[domain.DeploymentAvailable] = domain.NewDeploymentState(domain.DeploymentAvailable, d.Status.States[domain.DeploymentAvailable].Active, d.Status.States[domain.DeploymentProgress].Message, time.Now().Unix(), d.Status.States[domain.DeploymentAvailable].LastTransitionTimestamp)

	return nil
}

func (u *UpdateServiceGrpcHandler) ReconcileNewRevision(d *domain.Deployment, newRevision *domain.Revision, oldRevisions, activeRevisions []domain.Revision, activeRevisionsAppCount map[string]int64, newApps []domain.App, nodeIds ...string) error {

	log.Println("RECONCILE NEW REVISION: deployment app count:", d.Spec.AppCount)
	log.Println("RECONCILE NEW REVISION: new revision app count:", activeRevisionsAppCount[newRevision.Name])

	if d.Spec.AppCount == activeRevisionsAppCount[newRevision.Name] {
		log.Println("RECONCILE NEW REVISION: NO SCALE")
		return nil
	}
	if d.Spec.AppCount < activeRevisionsAppCount[newRevision.Name] {
		//Scale down
		log.Println("RECONCILE NEW REVISION: SCALE DOWN")
		err := u.ScaleRevision(d, newRevision, d.Spec.AppCount, activeRevisionsAppCount, newApps, nodeIds...)
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

	// log.Println("RECONCILE NEW REVISION: current deployment app count:", currentDeploymentAppCount)
	// log.Println("RECONCILE NEW REVISION: max deployment app count:", maxDeploymentAppCount)
	// log.Println("RECONCILE NEW REVISION: max surge:", maxSurge)

	if currentDeploymentAppCount >= maxDeploymentAppCount {
		newRevisionAppCount = activeRevisionsAppCount[newRevision.Name]
	} else {
		scaleUpCount := maxDeploymentAppCount - int64(currentDeploymentAppCount)
		scaleUpCount = min(scaleUpCount, d.Spec.AppCount-activeRevisionsAppCount[newRevision.Name])
		newRevisionAppCount = activeRevisionsAppCount[newRevision.Name] + scaleUpCount
	}

	// log.Println("RECONCILE NEW REVISION: scale up count:", newRevisionAppCount)
	// log.Println("RECONCILE NEW REVISION: new revision app count:", newRevisionAppCount)

	err := u.ScaleRevision(d, newRevision, newRevisionAppCount, activeRevisionsAppCount, newApps, nodeIds...)
	if err != nil {
		return err
	}
	return nil
}

func (u *UpdateServiceGrpcHandler) ReconcileOldRevisions(d *domain.Deployment, newRevision *domain.Revision, oldRevisions, activeRevisions []domain.Revision, activeRevisionsAppCount map[string]int64, newAvailableAppCount int64, totalApps, availableApps []domain.App, nodeIds ...string) error {

	var err error

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

	// log.Println("RECONCILE OLD REVISIONS: old apps count:", oldAppsCount)

	allAppsCount := int64(0)
	allAppsCount += oldAppsCount
	allAppsCount += activeRevisionsAppCount[newRevision.Name]

	// log.Println("RECONCILE OLD REVISIONS: all apps count:", allAppsCount)

	maxUnavailable := d.Spec.Strategy.RollingUpdate.MaxUnavailable
	minAvailable := d.Spec.AppCount - *maxUnavailable

	// log.Println("RECONCILE OLD REVISIONS: max unavailable:", *maxUnavailable)
	// log.Println("RECONCILE OLD REVISIONS: min available:", minAvailable)

	newRevisionUnavailableAppCount := activeRevisionsAppCount[newRevision.Name] - newAvailableAppCount
	maxScaledDown := allAppsCount - minAvailable - newRevisionUnavailableAppCount
	if maxScaledDown <= 0 {
		return nil
	}

	// log.Println("RECONCILE OLD REVISIONS: new revision unavailable app count:")
	// log.Println("RECONCILE OLD REVISIONS: max scaled down:", maxScaledDown)

	oldUnavailableApps := GetOldUnavailableApps(totalApps, availableApps, newRevision)

	// log.Println("RECONCILE OLD REVISIONS: old unavailable apps:", len(oldUnavailableApps))

	// log.Println("RECONCILE OLD REVISIONS: stop unavailable apps CHECKPOINT")

	//Scale down unavailable/unhealthy old apps
	stopAppsArgs := append(nodeIds, d.OrgId)
	stopAppsArgs = append(stopAppsArgs, d.Namespace)

	err = u.StopApps(d, int(maxScaledDown), oldUnavailableApps, stopAppsArgs...)
	if err != nil {
		log.Println("RECONCILE OLD REVISIONS: stop unavailable apps error:", err)
	}

	//Add downscale of available apps
	availableAppsCount := int64(len(availableApps))

	// log.Println("RECONCILE OLD REVISIONS: available apps count:", availableAppsCount)

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

		// log.Printf("RECONCILE OLD REVISIONS: scaling revision %s", revision.Name)
		// log.Println("RECONCILE OLD REVISIONS: new apps count:", newAppsCount)
		err := u.ScaleRevision(d, &revision, newAppsCount, activeRevisionsAppCount, oldApps, nodeIds...)
		if err != nil {
			return err
		}
		// log.Println("RECONCILE OLD REVISIONS: scaled down count:", scaleDownCount)
		totalScaledDown += scaleDownCount
		// log.Println("RECONCILE OLD REVISIONS: total scaled down:", totalScaledDown)
	}

	// log.Println("RECONCILE OLD REVISIONS: final total scaled down:", totalScaledDown)
	totalScaledDown += int64(len(oldUnavailableApps))

	return nil
}

func (u *UpdateServiceGrpcHandler) ScaleRevision(d *domain.Deployment, revision *domain.Revision, newSize int64, activeRevisionsAppCount map[string]int64, apps []domain.App, nodeIds ...string) error {

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

		err := u.StartApps(d, revision, int64(newSize-oldSize), nodeIds...)
		if err != nil {
			return err
		}
	} else {
		//Scale down
		scaleDirection = worker.ScaleDown
		log.Printf("Scaling %s %s, Old size: %d, New size: %d", revision.Name, scaleDirection, oldSize, newSize)

		err := u.StopApps(d, int(oldSize-newSize), apps, nodeIds...)
		if err != nil {
			return err
		}
	}
	return nil
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

func UpdateStatusStates(d *domain.Deployment, availableNewRevisionAppCount int64) error {
	//Should update status, based on its count, previous status, timestamps and other fields

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
		// if IsDeadlineExceeded(d, d.Status.States[domain.DeploymentProgress].LastTransitionTimestamp) && !d.Status.Paused {
		if IsDeadlineExceeded(d, d.Status.States[domain.DeploymentProgress].LastUpdateTimestamp) && !d.Status.Paused {
			log.Println("Deadline now exceeded")
			d.Status.States[domain.DeploymentProgress] = domain.NewDeploymentState(domain.DeploymentProgress, false, "Deadline exceeded", d.Status.States[domain.DeploymentProgress].LastUpdateTimestamp, time.Now().Unix())
			d.Status.States[domain.DeploymentFailure] = domain.NewDeploymentState(domain.DeploymentFailure, true, "Deadline exceeded, failed", d.Status.States[domain.DeploymentFailure].LastUpdateTimestamp, time.Now().Unix())
		} else {
			d.Status.States[domain.DeploymentFailure] = domain.NewDeploymentState(domain.DeploymentFailure, false, "Deployment has not failed", d.Status.States[domain.DeploymentFailure].LastUpdateTimestamp, time.Now().Unix())
		}
	}

	return nil
}
