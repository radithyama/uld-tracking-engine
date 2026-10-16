// Package logx configures structured JSON logging and process lifecycle helpers.
//
// Every line written to stdout is a single JSON object whose field names
// follow the Elastic Common Schema (ECS), so Docker's json-file driver output
// can be shipped straight into Elastic / Security Onion without grok parsing:
//
//	{"@timestamp":"…","log.level":"warn","message":"…","service":{"name":"ingest"},"ecs":{"version":"8.11.0"},…}
package logx

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

const ecsVersion = "8.11.0"

// New returns an ECS-shaped JSON logger tagged with the service name, and
// installs it as the default logger (which also routes the standard library
// "log" package — e.g. net/http server errors — through it, keeping stdout
// strictly JSON).
func New(service, level string) *slog.Logger {
	return NewWithWriter(os.Stdout, service, level)
}

func NewWithWriter(w io.Writer, service, level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: lvl, ReplaceAttr: ecsAttr})
	l := slog.New(h).With(
		slog.Group("service", slog.String("name", service), slog.String("type", "uld-engine")),
		slog.Group("ecs", slog.String("version", ecsVersion)),
	)
	slog.SetDefault(l)
	return l
}

// ecsAttr renames slog's built-in keys to their ECS equivalents.
func ecsAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) > 0 {
		return a
	}
	switch a.Key {
	case slog.TimeKey:
		return slog.String("@timestamp", a.Value.Time().UTC().Format(time.RFC3339Nano))
	case slog.LevelKey:
		return slog.String("log.level", strings.ToLower(a.Value.String()))
	case slog.MessageKey:
		return slog.Attr{Key: "message", Value: a.Value}
	case "err":
		return slog.Attr{Key: "error.message", Value: a.Value}
	}
	return a
}

// SignalContext is cancelled on SIGINT/SIGTERM so every goroutine can drain.
func SignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// Fatal logs and exits; used only during startup.
func Fatal(l *slog.Logger, msg string, err error) {
	l.Error(msg, "err", err)
	os.Exit(1)
}

// Sampled wraps a printf-style logger callback (as used by kafka-go) so it
// emits at most one line per interval. Client libraries retry in tight loops
// during an outage; without sampling a 60s broker pause floods the SIEM.
func Sampled(l *slog.Logger, level slog.Level, component string, every time.Duration) func(string, ...any) {
	var last atomic.Int64
	var suppressed atomic.Int64
	return func(format string, args ...any) {
		now := time.Now().UnixNano()
		prev := last.Load()
		if now-prev < int64(every) || !last.CompareAndSwap(prev, now) {
			suppressed.Add(1)
			return
		}
		l.Log(context.Background(), level, "client library message",
			"component", component,
			"detail", sprintf(format, args...),
			"suppressed_since_last", suppressed.Swap(0))
	}
}

func sprintf(format string, args ...any) string {
	s := fmt.Sprintf(format, args...)
	if len(s) > 512 {
		s = s[:512] + "…"
	}
	return s
}
