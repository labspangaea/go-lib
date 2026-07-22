package telemetry

import (
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type config struct {
	endpoint          string
	protocol          string
	insecure          bool
	gzip              bool
	headers           map[string]string
	serviceName       string
	serviceVersion    string
	serviceInstanceID string
	sampler           sdktrace.Sampler
}

const (
	defaultEndpoint = "localhost:4317"
	compressorGzip  = "gzip"

	// ProtocolGRPC selects OTLP/gRPC (default port 4317).
	ProtocolGRPC = "grpc"
	// ProtocolHTTPProtobuf selects OTLP/HTTP with protobuf payloads (default port 4318).
	ProtocolHTTPProtobuf = "http/protobuf"
)

func defaultConfig() *config {
	return &config{
		endpoint: defaultEndpoint,
		protocol: ProtocolGRPC,
		sampler:  sdktrace.AlwaysSample(),
	}
}

// Option configures the OTel SDK at setup time.
type Option func(*config)

// WithEndpoint sets the OTLP endpoint of the collector or APM agent.
// Format: "host:port" (default: "localhost:4317").
func WithEndpoint(addr string) Option {
	return func(c *config) { c.endpoint = addr }
}

// WithProtocol selects the OTLP wire protocol. Accepted values:
//   - "grpc"          — OTLP/gRPC (default; port 4317)
//   - "http/protobuf" — OTLP/HTTP with protobuf payloads (port 4318)
//
// Unknown values fall back to gRPC.
func WithProtocol(p string) Option {
	return func(c *config) {
		if p == ProtocolGRPC || p == ProtocolHTTPProtobuf {
			c.protocol = p
		}
	}
}

// WithHeaders attaches headers to every OTLP request. Use for SaaS backends
// that authenticate via header (e.g. New Relic api-key).
func WithHeaders(h map[string]string) Option {
	return func(c *config) { c.headers = h }
}

// WithServiceInfo sets the service.name, service.version, and service.instance.id
// resource attributes attached to every span and log record.
func WithServiceInfo(name, version, instanceID string) Option {
	return func(c *config) {
		c.serviceName = name
		c.serviceVersion = version
		c.serviceInstanceID = instanceID
	}
}

// WithInsecure disables TLS on the OTLP connection.
// Use for a local OpenTelemetry Collector or non-production environments.
func WithInsecure() Option {
	return func(c *config) { c.insecure = true }
}

// WithGzipCompression enables gzip compression on the OTLP exporter.
func WithGzipCompression() Option {
	return func(c *config) { c.gzip = true }
}

// WithSampler overrides the default trace sampler (AlwaysSample).
// For production, consider sdktrace.ParentBased(sdktrace.TraceIDRatioBased(0.1))
// to sample 10% of traces and reduce collector load.
func WithSampler(s sdktrace.Sampler) Option {
	return func(c *config) { c.sampler = s }
}

func (c *config) traceGRPCOpts() []otlptracegrpc.Option {
	opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(c.endpoint)}
	if c.insecure {
		opts = append(opts, otlptracegrpc.WithInsecure()) //nolint:staticcheck // WithInsecure is the clearest option for dev use
	}
	if c.gzip {
		opts = append(opts, otlptracegrpc.WithCompressor(compressorGzip))
	}
	if len(c.headers) > 0 {
		opts = append(opts, otlptracegrpc.WithHeaders(c.headers))
	}
	return opts
}

func (c *config) traceHTTPOpts() []otlptracehttp.Option {
	opts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(c.endpoint)}
	if c.insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	if c.gzip {
		opts = append(opts, otlptracehttp.WithCompression(otlptracehttp.GzipCompression))
	}
	if len(c.headers) > 0 {
		opts = append(opts, otlptracehttp.WithHeaders(c.headers))
	}
	return opts
}

func (c *config) logGRPCOpts() []otlploggrpc.Option {
	opts := []otlploggrpc.Option{otlploggrpc.WithEndpoint(c.endpoint)}
	if c.insecure {
		opts = append(opts, otlploggrpc.WithInsecure()) //nolint:staticcheck
	}
	if c.gzip {
		opts = append(opts, otlploggrpc.WithCompressor(compressorGzip))
	}
	if len(c.headers) > 0 {
		opts = append(opts, otlploggrpc.WithHeaders(c.headers))
	}
	return opts
}

func (c *config) logHTTPOpts() []otlploghttp.Option {
	opts := []otlploghttp.Option{otlploghttp.WithEndpoint(c.endpoint)}
	if c.insecure {
		opts = append(opts, otlploghttp.WithInsecure())
	}
	if c.gzip {
		opts = append(opts, otlploghttp.WithCompression(otlploghttp.GzipCompression))
	}
	if len(c.headers) > 0 {
		opts = append(opts, otlploghttp.WithHeaders(c.headers))
	}
	return opts
}
