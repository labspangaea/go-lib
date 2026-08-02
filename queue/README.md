# queue

Work-queue abstraction over Kafka, RabbitMQ and Redis Streams. Concrete
backends live in sub-packages so callers depend on the interface, not the SDK.

---

## queue or pubsub?

Both packages support the same three brokers. They differ in **who receives a
message**:

| | `queue` | `pubsub` |
|---|---|---|
| Delivery | exactly one member of a group | every subscriber |
| Adding replicas | divides the work | multiplies the work |
| On handler error | redelivered | backend-dependent |
| Survives no consumer | yes | Kafka/RabbitMQ yes, Redis **no** |

Pick **queue** for work that must happen once: orders to fulfil, emails to
send, invoices to generate.

Pick **pubsub** for facts every listener wants: cache invalidations, live
dashboard updates, notifications.

> The distinction is not cosmetic. Running a `queue` workload on `pubsub` with
> two replicas processes everything twice; running a `pubsub` workload on
> `queue` means only one replica ever hears about it.

### Why redis differs between the two

`pubsub/redis` is `PUBLISH`/`SUBSCRIBE` — no backlog, no groups, no acks. It is
the right tool for fan-out and the wrong one for work.

`queue/redis` is **Streams** (`XADD` / `XREADGROUP` / `XACK`), which is what
gives redis a durable backlog, real consumer groups, and redelivery of failed
messages. Kafka and RabbitMQ already behave this way in both packages.

---

## Quick start

```go
import (
    "github.com/labspangaea/go-lib/queue"
    queuekafka "github.com/labspangaea/go-lib/queue/kafka"
)

pub := queuekafka.NewPublisher([]string{"localhost:9092"})
defer pub.Close()

err := pub.Publish(ctx, "orders", queue.Message{
    Key:     order.ID,             // groups related messages in order
    Payload: body,
    Headers: map[string]string{"trace_id": traceID},
})
```

```go
con := queuekafka.NewConsumer([]string{"localhost:9092"})
defer con.Close()

// Blocks until ctx is cancelled. Every replica running this with the same
// group name shares the work.
err := con.Consume(ctx, "orders", "order-workers", func(ctx context.Context, m queue.Message) error {
    return handle(ctx, m.Payload)
})
```

---

## The handler contract

```go
type Handler func(ctx context.Context, msg Message) error
```

**Return `nil`** and the message is acknowledged — removed from the queue, not
redelivered.

**Return an error** and it is redelivered: the Kafka offset is left
uncommitted, the AMQP delivery is nacked with requeue, the Redis Streams entry
stays pending for reclaim.

> Return an error only when a retry could plausibly succeed. For a payload that
> will never parse, log it and return `nil` — otherwise the queue hands it back
> forever. Each backend has a `WithMaxAttempts` escape hatch, but relying on it
> means every poison message burns its full retry budget first.

`msg.Attempt` starts at 1 and counts deliveries. Treat it as a **lower bound**:
only Redis Streams reports a true count. Kafka tracks offsets rather than
attempts, and AMQP reports only that a delivery *was* redelivered, not how
often — both report 1, or 2 once known to be a retry.

---

## Backend differences that affect design

| | Kafka | RabbitMQ | Redis Streams |
|---|---|---|---|
| Concurrency ceiling | partition count | unbounded | unbounded |
| Ordering | per partition (use `Key`) | per queue, single consumer only | none |
| A stuck message | blocks its partition | blocks nothing | blocks nothing |
| True attempt count | no | no | yes |
| Backlog cap | retention policy | unbounded | `WithMaxLen`, default 100k |

Three consequences worth knowing before choosing:

**Kafka concurrency is capped by partitions.** Ten replicas on a
three-partition topic leaves seven idle, and a permanently failing message
stalls everything behind it in its partition.

**RabbitMQ scales by adding consumers**, but ordering goes with it — with more
than one consumer, messages are processed concurrently.

**Redis Streams are capped by default** (`WithMaxLen`, ~100k entries). An
uncapped stream whose consumers fall behind grows until redis evicts unrelated
keys, so the failure surfaces somewhere else entirely.

---

## Redis Streams specifics

A failed or abandoned message stays **pending** and is reclaimed by another
consumer once it has been idle for `WithMinIdleTime` (default 60s). That single
mechanism covers both a handler that returned an error and a consumer that died
mid-message.

> Keep `MinIdleTime` comfortably above your slowest handler. Set it too low and
> a slow-but-working consumer has its in-flight message stolen and processed
> twice.

Each consumer needs a unique name within its group — redis uses it to track who
holds which pending message. The default is `<hostname>-<pid>`, which is the
pod name under Kubernetes and already unique; override with
`WithConsumerName` only if you have a better identifier.

---

## Testing

`queue/redis` has integration tests covering the properties that distinguish a
queue from pub/sub — backlog, competing consumers, redelivery, the poison-message
cap. They need a real broker:

```bash
docker run --rm -p 6379:6379 redis:7-alpine
REDIS_ADDR=localhost:6379 go test ./queue/redis/
```

Without `REDIS_ADDR` they skip rather than fail, so `go test ./...` stays green
on a machine with no redis. CI sets it, because a suite that silently skips its
most important tests looks exactly like one that passes them.
