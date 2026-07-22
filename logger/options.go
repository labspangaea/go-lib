package logger

import (
	"log/slog"

	otellog "go.opentelemetry.io/otel/log"
)

type config struct {
	level          slog.Leveler
	development    bool
	addSource      bool
	serviceName    string
	baseAttrs      []slog.Attr
	extraHandler   slog.Handler
	bridgeProvider otellog.LoggerProvider
	bridgeMinLevel slog.Level
}

func defaultConfig() *config {
	return &config{level: slog.LevelInfo, bridgeMinLevel: slog.LevelError}
}

// Option configures a Logger at construction time.
type Option func(*config)

// WithLevel sets the minimum enabled log level.
func WithLevel(level slog.Leveler) Option {
	return func(c *config) { c.level = level }
}

// WithDevelopment enables development mode: text encoding to stderr, source location added.
func WithDevelopment() Option {
	return func(c *config) {
		c.development = true
		c.addSource = true
	}
}

// WithSource adds the source file and line number to every log record.
func WithSource() Option {
	return func(c *config) { c.addSource = true }
}

// WithHandler fans every log record out to h in addition to the primary handler.
// Use this to attach an OTel bridge, a Datadog sink, or any slog.Handler alongside
// the default stdout/stderr output.
//
// Example with OTel:
//
//	bridge := otelslog.NewHandler("my-service", otelslog.WithLoggerProvider(lp))
//	log := logger.New(logger.WithHandler(bridge))
func WithHandler(h slog.Handler) Option {
	return func(c *config) { c.extraHandler = h }
}

// WithServiceInfo attaches service.name, service.version, and service.instance.id
// to every log entry produced by the logger.
func WithServiceInfo(name, version, instanceID string) Option {
	return func(c *config) {
		c.serviceName = name
		c.baseAttrs = append(c.baseAttrs,
			slog.String(KeyServiceName, name),
			slog.String(KeyServiceVersion, version),
			slog.String(KeyServiceInstanceID, instanceID),
		)
	}
}

// WithOTelBridge fans log records at minLevel and above out to OTLP via the
// supplied LoggerProvider, in addition to the primary stdout/stderr handler.
// Records below minLevel are written to stdout only — useful for shipping just
// errors/warnings to a SaaS backend without blowing up log volume.
//
// Pass the LoggerProvider returned by telemetry.Setup. If lp is nil, this
// option is a no-op.
//
// Mutually exclusive with WithHandler — whichever is applied last wins.
func WithOTelBridge(lp otellog.LoggerProvider, minLevel slog.Level) Option {
	return func(c *config) {
		c.bridgeProvider = lp
		c.bridgeMinLevel = minLevel
	}
}
