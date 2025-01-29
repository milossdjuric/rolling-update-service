package handlers

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/milossdjuric/rolling_update_service/internal/domain"
	"github.com/milossdjuric/rolling_update_service/internal/utils"
)

func (u *UpdateServiceGrpcHandler) Reconcile(ctx context.Context, d *domain.Deployment, stopChan chan struct{}, interruptChan chan struct{}) {

	//check if reconciliation is interrupted by interrupChan
	if IsReconcileInterrupted(interruptChan) {
		log.Printf("DEPLOYMENT %s: Reconcile interrupted", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))
		return
	}

	log.Printf("DEPLOYMENT %s: Starting reconcile for deployment", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))

	// get new and old revision
	newRevision, oldRevisions, err := u.GetNewAndOldRevisions(d)
	if err != nil {
		return
	}

	if IsReconcileInterrupted(interruptChan) {
		log.Printf("DEPLOYMENT %s: Reconcile interrupted", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))
		return
	}

	// if there was no new revision, now put it
	log.Printf("DEPLOYMENT %s: Saving new revision: %s...", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), newRevision.Name)
	err = u.SaveRevision(newRevision)
	if err != nil {
		log.Printf("DEPLOYMENT %s: Failed to save new revision: %v", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), err)
		return
	}

	totalRevisions := append(oldRevisions, *newRevision)

	log.Printf("DEPLOYMENT %s: New revision: %s", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), newRevision.Name)

	if IsReconcileInterrupted(interruptChan) {
		log.Printf("DEPLOYMENT %s: Reconcile interrupted", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))
		return
	}

	// query org owned nodes, we can use
	nodes, err := u.QueryNodes(ctx, d.OrgId, 100)
	if err != nil {
		log.Printf("DEPLOYMENT %s: Failed to query nodes: %v", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), err)
		return
	}

	nodeIds := make([]string, 0)
	for _, node := range nodes {
		nodeIds = append(nodeIds, node.Id)
	}

	if IsReconcileInterrupted(interruptChan) {
		log.Printf("DEPLOYMENT %s: Reconcile interrupted", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))
		return
	}

	totalApps := make([]domain.App, 0)
	newApps := make([]domain.App, 0)
	readyApps := make([]domain.App, 0)
	availableApps := make([]domain.App, 0)
	oldApps := make([]domain.App, 0)

	// if we are using docker daemon on our local machine only call docker direct methods
	// if we are using c12s nodes on our local machine only, call node direct methods
	// if we are using c12s nodes on multiple machines, we should call node indirect methods
	// these methods should be changed in future to work with gravitiy and use dissemination instead
	if d.Spec.Mode == domain.DirectDockerDaemon || d.Spec.Mode == domain.NodeAgentDirectDockerDaemon {
		totalApps, newApps, readyApps, availableApps, oldApps, err = u.GetAppsDirect(d, newRevision, oldRevisions, nodeIds...)
	} else if d.Spec.Mode == domain.NodeAgentIndirectDockerDaemon {
		totalApps, newApps, readyApps, availableApps, oldApps, err = u.GetAppsIndirect(d, newRevision, oldRevisions, nodeIds...)
	}
	if err != nil {
		log.Printf("DEPLOYMENT %s: Failed to get apps: %v", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), err)
		return
	}

	log.Printf("DEPLOYMENT %s: Total apps: %v, New apps: %v, Ready apps: %v, Available apps: %v, Old apps: %v", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), int64(len(totalApps)), int64(len(newApps)), int64(len(readyApps)), int64(len(availableApps)), int64(len(oldApps)))

	d.Status.TotalAppCount = int64(len(totalApps))
	d.Status.UpdatedAppCount = int64(len(newApps))
	d.Status.ReadyAppCount = int64(len(readyApps))
	d.Status.AvailableAppCount = int64(len(availableApps))
	d.Status.UnavailableAppCount = d.Status.TotalAppCount - d.Status.AvailableAppCount

	if IsReconcileInterrupted(interruptChan) {
		log.Printf("DEPLOYMENT %s: Reconcile interrupted", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))
		return
	}

	// get revisions for who we have apps that are running
	// active revisions app count, represents a map of active revisions and how many apps are running on each revision
	activeRevisions, activeRevisionsAppCount, err := u.GetActiveRevisions(d, totalRevisions, totalApps)
	if err != nil {
		return
	}
	sort.Sort(sort.Reverse(domain.ByCreationTimestamp(activeRevisions)))

	if IsReconcileInterrupted(interruptChan) {
		log.Printf("DEPLOYMENT %s: Reconcile interrupted", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))
		return
	}

	// if deployment flag is set to stop, start goroutine to stop deployments apps
	if d.Status.Stopped {
		stopResChan := make(chan error, 1)
		go func() {
			log.Printf("DEPLOYMENT %s: Stopping deployment...", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))
			err := u.Stop(d, newRevision, oldRevisions, activeRevisions, activeRevisionsAppCount, totalApps, availableApps, nodeIds...)
			stopResChan <- err
			close(stopResChan)
		}()
		// goroutine to handle stop results
		go func() {
			for err := range stopResChan {
				if err != nil {
					log.Printf("DEPLOYMENT %s: Error stopping deployment: %s", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), err)
				} else {
					log.Printf("DEPLOYMENT %s: Stopping operation successful", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))
				}
			}
		}()

		if len(totalApps) > 0 {
			// interrupt stopping, so it doesnt send signal to stopChan
			log.Printf("DEPLOYMENT %s: Apps still exists, continuing reconciliation for stopping...", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))
			return
		}
		// if deployment flag not marked for deletion, send stop signal, if it is then proceed
		if !d.Status.Deleted {
			log.Printf("DEPLOYMENT %s: Sending stop signal...", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))
			go func() {
				stopChan <- struct{}{}
			}()
			return
		}
	}
	// if deployment flag is marked for deletion, delete revisions and deployment
	if d.Status.Deleted {
		if len(totalApps) != 0 {
			//interrupt reconcile because deployment is marked for deletion and apps still exist
			//will continue once apps are stopped
			return
		}
		err := u.revisionRepo.DeleteDeploymentOwned(d.Spec.SelectorLabels, d.Namespace, d.OrgId)
		if err != nil {
			log.Printf("DEPLOYMENT %s: Failed to delete deployment owned revisions: %v", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), err)
		}
		err = u.deploymentRepo.Delete(d.Name, d.Namespace, d.OrgId)
		if err != nil {
			log.Printf("DEPLOYMENT %s: Failed to delete deployment: %v", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), err)
		}

		go func() {
			stopChan <- struct{}{}
		}()
		return
	}

	if IsReconcileInterrupted(interruptChan) {
		log.Printf("DEPLOYMENT %s: Reconcile interrupted", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))
		return
	}

	// update deployment status states, states are progress, available and failure
	// each state is either true or false
	// progress is true if deployment is in progress and rolling, false if not
	// available is true if enough apps are available, false if not
	// failure is true if deployment failed, happens when deadline is exceeded
	err = UpdateStatusStates(d, activeRevisionsAppCount[newRevision.Name])
	if err != nil {
		return
	}
	log.Printf("DEPLOYMENT %s: Deployment status states: Progress - %v, Available - %v, Failure - %v", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), d.Status.States[domain.DeploymentProgress].Active, d.Status.States[domain.DeploymentAvailable].Active, d.Status.States[domain.DeploymentFailure].Active)
	log.Printf("DEPLOYMENT %s: Deployment status states messages: Progress - %v, Available - %v, Failure - %v", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), d.Status.States[domain.DeploymentProgress].Message, d.Status.States[domain.DeploymentAvailable].Message, d.Status.States[domain.DeploymentFailure].Message)

	if IsReconcileInterrupted(interruptChan) {
		log.Printf("DEPLOYMENT %s: Reconcile interrupted", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))
		return
	}

	// if deployment flag for automatic rollback is set and deployment failed, it rolls back
	// to previous revision automatically, if there is no previous revision, its stuck in this revision
	// if its not set, it just pauses the deployment and prevents further rolling
	if d.Status.States[domain.DeploymentFailure].Active && d.Spec.AutomaticRollback {
		log.Printf("DEPLOYMENT %s: Deployment failed, automatically rolling back", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))
		err := u.Rollback(d, "")
		if err != nil {
			log.Printf("DEPLOYMENT %s: Failed to rollback deployment: %v", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), err)
		}
	} else if d.Status.States[domain.DeploymentFailure].Active && !d.Spec.AutomaticRollback {
		log.Printf("DEPLOYMENT %s: Deployment failed, automatic rollback is disabled, pausing deployment", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))
		d.Status.Paused = true
	}

	if IsReconcileInterrupted(interruptChan) {
		log.Printf("DEPLOYMENT %s: Reconcile interrupted", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))
		return
	}

	// if deployment is in progress, it rolls the deployment, and scales up or down
	if d.Status.States[domain.DeploymentProgress].Active &&
		!d.Status.States[domain.DeploymentFailure].Active && !d.Status.Paused {
		go func() {
			log.Printf("DEPLOYMENT %s: Rolling deployment...", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))

			err := u.Roll(d, newRevision, oldRevisions, activeRevisions, activeRevisionsAppCount, totalApps, availableApps, nodeIds...)
			if err != nil {
				log.Printf("DEPLOYMENT %s: Error rolling deployment: %v", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), err)
			} else {
				log.Printf("DEPLOYMENT %s: Deployment rolling iteration successful", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))
			}
		}()
	}

	if IsReconcileInterrupted(interruptChan) {
		log.Printf("DEPLOYMENT %s: Reconcile interrupted", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))
		return
	}

	// save deployment on end of reconcile
	u.SaveDeployment(d)
}

// stop deployment apps, called when deployment has stop flag
func (u *UpdateServiceGrpcHandler) Stop(d *domain.Deployment, newRevision *domain.Revision, oldRevisions, activeRevisions []domain.Revision, activeRevisionsAppCount map[string]int64, totalApps, availableApps []domain.App, nodeIds ...string) error {
	err := u.StopApps(d, int(len(totalApps)), totalApps, nodeIds...)
	if err != nil {
		return err
	}
	return nil
}

// get new and old revisions, if no revision matches current deployment spec, it creates a new revision
func (u *UpdateServiceGrpcHandler) GetNewAndOldRevisions(d *domain.Deployment) (*domain.Revision, []domain.Revision, error) {

	allRevisions, err := u.revisionRepo.GetDeploymentOwned(d.Spec.SelectorLabels, d.Namespace, d.OrgId)
	if err != nil {
		return nil, nil, err
	}

	newRevision, err := GetNewRevision(d, allRevisions)
	if err != nil {
		return nil, nil, err
	}
	oldRevisions := make([]domain.Revision, 0)
	for _, revision := range allRevisions {
		if newRevision == nil || !revision.CompareRevisions(*newRevision) {
			oldRevisions = append(oldRevisions, revision)
		}
	}

	return newRevision, oldRevisions, nil
}

func GetNewRevision(d *domain.Deployment, revisions []domain.Revision) (*domain.Revision, error) {

	sort.Sort(sort.Reverse(domain.ByCreationTimestamp(revisions)))
	if len(revisions) != 0 && d.Spec.AppSpec.CompareAppSpecs(revisions[0].Spec.AppSpec) {
		return &revisions[0], nil
	}
	// if no new revision matches deployment spec, create new revision

	newRevision := domain.NewRevisionFromDeployment(*d)
	err := newRevision.Validate()
	if err != nil {
		return nil, fmt.Errorf("failed to validate new revision: %v", err)
	}

	return &newRevision, nil
}

// roll deployment, scale up or down, based on new and old revisions
func (u *UpdateServiceGrpcHandler) Roll(d *domain.Deployment, newRevision *domain.Revision, oldRevisions, activeRevisions []domain.Revision, activeRevisionsAppCount map[string]int64, totalApps, availableApps []domain.App, nodeIds ...string) error {

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

	err := u.ReconcileNewRevision(d, newRevision, oldRevisions, activeRevisions, activeRevisionsAppCount, newApps, nodeIds...)
	if err != nil {
		return err
	}

	err = u.ReconcileOldRevisions(d, newRevision, oldRevisions, activeRevisions, activeRevisionsAppCount, newAvailableAppCount, totalApps, availableApps, nodeIds...)
	if err != nil {
		return err
	}
	return nil
}

// rollback deployment to previous revision
func (u *UpdateServiceGrpcHandler) Rollback(d *domain.Deployment, revisionName string) error {

	newRevision, oldRevisions, err := u.GetNewAndOldRevisions(d)
	if err != nil {
		log.Printf("DEPLOYMENT %s: Failed to get new and old revisions: %v", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), err)
	}

	allRevisions := append(oldRevisions, *newRevision)
	var rollbackRevision *domain.Revision

	//if no revision is specififed, rollback to last revision
	if revisionName == "" {
		sort.Sort(sort.Reverse(domain.ByCreationTimestamp(allRevisions)))
		//if there is no revision to rollback to, return
		if len(allRevisions) < 2 {
			return fmt.Errorf("no revision to rollback to")
		}
		rollbackRevision = &allRevisions[1]
	} else {
		// if revision is specified, rollback to that revision
		rollbackRevision, err = u.revisionRepo.Get(revisionName, d.Namespace, d.OrgId)
		if err != nil {
			return fmt.Errorf("failed to get rollback revision")
		}
	}
	// when we do a rollback we automatically make a new revision from the revision we want to rollback to
	// revision has the same spec but different name/selector labels

	// delete existing selector labels from revision we want to rollback to
	// add new selector labels name
	delete(rollbackRevision.Spec.SelectorLabels, "revision")
	delete(rollbackRevision.Spec.AppSpec.SelectorLabels, "revision")

	rollbackRevision.Name = utils.GenerateUniqueName(d.Name)
	rollbackRevision.Spec.SelectorLabels["revision"] = rollbackRevision.Name
	rollbackRevision.Spec.AppSpec.SelectorLabels["revision"] = rollbackRevision.Name
	rollbackRevision.CreationTimestamp = time.Now().Unix()

	err = u.SaveRevision(rollbackRevision)
	if err != nil {
		return fmt.Errorf("failed to save rollback revision")
	}

	//set appSpec of deployment to that of rollback revision
	d.Spec.AppSpec = rollbackRevision.Spec.AppSpec

	// if revisions number surpasses revision limit, delete oldest revisions first
	if len(allRevisions)+1 > int(*d.Spec.RevisionLimit) {
		log.Printf("DEPLYOMENT %s: Deleting oldest revisions...", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))
		sufficientRevisions := len(allRevisions) + 1 - int(*d.Spec.RevisionLimit)
		if sufficientRevisions <= len(allRevisions) {
			oldestRevisions := allRevisions[len(allRevisions)-sufficientRevisions:]
			for _, oldestRevision := range oldestRevisions {
				u.revisionRepo.Delete(oldestRevision.Name, oldestRevision.Namespace, oldestRevision.OrgId)
			}
		}
	}

	//update status states, reset progress and failure states
	d.Status.States[domain.DeploymentFailure] = domain.NewDeploymentState(domain.DeploymentFailure, false, "Deployment in rollback", time.Now().Unix(), time.Now().Unix())
	d.Status.States[domain.DeploymentProgress] = domain.NewDeploymentState(domain.DeploymentProgress, true, "Deployment in rollback", time.Now().Unix(), time.Now().Unix())
	d.Status.States[domain.DeploymentAvailable] = domain.NewDeploymentState(domain.DeploymentAvailable, d.Status.States[domain.DeploymentAvailable].Active, d.Status.States[domain.DeploymentProgress].Message, time.Now().Unix(), d.Status.States[domain.DeploymentAvailable].LastTransitionTimestamp)

	return nil
}

func (u *UpdateServiceGrpcHandler) ReconcileNewRevision(d *domain.Deployment, newRevision *domain.Revision, oldRevisions, activeRevisions []domain.Revision, activeRevisionsAppCount map[string]int64, newApps []domain.App, nodeIds ...string) error {

	// if new revision app count is equal to deployment spec app count, no need for scaling, return
	if d.Spec.AppCount == activeRevisionsAppCount[newRevision.Name] {
		return nil
	}
	if d.Spec.AppCount < activeRevisionsAppCount[newRevision.Name] {
		//Scale down
		err := u.ScaleRevision(d, newRevision, d.Spec.AppCount, activeRevisionsAppCount, newApps, nodeIds...)
		if err != nil {
			return err
		}
		return nil
	}

	//Scale up
	newRevisionAppCount := int64(0)
	currentDeploymentAppCount := int64(0)

	//max surge is maximum number of apps that can be added to deployment
	maxSurge := *d.Spec.Strategy.RollingUpdate.MaxSurge
	// get current deployment app count
	for _, revision := range activeRevisions {
		currentDeploymentAppCount += activeRevisionsAppCount[revision.Name]
	}
	// maximum number of apps deployment can have
	maxDeploymentAppCount := d.Spec.AppCount + maxSurge

	// if current deployment app count is greater than or equal to max deployment app count,
	// new revision app count remains same, otherwise scale up
	if currentDeploymentAppCount >= maxDeploymentAppCount {
		newRevisionAppCount = activeRevisionsAppCount[newRevision.Name]
	} else {
		scaleUpCount := maxDeploymentAppCount - int64(currentDeploymentAppCount)
		scaleUpCount = min(scaleUpCount, d.Spec.AppCount-activeRevisionsAppCount[newRevision.Name])
		newRevisionAppCount = activeRevisionsAppCount[newRevision.Name] + scaleUpCount
	}

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

	allAppsCount := int64(0)
	allAppsCount += oldAppsCount
	allAppsCount += activeRevisionsAppCount[newRevision.Name]

	// get max unavailable apps, minimum available apps
	maxUnavailable := d.Spec.Strategy.RollingUpdate.MaxUnavailable
	minAvailable := d.Spec.AppCount - *maxUnavailable

	// max scaled down is number of apps that can be scaled down
	newRevisionUnavailableAppCount := activeRevisionsAppCount[newRevision.Name] - newAvailableAppCount
	maxScaledDown := allAppsCount - minAvailable - newRevisionUnavailableAppCount
	// if we cant scale down, return
	if maxScaledDown <= 0 {
		return nil
	}

	// get old unavailable apps
	oldUnavailableApps := GetOldUnavailableApps(totalApps, availableApps, newRevision)

	//Scale down unavailable/unhealthy old apps
	err = u.StopApps(d, int(maxScaledDown), oldUnavailableApps, nodeIds...)
	if err != nil {
		return err
	}

	//add downscale of available apps
	availableAppsCount := int64(len(availableApps))

	// total scaled down is number of apps that are scaled downs
	totalScaledDown := int64(0)
	// total scaled down count is number of apps that can be scaled down
	totalScaledDownCount := availableAppsCount - minAvailable
	for _, revision := range oldRevisions {
		if totalScaledDown >= totalScaledDownCount {
			log.Printf("DEPLOYMENT %s: Total scale down reached for revision: %s", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), revision.Name)
			break
		}

		if activeRevisionsAppCount[revision.Name] == 0 {
			log.Printf("DEPLOYMENT %s: Active revision app count is 0, revision: %s", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), revision.Name)
			continue
		}
		// scale down count is number of apps to be scaled down
		// if current number of apps is smaller than scale down count, keep it as is
		scaleDownCount := min(activeRevisionsAppCount[revision.Name], totalScaledDownCount-totalScaledDown)
		// new apps count for this revision after scale down
		newAppsCount := activeRevisionsAppCount[revision.Name] - scaleDownCount
		// if new apps count for revisions is greater than current, invalid request
		if newAppsCount > activeRevisionsAppCount[revision.Name] {
			return fmt.Errorf("invalid request to scale down")
		}

		// scale down revision based on new apps count
		err := u.ScaleRevision(d, &revision, newAppsCount, activeRevisionsAppCount, oldApps, nodeIds...)
		if err != nil {
			return err
		}
		// increment number of total apps that are scaled down
		totalScaledDown += scaleDownCount
	}

	totalScaledDown += int64(len(oldUnavailableApps))
	log.Printf("DEPLOYMENT %s: Total scaled down apps: %d", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), totalScaledDown)

	return nil
}

func (u *UpdateServiceGrpcHandler) ScaleRevision(d *domain.Deployment, revision *domain.Revision, newSize int64, activeRevisionsAppCount map[string]int64, apps []domain.App, nodeIds ...string) error {

	log.Printf("DEPLOYMENT %s: Scaling revision %s...", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), revision.Name)

	// bothing to scale, current size is equal to new size
	if activeRevisionsAppCount[revision.Name] == newSize {
		return nil
	}

	oldSize := activeRevisionsAppCount[revision.Name]
	if activeRevisionsAppCount[revision.Name] < newSize {
		//Scale up
		log.Printf("DEPLOYMENT %s: Scaling up %s , Old size: %d, New size: %d", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), revision.Name, oldSize, newSize)

		err := u.StartApps(d, revision, int64(newSize-oldSize), nodeIds...)
		if err != nil {
			return err
		}
	} else {
		//Scale down
		log.Printf("DEPLOYMENT %s: Scaling down %s , Old size: %d, New size: %d", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), revision.Name, oldSize, newSize)

		err := u.StopApps(d, int(oldSize-newSize), apps, nodeIds...)
		if err != nil {
			return err
		}
	}
	return nil
}

func (u *UpdateServiceGrpcHandler) GetActiveRevisions(d *domain.Deployment, revisions []domain.Revision, apps []domain.App) ([]domain.Revision, map[string]int64, error) {
	// Get active revisions, those are revisions which contain selector labels that match the labels of apps
	log.Printf("DEPLOYMENT %s: Getting active revisions...", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))

	activeRevisionsAppCount := make(map[string]int64)
	appLabels := make([]*domain.App, 0)
	for _, app := range apps {
		appLabels = append(appLabels, &app)
	}

	activeRevisions := make([]domain.Revision, 0)
	for _, revision := range revisions {

		appCount := CountMatchingAppsForRevisons(&revision, appLabels)
		if appCount > 0 {
			activeRevisions = append(activeRevisions, revision)
			activeRevisionsAppCount[revision.Name] = appCount
		}
	}

	return activeRevisions, activeRevisionsAppCount, nil
}

func UpdateStatusStates(d *domain.Deployment, availableNewRevisionAppCount int64) error {
	//Should update status, based on its count, previous status, timestamps and other fields

	// if available app count is equal or grater to min available apps, deployment is available
	if d.Status.AvailableAppCount >= d.Spec.AppCount-*d.Spec.Strategy.RollingUpdate.MaxUnavailable && d.Status.AvailableAppCount > 0 {

		d.Status.States[domain.DeploymentAvailable] = domain.NewDeploymentState(domain.DeploymentAvailable, true, "Deployment available", d.Status.States[domain.DeploymentAvailable].LastUpdateTimestamp, time.Now().Unix())
		log.Printf("DEPLOYMENT %s: Deployment is now available...", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))

		// if spec app, available and total app count are equal to new app count, deployment is completed
		if d.Status.AvailableAppCount == availableNewRevisionAppCount && d.Status.TotalAppCount == availableNewRevisionAppCount && d.Spec.AppCount == availableNewRevisionAppCount {
			d.Status.States[domain.DeploymentProgress] = domain.NewDeploymentState(domain.DeploymentProgress, false, "Deployment rollout completed", d.Status.States[domain.DeploymentProgress].LastUpdateTimestamp, time.Now().Unix())
		} else {
			//continue progress
			d.Status.States[domain.DeploymentProgress] = domain.NewDeploymentState(domain.DeploymentProgress, true, "Deployment in progress", d.Status.States[domain.DeploymentProgress].LastUpdateTimestamp, time.Now().Unix())
		}
	} else {
		// deployment is not available, continue progress
		d.Status.States[domain.DeploymentAvailable] = domain.NewDeploymentState(domain.DeploymentAvailable, false, "Deployment not available", d.Status.States[domain.DeploymentAvailable].LastUpdateTimestamp, time.Now().Unix())
		d.Status.States[domain.DeploymentProgress] = domain.NewDeploymentState(domain.DeploymentProgress, true, "Deployment in progress", d.Status.States[domain.DeploymentProgress].LastUpdateTimestamp, time.Now().Unix())
	}

	if d.Status.States[domain.DeploymentProgress].Active {
		log.Printf("DEPLOYMENT %s: Deployment is in progress...", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))

		// if progress is active, check if deadline is exceeded
		if IsDeadlineExceeded(d, d.Status.States[domain.DeploymentProgress].LastUpdateTimestamp) && !d.Status.Paused {

			log.Printf("DEPLOYMENT %s: Deployment deadline now exceeded...", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name))
			d.Status.States[domain.DeploymentProgress] = domain.NewDeploymentState(domain.DeploymentProgress, false, "Deadline exceeded", d.Status.States[domain.DeploymentProgress].LastUpdateTimestamp, time.Now().Unix())

			if d.Spec.AppCount == 0 {
				// if deployment spec app count is 0, note in message
				d.Status.States[domain.DeploymentProgress] = domain.NewDeploymentState(domain.DeploymentProgress, false, "Deadline exceeded, appCount is currently set to 0 in deployment spec", d.Status.States[domain.DeploymentProgress].LastUpdateTimestamp, time.Now().Unix())
			}
			d.Status.States[domain.DeploymentFailure] = domain.NewDeploymentState(domain.DeploymentFailure, true, "Deadline exceeded, failed", d.Status.States[domain.DeploymentFailure].LastUpdateTimestamp, time.Now().Unix())
		} else {
			// continue progress
			d.Status.States[domain.DeploymentFailure] = domain.NewDeploymentState(domain.DeploymentFailure, false, "Deployment has not failed", d.Status.States[domain.DeploymentFailure].LastUpdateTimestamp, time.Now().Unix())
		}
	}

	return nil
}
