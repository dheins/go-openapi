package goapiotel

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// LogHandler wraps h so that records logged with a context carrying a span
// get its trace_id and span_id, which link log lines to traces. The
// server logs with the request's context, so wrapping the handler of the
// server's logger (zap's included) is enough:
//
//	logger := slog.New(goapiotel.LogHandler(zapslog.NewHandler(zl.Core())))
func LogHandler(h slog.Handler) slog.Handler {
	return logHandler{h}
}

type logHandler struct {
	slog.Handler
}

func (h logHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		// The record may share its attributes with the caller's copy.
		r = r.Clone()
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	return h.Handler.Handle(ctx, r)
}

func (h logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return logHandler{h.Handler.WithAttrs(attrs)}
}

func (h logHandler) WithGroup(name string) slog.Handler {
	return logHandler{h.Handler.WithGroup(name)}
}
