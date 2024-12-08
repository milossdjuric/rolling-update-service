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
            "mode": "DirectStar"
        }
}
```

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
