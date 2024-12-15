# Rolling Update Service


Rolling Update Service is a service designed to handle requests for deploying applications via Docker containers. It works as a controller which maintains a number of containers and supports rolling updates for new deployment specifications. Currently it should support deployment either with Star node agents on local and remote machines or directly with Docker daemon on local machine. In future instead of direct communication with Star node agents, support for Gravity service should be implemented.

## Configuration

Rolling Update Service's configuration consists of config for application, as well as default docker client image that is used for deployed containers. 

### Application configuration

| Parameter | Description | Default value |
|--|--|--|
| UPDATE_SERVICE_PORT | The port number at which the Rolling Update Service GRPC server is listening. | 8800 |
| UPDAte_SERVICE_HOSTNAME | The address where Rolling Update Service is on control plane. | rolling_update_service_etcd |
| UPDATE_SERVICE_ETCD_HOSTNAME | The address where etcd database for service is located. | rolling_update_service_etcd |
| UPDATE_SERVICE_ETCD_PORT | The port where etcd database is listening. | rolling_update_service_etcd |
| UPDATE_SERVICE_DOCKER_CLIENT_ADDRESS | The address on which docker client API is connected to Rolling Update Service, mounted on docker socket | unix:///var/run/docker.sock |

## Usage

The Rolling Update Service for gRPC requests is, by default, available at [127.0.0.1:8800](127.0.0.1:8800).

## Endpoints

### gRPC Endpoints

#### /PutDeployment

The endpoint for saving deployment, automatically starts the deployment as well.

#### Request body
```json
{
        "name": "my-deployment",
        "namespace": "default",
        "orgId": "c12s",  
        "labels": {
            "env": "production",
            "team": "backend"
        },
        "spec": {
            "selectorLabels": {
            "app": "example-app"
            },
            "appCount": 7,
            "revisionLimit": 200,
            "strategy": {
            "type": "RollingUpdate",
            "rollingUpdate": {
                "maxUnavailable": 1,
                "maxSurge": 2
            }
            },
            "app": {
            "name": "new application",                    
            "quotas": {
                "cpu": 2.0,     
                "mem": 4.0
            },
            "profile": {
                "version": "v1",
                "default_action": "NOT_ALLOWED",
                "syscalls": [
                {
                    "names": ["syscall1", "syscall2"],
                    "action": "SCMP_ACT_ERRNO"
                }
                ]
            },    
            "seccompDefinitionStrategy": "runtimeDefault"
            },
            "minReadySeconds": 30,
            "deadlineExceeded": 200,
            "automaticRollback": true,
            "mode": "DirectStar",
            "reconcilePeriod": 30,
        }
}
```

##### Body Parameters
| Parameter | Type   | Description                                                  |
|-----------|--------|--------------------------------------------------------------|
| name | string | Name of the deployment. |
| namespace    | string | Namespace where deployment is located. |
| orgId    | string | Organisation to which namespace and name belong to. |
| labels    | map | Map of labels attached to deployment |
| spec    | map | Specification of deployment. |
| selectorLabels    | map | Map of selector labels, by which we determine which revisions and apps belong to which deployment. |
| appCount    | int | How many apps we want deployed. |
| revisionLimit    | int (optional) | How many previous revisions we want saved in our history. Default is 10. |
| strategy    | DeploymentStrategy | Which strategy we want for our deployment, only rolling update currently available. |
| maxUnavailable | int | Maximum amount of apps that can be unavailable at the time. |
| maxSurge    | int | Maximum number of apps to which we are allowed to scale during rolling update. |
| app    | map | Specification of application we want to run |
| name    | string | Name of application. |
| quotas | map (float) | Resource quotas for each app, based on this total resource quota for deployment is calculated.|
| profile    | SeccompProfile | Seccomp profile described. |
| version | string | Seccomp profile version |
| default_action | string | Default action |
| syscalls | map | System calls |
| names | array | Names of syscalls |
| action | string | Action |
| seccompDefinitionStrategy | string | Seccomp strategy |
| minReadySeconds | int | Minimum number of seconds application needs to be running to be considered available for service |
| deadlineExceeded | int | Number of seconds until deployment is considered to fail if it does not meet its wanted state |
| automaticRollback | bool | If true, when deployment fails, it will automatically rollback to previous revision if one is available. If false, it will just pause the deployment |
| mode | string (IndirectStar, DirectStar, DirectDocker) | How our deployment is deployed, IndirectStar is if we are running containers on node agents on remote machines, DirectStar is if we are running containers on node agents only on our local machine, DirectDocker is if we are running containers directly via Docker Daemon |
| reconciliationPeriod | int (optional) | Number of seconds until deployment is periodically reconciled. Default is 30. |


#### Response - 0 OK

```json
{}
```

#### /GetDeployment

The endpoint for getting deployment.

#### Request body

```json
{
    "name": "my-deployment",
    "namespace": "default",
    "orgId": "c12s"
}
```

##### Body Parameters
| Parameter | Type   | Description                                                  |
|-----------|--------|--------------------------------------------------------------|
| name | string | Name of the deployment. |
| namespace    | string | Namespace where deployment is located. |
| orgId    | string | Organisation to which namespace and name belong to. |


#### Response - 0 OK 

```json
{
    "deployment": {
        "labels": {
            "team": "backend",
            "env": "production"
        },
        "name": "my-deployment",
        "namespace": "default",
        "orgId": "c12s",
        "spec": {
            "selectorLabels": {
                "app": "example-app"
            },
            "resourceQuotas": {
                "cpu": 14,
                "mem": 28
            },
            "appCount": "7",
            "revisionLimit": "200",
            "strategy": {
                "type": "RollingUpdate",
                "rollingUpdate": {
                    "maxUnavailable": "1",
                    "maxSurge": "2"
                }
            },
            "appSpec": {
                "selectorLabels": {
                    "app": "example-app",
                    "deployment": "my-deployment"
                },
                "quotas": {
                    "mem": 4,
                    "cpu": 2
                },
                "name": "new application",
                "namespace": "default",
                "orgId": "c12s",
                "seccompProfile": {
                    "syscalls": [
                        {
                            "names": [
                                "syscall1",
                                "syscall2"
                            ],
                            "action": "SCMP_ACT_ERRNO"
                        }
                    ],
                    "version": "v1",
                    "default_action": "NOT_ALLOWED"
                },
                "seccompDefinitionStrategy": "runtimeDefault"
            },
            "minReadySeconds": "30",
            "deadlineExceeded": "200",
            "automaticRollback": true,
            "mode": "DirectStar"
        },
        "status": {
            "states": {
                "Progress": {
                    "type": "Progress",
                    "active": false,
                    "lastUpdateTimestamp": "1733596483",
                    "lastTransitionTimestamp": "1733596483",
                    "message": "Deployment started"
                },
                "Available": {
                    "type": "Available",
                    "active": false,
                    "lastUpdateTimestamp": "1733596483",
                    "lastTransitionTimestamp": "1733596483",
                    "message": "Deployment not available yet"
                },
                "Failure": {
                    "type": "Failure",
                    "active": false,
                    "lastUpdateTimestamp": "1733596483",
                    "lastTransitionTimestamp": "1733596483",
                    "message": "Deployment has not failed"
                }
            },
            "totalAppCount": "0",
            "updatedAppCount": "0",
            "readyAppCount": "0",
            "availableAppCount": "0",
            "unavailableAppCount": "0",
            "paused": false,
            "stopped": false,
            "deleted": false
        }
    }
}
```

##### Body Parameters
| Parameter | Type   | Description                                                  |
|-----------|--------|--------------------------------------------------------------|
| name | string | Name of the deployment. |
| namespace    | string | Namespace where deployment is located. |
| orgId    | string | Organisation to which namespace and name belong to. |
| labels    | map | Map of labels attached to deployment |
| spec    | map | Specification of deployment. |
| selectorLabels    | map | Map of selector labels, by which we determine which revisions and apps belong to which deployment. |
| appCount    | int | How many apps we want deployed. |
| revisionLimit    | int (optional) | How many previous revisions we want saved in our history. Default is 10. |
| strategy    | DeploymentStrategy | Which strategy we want for our deployment, only rolling update currently available. |
| maxUnavailable | int | Maximum amount of apps that can be unavailable at the time. |
| maxSurge    | int | Maximum number of apps to which we are allowed to scale during rolling update. |
| app    | map | Specification of application we want to run |
| name    | string | Name of application. |
| quotas | map | Resource quotas for each app, based on this total resource quota for deployment is calculated. |
| profile    | SeccompProfile | Seccomp profile described. |
| version | string | Seccomp profile version |
| default_action | string | Default action |
| syscalls | map | System calls |
| names | array | Names of syscalls |
| action | string | Action |
| seccompDefinitionStrategy | string | Seccomp strategy |
| minReadySeconds | int | Minimum number of seconds application needs to be running to be considered available for service |
| deadlineExceeded | int | Number of seconds until deployment is considered to fail if it does not meet its wanted state |
| automaticRollback | bool | If true, when deployment fails, it will automatically rollback to previous revision if one is available. If false, it will just pause the deployment |
| mode | string (IndirectStar, DirectStar, DirectDocker) | How our deployment is deployed, IndirectStar is if we are running containers on node agents on remote machines, DirectStar is if we are running containers on node agents only on our local machine, DirectDocker is if we are running containers directly via Docker Daemon |
| reconciliationPeriod | int (optional) | Number of seconds until deployment is periodically reconciled. Default is 30. |
| status | map | Status of deployment. |
| states | map | Status of deployment has 3 states, each represent seperate part of status, Progress shows if deployment is currently rolling or not, it does not roll either when its completed, it failed or is paused. Available shows if enough available apps are running for deployment to be considered available. Failue shows if deployment failed, it is failed if deadline for progress exceeds. |
| active | bool | If state is active it is currently true, if not it is not |
| lastUpdateTimestamp | int | Last timestamp since update for deployment has been requested |
| lastTransitionTimestamp | int | Last timestamp since state has been updated |
| totalAppCount | string | Number of apps running |
| updatedAppCount | string | Number of apps with newest revision running |
| readyAppCount | string | Number of apps that are healthy running |
| availableAppCount | string | Number of apps thate are available running |
| unavailableAppCount | string | Number of apps that are not available running |
| paused | bool | If true, rolling is paused, and deadline is not exceeding |
| stopped | bool | If true, stops all apps with selector labels of current recorded revisions  |
| deleted | bool | if true, deletes deployment and all deployment owned revisions, stops deployment's apps if running.|



#### /RollbackRevision

The endpoint for rolling back a revision for deployment

#### Request body

```json
{
    "name": "my-deployment",
    "namespace": "default",
    "orgId": "c12s",
    "revisionName": "my-deployment-2f85684166"
}
```
##### Body Parameters
| Parameter | Type   | Description                                                  |
|-----------|--------|--------------------------------------------------------------|
| name | string | Name of the deployment. |
| namespace    | string | Namespace where deployment is located. |
| orgId    | string | Organisation to which namespace and name belong to. |
| revisionName    | string (optional) | Which revision to rollback to, if left empty "", it will rollback to previous revision. |

#### Response - 0 OK

```json
{}
```

#### /GetDeploymentOwnedRevisions

The endpoint for getting all revisions owned by a deployment.

#### Request body

```json
{
    "name": "my-deployment",
    "namespace": "default",
    "orgId": "c12s"
}
```

#### Response - 0 OK

```json
{
        "revisions": [
            {
                "labels": {
                    "team": "backend",
                    "env": "production"
                },
                "name": "my-deployment-2f85684166",
                "namespace": "default",
                "orgId": "c12s",
                "creationTimestamp": "1733596483",
                "spec": {
                    "selectorLabels": {
                        "deployment": "my-deployment",
                        "revision": "my-deployment-2f85684166",
                        "app": "example-app"
                    },
                    "appSpec": {
                        "selectorLabels": {
                            "deployment": "my-deployment",
                            "revision": "my-deployment-2f85684166",
                            "app": "example-app"
                        },
                        "quotas": {
                            "mem": 4,
                            "cpu": 2
                        },
                        "name": "another whole new application",
                        "namespace": "default",
                        "orgId": "c12s",
                        "seccompProfile": {
                            "syscalls": [
                                {
                                    "names": [
                                        "syscall1",
                                        "syscall2"
                                    ],
                                    "action": "SCMP_ACT_ERRNO"
                                }
                            ],
                            "version": "v1",
                            "default_action": "NOT_ALLOWED"
                        },
                        "seccompDefinitionStrategy": "runtimeDefault"
                    }
                }
            }
        ]
}
```


##### Body Parameters
| Parameter | Type   | Description                                                  |
|-----------|--------|--------------------------------------------------------------|
| name | string | Name of the revision. |
| namespace    | string | Namespace where revision is located. |
| orgId    | string | Organisation to which namespace and name belong to. |
| labels    | map | Map of labels attached to reivison |
| spec    | map | Specification of revision. |
| selectorLabels    | map | Map of selector labels, by which we determine which revisions and apps belong to which deployment. |
| timestamp    | int | Timestamp of when revision was created. |
| app    | map | Specification of application we want to run |
| name    | string | Name of application. |
| quotas | map (float) | Resource quotas for each app, based on this total resource quota for deployment is calculated.|
| profile    | SeccompProfile | Seccomp profile described. |
| version | string | Seccomp profile version |
| default_action | string | Default action |
| syscalls | map | System calls |
| names | array | Names of syscalls |
| action | string | Action |
| seccompDefinitionStrategy | string | Seccomp strategy |
| minReadySeconds | int | Minimum number of seconds application needs to be running to be considered available for service |


#### /GetNewRevision

The endpoint for getting the newest recorded revision.

#### Request body

```json
{
    "name": "my-deployment",
    "namespace": "default",
    "orgId": "c12s"
}
```

#### Response - 0 OK

```json
{
        "revision": {
            "labels": {
                "team": "backend",
                "env": "production"
            },
            "name": "my-deployment-2f85684166",
            "namespace": "default",
            "orgId": "c12s",
            "creationTimestamp": "1733596483",
            "spec": {
                "selectorLabels": {
                    "revision": "my-deployment-2f85684166",
                    "app": "example-app",
                    "deployment": "my-deployment"
                },
                "appSpec": {
                    "selectorLabels": {
                        "deployment": "my-deployment",
                        "revision": "my-deployment-2f85684166",
                        "app": "example-app"
                    },
                    "quotas": {
                        "cpu": 2,
                        "mem": 4
                    },
                    "name": "another whole new application",
                    "namespace": "default",
                    "orgId": "c12s",
                    "seccompProfile": {
                        "syscalls": [
                            {
                                "names": [
                                    "syscall1",
                                    "syscall2"
                                ],
                                "action": "SCMP_ACT_ERRNO"
                            }
                        ],
                        "version": "v1",
                        "default_action": "NOT_ALLOWED"
                    },
                    "seccompDefinitionStrategy": "runtimeDefault"
                }
            }
        }
}
```

##### Body Parameters
| Parameter | Type   | Description                                                  |
|-----------|--------|--------------------------------------------------------------|
| name | string | Name of the revision. |
| namespace    | string | Namespace where revision is located. |
| orgId    | string | Organisation to which namespace and name belong to. |
| labels    | map | Map of labels attached to reivison |
| spec    | map | Specification of revision. |
| selectorLabels    | map | Map of selector labels, by which we determine which revisions and apps belong to which deployment. |
| timestamp    | int | Timestamp of when revision was created. |
| app    | map | Specification of application we want to run |
| name    | string | Name of application. |
| quotas | map (float) | Resource quotas for each app, based on this total resource quota for deployment is calculated.|
| profile    | SeccompProfile | Seccomp profile described. |
| version | string | Seccomp profile version |
| default_action | string | Default action |
| syscalls | map | System calls |
| names | array | Names of syscalls |
| action | string | Action |
| seccompDefinitionStrategy | string | Seccomp strategy |
| minReadySeconds | int | Minimum number of seconds application needs to be running to be considered available for service |


#### /PauseDeployment

The endpoint for pausing the deployment, when paused deadline does not exceed, rolling of new containers is also paused.

#### Request body

```json
{
    "name": "my-deployment",
    "namespace": "default",
    "orgId": "c12s"
}
```

#### Response - 0 OK

```json
{}
```

#### /UnpauseDeployment

The endpoint for unpausing the deployment.

#### Request body

```json
{
    "name": "my-deployment",
    "namespace": "default",
    "orgId": "c12s"
}
```

#### Response - 0 OK

```json
{}
```

#### /StopDeployment

The endpoint for stopping the deployment, stops all apps with selector labels of current recorded revisions.

#### Request body

```json
{
    "name": "my-deployment",
    "namespace": "default",
    "orgId": "c12s"
}
```

#### Response - 0 OK

```json
{}
```

#### /DeleteDeployment

The endpoint for deleting deployment and all deployment owned revisions, stops deployment apps if running.

#### Request body

```json
{
    "name": "my-deployment",
    "namespace": "default",
    "orgId": "c12s"
}
```

#### Response - 0 OK

```json
{}
```


## Nats communication

Rolling Update Service communicates with the Star node agents via NATS, where Rolling Update Service sends operations to apply on containers running on nodes.
