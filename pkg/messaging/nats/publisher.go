package nats

import (
	"errors"
	"update-service/pkg/messaging"

	"github.com/nats-io/nats.go"
)

type publisher struct {
	conn *nats.Conn
}

func NewPublisher(conn *nats.Conn) (messaging.Publisher, error) {
	if conn == nil || !conn.IsConnected() {
		return nil, errors.New("connection error")
	}
	return &publisher{
		conn: conn,
	}, nil
}

func (p publisher) Publish(msg []byte, subject string) error {
	return p.conn.Publish(subject, msg)
}

func (p publisher) Subscribe(msg []byte, subject, replySubject string) error {
	return p.conn.PublishRequest(subject, replySubject, msg)
}

func (p publisher) GenerateReplySubject() string {
	return nats.NewInbox()
}
