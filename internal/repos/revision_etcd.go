package repos

import (
	"context"
	"fmt"
	"log"

	"github.com/milossdjuric/rolling_update_service/internal/domain"
	"github.com/milossdjuric/rolling_update_service/internal/utils"

	etcd "go.etcd.io/etcd/client/v3"
)

type revisionEtcdRepo struct {
	etcd               *etcd.Client
	revisionMarshaller domain.RevisionMarshaller
}

func NewRevisionEtcdRepo(etcd *etcd.Client, revisionMarshaller domain.RevisionMarshaller) (domain.RevisionRepo, error) {
	return &revisionEtcdRepo{
		etcd:               etcd,
		revisionMarshaller: revisionMarshaller,
	}, nil
}

func (r revisionEtcdRepo) Put(revision domain.Revision) error {

	revisionMarshalled, err := r.revisionMarshaller.Marshal(revision)
	if err != nil {
		return err
	}

	key := createRevisionKey(revision)

	_, err = r.etcd.Put(context.TODO(), key, string(revisionMarshalled))
	if err != nil {
		return err
	}

	return nil
}

func (r revisionEtcdRepo) Get(name, namespace, orgId string) (*domain.Revision, error) {

	key := getRevisionKey(domain.Revision{Name: name, Namespace: namespace, OrgId: orgId})
	resp, err := r.etcd.Get(context.TODO(), key)
	if err != nil {
		return nil, err
	}
	if len(resp.Kvs) == 0 {
		return nil, fmt.Errorf("revision not found")
	}

	revisionUnmarshalled, err := r.revisionMarshaller.Unmarshal(resp.Kvs[0].Value)
	if err != nil {
		return nil, err
	}

	return revisionUnmarshalled, nil
}

func (r revisionEtcdRepo) Delete(name, namespace, orgId string) error {
	key := getRevisionKey(domain.Revision{Name: name, Namespace: namespace, OrgId: orgId})
	_, err := r.etcd.Delete(context.TODO(), key)
	if err != nil {
		log.Printf("Error deleting revision: %s", err)
		return err
	}

	log.Printf("Successfully deleted revision: %s", key)
	return nil
}

func (r revisionEtcdRepo) GetDeploymentOwned(selectorLabels map[string]string, namespace, orgId string) ([]domain.Revision, error) {
	keyPrefix := fmt.Sprintf("%s/orgs/%s/%s", revisionPrefix, orgId, namespace)

	return r.SelectRevisions(selectorLabels, keyPrefix)
}

func (r revisionEtcdRepo) DeleteDeploymentOwned(selectorLabels map[string]string, namespace, orgId string) error {
	keyPrefix := fmt.Sprintf("%s/orgs/%s/%s", revisionPrefix, orgId, namespace)

	selectedRevisions, err := r.SelectRevisions(selectorLabels, keyPrefix)
	if err != nil {
		return err
	}
	for _, revision := range selectedRevisions {
		err := r.Delete(revision.Name, revision.Namespace, revision.OrgId)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r revisionEtcdRepo) SelectRevisions(selectorLabels map[string]string, keyPrefix string) ([]domain.Revision, error) {

	revisions, err := r.etcd.Get(context.TODO(), keyPrefix, etcd.WithPrefix())
	if err != nil {
		return nil, err
	}

	var matchingRevisions []domain.Revision

	for _, kv := range revisions.Kvs {

		revision, err := r.revisionMarshaller.Unmarshal(kv.Value)
		if err != nil {
			return nil, err
		}
		if utils.MatchLabels(revision.Spec.SelectorLabels, selectorLabels) {
			matchingRevisions = append(matchingRevisions, *revision)
		}
	}
	return matchingRevisions, nil
}

const (
	revisionPrefix = "revisions"
)

func createRevisionKey(revision domain.Revision) string {
	return fmt.Sprintf("%s/orgs/%s/%s/%s", revisionPrefix, revision.OrgId, revision.Namespace, revision.Name)
}

func getRevisionKey(revision domain.Revision) string {
	return fmt.Sprintf("%s/orgs/%s/%s/%s", revisionPrefix, revision.OrgId, revision.Namespace, revision.Name)
}
