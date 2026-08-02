package redis_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/labspangaea/go-lib/queue"
	queueredis "github.com/labspangaea/go-lib/queue/redis"
)

// These exercise the properties that distinguish a queue from pubsub —
// backlog, competing consumers, redelivery — none of which can be observed
// without a real broker. Set REDIS_ADDR to run them:
//
//	docker run --rm -p 6379:6379 redis:7-alpine
//	REDIS_ADDR=localhost:6379 go test ./queue/redis/
//
// Skipped rather than failed when unset, so `go test ./...` stays green on a
// machine with no redis. The trade is that a green run does not by itself mean
// these ran — CI sets REDIS_ADDR so it is not left to chance.
func testClient(t *testing.T) *goredis.Client {
	t.Helper()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("REDIS_ADDR not set — skipping Redis Streams integration test")
	}
	c := goredis.NewClient(&goredis.Options{
		Addr:     addr,
		Password: os.Getenv("REDIS_PASSWORD"),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Ping(ctx).Err(); err != nil {
		t.Skipf("redis at %s unreachable: %v", addr, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// uniqueName keeps parallel tests and repeat runs from sharing a stream.
func uniqueName(t *testing.T, prefix string) string {
	t.Helper()
	return fmt.Sprintf("%s-%d-%s", prefix, time.Now().UnixNano(), t.Name())
}

func TestPublishThenConsume(t *testing.T) {
	c := testClient(t)
	stream := uniqueName(t, "q")
	t.Cleanup(func() { c.Del(context.Background(), stream) })

	pub := queueredis.NewPublisher(c)
	con := queueredis.NewConsumer(c, queueredis.WithBlockTimeout(200*time.Millisecond))

	// Publish before any consumer exists. This is the property Pub/Sub cannot
	// offer and the reason this backend is built on Streams: with pubsub/redis
	// this message would simply be gone.
	err := pub.Publish(context.Background(), stream, queue.Message{
		Key:     "k1",
		Payload: []byte(`{"order":"1"}`),
		Headers: map[string]string{"trace": "abc"},
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	got := make(chan queue.Message, 1)
	go func() {
		_ = con.Consume(ctx, stream, "g1", func(_ context.Context, m queue.Message) error {
			got <- m
			return nil
		})
	}()

	select {
	case m := <-got:
		if string(m.Payload) != `{"order":"1"}` {
			t.Errorf("payload = %q", m.Payload)
		}
		if m.Key != "k1" {
			t.Errorf("key = %q, want k1", m.Key)
		}
		if m.Headers["trace"] != "abc" {
			t.Errorf("headers = %v, want trace=abc", m.Headers)
		}
		if m.ID == "" {
			t.Error("ID is empty; redis should have assigned a stream id")
		}
		if m.Attempt != 1 {
			t.Errorf("Attempt = %d, want 1 on first delivery", m.Attempt)
		}
	case <-ctx.Done():
		t.Fatal("message published before the consumer started was never delivered")
	}
}

// The defining difference from pubsub: members of one group split the work
// rather than each receiving everything.
func TestGroupMembersCompeteForMessages(t *testing.T) {
	c := testClient(t)
	stream := uniqueName(t, "q")
	t.Cleanup(func() { c.Del(context.Background(), stream) })

	const total = 20
	pub := queueredis.NewPublisher(c)
	for i := range total {
		if err := pub.Publish(context.Background(), stream, queue.Message{
			Payload: []byte(fmt.Sprintf(`{"n":%d}`, i)),
		}); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var handled atomic.Int64
	var mu sync.Mutex
	perConsumer := map[string]int{}
	done := make(chan struct{})
	var once sync.Once

	for _, name := range []string{"c1", "c2"} {
		con := queueredis.NewConsumer(c,
			queueredis.WithConsumerName(name),
			queueredis.WithBlockTimeout(200*time.Millisecond),
		)
		go func() {
			_ = con.Consume(ctx, stream, "shared", func(_ context.Context, _ queue.Message) error {
				mu.Lock()
				perConsumer[name]++
				mu.Unlock()
				if handled.Add(1) == total {
					once.Do(func() { close(done) })
				}
				return nil
			})
		}()
	}

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatalf("only %d/%d messages handled", handled.Load(), total)
	}

	mu.Lock()
	defer mu.Unlock()
	sum := 0
	for _, n := range perConsumer {
		sum += n
	}
	// Exactly total, not 2*total: every message went to one consumer, not both.
	// Under fan-out this would be 40.
	if sum != total {
		t.Errorf("consumers handled %d messages in total, want %d — work was duplicated, not shared", sum, total)
	}
}

// A handler that fails must not lose the message: it stays pending and is
// reclaimed once idle. This is what "at-least-once" buys over pubsub/redis,
// where a handler error logs and the message is gone.
func TestFailedHandlerMessageIsRedelivered(t *testing.T) {
	c := testClient(t)
	stream := uniqueName(t, "q")
	t.Cleanup(func() { c.Del(context.Background(), stream) })

	pub := queueredis.NewPublisher(c)
	if err := pub.Publish(context.Background(), stream, queue.Message{Payload: []byte("work")}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	// A short idle window so the reclaim pass happens within the test rather
	// than the 60s production default.
	con := queueredis.NewConsumer(c,
		queueredis.WithBlockTimeout(200*time.Millisecond),
		queueredis.WithMinIdleTime(1*time.Second),
	)

	var attempts atomic.Int64
	succeeded := make(chan int, 1)

	go func() {
		_ = con.Consume(ctx, stream, "retry", func(_ context.Context, m queue.Message) error {
			n := attempts.Add(1)
			if n == 1 {
				return errors.New("simulated handler failure")
			}
			succeeded <- m.Attempt
			return nil
		})
	}()

	select {
	case attempt := <-succeeded:
		if attempts.Load() < 2 {
			t.Errorf("handler ran %d times; the failed message was not retried", attempts.Load())
		}
		if attempt < 2 {
			t.Errorf("Attempt = %d on redelivery, want >= 2", attempt)
		}
	case <-ctx.Done():
		t.Fatalf("failed message was never redelivered (handler ran %d times)", attempts.Load())
	}
}

// Past the cap a message is acked and dropped, so a poison payload cannot
// occupy a consumer forever.
func TestMaxAttemptsDropsPoisonMessage(t *testing.T) {
	c := testClient(t)
	stream := uniqueName(t, "q")
	t.Cleanup(func() { c.Del(context.Background(), stream) })

	pub := queueredis.NewPublisher(c)
	if err := pub.Publish(context.Background(), stream, queue.Message{Payload: []byte("poison")}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	con := queueredis.NewConsumer(c,
		queueredis.WithBlockTimeout(200*time.Millisecond),
		queueredis.WithMinIdleTime(500*time.Millisecond),
		queueredis.WithMaxAttempts(2),
	)

	var attempts atomic.Int64
	go func() {
		_ = con.Consume(ctx, stream, "poison", func(_ context.Context, _ queue.Message) error {
			attempts.Add(1)
			return errors.New("always fails")
		})
	}()

	// Give the reclaim loop enough passes to exceed the cap and give up.
	deadline := time.After(15 * time.Second)
	for {
		pending, err := c.XPending(context.Background(), stream, "poison").Result()
		if err == nil && pending.Count == 0 && attempts.Load() > 0 {
			return // acked and dropped, as intended
		}
		select {
		case <-deadline:
			t.Fatalf("message still pending after %d attempts; the cap never applied", attempts.Load())
		case <-time.After(500 * time.Millisecond):
		}
	}
}
