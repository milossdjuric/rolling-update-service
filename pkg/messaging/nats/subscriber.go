package nats

import (
	"errors"
	"log"

	"github.com/milossdjuric/rolling_update_service/pkg/messaging"
	"github.com/nats-io/nats.go"
)

type subscriber struct {
	conn         *nats.Conn
	subscription *nats.Subscription
	subject      string
	queue        string
	js           nats.JetStreamContext
}

func NewSubscriber(conn *nats.Conn, subject, queue string) (messaging.Subsriber, error) {
	if conn == nil {
		return nil, errors.New("connection error")
	}

	js, err := conn.JetStream()
	if err != nil {
		log.Println("JetStream not available")
		return nil, err
	}

	return &subscriber{
		conn:    conn,
		subject: subject,
		queue:   queue,
		js:      js,
	}, nil
}

func (s *subscriber) Subscribe(handler func(msg []byte, replySubject string)) error {
	if s.subscription != nil {
		return errors.New("already subscribed")
	}

	subscription, err := s.conn.QueueSubscribe(s.subject, s.queue, func(msg *nats.Msg) {
		handler(msg.Data, msg.Reply)
	})
	if err != nil {
		return err
	}
	s.subscription = subscription
	return nil
}

func (s *subscriber) Unsubscribe() error {
	if s.subscription != nil && s.subscription.IsValid() {
		return s.subscription.Drain()
	}
	return nil
}

func (s *subscriber) ChannelSubscribe(channel chan *nats.Msg) error {

	if s.subscription != nil {
		return errors.New("already subscribed")
	}

	subscription, err := s.conn.ChanSubscribe(s.subject, channel)
	if err != nil {
		return err
	}

	s.subscription = subscription
	return nil
}
