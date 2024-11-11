package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"rolling_update_service/internal/handlers"
	"rolling_update_service/internal/marshallers/proto"
	"rolling_update_service/internal/repos"
	"rolling_update_service/pkg/api"
	"syscall"

	"github.com/docker/docker/client"
	"github.com/nats-io/nats.go"
	etcd "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {

	fmt.Printf("Connecting to etcd at: http://%s\n", os.Getenv("UPDATE_SERVICE_ETCD_ADDRESS"))

	etcdClient, err := etcd.New(etcd.Config{
		Endpoints: []string{fmt.Sprintf("http://%s", os.Getenv("UPDATE_SERVICE_ETCD_ADDRESS"))},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer etcdClient.Close()

	natsAddress := fmt.Sprintf("nats://%s:%s", os.Getenv("NATS_HOSTNAME"), os.Getenv("NATS_PORT"))
	log.Println("Connecting to NATS at:", natsAddress)
	natsConn, err := nats.Connect(natsAddress)
	if err != nil {
		log.Println("Failed to connect to NATS:", err)
		log.Fatal(err)
	}
	defer natsConn.Close()

	dockerClientAddress := os.Getenv("DOCKER_CLIENT_ADDRESS")
	dockerClient, err := client.NewClientWithOpts(client.WithHost(dockerClientAddress), client.WithAPIVersionNegotiation())
	log.Println("Connecting to Docker:", dockerClient.DaemonHost())
	if err != nil {
		log.Fatal(err)
	}
	defer dockerClient.Close()

	deploymentMarshaller := proto.NewProtoDeploymentMarshaller()
	deploymentRepo, err := repos.NewDeploymentEtcdRepo(etcdClient, deploymentMarshaller)
	if err != nil {
		log.Fatal(err)
	}

	revisionMarshaller := proto.NewProtoRevisionMarshaller()
	revisionRepo, err := repos.NewRevisionEtcdRepo(etcdClient, revisionMarshaller)
	if err != nil {
		log.Fatal(err)
	}

	updateService := handlers.NewUpdateServiceGrpcHandler(deploymentRepo, revisionRepo, natsConn, dockerClient)

	server := grpc.NewServer()
	api.RegisterUpdateServiceServer(server, updateService)
	reflection.Register(server)

	listen, err := net.Listen("tcp", os.Getenv("UPDATE_SERVICE_ADDRESS"))
	if err != nil {
		log.Fatal(err)
	}

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGTERM, syscall.SIGINT)

	go func() {

		log.Printf("Server listening on %v", listen.Addr())
		if err := server.Serve(listen); err != nil {
			log.Fatal("failed to serve: ", err)
		}
	}()

	<-shutdown

	server.GracefulStop()
}
