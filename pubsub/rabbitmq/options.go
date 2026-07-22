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
