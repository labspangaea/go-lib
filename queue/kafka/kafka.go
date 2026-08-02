// Package kafka provides Kafka-backed implementations of queue.Publisher and
// queue.Consumer using the segmentio/kafka-go driver.
//
// Kafka consumer groups are already work-queue semantics — each partition is
// assigned to exactly one member, so replicas divide the topic rather than
// duplicating it. This package is thin for that reason: it adapts the same
// mechanics pubsub/kafka uses to the queue interface, where the naming matches
// the behaviour.
//
// One consequence of partition assignment is worth knowing before scaling:
// concurrency is capped by partition count. Ten replicas on a three-partition
// topic leaves seven idle.
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
	"github.com/labspangaea/go-lib/queue"
)

// Publisher sends messages to a Kafka cluster.
// Safe for concurrent use by multiple goroutines.
type Publisher struct {
	writer *kafkago.Writer
}

// NewPublisher creates a Publisher that writes to the given brokers.
//
// Configured for durability over throughput: RequireAll waits for the leader
// and every in-sync replica, and writes are synchronous so Publish returning
// nil means the cluster has the message. A queue that loses accepted work on a
// broker restart is not a queue.
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

// Publish writes msg to the topic named q, blocking until the cluster confirms.
func (p *Publisher) Publish(ctx context.Context, q string, msg queue.Message) error {
	km := kafkago.Message{
		Topic: q,
		Key:   []byte(msg.Key),
		Value: msg.Payload,
	}
	for k, v := range msg.Headers {
		km.Headers = append(km.Headers, kafkago.Header{Key: k, Value: []byte(v)})
	}
	if err := p.writer.WriteMessages(ctx, km); err != nil {
		return fmt.Errorf("kafka queue: publish to %s: %w", q, err)
	}
	return nil
}

// Close flushes buffered writes and releases the writer.
func (p *Publisher) Close() error {
	if err := p.writer.Close(); err != nil {
		return fmt.Errorf("kafka queue: close publisher: %w", err)
	}
	return nil
}

// Consumer reads messages from a Kafka topic as part of a consumer group.
type Consumer struct {
	brokers []string
	cfg     *consumerConfig

	mu     sync.Mutex
	reader *kafkago.Reader
}

// NewConsumer creates a Consumer reading from the given brokers.
func NewConsumer(brokers []string, opts ...ConsumerOption) *Consumer {
	cfg := defaultConsumerConfig()
	for _, o := range opts {
		o(cfg)
	}
	return &Consumer{brokers: brokers, cfg: cfg}
}

// Consume reads from topic q as a member of group until ctx is cancelled.
//
// Offsets are committed only after the handler returns nil, which is what
// makes delivery at-least-once: a failed message is redelivered on restart
// because its offset was never advanced past it.
func (c *Consumer) Consume(ctx context.Context, q, group string, handler queue.Handler) error {
	r := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:     c.brokers,
		Topic:       q,
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
			return fmt.Errorf("kafka queue: fetch %s/%s: %w", q, group, err)
		}

		// context.WithoutCancel: shutdown cancels the fetch loop but the
		// handler sees a live context so its own DB/HTTP calls are not aborted.
		if err := handler(context.WithoutCancel(ctx), toMessage(m)); err != nil {
			logger.FromContext(ctx).Warn("kafka queue: handler error",
				slog.String("queue", q),
				slog.String("group", group),
				slog.Int64("offset", m.Offset),
				slog.Any(logger.KeyError, err),
			)
			// Offset deliberately not committed → redelivered on restart.
			//
			// Note this blocks the partition: kafka has no per-message retry,
			// so a permanently failing message stalls everything behind it.
			// Return nil from the handler once a payload is beyond saving.
			continue
		}

		commitCtx, cancel := context.WithTimeout(context.Background(), c.cfg.commitTimeout)
		commitErr := r.CommitMessages(commitCtx, m)
		cancel()
		if commitErr != nil && ctx.Err() == nil {
			return fmt.Errorf("kafka queue: commit %s/%s offset %d: %w", q, group, m.Offset, commitErr)
		}
	}
}

// Close stops an active Consume loop and releases the reader.
func (c *Consumer) Close() error {
	c.mu.Lock()
	r := c.reader
	c.mu.Unlock()
	if r == nil {
		return nil
	}
	if err := r.Close(); err != nil {
		return fmt.Errorf("kafka queue: close consumer: %w", err)
	}
	return nil
}

// toMessage converts a kafka record into a queue.Message.
//
// Attempt is always 1: kafka tracks offsets, not delivery counts, so a
// redelivered message is indistinguishable from a first delivery. Callers
// needing a real count must carry it in a header themselves.
func toMessage(m kafkago.Message) queue.Message {
	headers := make(map[string]string, len(m.Headers))
	for _, h := range m.Headers {
		headers[h.Key] = string(h.Value)
	}
	return queue.Message{
		ID:      fmt.Sprintf("%d/%d", m.Partition, m.Offset),
		Key:     string(m.Key),
		Payload: m.Value,
		Headers: headers,
		Attempt: 1,
	}
}
