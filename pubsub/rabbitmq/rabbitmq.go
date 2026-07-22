// Package rabbitmq provides RabbitMQ-backed implementations of pubsub.Publisher
// and pubsub.Consumer using the amqp091 driver.
package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	amqp091 "github.com/rabbitmq/amqp091-go"

	"github.com/labspangaea/go-lib/logger"
	"github.com/labspangaea/go-lib/pubsub"
)

// Publisher sends messages to a RabbitMQ exchange.
// Safe for concurrent use by multiple goroutines.
type Publisher struct {
	mu       sync.Mutex
	ch       *amqp091.Channel
	exchange string
}

// NewPublisher creates a Publisher that publishes to exchange.
//
// The exchange is declared as durable (survives broker restarts). The caller
// is responsible for managing the connection lifecycle — pass the same
// *amqp091.Connection to both Publisher and Consumer where they share a broker.
func NewPublisher(conn *amqp091.Connection, exchange string, opts ...PublisherOption) (*Publisher, error) {
	cfg := defaultPublisherConfig()
	for _, o := range opts {
		o(cfg)
	}

	ch, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("rabbitmq: open channel: %w", err)
	}
	if err := ch.ExchangeDeclare(exchange, string(cfg.exchangeType), true, false, false, false, nil); err != nil {
		_ = ch.Close()
		return nil, fmt.Errorf("rabbitmq: declare exchange %q: %w", exchange, err)
	}
	return &Publisher{ch: ch, exchange: exchange}, nil
}

// Publish sends msg to the exchange using topic as the routing key.
// The message is marked persistent (survives broker restarts).
func (p *Publisher) Publish(ctx context.Context, topic string, msg pubsub.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	pub := amqp091.Publishing{
		MessageId:    msg.ID,
		Body:         msg.Payload,
		Headers:      toAMQPTable(msg.Headers),
		ContentType:  "application/octet-stream",
		DeliveryMode: amqp091.Persistent,
	}
	if err := p.ch.PublishWithContext(ctx, p.exchange, topic, false, false, pub); err != nil {
		return fmt.Errorf("rabbitmq: publish to %s/%s: %w", p.exchange, topic, err)
	}
	return nil
}

// Close closes the channel. The underlying connection is owned by the caller.
func (p *Publisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ch.Close()
}

// Consumer receives messages from a RabbitMQ queue bound to an exchange.
type Consumer struct {
	conn     *amqp091.Connection
	exchange string
	cfg      consumerConfig
}

// NewConsumer creates a Consumer that reads from queues on the given exchange.
// A new channel is created per Subscribe call, so multiple Subscribe calls
// on the same Consumer are safe and independent.
func NewConsumer(conn *amqp091.Connection, exchange string, opts ...ConsumerOption) *Consumer {
	cfg := defaultConsumerConfig()
	for _, o := range opts {
		o(cfg)
	}
	return &Consumer{conn: conn, exchange: exchange, cfg: *cfg}
}

// Subscribe blocks, consuming messages from queue group, bound to exchange with
// routing key topic.
//
// Topology declared automatically:
//   - Exchange: durable, type from WithConsumerExchangeType (default: direct)
//   - Queue: durable, named group
//   - Binding: queue group ← exchange, routing key = topic
//
// Acks on nil handler return; nacks with requeue on error return.
// Returns nil when ctx is cancelled, non-nil on unrecoverable broker error.
func (c *Consumer) Subscribe(ctx context.Context, topic, group string, handler pubsub.Handler) error {
	ch, err := c.conn.Channel()
	if err != nil {
		return fmt.Errorf("rabbitmq: open channel: %w", err)
	}
	defer ch.Close()

	if err := ch.ExchangeDeclare(c.exchange, string(c.cfg.exchangeType), true, false, false, false, nil); err != nil {
		return fmt.Errorf("rabbitmq: declare exchange %q: %w", c.exchange, err)
	}
	if _, err := ch.QueueDeclare(group, true, false, false, false, nil); err != nil {
		return fmt.Errorf("rabbitmq: declare queue %q: %w", group, err)
	}
	if err := ch.QueueBind(group, topic, c.exchange, false, nil); err != nil {
		return fmt.Errorf("rabbitmq: bind %q → %s/%s: %w", group, c.exchange, topic, err)
	}
	if err := ch.Qos(c.cfg.prefetchCount, 0, false); err != nil {
		return fmt.Errorf("rabbitmq: set QoS: %w", err)
	}

	deliveries, err := ch.Consume(group, "", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("rabbitmq: consume %q: %w", group, err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case d, ok := <-deliveries:
			if !ok {
				return errors.New("rabbitmq: delivery channel closed")
			}
			if err := handler(ctx, amqpToMessage(d)); err != nil {
				logger.FromContext(ctx).Warn("rabbitmq: handler error",
					slog.String("exchange", c.exchange),
					slog.String("queue", group),
					slog.String("routing_key", topic),
					slog.Any(logger.KeyError, err),
				)
				if nackErr := d.Nack(false, true); nackErr != nil {
					return fmt.Errorf("rabbitmq: nack %s/%s: %w", group, topic, nackErr)
				}
			} else {
				if ackErr := d.Ack(false); ackErr != nil {
					return fmt.Errorf("rabbitmq: ack %s/%s: %w", group, topic, ackErr)
				}
			}
		}
	}
}

// Close is a no-op — Subscribe channels are scoped to their call and closed
// via context cancellation. Implement broker reconnect logic at the
// connection level if needed.
func (c *Consumer) Close() error { return nil }

func toAMQPTable(headers map[string]string) amqp091.Table {
	if len(headers) == 0 {
		return nil
	}
	t := make(amqp091.Table, len(headers))
	for k, v := range headers {
		t[k] = v
	}
	return t
}

func amqpToMessage(d amqp091.Delivery) pubsub.Message {
	headers := make(map[string]string, len(d.Headers))
	for k, v := range d.Headers {
		if s, ok := v.(string); ok {
			headers[k] = s
		}
	}
	return pubsub.Message{
		ID:      d.MessageId,
		Key:     d.RoutingKey,
		Payload: d.Body,
		Headers: headers,
	}
}
