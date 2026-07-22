package rabbitmq

import (
	amqp091 "github.com/rabbitmq/amqp091-go"

	"github.com/labspangaea/go-lib/pubsub"
)

// ToAMQPTableForTest exposes toAMQPTable for external test packages.
func ToAMQPTableForTest(headers map[string]string) amqp091.Table {
	return toAMQPTable(headers)
}

// AMQPToMessageForTest exposes amqpToMessage for external test packages.
func AMQPToMessageForTest(d amqp091.Delivery) pubsub.Message {
	return amqpToMessage(d)
}
