// Package pubsub defines a minimal publish/subscribe abstraction for message
// brokers. Concrete backends (Kafka, RabbitMQ) live in sub-packages so callers
// depend on the interface, not the SDK.
//
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=pubsub.go -destination=mocks/mock_pubsub.go -package=mocks
package pubsub

import (
	"context"
	"errors"
)

// ErrClosed is returned when Publish or Subscribe is called after Close.
var ErrClosed = errors.New("pubsub: client is closed")

// Message is the unit of data exchanged over a topic.
type Message struct {
	// ID uniquely identifies the message. If empty on Publish, the backend
	// may generate one. Always populated on receive.
	ID string

	// Key is the partition key (Kafka) or routing key (RabbitMQ).
	// Leave empty for round-robin / default routing.
	Key string

	// Payload is the raw message body.
	Payload []byte

	// Headers carries arbitrary metadata alongside the message.
	Headers map[string]string
}

// Handler processes a single received message.
//
// Return nil to acknowledge the message (commit Kafka offset / AMQP ack).
// Return a non-nil error to nack/reject — the backend decides whether to
// requeue or move the message to a dead-letter destination.
type Handler func(ctx context.Context, msg Message) error

// Publisher sends messages to a named topic.
// Implementations must be safe for concurrent use.
type Publisher interface {
	// Publish sends msg to topic. The call blocks until the broker confirms
	// receipt (at-least-once delivery guarantee).
	Publish(ctx context.Context, topic string, msg Message) error

	// Close flushes any buffered messages and releases resources.
	// No further Publish calls should be made after Close.
	Close() error
}

// Consumer receives messages from a named topic within a consumer group.
// Implementations must be safe for concurrent use.
type Consumer interface {
	// Subscribe blocks, reading messages from topic for group, until ctx is
	// cancelled or a fatal broker error occurs.
	//
	// Each message is dispatched to handler:
	//   - handler returns nil  → message is acknowledged
	//   - handler returns error → message is nacked / requeued
	//
	// Subscribe returns nil on clean shutdown (ctx cancelled). It returns a
	// non-nil error only for unrecoverable broker failures.
	Subscribe(ctx context.Context, topic, group string, handler Handler) error

	// Close stops an active Subscribe loop and releases resources.
	Close() error
}
