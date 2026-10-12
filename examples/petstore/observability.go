package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/exp/zapslog"
	"go.uber.org/zap/zapcore"

	goapiotel "github.com/dheins/go-openapi/contrib/otel"
	"github.com/dheins/go-openapi/contrib/zaplog"
)

// zapConfig is how the example configures zap; change it to log
// differently. level is a zap level ("debug", "info", "warn", "error");
// encoding is "json", for log pipelines, or "console", for people.
func zapConfig(level, encoding string) (zap.Config, error) {
	lvl, err := zap.ParseAtomicLevel(level)
	if err != nil {
		return zap.Config{}, fmt.Errorf("-log-level: %w", err)
	}
	enc := zap.NewProductionEncoderConfig()
	enc.TimeKey = "time"
	enc.EncodeTime = zapcore.ISO8601TimeEncoder        // 2026-10-12T17:30:04.011-0700
	enc.EncodeDuration = zapcore.StringDurationEncoder // the access log's "duration": "412µs"
	switch encoding {
	case "json":
	case "console":
		enc.EncodeLevel = zapcore.CapitalColorLevelEncoder
	default:
		return zap.Config{}, fmt.Errorf("-log-encoding %q: use json or console", encoding)
	}
	return zap.Config{
		Level:            lvl,
		Encoding:         encoding,
		EncoderConfig:    enc,
		OutputPaths:      []string{"stderr"},
		ErrorOutputPaths: []string{"stderr"},
		// Fields on every entry.
		InitialFields: map[string]any{"service": "petstore"},
		// No sampling: zap samples per message, and every access log
		// entry has the message "request", so sampling would drop most of
		// them under load. Turn it on (zap.SamplingConfig) for chatty
		// application logs if the access log is off.
		Sampling: nil,
	}, nil
}

// newLogger builds the zap logger and the *slog.Logger the server logs
// through, which adds trace and span IDs to entries logged during a
// traced request. The returned function flushes zap's buffers.
func newLogger(level, encoding string) (*slog.Logger, func(), error) {
	cfg, err := zapConfig(level, encoding)
	if err != nil {
		return nil, nil, err
	}
	zl, err := cfg.Build()
	if err != nil {
		return nil, nil, fmt.Errorf("zap: %w", err)
	}
	// The server logs through slog. WithCaller records the call site in
	// "caller". zapslog adds a stack trace to every Error entry by
	// default, which would put one on each 5xx access log entry; the
	// runtime already logs a panic's stack, so raise the threshold above
	// every level in use.
	const noStacktraces = slog.LevelError + 4
	h := zaplog.Logger(zl, zapslog.WithCaller(true), zapslog.AddStacktraceAt(noStacktraces)).Handler()
	// Sync fails for stderr on some terminals; there is nothing to do
	// about it at exit.
	sync := func() { _ = zl.Sync() }
	return slog.New(goapiotel.LogHandler(h)), sync, nil
}

// setupTelemetry exports traces and metrics to the exporters named in
// traces and metrics (comma-separated), or as the OTEL_* environment
// variables say if they are empty. Nothing configured anywhere, or
// "none", disables telemetry.
func setupTelemetry(ctx context.Context, traces, metrics string) (*goapiotel.Telemetry, error) {
	return goapiotel.Setup(ctx, goapiotel.Config{
		ServiceName: "petstore",
		Traces:      exporters(traces),
		Metrics:     exporters(metrics),
	})
}

func exporters(list string) []goapiotel.Exporter {
	var out []goapiotel.Exporter
	for _, kind := range strings.Split(list, ",") {
		if kind = strings.TrimSpace(kind); kind != "" {
			out = append(out, goapiotel.Exporter{Kind: kind})
		}
	}
	return out
}
