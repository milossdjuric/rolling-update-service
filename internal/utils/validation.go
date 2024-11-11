package utils

import (
	"fmt"
)

func ValidateGetDeploymentReqTask(payload map[string]interface{}) (string, string, string, error) {
	name, nameOk := payload["name"].(string)
	namespace, namespaceOk := payload["namespace"].(string)
	orgId, orgIdOk := payload["orgId"].(string)

	if !nameOk || !orgIdOk || !namespaceOk {
		return "", "", "", fmt.Errorf("Payload must contain valid fields: Name, Namespace and OrgId")
	}
	return name, namespace, orgId, nil
}

func ValidateGetDeploymentOwnedRevisionsReqTask(payload map[string]interface{}) (string, string, string, error) {
	name, nameOk := payload["name"].(string)
	namespace, namespaceOk := payload["namespace"].(string)
	orgId, orgIdOk := payload["orgId"].(string)

	if !nameOk || !orgIdOk || !namespaceOk {
		return "", "", "", fmt.Errorf("Payload must contain valid fields: Name, Namespace and OrgId")
	}
	return name, namespace, orgId, nil
}
