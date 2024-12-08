package repos

import (
	"context"
	"fmt"

	"github.com/milossdjuric/rolling_update_service/internal/domain"

	etcd "go.etcd.io/etcd/client/v3"
)

type deploymentEtcdRepo struct {
	etcd                 *etcd.Client
	deploymentMarshaller domain.DeploymentMarshaller
}

func NewDeploymentEtcdRepo(etcd *etcd.Client, deploymentMarshaller domain.DeploymentMarshaller) (domain.DeploymentRepo, error) {
	return &deploymentEtcdRepo{
		etcd:                 etcd,
		deploymentMarshaller: deploymentMarshaller,
	}, nil
}

func (d deploymentEtcdRepo) Put(deployment domain.Deployment) error {

	deploymentMarshalled, err := d.deploymentMarshaller.Marshal(deployment)
	if err != nil {
		return err
	}

	key := getDeploymentKey(deployment)

	_, err = d.etcd.Put(context.TODO(), key, string(deploymentMarshalled))
	if err != nil {
		return err
	}

	return nil
}

func (d deploymentEtcdRepo) Get(name, namespace, orgId string) (*domain.Deployment, error) {

	key := getDeploymentKey(domain.Deployment{Name: name, Namespace: namespace, OrgId: orgId})
	resp, err := d.etcd.Get(context.TODO(), key)
	if err != nil {
		return nil, err
	}
	if len(resp.Kvs) == 0 {
		return nil, fmt.Errorf("deployment not found")
	}

	deploymentUnmarshalled, err := d.deploymentMarshaller.Unmarshal(resp.Kvs[0].Value)
	if err != nil {
		return nil, err
	}

	return deploymentUnmarshalled, nil
}

func (d deploymentEtcdRepo) Delete(name, namespace, orgId string) error {
	key := getDeploymentKey(domain.Deployment{Name: name, Namespace: namespace, OrgId: orgId})
	_, err := d.etcd.Delete(context.Background(), key)
	if err != nil {
		return err
	}
	return nil
}

const (
	deploymentPrefix = "deployments"
)

func getDeploymentKey(deployment domain.Deployment) string {
	return fmt.Sprintf("%s/orgs/%s/%s/%s", deploymentPrefix, deployment.OrgId, deployment.Namespace, deployment.Name)
}
