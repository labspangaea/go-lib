// Package redis provides a Redis Pub/Sub-backed implementation of pubsub.Publisher
// and pubsub.Consumer using the go-redis/v9 driver.
//
// Redis Pub/Sub is a fire-and-forget broadcast: messages are not persisted and
// are delivered only to subscribers active at the moment of publish. There is no
// consumer group, no offset, and no redelivery on failure.
//
// Use this backend for low-latency fanout (live notifications, cache invalidation).
// For durable, at-least-once delivery use the Kafka or RabbitMQ backends instead.
package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	goredis "github.com/redis/go-redis/v9"

	"github.com/labspangaea/go-lib/logger"
	"github.com/labspangaea/go-lib/pubsub"
)

// wire is the JSON envelope stored in a Redis channel message.
// We encode the full pubsub.Message so ID, Key, and Headers are preserved.
type wire struct {
	ID      string            `json:"id,omitempty"`
	Key     string            `json:"key,omitempty"`
	Payload []byte            `json:"payload,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// Publisher sends messages to a Redis channel.
// Safe for concurrent use by multiple goroutines.
type Publisher struct {
	client *goredis.Client
}

// NewPublisher creates a Publisher that publishes to the given Redis client.
// The caller owns the client lifecycle — call client.Close() after pub.Close().
func NewPublisher(client *goredis.Client) *Publisher {
	return &Publisher{client: client}
}

// Publish sends msg to the Redis channel named topic.
// The message is JSON-encoded so all fields (ID, Key, Headers, Payload) are preserved.
func (p *Publisher) Publish(ctx context.Context, topic string, msg pubsub.Message) error {
	data, err := json.Marshal(wire{
		ID:      msg.ID,
		Key:     msg.Key,
		Payload: msg.Payload,
		Headers: msg.Headers,
	})
	if err != nil {
		return fmt.Errorf("redis pubsub: marshal message: %w", err)
	}
	if err := p.client.Publish(ctx, topic, data).Err(); err != nil {
		return fmt.Errorf("redis pubsub: publish to %s: %w", topic, err)
	}
	return nil
}

// Close is a no-op — the Redis client is owned by the caller.
func (p *Publisher) Close() error { return nil }

// Consumer receives messages from a Redis channel.
// Safe for concurrent use; each Subscribe call creates an independent subscription.
type Consumer struct {
	client *goredis.Client
}

// NewConsumer creates a Consumer backed by client.
// The caller owns the client lifecycle — call client.Close() after con.Close().
func NewConsumer(client *goredis.Client) *Consumer {
	return &Consumer{client: client}
}

// Subscribe blocks, dispatching messages from the Redis channel named topic to handler.
//
// The group parameter is accepted for interface compatibility but is not used —
// Redis Pub/Sub has no consumer-group concept. Every active subscriber on a channel
// receives every message (broadcast semantics).
//
// Handler return values are noted but have no broker-level effect:
//   - nil  → message considered processed
//   - error → logged by the caller; the message is not requeued
//
// Returns nil when ctx is cancelled, non-nil on a broker error.
func (c *Consumer) Subscribe(ctx context.Context, topic, _ string, handler pubsub.Handler) error {
	sub := c.client.Subscribe(ctx, topic)
	defer sub.Close()

	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-ch:
			if !ok {
				return errors.New("redis pubsub: subscription channel closed")
			}
			var w wire
			if err := json.Unmarshal([]byte(msg.Payload), &w); err != nil {
				return fmt.Errorf("redis pubsub: unmarshal message on %s: %w", topic, err)
			}
			// context.WithoutCancel: shutdown cancels the receive loop but the
			// handler sees a live context so its own DB/HTTP calls are not aborted.
			if err := handler(context.WithoutCancel(ctx), pubsub.Message{
				ID:      w.ID,
				Key:     w.Key,
				Payload: w.Payload,
				Headers: w.Headers,
			}); err != nil {
				logger.FromContext(ctx).Warn("redis pubsub: handler error",
					slog.String("topic", topic),
					slog.Any(logger.KeyError, err),
				)
			}
		}
	}
}

// Close is a no-op — Subscribe subscriptions are scoped to their call and closed
// when ctx is cancelled. The Redis client is owned by the caller.
func (c *Consumer) Close() error { return nil }
