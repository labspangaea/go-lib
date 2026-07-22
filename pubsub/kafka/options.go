package kafka

import (
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

type publisherConfig struct {
	maxAttempts int
}

func defaultPublisherConfig() *publisherConfig {
	return &publisherConfig{maxAttempts: 3}
}

// PublisherOption configures a Kafka Publisher at construction time.
type PublisherOption func(*publisherConfig)

// WithMaxAttempts sets how many times the writer retries a failed write.
func WithMaxAttempts(n int) PublisherOption {
	return func(c *publisherConfig) { c.maxAttempts = n }
}

type consumerConfig struct {
	startOffset   int64
	maxBytes      int
	commitTimeout time.Duration
}

func defaultConsumerConfig() *consumerConfig {
	return &consumerConfig{
		startOffset:   kafkago.FirstOffset,
		maxBytes:      10 << 20, // 10 MB
		commitTimeout: 5 * time.Second,
	}
}

// ConsumerOption configures a Kafka Consumer at construction time.
type ConsumerOption func(*consumerConfig)

// WithLastOffset starts consumption from the latest message (skip backlog).
// Default is FirstOffset (consume all unread messages from the group).
func WithLastOffset() ConsumerOption {
	return func(c *consumerConfig) { c.startOffset = kafkago.LastOffset }
}

// WithMaxBytes sets the maximum number of bytes to fetch per request.
func WithMaxBytes(n int) ConsumerOption {
	return func(c *consumerConfig) { c.maxBytes = n }
}

// WithCommitTimeout sets the maximum time allowed to commit a Kafka offset
// after the handler returns. This budget applies during both normal operation
// and graceful shutdown (when the main context may already be cancelled).
// Default: 5s.
func WithCommitTimeout(d time.Duration) ConsumerOption {
	return func(c *consumerConfig) { c.commitTimeout = d }
}
