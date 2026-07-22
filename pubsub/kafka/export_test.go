package kafka

import (
	kafkago "github.com/segmentio/kafka-go"

	"github.com/labspangaea/go-lib/pubsub"
)

// KafkaToMessageForTest exposes kafkaToMessage for external test packages.
func KafkaToMessageForTest(m kafkago.Message) pubsub.Message {
	return kafkaToMessage(m)
}
