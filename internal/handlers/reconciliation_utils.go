package handlers

import (
	"context"
	"fmt"
	"log"
	"math"
	"time"

	magnetarapi "github.com/c12s/magnetar/pkg/api"
	"github.com/milossdjuric/rolling_update_service/internal/domain"
	"github.com/milossdjuric/rolling_update_service/internal/utils"
	"golang.org/x/exp/rand"
	"google.golang.org/grpc/metadata"
)

// count apps that match the revision via selector labels
func CountMatchingAppsForRevisons(revision *domain.Revision, apps []*domain.App) int64 {
	appCount := int64(0)
	for _, app := range apps {
		if utils.MatchLabels(revision.Spec.SelectorLabels, app.SelectorLabels) {
			appCount++
		}
	}
	return appCount
}

func IsDeadlineExceeded(d *domain.Deployment, timestamp int64) bool {
	log.Printf("DEPLOYMENT %s: Deadline for exceeding is: %v seconds", fmt.Sprintf("%s/%s/%s", d.OrgId, d.Namespace, d.Name), d.Spec.DeadlineExceeded)
	deadline := d.Spec.DeadlineExceeded + timestamp
	return time.Now().Unix() > deadline
}

// get apps that are not in available apps map and do not match new revision selector labels
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

func (u *UpdateServiceGrpcHandler) SaveRevision(r *domain.Revision) error {
	return u.revisionRepo.Put(*r)
}

// check if context is interrupted, if so returns bool on which it stops the Reconcile() method
func IsReconcileInterrupted(interruptChan chan struct{}) bool {
	select {
	case <-interruptChan:
		return true
	default:
		return false
	}
}

// checks if we are using node agent for deployment
func IsWithNodeAgent(d *domain.Deployment) bool {
	if d.Spec.Mode == domain.NodeAgentDirectDockerDaemon || d.Spec.Mode == domain.NodeAgentIndirectDockerDaemon {
		return true
	}
	return false
}

// calls magnetar service to get org owned nodes
func (u *UpdateServiceGrpcHandler) QueryNodes(ctx context.Context, orgId string, percentage int32) ([]*magnetarapi.NodeStringified, error) {

	queryReq := &magnetarapi.ListOrgOwnedNodesNoAuthReq{
		Org: orgId,
	}
	ctx = setOutgoingContext(ctx)
	queryResp, err := u.magnetar.ListOrgOwnedNodesNoAuth(ctx, queryReq)
	if err != nil {
		return nil, err
	}

	// log.Printf("query.Resp.Nodes: %v", queryResp.Nodes)

	nodes := selectRandomNodes(queryResp.Nodes, percentage)
	return nodes, nil
}

// selects random nodes from the list of nodes
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

// get random node id from the list of node idss
func GetRandomNodeId(nodeIds []string) (string, error) {
	if len(nodeIds) == 0 {
		return "", fmt.Errorf("no nodes available")
	}
	seed := uint64(time.Now().UnixNano())
	r := rand.New(rand.NewSource(seed))
	return nodeIds[r.Intn(len(nodeIds))], nil
}

// prepare arguments for app operation, important for node agent
func PrepareAppOperationArgs(orgId string, namespace string, nodeIds ...string) []string {
	args := make([]string, 0)
	args = append(args, orgId)
	args = append(args, namespace)
	args = append(args, nodeIds...)
	return args
}
