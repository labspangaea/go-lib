// Package logger provides structured, context-aware logging built on log/slog.
// Field keys follow OpenTelemetry Semantic Conventions v1.26
// (https://opentelemetry.io/docs/specs/semconv/).
package logger

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"go.opentelemetry.io/contrib/bridges/otelslog"
)

// nopLogger is the shared discard logger returned by Nop() and FromContext
// misses. It is a singleton — callers may safely compare with == to check
// whether a logger is the discard instance (or use IsNop for clarity).
var nopLogger = slog.New(slog.DiscardHandler)

// Nop returns a logger that discards all output. Useful as a safe default and in tests.
// The returned value is a singleton; pointer comparison with == is valid.
func Nop() *slog.Logger { return nopLogger }

// IsNop reports whether l is the discard logger returned by Nop().
func IsNop(l *slog.Logger) bool { return l == nopLogger }

// New returns a production-ready *slog.Logger configured by the supplied options.
// Base fields (e.g. service info) are applied once and inherited by all derived
// loggers created via With.
func New(opts ...Option) *slog.Logger {
	cfg := defaultConfig()
	for _, o := range opts {
		o(cfg)
	}

	handlerOpts := &slog.HandlerOptions{
		Level:     cfg.level,
		AddSource: cfg.addSource,
	}

	var handler slog.Handler
	if cfg.development {
		handler = slog.NewTextHandler(os.Stderr, handlerOpts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, handlerOpts)
	}

	extra := cfg.extraHandler
	if cfg.bridgeProvider != nil {
		bridge := otelslog.NewHandler(cfg.serviceName, otelslog.WithLoggerProvider(cfg.bridgeProvider))
		extra = &levelFilterHandler{base: bridge, minLevel: cfg.bridgeMinLevel}
	}

	var h slog.Handler = handler
	if extra != nil {
		h = &multiHandler{primary: handler, extra: extra}
	}

	l := slog.New(h)
	if len(cfg.baseAttrs) > 0 {
		l = slog.New(l.Handler().WithAttrs(cfg.baseAttrs))
	}
	return l
}

// multiHandler fans a log record out to two handlers: primary (stdout/stderr) and extra.
type multiHandler struct {
	primary slog.Handler
	extra   slog.Handler
}

func (m *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return m.primary.Enabled(ctx, level) || m.extra.Enabled(ctx, level)
}

func (m *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	if m.primary.Enabled(ctx, r.Level) {
		if err := m.primary.Handle(ctx, r.Clone()); err != nil {
			errs = append(errs, err)
		}
	}
	if m.extra.Enabled(ctx, r.Level) {
		if err := m.extra.Handle(ctx, r.Clone()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &multiHandler{
		primary: m.primary.WithAttrs(attrs),
		extra:   m.extra.WithAttrs(attrs),
	}
}

func (m *multiHandler) WithGroup(name string) slog.Handler {
	return &multiHandler{
		primary: m.primary.WithGroup(name),
		extra:   m.extra.WithGroup(name),
	}
}

// levelFilterHandler drops records below minLevel before delegating to base.
// Used by WithOTelBridge so only high-severity records are pushed over OTLP
// while the primary stdout handler keeps full-fidelity logging.
type levelFilterHandler struct {
	base     slog.Handler
	minLevel slog.Level
}

func (l *levelFilterHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if level < l.minLevel {
		return false
	}
	return l.base.Enabled(ctx, level)
}

func (l *levelFilterHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level < l.minLevel {
		return nil
	}
	return l.base.Handle(ctx, r)
}

func (l *levelFilterHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &levelFilterHandler{base: l.base.WithAttrs(attrs), minLevel: l.minLevel}
}

func (l *levelFilterHandler) WithGroup(name string) slog.Handler {
	return &levelFilterHandler{base: l.base.WithGroup(name), minLevel: l.minLevel}
}
