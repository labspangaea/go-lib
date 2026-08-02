package rabbitmq

// ExchangeType constrains the AMQP exchange types accepted by options.
type ExchangeType string

const (
	ExchangeDirect  ExchangeType = "direct"
	ExchangeTopic   ExchangeType = "topic"
	ExchangeFanout  ExchangeType = "fanout"
	ExchangeHeaders ExchangeType = "headers"
)

type publisherConfig struct {
	exchangeType ExchangeType
}

func defaultPublisherConfig() *publisherConfig {
	return &publisherConfig{exchangeType: ExchangeDirect}
}

// PublisherOption configures a RabbitMQ Publisher at construction time.
type PublisherOption func(*publisherConfig)

// WithExchangeType sets the AMQP exchange type declared by the Publisher.
// Use ExchangeDirect (default), ExchangeTopic, ExchangeFanout, or ExchangeHeaders.
func WithExchangeType(typ ExchangeType) PublisherOption {
	return func(c *publisherConfig) { c.exchangeType = typ }
}

type consumerConfig struct {
	maxAttempts   int
	exchangeType  ExchangeType
	prefetchCount int
}

func defaultConsumerConfig() *consumerConfig {
	return &consumerConfig{
		exchangeType:  ExchangeDirect,
		prefetchCount: 1,
	}
}

// ConsumerOption configures a RabbitMQ Consumer at construction time.
type ConsumerOption func(*consumerConfig)

// WithConsumerExchangeType sets the AMQP exchange type the Consumer declares.
// Must match the exchange type used by the Publisher on the same exchange.
func WithConsumerExchangeType(typ ExchangeType) ConsumerOption {
	return func(c *consumerConfig) { c.exchangeType = typ }
}

// WithPrefetchCount sets how many unacked messages the broker sends at once.
// Lower values reduce throughput but improve fairness across consumers.
// Default is 1 (process one message at a time per consumer goroutine).
func WithPrefetchCount(n int) ConsumerOption {
	return func(c *consumerConfig) { c.prefetchCount = n }
}

// WithMaxAttempts caps how many times a redelivered message is retried before
// it is acknowledged and dropped with an error log. Pass 0 to requeue forever
// — appropriate only when every failure is genuinely transient, since AMQP
// requeues to the head and a poison message otherwise occupies a consumer
// indefinitely.
//
// AMQP reports only that a delivery was redelivered, not how many times, so
// this is a coarse cap rather than an exact count.
func WithMaxAttempts(n int) ConsumerOption {
	return func(c *consumerConfig) { c.maxAttempts = n }
}
