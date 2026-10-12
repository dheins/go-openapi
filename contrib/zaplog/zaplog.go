// Package zaplog makes generated servers log with zap.
//
// The runtime logs through log/slog; this package adapts a *zap.Logger to
// it with zap's own slog handler, so runtime errors, recovered panics and
// the access log (server.WithAccessLog) become zap entries:
//
//	zl, _ := zap.NewProduction()
//	h := gen.NewServer(store, auth, zaplog.WithZap(zl), server.WithAccessLog(slog.LevelInfo))
package zaplog

import (
	"log/slog"

	"go.uber.org/zap"
	"go.uber.org/zap/exp/zapslog"

	"github.com/dheins/go-openapi/runtime/server"
)

// Logger returns a *slog.Logger that writes to l. opts configure the
// handler, for example zapslog.WithCaller(true) to record callers.
func Logger(l *zap.Logger, opts ...zapslog.HandlerOption) *slog.Logger {
	return slog.New(zapslog.NewHandler(l.Core(), opts...))
}

// WithZap makes a generated server log to l.
func WithZap(l *zap.Logger, opts ...zapslog.HandlerOption) server.Option {
	return server.WithLogger(Logger(l, opts...))
}
