// Package redis implements queue.Publisher and queue.Consumer on Redis
// Streams.
//
// Streams rather than Pub/Sub, because a work queue needs the three things
// Pub/Sub does not have: a backlog that survives having no consumer attached,
// consumer groups so replicas divide work instead of duplicating it, and
// acknowledgement so a failed message comes back rather than vanishing.
//
// pubsub/redis remains the right choice for fan-out. This package is for work
// that must happen once per group.
package redis

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	goredis "github.com/redis/go-redis/v9"

	"github.com/labspangaea/go-lib/logger"
	"github.com/labspangaea/go-lib/queue"
)

// Field names inside a stream entry. Streams entries are flat field/value
// maps, so the message is spread across fixed keys rather than stored as one
// JSON blob — entries stay readable in redis-cli and XADD stays a single round
// trip.
const (
	fieldKey     = "key"
	fieldPayload = "payload"
	headerPrefix = "h:" // headers are stored as h:<name>
)

// Publisher appends messages to a Redis stream.
// Safe for concurrent use by multiple goroutines.
type Publisher struct {
	client *goredis.Client
	cfg    *publisherConfig
}

// NewPublisher creates a Publisher writing through the given Redis client.
// The caller owns the client lifecycle — call client.Close() after pub.Close().
func NewPublisher(client *goredis.Client, opts ...PublisherOption) *Publisher {
	cfg := defaultPublisherConfig()
	for _, o := range opts {
		o(cfg)
	}
	return &Publisher{client: client, cfg: cfg}
}

// Publish appends msg to the stream named q.
//
// The stream is capped approximately at the configured max length. Without a
// cap, a queue whose consumers fall behind grows until redis runs out of
// memory — and that failure arrives as the eviction of unrelated keys rather
// than as backpressure here.
//
// msg.ID is not forwarded: a stream ID must be <millis>-<seq>, which an
// arbitrary caller string is not. Redis assigns the ID, and that assigned
// value is both what the consumer sees and what XACK needs.
func (p *Publisher) Publish(ctx context.Context, q string, msg queue.Message) error {
	values := make(map[string]any, len(msg.Headers)+2)
	values[fieldKey] = msg.Key
	values[fieldPayload] = msg.Payload
	for k, v := range msg.Headers {
		values[headerPrefix+k] = v
	}

	args := &goredis.XAddArgs{Stream: q, Values: values, Approx: true}
	if p.cfg.maxLen > 0 {
		args.MaxLen = p.cfg.maxLen
	}

	if err := p.client.XAdd(ctx, args).Err(); err != nil {
		return fmt.Errorf("redis queue: xadd to %s: %w", q, err)
	}
	return nil
}

// Close is a no-op — the Redis client is owned by the caller.
func (p *Publisher) Close() error { return nil }

// Consumer reads messages from a Redis stream as part of a consumer group.
// Safe for concurrent use; each Consume call creates an independent reader.
type Consumer struct {
	client *goredis.Client
	cfg    *consumerConfig
}

// NewConsumer creates a Consumer reading through the given Redis client.
func NewConsumer(client *goredis.Client, opts ...ConsumerOption) *Consumer {
	cfg := defaultConsumerConfig()
	for _, o := range opts {
		o(cfg)
	}
	return &Consumer{client: client, cfg: cfg}
}

// Consume reads from the stream named q as a member of group, dispatching each
// message to handler until ctx is cancelled.
//
// A message whose handler fails is left pending and reclaimed by a later pass
// once it has been idle long enough — the same mechanism that recovers work
// from a consumer which died mid-message.
func (c *Consumer) Consume(ctx context.Context, q, group string, handler queue.Handler) error {
	// MKSTREAM so a consumer can start before anything has been published.
	// Without it, group creation fails on a stream that does not exist yet and
	// the service dies at startup for want of a message that has not been sent.
	err := c.client.XGroupCreateMkStream(ctx, q, group, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return fmt.Errorf("redis queue: create group %s on %s: %w", group, q, err)
	}

	for {
		if ctx.Err() != nil {
			return nil // clean shutdown
		}

		// Reclaim before reading new work, so a message stranded by a crashed
		// consumer is retried rather than starved behind a busy stream.
		if err := c.reclaim(ctx, q, group, handler); err != nil {
			return err
		}

		res, err := c.client.XReadGroup(ctx, &goredis.XReadGroupArgs{
			Group:    group,
			Consumer: c.cfg.consumerName,
			Streams:  []string{q, ">"},
			Count:    c.cfg.batchSize,
			Block:    c.cfg.blockTimeout,
		}).Result()
		if err != nil {
			if errors.Is(err, goredis.Nil) {
				continue // block timeout with nothing waiting — normal
			}
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("redis queue: xreadgroup %s/%s: %w", q, group, err)
		}

		for _, stream := range res {
			for _, entry := range stream.Messages {
				// Straight off the stream, so this is the first delivery.
				c.dispatch(ctx, q, group, entry, 1, handler)
			}
		}
	}
}

// reclaim takes over messages pending longer than the configured idle time.
// This is how work survives a consumer that died between reading and acking.
func (c *Consumer) reclaim(ctx context.Context, q, group string, handler queue.Handler) error {
	entries, _, err := c.client.XAutoClaim(ctx, &goredis.XAutoClaimArgs{
		Stream:   q,
		Group:    group,
		Consumer: c.cfg.consumerName,
		MinIdle:  c.cfg.minIdleTime,
		Start:    "0",
		Count:    c.cfg.batchSize,
	}).Result()
	if err != nil {
		if errors.Is(err, goredis.Nil) || ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("redis queue: xautoclaim %s/%s: %w", q, group, err)
	}

	for _, entry := range entries {
		// Only reclaimed messages can be retries, so the XPENDING lookup for a
		// true attempt count is confined to this path — on the hot path every
		// message would cost an extra round trip to learn it is attempt 1.
		c.dispatch(ctx, q, group, entry, c.deliveryCount(ctx, q, group, entry.ID), handler)
	}
	return nil
}

// dispatch hands one entry to the handler and acknowledges it on success.
//
// A failed handler deliberately leaves the entry pending rather than acking
// it: pending is what makes it eligible for reclaim, and acking a failure
// would silently drop the work.
func (c *Consumer) dispatch(ctx context.Context, q, group string, entry goredis.XMessage, attempt int, handler queue.Handler) {
	msg := toMessage(entry)
	msg.Attempt = attempt

	// A message past the attempt limit will not start succeeding on the next
	// identical try, and left pending it is reclaimed forever. Acknowledge it
	// so the queue moves on — but loudly, because dropping it silently is how
	// a poison payload becomes an unexplained gap in the data weeks later.
	if c.cfg.maxAttempts > 0 && attempt > c.cfg.maxAttempts {
		logger.FromContext(ctx).Error("redis queue: dropping message after max attempts",
			slog.String("queue", q),
			slog.String("group", group),
			slog.String("id", msg.ID),
			slog.Int("attempts", attempt),
		)
		c.ack(ctx, q, group, entry.ID)
		return
	}

	// context.WithoutCancel: shutdown stops the read loop, but the handler sees
	// a live context so its own DB/HTTP calls are not aborted mid-flight.
	if err := handler(context.WithoutCancel(ctx), msg); err != nil {
		logger.FromContext(ctx).Warn("redis queue: handler error",
			slog.String("queue", q),
			slog.String("group", group),
			slog.String("id", msg.ID),
			slog.Int("attempt", attempt),
			slog.Any(logger.KeyError, err),
		)
		return // stays pending → reclaimed after the idle window
	}
	c.ack(ctx, q, group, entry.ID)
}

// ack removes a message from the pending list.
//
// A failed ack is logged rather than propagated: the handler already succeeded,
// so the work is done and the only consequence is a later redelivery. Failing
// the whole Consume loop over it would turn a duplicate into an outage.
func (c *Consumer) ack(ctx context.Context, q, group, id string) {
	if err := c.client.XAck(ctx, q, group, id).Err(); err != nil && ctx.Err() == nil {
		logger.FromContext(ctx).Warn("redis queue: ack failed",
			slog.String("queue", q),
			slog.String("group", group),
			slog.String("id", id),
			slog.Any(logger.KeyError, err),
		)
	}
}

// deliveryCount reports how many times redis has handed out this entry.
// Best-effort: if the pending record cannot be read, fall back to 2, since
// only an already-reclaimed message reaches this path.
func (c *Consumer) deliveryCount(ctx context.Context, q, group, id string) int {
	res, err := c.client.XPendingExt(ctx, &goredis.XPendingExtArgs{
		Stream: q,
		Group:  group,
		Start:  id,
		End:    id,
		Count:  1,
	}).Result()
	if err != nil || len(res) == 0 {
		return 2
	}
	return int(res[0].RetryCount)
}

// Close is a no-op — the Redis client is owned by the caller.
func (c *Consumer) Close() error { return nil }

// toMessage converts a stream entry back into a queue.Message.
func toMessage(entry goredis.XMessage) queue.Message {
	msg := queue.Message{ID: entry.ID, Attempt: 1}
	for k, v := range entry.Values {
		s, _ := v.(string)
		switch {
		case k == fieldKey:
			msg.Key = s
		case k == fieldPayload:
			msg.Payload = []byte(s)
		case strings.HasPrefix(k, headerPrefix):
			if msg.Headers == nil {
				msg.Headers = make(map[string]string)
			}
			msg.Headers[strings.TrimPrefix(k, headerPrefix)] = s
		}
	}
	return msg
}
