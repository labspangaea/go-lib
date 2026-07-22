package rabbitmq_test

import (
	"testing"

	amqp091 "github.com/rabbitmq/amqp091-go"

	"github.com/labspangaea/go-lib/pubsub/rabbitmq"
)

func TestToAMQPTable_Nil(t *testing.T) {
	table := rabbitmq.ToAMQPTableForTest(nil)
	if table != nil {
		t.Errorf("expected nil for nil input, got %v", table)
	}
}

func TestToAMQPTable_Empty(t *testing.T) {
	table := rabbitmq.ToAMQPTableForTest(map[string]string{})
	if table != nil {
		t.Errorf("expected nil for empty map, got %v", table)
	}
}

func TestToAMQPTable_Populated(t *testing.T) {
	table := rabbitmq.ToAMQPTableForTest(map[string]string{
		"trace_id": "abc",
		"source":   "test",
	})
	if table["trace_id"] != "abc" {
		t.Errorf("trace_id = %v, want abc", table["trace_id"])
	}
	if table["source"] != "test" {
		t.Errorf("source = %v, want test", table["source"])
	}
}

func TestAMQPToMessage_Full(t *testing.T) {
	d := amqp091.Delivery{
		MessageId:  "msg-1",
		RoutingKey: "orders.created",
		Body:       []byte(`{"id":1}`),
		Headers: amqp091.Table{
			"trace_id": "xyz",
			"version":  "2",
		},
	}
	msg := rabbitmq.AMQPToMessageForTest(d)

	if msg.ID != "msg-1" {
		t.Errorf("ID = %q, want msg-1", msg.ID)
	}
	if msg.Key != "orders.created" {
		t.Errorf("Key = %q, want orders.created", msg.Key)
	}
	if string(msg.Payload) != `{"id":1}` {
		t.Errorf("Payload = %q, want {\"id\":1}", msg.Payload)
	}
	if msg.Headers["trace_id"] != "xyz" {
		t.Errorf("Headers[trace_id] = %q, want xyz", msg.Headers["trace_id"])
	}
	if msg.Headers["version"] != "2" {
		t.Errorf("Headers[version] = %q, want 2", msg.Headers["version"])
	}
}

func TestAMQPToMessage_NonStringHeadersSkipped(t *testing.T) {
	d := amqp091.Delivery{
		Headers: amqp091.Table{
			"string_val": "ok",
			"int_val":    42, // non-string — should be skipped
		},
	}
	msg := rabbitmq.AMQPToMessageForTest(d)

	if msg.Headers["string_val"] != "ok" {
		t.Errorf("string_val = %q, want ok", msg.Headers["string_val"])
	}
	if _, exists := msg.Headers["int_val"]; exists {
		t.Errorf("int_val should be skipped, but found %q", msg.Headers["int_val"])
	}
}

func TestExchangeTypeConstants(t *testing.T) {
	tests := []struct {
		name string
		got  rabbitmq.ExchangeType
		want string
	}{
		{"Direct", rabbitmq.ExchangeDirect, "direct"},
		{"Topic", rabbitmq.ExchangeTopic, "topic"},
		{"Fanout", rabbitmq.ExchangeFanout, "fanout"},
		{"Headers", rabbitmq.ExchangeHeaders, "headers"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.got) != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}
