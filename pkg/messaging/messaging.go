package messaging

import "github.com/nats-io/nats.go"

type Subsriber interface {
	Subscribe(handler func(msg []byte, replySubject string)) error
	Unsubscribe() error
	ChannelSubscribe(channel chan *nats.Msg) error
	SubscribeJetStream(handler func(msg []byte, replySubject string)) error
	UnsubscribeJetStream() error
	ChannelSubscribeJetStream(channel chan *nats.Msg) error
}

type Publisher interface {
	Publish(msg []byte, subject string) error
	Subscribe(msg []byte, subject, replySubject string) error
	GenerateReplySubject() string
}
