// Package queue defines a work-queue abstraction over message brokers.
// Concrete backends (Kafka, RabbitMQ, Redis Streams) live in sub-packages so
// callers depend on the interface, not the SDK.
//
// # queue vs pubsub
//
// The two packages differ in who receives a message, not in which brokers they
// support:
//
//   - queue   — competing consumers. Each message is delivered to exactly one
//     member of a group. Adding replicas divides the work.
//   - pubsub  — fan-out. Each subscriber receives every message. Adding
//     replicas multiplies the work.
//
// Every backend here guarantees at-least-once delivery within a group: a
// message whose handler returns an error is redelivered rather than dropped.
// That is the property pubsub's redis backend cannot offer, since Redis
// Pub/Sub has no backlog and no acknowledgement — which is exactly why this
// package's redis backend is built on Streams instead.
//
// Pick queue for work that must happen once: orders to fulfil, emails to send,
// invoices to generate. Pick pubsub for facts every listener wants: cache
// invalidations, live dashboard updates, notifications.
//
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=queue.go -destination=mocks/mock_queue.go -package=mocks
package queue

import (
	"context"
	"errors"
)

// ErrClosed is returned when Publish or Consume is called after Close.
var ErrClosed = errors.New("queue: client is closed")

// Message is the unit of work exchanged over a queue.
type Message struct {
	// ID uniquely identifies the message. If empty on Publish, the backend
	// generates one. Always populated on receive.
	ID string

	// Key groups related messages so they are processed in order relative to
	// one another: the Kafka partition key, the AMQP routing key, or ignored
	// by Redis Streams, which has no partitioning.
	//
	// Ordering across different keys is never guaranteed.
	Key string

	// Payload is the raw message body.
	Payload []byte

	// Headers carries arbitrary metadata alongside the message.
	Headers map[string]string

	// Attempt counts how many times this message has been delivered, starting
	// at 1. Values above 1 mean an earlier attempt failed or timed out.
	//
	// Use it to stop retrying a message that will never succeed — a poison
	// payload redelivered forever blocks nothing on Kafka but will occupy a
	// RabbitMQ consumer indefinitely. Backends that cannot report a true count
	// leave this at 1; treat it as a lower bound, not an exact figure.
	Attempt int
}

// Handler processes a single unit of work.
//
// Return nil to acknowledge: the message is removed from the queue and will
// not be redelivered.
//
// Return a non-nil error to signal failure. The message is redelivered — the
// Kafka offset is left uncommitted, the AMQP delivery is nacked with requeue,
// the Redis Streams entry is left pending for reclaim. Return an error only
// when a retry could plausibly succeed; for a permanently bad payload, log it
// and return nil, or the queue will hand it back forever.
type Handler func(ctx context.Context, msg Message) error

// Publisher sends work to a named queue.
// Implementations must be safe for concurrent use.
type Publisher interface {
	// Publish sends msg to queue, blocking until the broker confirms receipt.
	Publish(ctx context.Context, queue string, msg Message) error

	// Close flushes any buffered messages and releases resources.
	Close() error
}

// Consumer receives work from a named queue as part of a group.
// Implementations must be safe for concurrent use.
type Consumer interface {
	// Consume blocks, reading messages from queue as a member of group, until
	// ctx is cancelled or a fatal broker error occurs.
	//
	// Every member of the same group competes for messages: each message goes
	// to exactly one of them. Members of different groups each get their own
	// copy, so a second group is how you add an independent consumer without
	// stealing work from the first.
	//
	// Consume returns nil on clean shutdown (ctx cancelled) and a non-nil
	// error only for unrecoverable broker failures.
	Consume(ctx context.Context, queue, group string, handler Handler) error

	// Close stops an active Consume loop and releases resources.
	Close() error
}
