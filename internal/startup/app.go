package startup

import (
	"fmt"
	"log"
	"net"

	magnetarapi "github.com/c12s/magnetar/pkg/api"
	"github.com/docker/docker/client"
	"github.com/milossdjuric/rolling_update_service/internal/configs"
	"github.com/milossdjuric/rolling_update_service/internal/domain"
	"github.com/milossdjuric/rolling_update_service/internal/handlers"
	"github.com/milossdjuric/rolling_update_service/internal/marshallers/proto"
	"github.com/milossdjuric/rolling_update_service/internal/repos"
	"github.com/milossdjuric/rolling_update_service/pkg/api"
	"github.com/nats-io/nats.go"
	etcd "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"
)

type app struct {
	config               *configs.Config
	grpcServer           *grpc.Server
	magnetar             magnetarapi.MagnetarClient
	deploymentRepo       domain.DeploymentRepo
	revisionRepo         domain.RevisionRepo
	deploymentMarshaller domain.DeploymentMarshaller
	revisionMarshaller   domain.RevisionMarshaller
	dockerClient         *client.Client
	shutdownProcesses    []func()
}

func NewAppWithConfig(config *configs.Config) (*app, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}
	return &app{
		config:            config,
		shutdownProcesses: make([]func(), 0),
	}, nil
}

func (a *app) Start() error {
	a.init()
	return a.startGrpcServer()
}

func (a *app) startGrpcServer() error {
	listen, err := net.Listen("tcp", a.config.ServerAddress())
	if err != nil {
		return err
	}
	go func() {
		log.Printf("Server listening on %v", listen.Addr())
		if err := a.grpcServer.Serve(listen); err != nil {
			log.Fatal("failed to serve: ", err)
		}
	}()
	return nil
}

func (a *app) init() {

	etcdClient, err := etcd.New(etcd.Config{
		Endpoints: []string{fmt.Sprintf("http://" + a.config.EtcdAddress())},
	})
	if err != nil {
		log.Fatal(err)
	}
	a.shutdownProcesses = append(a.shutdownProcesses, func() {
		etcdClient.Close()
	})

	natsConn, err := nats.Connect(fmt.Sprintf("nats://" + a.config.NatsAddress()))
	if err != nil {
		log.Fatal(err)
	}
	a.shutdownProcesses = append(a.shutdownProcesses, func() {
		natsConn.Close()
	})

	a.dockerClient, err = client.NewClientWithOpts(client.WithHost(a.config.DockerAddress()), client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatal(err)
	}
	a.shutdownProcesses = append(a.shutdownProcesses, func() {
		a.dockerClient.Close()
	})

	connMagnetar, err := grpc.NewClient(a.config.MagnetarAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatal(err)
	}
	a.shutdownProcesses = append(a.shutdownProcesses, func() {
		connMagnetar.Close()
	})
	a.magnetar = magnetarapi.NewMagnetarClient(connMagnetar)

	deploymentMarshaller := proto.NewProtoDeploymentMarshaller()
	revisionMarshaller := proto.NewProtoRevisionMarshaller()

	deploymentRepo, err := repos.NewDeploymentEtcdRepo(etcdClient, deploymentMarshaller)
	if err != nil {
		log.Fatal(err)
	}
	a.deploymentRepo = deploymentRepo

	revisionRepo, err := repos.NewRevisionEtcdRepo(etcdClient, revisionMarshaller)
	if err != nil {
		log.Fatal(err)
	}
	a.revisionRepo = revisionRepo

	a.deploymentMarshaller = deploymentMarshaller
	a.revisionMarshaller = revisionMarshaller

	updateService := handlers.NewUpdateServiceGrpcHandler(a.deploymentRepo, a.revisionRepo, natsConn, a.dockerClient, a.magnetar)

	a.grpcServer = grpc.NewServer()
	api.RegisterUpdateServiceServer(a.grpcServer, updateService)
	reflection.Register(a.grpcServer)
}

func (a *app) Shutdown() {
	for _, process := range a.shutdownProcesses {
		process()
	}
}
