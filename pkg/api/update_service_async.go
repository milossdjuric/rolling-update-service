package api

import (
	"fmt"
	"log"

	"github.com/milossdjuric/rolling_update_service/pkg/messaging"
	"github.com/milossdjuric/rolling_update_service/pkg/messaging/nats"

	natsgo "github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

type UpdateServiceAsyncClient struct {
	subscriber messaging.Subsriber
	publisher  messaging.Publisher
}

func NewUpdateServiceAsyncClient(address, nodeId string) (*UpdateServiceAsyncClient, error) {

	conn, err := natsgo.Connect(fmt.Sprintf("nats://%s", address))
	if err != nil {
		return nil, err
	}
	subscriber, err := nats.NewSubscriber(conn, Subject(nodeId), nodeId)
	if err != nil {
		return nil, err
	}
	publisher, err := nats.NewPublisher(conn)
	if err != nil {
		return nil, err
	}
	return &UpdateServiceAsyncClient{
		subscriber: subscriber,
		publisher:  publisher,
	}, nil
}

func (c *UpdateServiceAsyncClient) GracefulStop() {
	err := c.subscriber.Unsubscribe()
	if err != nil {
		log.Println("Error unsubscribing: ", err)
	}
}

func Subject(nodeId string) string {
	return fmt.Sprintf("%s.app_operation", nodeId)
}

func (c *UpdateServiceAsyncClient) ReceiveAppOperation(handler ApplyAppOperationHandler) error {
	return c.subscriber.Subscribe(func(msg []byte, replySubject string) {
		cmd := &ApplyAppOperationCommand{}
		err := proto.Unmarshal(msg, cmd)
		if err != nil {
			log.Println(err)
			return
		}
		err = handler(cmd.OrgId, cmd.Namespace, cmd.Name, cmd.Operation, cmd.SelectorLabels, cmd.MinReadySeconds)
		if err != nil {
			log.Println(err)
		}
	})
}

type ApplyAppOperationHandler func(orgId, namespace, name, operation string, selectorLabels map[string]string, minReadySeconds int64) error

// func (c *UpdateServiceAsyncClient) StartApp(name string, selectorLabels map[string]string) error {
// 	cmd := &StartAppCommand{
// 		Name:           name,
// 		SelectorLabels: selectorLabels,
// 	}
// 	data, err := proto.Marshal(cmd)
// 	if err != nil {
// 		return err
// 	}
// 	return c.publisher.Publish(data, Subject("us_start_app"))
// }

// func (c *UpdateServiceAsyncClient) StopApp(name string, selectorLabels map[string]string) error {
// 	cmd := &StopAppCommand{
// 		Name: name,
// 	}
// 	data, err := proto.Marshal(cmd)
// 	if err != nil {
// 		return err
// 	}
// 	return c.publisher.Publish(data, Subject("us_stop_app"))
// }

// func (c *UpdateServiceAsyncClient) QueryApp(prefix string, selectorLabels map[string]string) error {
// 	cmd := &QueryAppCommand{
// 		Prefix:         prefix,
// 		SelectorLabels: selectorLabels,
// 	}
// 	data, err := proto.Marshal(cmd)
// 	if err != nil {
// 		return err
// 	}
// 	return c.publisher.Publish(data, Subject("us_query_app"))
// }

// func (c *UpdateServiceAsyncClient) HealthCheckApp(name string) error {
// 	cmd := &HealtCheckCommand{
// 		Name: name,
// 	}
// 	data, err := proto.Marshal(cmd)
// 	if err != nil {
// 		return err
// 	}
// 	return c.publisher.Publish(data, Subject("us_health_check_app"))
// }

// func (c *UpdateServiceAsyncClient) AvailabilityCheckApp(name string) error {
// 	cmd := &AvailabilityCheckCommand{
// 		Name: name,
// 	}
// 	data, err := proto.Marshal(cmd)
// 	if err != nil {
// 		return err
// 	}
// 	return c.publisher.Publish(data, Subject("us_availability_check_app"))
// }
