// Package rabbitmq provides RabbitMQ-backed implementations of
// queue.Publisher and queue.Consumer using amqp091-go.
//
// A durable queue with multiple consumers is already work-queue semantics —
// AMQP round-robins deliveries across whoever is attached, so replicas divide
// the work. This package adapts the same mechanics pubsub/rabbitmq uses to the
// queue interface, where the naming matches the behaviour.
//
// Unlike Kafka, concurrency here is not capped by partition count: adding a
// consumer to a queue immediately increases throughput. The trade is ordering
// — with more than one consumer, messages are processed concurrently and their
// relative order is not preserved.
package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/rabbitmq/amqp091-go"

	"github.com/labspangaea/go-lib/logger"
	"github.com/labspangaea/go-lib/queue"
)

// Publisher sends messages to a RabbitMQ exchange.
// Safe for concurrent use by multiple goroutines.
type Publisher struct {
	mu       sync.Mutex
	ch       *amqp091.Channel
	exchange string
}

// NewPublisher opens a channel on conn and declares the exchange.
// The caller owns the connection — close it after pub.Close().
func NewPublisher(conn *amqp091.Connection, exchange string, opts ...PublisherOption) (*Publisher, error) {
	cfg := defaultPublisherConfig()
	for _, o := range opts {
		o(cfg)
	}
	ch, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("rabbitmq queue: open channel: %w", err)
	}
	if err := ch.ExchangeDeclare(exchange, string(cfg.exchangeType), true, false, false, false, nil); err != nil {
		_ = ch.Close()
		return nil, fmt.Errorf("rabbitmq queue: declare exchange %q: %w", exchange, err)
	}
	return &Publisher{ch: ch, exchange: exchange}, nil
}

// Publish sends msg to the exchange with q as the routing key.
//
// DeliveryMode is Persistent so accepted work survives a broker restart. A
// durable queue holding transient messages loses them anyway, which is the
// sort of half-durability that only shows up during an incident.
func (p *Publisher) Publish(ctx context.Context, q string, msg queue.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	pub := amqp091.Publishing{
		MessageId:    msg.ID,
		Body:         msg.Payload,
		Headers:      toAMQPTable(msg.Headers),
		ContentType:  "application/octet-stream",
		DeliveryMode: amqp091.Persistent,
	}
	if err := p.ch.PublishWithContext(ctx, p.exchange, q, false, false, pub); err != nil {
		return fmt.Errorf("rabbitmq queue: publish to %s/%s: %w", p.exchange, q, err)
	}
	return nil
}

// Close releases the publisher channel.
func (p *Publisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ch == nil {
		return nil
	}
	if err := p.ch.Close(); err != nil {
		return fmt.Errorf("rabbitmq queue: close publisher: %w", err)
	}
	p.ch = nil
	return nil
}

// Consumer receives messages from a RabbitMQ queue.
type Consumer struct {
	conn     *amqp091.Connection
	exchange string
	cfg      *consumerConfig
}

// NewConsumer creates a Consumer reading from the given connection.
func NewConsumer(conn *amqp091.Connection, exchange string, opts ...ConsumerOption) *Consumer {
	cfg := defaultConsumerConfig()
	for _, o := range opts {
		o(cfg)
	}
	return &Consumer{conn: conn, exchange: exchange, cfg: cfg}
}

// Consume reads from the durable queue named group, bound to the exchange with
// q as the routing key, until ctx is cancelled.
//
// Topology is declared on every call and is idempotent: a durable exchange, a
// durable queue named group, and a binding between them. Declaring rather than
// assuming means a fresh broker needs no setup step.
//
// Prefetch bounds how many unacked messages one consumer holds. Without it a
// single consumer would greedily buffer the whole queue and the others would
// sit idle — the classic reason "we added replicas and nothing got faster".
func (c *Consumer) Consume(ctx context.Context, q, group string, handler queue.Handler) error {
	ch, err := c.conn.Channel()
	if err != nil {
		return fmt.Errorf("rabbitmq queue: open channel: %w", err)
	}
	defer ch.Close()

	if err := ch.ExchangeDeclare(c.exchange, string(c.cfg.exchangeType), true, false, false, false, nil); err != nil {
		return fmt.Errorf("rabbitmq queue: declare exchange %q: %w", c.exchange, err)
	}
	if _, err := ch.QueueDeclare(group, true, false, false, false, nil); err != nil {
		return fmt.Errorf("rabbitmq queue: declare queue %q: %w", group, err)
	}
	if err := ch.QueueBind(group, q, c.exchange, false, nil); err != nil {
		return fmt.Errorf("rabbitmq queue: bind %q → %s/%s: %w", group, c.exchange, q, err)
	}
	if err := ch.Qos(c.cfg.prefetchCount, 0, false); err != nil {
		return fmt.Errorf("rabbitmq queue: set QoS: %w", err)
	}

	deliveries, err := ch.Consume(group, "", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("rabbitmq queue: consume %q: %w", group, err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case d, ok := <-deliveries:
			if !ok {
				return errors.New("rabbitmq queue: delivery channel closed")
			}
			c.dispatch(ctx, q, group, d, handler)
		}
	}
}

// dispatch handles one delivery, acking on success and nacking on failure.
func (c *Consumer) dispatch(ctx context.Context, q, group string, d amqp091.Delivery, handler queue.Handler) {
	msg := toMessage(d)

	// Redelivered marks a message that has been handed out before, but AMQP
	// does not count how often. Once the attempt cap is reached, ack to drop
	// it: an infinitely requeued poison message is handed to a consumer
	// forever, which looks like a busy service doing nothing.
	if c.cfg.maxAttempts > 0 && d.Redelivered && msg.Attempt > c.cfg.maxAttempts {
		logger.FromContext(ctx).Error("rabbitmq queue: dropping redelivered message",
			slog.String("queue", group),
			slog.String("routing_key", q),
			slog.String("id", msg.ID),
		)
		_ = d.Ack(false)
		return
	}

	// context.WithoutCancel: shutdown stops the delivery loop, but the handler
	// keeps a live context so its own calls are not aborted mid-flight.
	if err := handler(context.WithoutCancel(ctx), msg); err != nil {
		logger.FromContext(ctx).Warn("rabbitmq queue: handler error",
			slog.String("exchange", c.exchange),
			slog.String("queue", group),
			slog.String("routing_key", q),
			slog.Any(logger.KeyError, err),
		)
		if nackErr := d.Nack(false, true); nackErr != nil {
			logger.FromContext(ctx).Error("rabbitmq queue: nack failed",
				slog.String("queue", group),
				slog.Any(logger.KeyError, nackErr),
			)
		}
		return
	}
	if ackErr := d.Ack(false); ackErr != nil {
		logger.FromContext(ctx).Warn("rabbitmq queue: ack failed",
			slog.String("queue", group),
			slog.Any(logger.KeyError, ackErr),
		)
	}
}

// Close is a no-op — Consume channels are scoped to their call and released
// when the context is cancelled. Reconnect logic belongs at the connection
// level, which the caller owns.
func (c *Consumer) Close() error { return nil }

// toMessage converts an AMQP delivery into a queue.Message.
//
// Attempt is 2 for anything marked Redelivered and 1 otherwise: AMQP reports
// that a message has been redelivered but not how many times, so this is a
// floor rather than a count.
func toMessage(d amqp091.Delivery) queue.Message {
	headers := make(map[string]string, len(d.Headers))
	for k, v := range d.Headers {
		if s, ok := v.(string); ok {
			headers[k] = s
		}
	}
	attempt := 1
	if d.Redelivered {
		attempt = 2
	}
	return queue.Message{
		ID:      d.MessageId,
		Key:     d.RoutingKey,
		Payload: d.Body,
		Headers: headers,
		Attempt: attempt,
	}
}

func toAMQPTable(h map[string]string) amqp091.Table {
	if len(h) == 0 {
		return nil
	}
	t := make(amqp091.Table, len(h))
	for k, v := range h {
		t[k] = v
	}
	return t
}
