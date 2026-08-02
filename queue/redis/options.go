package redis

import (
	"fmt"
	"os"
	"time"
)

type publisherConfig struct {
	maxLen int64
}

func defaultPublisherConfig() *publisherConfig {
	// An unbounded stream is a memory leak with a slow fuse: it only shows up
	// once redis starts evicting unrelated keys. 100k entries is small enough
	// to be safe by default and large enough that a healthy consumer never
	// notices the cap.
	return &publisherConfig{maxLen: 100_000}
}

// PublisherOption customises a Publisher.
type PublisherOption func(*publisherConfig)

// WithMaxLen caps the stream at approximately n entries, trimming the oldest
// beyond that. Pass 0 to disable trimming — only do that if something else
// bounds the stream, since the overflow failure mode is redis-wide.
func WithMaxLen(n int64) PublisherOption {
	return func(c *publisherConfig) { c.maxLen = n }
}

type consumerConfig struct {
	consumerName string
	batchSize    int64
	blockTimeout time.Duration
	minIdleTime  time.Duration
	maxAttempts  int
}

func defaultConsumerConfig() *consumerConfig {
	return &consumerConfig{
		consumerName: defaultConsumerName(),
		batchSize:    10,
		// Long enough that an idle queue is not a busy-poll, short enough that
		// shutdown and the reclaim pass are not noticeably delayed.
		blockTimeout: 5 * time.Second,
		// A message is only considered abandoned after this long, so it must
		// exceed the slowest legitimate handler — otherwise a slow-but-working
		// consumer has its own in-flight message stolen and processed twice.
		minIdleTime: 60 * time.Second,
		maxAttempts: 5,
	}
}

// defaultConsumerName identifies this process within the group. Redis uses it
// to track which consumer holds which pending message, so two replicas sharing
// a name would each believe the other's in-flight work was their own.
//
// Hostname is the pod name under Kubernetes, which is exactly the right
// granularity; the PID suffix keeps two processes on one host distinct.
func defaultConsumerName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}

// ConsumerOption customises a Consumer.
type ConsumerOption func(*consumerConfig)

// WithConsumerName overrides the per-process consumer identity. It must be
// unique across the replicas of a group; the default already is.
func WithConsumerName(name string) ConsumerOption {
	return func(c *consumerConfig) { c.consumerName = name }
}

// WithBatchSize sets how many entries a single read may return.
func WithBatchSize(n int64) ConsumerOption {
	return func(c *consumerConfig) { c.batchSize = n }
}

// WithBlockTimeout sets how long a read waits on an empty stream before
// looping. Shorter means faster shutdown and more frequent reclaim passes at
// the cost of more idle round trips.
func WithBlockTimeout(d time.Duration) ConsumerOption {
	return func(c *consumerConfig) { c.blockTimeout = d }
}

// WithMinIdleTime sets how long a message must sit pending before another
// consumer may reclaim it. Keep it comfortably above the slowest handler:
// setting it too low turns a slow consumer into duplicated work.
func WithMinIdleTime(d time.Duration) ConsumerOption {
	return func(c *consumerConfig) { c.minIdleTime = d }
}

// WithMaxAttempts caps redeliveries before a message is acknowledged and
// dropped with an error log. Pass 0 to retry forever — appropriate only when
// every failure is genuinely transient, since a poison payload otherwise
// occupies a consumer indefinitely.
func WithMaxAttempts(n int) ConsumerOption {
	return func(c *consumerConfig) { c.maxAttempts = n }
}
