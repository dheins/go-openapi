package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// Observer is told about every request routed to an operation: Start
// before it is handled, End once its response is written or a panic in
// it recovered. Observers implement logging, tracing and metrics, such
// as WithAccessLog, or a tracer from the library of your choice.
//
// Start returns the context the request is handled with, which the
// handler, the middleware and the server's logging see; it can carry a
// span, for example. End gets the context Start returned.
type Observer interface {
	Start(ctx context.Context, op Operation, r *http.Request) context.Context
	End(ctx context.Context, op Operation, r *http.Request, res Result)
}

// Result describes how a request ended.
type Result struct {
	// Status is the status code sent: 200 if the handler wrote a body
	// without one, 500 after a recovered panic, 0 if nothing was sent.
	Status int
	// Bytes is the size of the body sent.
	Bytes    int64
	Duration time.Duration
	// Err is the error the request was answered for: invalid input (a
	// *BindError), a *problem.Problem or any other error a handler
	// returned. It is nil for a successful response.
	Err error
	// Panic is the value a handler or middleware panicked with, if any.
	Panic any
}

// WithObserver adds observers, called in order by Start and in reverse
// order by End.
func WithObserver(o ...Observer) Option {
	return func(c *Config) { c.Observers = append(c.Observers, o...) }
}

// outcome collects, for the observers, what happened to a request that
// the response writer cannot tell.
type outcome struct {
	err   error
	panic any
}

type outcomeKey struct{}

// recordError notes err as the error the request in ctx was answered for.
func recordError(ctx context.Context, err error) {
	if o, ok := ctx.Value(outcomeKey{}).(*outcome); ok && o.err == nil {
		o.err = err
	}
}

// recordPanic notes the value the request in ctx panicked with.
func recordPanic(ctx context.Context, v any) {
	if o, ok := ctx.Value(outcomeKey{}).(*outcome); ok {
		o.panic = v
	}
}

// observe wraps the handling of op, outside panic recovery, so that the
// observers see every request with its final status.
func (c *Config) observe(op Operation, next http.Handler) http.Handler {
	observers := c.Observers
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		o := &outcome{}
		ctx := context.WithValue(r.Context(), outcomeKey{}, o)
		ctxs := make([]context.Context, len(observers))
		for i, ob := range observers {
			ctx = ob.Start(ctx, op, r)
			ctxs[i] = ctx
		}
		r = r.WithContext(ctx)
		tw := &trackingWriter{ResponseWriter: w}
		// End runs even when a panic escapes recovery (http.ErrAbortHandler).
		defer func() {
			res := Result{Status: tw.status, Bytes: tw.bytes, Duration: time.Since(start), Err: o.err, Panic: o.panic}
			for i := len(observers) - 1; i >= 0; i-- {
				observers[i].End(ctxs[i], op, r, res)
			}
		}()
		next.ServeHTTP(tw, r)
	})
}

// WithAccessLog logs every request routed to an operation with the
// server's Logger: its operation, method, route, path, status, duration,
// body size and error. Requests answered with 5xx, or that panicked, are
// logged at Error, the others at level.
func WithAccessLog(level slog.Level) Option {
	return func(c *Config) { c.Observers = append(c.Observers, &accessLog{c: c, level: level}) }
}

type accessLog struct {
	c     *Config
	level slog.Level
}

func (a *accessLog) Start(ctx context.Context, _ Operation, _ *http.Request) context.Context {
	return ctx
}

func (a *accessLog) End(ctx context.Context, op Operation, r *http.Request, res Result) {
	level := a.level
	if res.Status >= 500 || res.Panic != nil {
		level = slog.LevelError
	}
	operation := op.ID
	if operation == "" {
		operation = op.Name
	}
	attrs := []slog.Attr{
		slog.String("operation", operation),
		slog.String("method", r.Method),
		slog.String("route", op.Path),
		slog.String("path", r.URL.Path),
		slog.Int("status", res.Status),
		slog.Duration("duration", res.Duration),
		slog.Int64("bytes", res.Bytes),
	}
	if res.Err != nil {
		attrs = append(attrs, slog.String("error", res.Err.Error()))
	}
	// The Logger is read here, not when the option is applied, so the
	// order of WithAccessLog and WithLogger does not matter.
	a.c.Logger.LogAttrs(ctx, level, "request", attrs...)
}
