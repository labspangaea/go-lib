// Package kafka provides Kafka-backed implementations of pubsub.Publisher and
// pubsub.Consumer using the segmentio/kafka-go driver.
package kafka

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/labspangaea/go-lib/logger"
	"github.com/labspangaea/go-lib/pubsub"
)

// Publisher sends messages to a Kafka cluster.
// Safe for concurrent use by multiple goroutines.
type Publisher struct {
	writer *kafkago.Writer
}

// NewPublisher creates a Publisher that writes to the given brokers.
//
// The writer is configured with:
//   - LeastBytes balancer (routes to partition with least buffered data)
//   - RequireAll acks (waits for leader + all ISR replicas)
//   - Synchronous writes (Publish blocks until broker confirms)
func NewPublisher(brokers []string, opts ...PublisherOption) *Publisher {
	cfg := defaultPublisherConfig()
	for _, o := range opts {
		o(cfg)
	}
	return &Publisher{
		writer: &kafkago.Writer{
			Addr:         kafkago.TCP(brokers...),
			Balancer:     &kafkago.LeastBytes{},
			RequiredAcks: kafkago.RequireAll,
			MaxAttempts:  cfg.maxAttempts,
			Async:        false,
		},
	}
}

// Publish sends msg to topic. Blocks until the broker confirms receipt.
func (p *Publisher) Publish(ctx context.Context, topic string, msg pubsub.Message) error {
	km := kafkago.Message{
		Topic: topic,
		Value: msg.Payload,
	}
	if msg.Key != "" {
		km.Key = []byte(msg.Key)
	}
	for k, v := range msg.Headers {
		km.Headers = append(km.Headers, kafkago.Header{Key: k, Value: []byte(v)})
	}
	if err := p.writer.WriteMessages(ctx, km); err != nil {
		return fmt.Errorf("kafka: publish to %s: %w", topic, err)
	}
	return nil
}

// Close flushes pending messages and shuts down the writer.
func (p *Publisher) Close() error {
	return p.writer.Close()
}

// Consumer reads messages from a Kafka topic within a consumer group.
// Safe for concurrent use; at most one Subscribe may be active at a time.
type Consumer struct {
	brokers []string
	cfg     consumerConfig

	mu     sync.Mutex
	reader *kafkago.Reader
}

// NewConsumer creates a Consumer that connects to the given brokers.
// A Reader is created per Subscribe call, bound to the topic + group pair.
func NewConsumer(brokers []string, opts ...ConsumerOption) *Consumer {
	cfg := defaultConsumerConfig()
	for _, o := range opts {
		o(cfg)
	}
	return &Consumer{brokers: brokers, cfg: *cfg}
}

// Subscribe blocks, reading messages from topic for group, until ctx is
// cancelled or Close is called.
//
// Offset commit happens only when handler returns nil, implementing
// at-least-once delivery: a handler error leaves the offset uncommitted so
// the message is redelivered on the next poll.
func (c *Consumer) Subscribe(ctx context.Context, topic, group string, handler pubsub.Handler) error {
	r := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:     c.brokers,
		Topic:       topic,
		GroupID:     group,
		MinBytes:    1,
		MaxBytes:    c.cfg.maxBytes,
		StartOffset: c.cfg.startOffset,
	})

	c.mu.Lock()
	c.reader = r
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.reader = nil
		c.mu.Unlock()
		_ = r.Close()
	}()

	for {
		m, err := r.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, io.EOF) {
				return nil // clean shutdown
			}
			return fmt.Errorf("kafka: fetch %s/%s: %w", topic, group, err)
		}

		// context.WithoutCancel: shutdown cancels the fetch loop but the
		// handler sees a live context so its own DB/HTTP calls are not aborted.
		if err := handler(context.WithoutCancel(ctx), kafkaToMessage(m)); err != nil {
			logger.FromContext(ctx).Warn("kafka: handler error",
				slog.String("topic", topic),
				slog.String("group", group),
				slog.Int64("offset", m.Offset),
				slog.Any(logger.KeyError, err),
			)
			// offset not committed → message redelivered on restart
		} else {
			commitCtx, commitCancel := context.WithTimeout(context.Background(), c.cfg.commitTimeout)
			commitErr := r.CommitMessages(commitCtx, m)
			commitCancel()
			if commitErr != nil && ctx.Err() == nil {
				return fmt.Errorf("kafka: commit %s/%s offset %d: %w", topic, group, m.Offset, commitErr)
			}
		}
	}
}

// Close stops an active Subscribe loop by closing the underlying reader.
// FetchMessage returns immediately, causing Subscribe to exit cleanly.
func (c *Consumer) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reader != nil {
		return c.reader.Close()
	}
	return nil
}

func kafkaToMessage(m kafkago.Message) pubsub.Message {
	headers := make(map[string]string, len(m.Headers))
	for _, h := range m.Headers {
		headers[h.Key] = string(h.Value)
	}
	return pubsub.Message{
		ID:      fmt.Sprintf("%d/%d", m.Partition, m.Offset),
		Key:     string(m.Key),
		Payload: m.Value,
		Headers: headers,
	}
}
