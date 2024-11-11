package domain

import (
	"log"
	"time"
	"update-service/internal/utils"
)

type Revision struct {
	Name              string
	Namespace         string
	OrgId             string
	CreationTimestamp int64
	Labels            map[string]string
	Spec              RevisionSpec
}

type RevisionSpec struct {
	SelectorLabels map[string]string
	AppSpec        AppSpec
}

func NewRevisionFromDeployment(deployment Deployment) Revision {
	appSpec := NewAppSpec(
		deployment.Spec.AppSpec.Name,
		deployment.Spec.AppSpec.Namespace,
		deployment.Spec.AppSpec.OrgId,
		deployment.Spec.AppSpec.SelectorLabels,
		deployment.Spec.AppSpec.SeccompProfile,
		deployment.Spec.AppSpec.SeccompDefintionStrategy,
	)
	for resource, quota := range deployment.Spec.AppSpec.Quotas {
		err := appSpec.AddResourceQuota(resource, quota)
		if err != nil {
			//if error occurs return empty revision, on call of this method should check if there are any values in revision
			log.Println(err)
			return Revision{}
		}
	}

	revisionSpec := RevisionSpec{
		SelectorLabels: map[string]string{},
		AppSpec:        appSpec,
	}
	for k, v := range deployment.Spec.SelectorLabels {
		revisionSpec.SelectorLabels[k] = v
	}
	revisionSpec.SelectorLabels["deployment"] = deployment.Name
	for k, v := range revisionSpec.SelectorLabels {
		revisionSpec.AppSpec.SelectorLabels[k] = v
	}
	revisionSpec.AppSpec.SelectorLabels["deployment"] = deployment.Name

	revision := Revision{
		Name:              utils.GenerateUniqueName(deployment.Name),
		Namespace:         deployment.Namespace,
		OrgId:             deployment.OrgId,
		CreationTimestamp: time.Now().Unix(),
		Labels:            deployment.Labels,
		Spec:              revisionSpec,
	}
	revisionSpec.SelectorLabels["revision"] = revision.Name
	revisionSpec.AppSpec.SelectorLabels["revision"] = revision.Name

	return revision
}

type RevisionRepo interface {
	Put(revision Revision) error
	Get(name, namespace, orgId string) (*Revision, error)
	Delete(name, namespace, orgId string) error
	GetDeploymentOwnedRevisions(selectorLabels map[string]string, namespace, orgId string) ([]Revision, error)
	SelectRevisions(selectorLabels map[string]string, keyPrefix string) ([]Revision, error)
}

type RevisionMarshaller interface {
	Marshal(revision Revision) ([]byte, error)
	Unmarshal(data []byte) (*Revision, error)
}

func (r Revision) CompareRevisions(other Revision) bool {

	// log.Println("-----------------COMPARING REVISIONS-----------------")

	if r.Name != other.Name ||
		r.Namespace != other.Namespace ||
		r.OrgId != other.OrgId ||
		!utils.MatchLabels(r.Spec.SelectorLabels, other.Spec.SelectorLabels) ||
		!r.Spec.AppSpec.CompareAppSpecs(other.Spec.AppSpec) {
		// log.Printf("Revision mismatch")
		return false
	}
	// log.Printf("Revision match")
	return true
}

type ByCreationTimestamp []Revision

func (r ByCreationTimestamp) Len() int {
	return len(r)
}

func (r ByCreationTimestamp) Less(i, j int) bool {
	return r[i].CreationTimestamp < r[j].CreationTimestamp
}

func (r ByCreationTimestamp) Swap(i, j int) {
	r[i], r[j] = r[j], r[i]
}
