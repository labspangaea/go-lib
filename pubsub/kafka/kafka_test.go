package kafka_test

import (
	"fmt"
	"testing"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/labspangaea/go-lib/pubsub"
	"github.com/labspangaea/go-lib/pubsub/kafka"
)

func TestKafkaToMessage_Full(t *testing.T) {
	km := kafkago.Message{
		Partition: 3,
		Offset:    42,
		Key:       []byte("order-123"),
		Value:     []byte(`{"id":1}`),
		Headers: []kafkago.Header{
			{Key: "trace_id", Value: []byte("abc")},
			{Key: "source", Value: []byte("test")},
		},
	}
	msg := KafkaToMessage(km)

	wantID := fmt.Sprintf("%d/%d", km.Partition, km.Offset)
	if msg.ID != wantID {
		t.Errorf("ID = %q, want %q", msg.ID, wantID)
	}
	if msg.Key != "order-123" {
		t.Errorf("Key = %q, want order-123", msg.Key)
	}
	if string(msg.Payload) != `{"id":1}` {
		t.Errorf("Payload = %q, want {\"id\":1}", msg.Payload)
	}
	if msg.Headers["trace_id"] != "abc" {
		t.Errorf("Headers[trace_id] = %q, want abc", msg.Headers["trace_id"])
	}
	if msg.Headers["source"] != "test" {
		t.Errorf("Headers[source] = %q, want test", msg.Headers["source"])
	}
}

func TestKafkaToMessage_EmptyHeaders(t *testing.T) {
	km := kafkago.Message{
		Partition: 0,
		Offset:    0,
		Value:     []byte("hello"),
	}
	msg := KafkaToMessage(km)
	if len(msg.Headers) != 0 {
		t.Errorf("Headers = %v, want empty", msg.Headers)
	}
}

func TestKafkaToMessage_EmptyKey(t *testing.T) {
	km := kafkago.Message{Value: []byte("v")}
	msg := KafkaToMessage(km)
	if msg.Key != "" {
		t.Errorf("Key = %q, want empty", msg.Key)
	}
}

func TestNewPublisher(t *testing.T) {
	// Verify that construction does not panic with valid brokers.
	p := kafka.NewPublisher([]string{"localhost:9092"}, kafka.WithMaxAttempts(5))
	if p == nil {
		t.Fatal("expected non-nil publisher")
	}
	_ = p.Close()
}

func TestNewConsumer(t *testing.T) {
	c := kafka.NewConsumer(
		[]string{"localhost:9092"},
		kafka.WithLastOffset(),
		kafka.WithMaxBytes(1<<20),
	)
	if c == nil {
		t.Fatal("expected non-nil consumer")
	}
	_ = c.Close()
}

// KafkaToMessage is a test helper that calls the unexported kafkaToMessage.
// Since we're in an external test package, we need to expose it via an
// export_test.go file in the kafka package.
func KafkaToMessage(m kafkago.Message) pubsub.Message {
	return kafka.KafkaToMessageForTest(m)
}
