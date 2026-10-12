// Package goapiotel adds OpenTelemetry traces and metrics to generated
// servers, for any vendor that accepts OTLP, a Prometheus scrape or
// stdout. Setup builds the providers from a Config or the standard OTEL_*
// environment variables; Observer and WithTelemetry instrument a server
// with providers you already have; LogHandler puts trace and span IDs in
// slog records.
package goapiotel

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/dheins/go-openapi/runtime/server"
)

// instrumentationName names the tracer and meter of this package.
const instrumentationName = "github.com/dheins/go-openapi/contrib/otel"

// OperationIDKey is the span and metric attribute holding the operationId.
const OperationIDKey = attribute.Key("goapi.operation.id")

// Option configures Observer.
type Option func(*options)

type options struct {
	propagator propagation.TextMapPropagator
}

// WithPropagator sets how the trace context of incoming requests is read;
// the default is otel.GetTextMapPropagator().
func WithPropagator(p propagation.TextMapPropagator) Option {
	return func(o *options) { o.propagator = p }
}

// Observer returns a server.Observer that records a server span and the
// HTTP server metrics of every request:
//
//   - a span named "{method} {route}", such as "GET /pets/{petId}", with
//     the HTTP semantic convention attributes and the operationId, a child
//     of the trace context the request carries; requests answered with 5xx
//     or that panicked are marked as errors;
//   - the histogram http.server.request.duration (seconds) and the counter
//     http.server.active_requests, by method, route and status.
func Observer(tp trace.TracerProvider, mp metric.MeterProvider, opts ...Option) server.Observer {
	o := options{propagator: otel.GetTextMapPropagator()}
	for _, opt := range opts {
		opt(&o)
	}
	meter := mp.Meter(instrumentationName)
	duration, err := meter.Float64Histogram("http.server.request.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of HTTP server requests."),
		metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10))
	if err != nil {
		// Reported to the OpenTelemetry error handler, as SDKs do; the
		// returned instrument still works as a no-op.
		otel.Handle(err)
	}
	active, err := meter.Int64UpDownCounter("http.server.active_requests",
		metric.WithUnit("{request}"),
		metric.WithDescription("Number of active HTTP server requests."))
	if err != nil {
		otel.Handle(err)
	}
	return &observer{
		tracer:     tp.Tracer(instrumentationName),
		propagator: o.propagator,
		duration:   duration,
		active:     active,
	}
}

// WithTelemetry instruments a generated server with tp and mp; see
// Observer.
func WithTelemetry(tp trace.TracerProvider, mp metric.MeterProvider, opts ...Option) server.Option {
	return server.WithObserver(Observer(tp, mp, opts...))
}

type observer struct {
	tracer     trace.Tracer
	propagator propagation.TextMapPropagator
	duration   metric.Float64Histogram
	active     metric.Int64UpDownCounter
}

func (o *observer) Start(ctx context.Context, op server.Operation, r *http.Request) context.Context {
	ctx = o.propagator.Extract(ctx, propagation.HeaderCarrier(r.Header))
	attrs := []attribute.KeyValue{
		semconv.HTTPRequestMethodKey.String(r.Method),
		semconv.HTTPRouteKey.String(op.Path),
		semconv.URLPathKey.String(r.URL.Path),
		OperationIDKey.String(operationID(op)),
	}
	if host := hostOnly(r.Host); host != "" {
		attrs = append(attrs, semconv.ServerAddressKey.String(host))
	}
	ctx, _ = o.tracer.Start(ctx, r.Method+" "+op.Path, trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(attrs...))
	o.active.Add(ctx, 1, metric.WithAttributes(semconv.HTTPRequestMethodKey.String(r.Method), semconv.HTTPRouteKey.String(op.Path)))
	return ctx
}

func (o *observer) End(ctx context.Context, op server.Operation, r *http.Request, res server.Result) {
	span := trace.SpanFromContext(ctx)
	attrs := []attribute.KeyValue{
		semconv.HTTPRequestMethodKey.String(r.Method),
		semconv.HTTPRouteKey.String(op.Path),
		OperationIDKey.String(operationID(op)),
	}
	if res.Status != 0 {
		attrs = append(attrs, semconv.HTTPResponseStatusCodeKey.Int(res.Status))
		span.SetAttributes(semconv.HTTPResponseStatusCodeKey.Int(res.Status))
	}
	// Per the HTTP conventions a server span is an error for 5xx only:
	// 4xx are the client's.
	errorType := ""
	switch {
	case res.Panic != nil:
		errorType = "panic"
		span.RecordError(fmt.Errorf("panic: %v", res.Panic))
	case res.Status >= 500:
		errorType = strconv.Itoa(res.Status)
		if res.Err != nil {
			span.RecordError(res.Err)
		}
	}
	if errorType != "" {
		attrs = append(attrs, semconv.ErrorTypeKey.String(errorType))
		span.SetAttributes(semconv.ErrorTypeKey.String(errorType))
		span.SetStatus(codes.Error, http.StatusText(res.Status))
	}
	span.End()
	o.duration.Record(ctx, res.Duration.Seconds(), metric.WithAttributes(attrs...))
	o.active.Add(ctx, -1, metric.WithAttributes(semconv.HTTPRequestMethodKey.String(r.Method), semconv.HTTPRouteKey.String(op.Path)))
}

func operationID(op server.Operation) string {
	if op.ID != "" {
		return op.ID
	}
	return op.Name
}

// hostOnly returns the host of a Host header, without its port.
func hostOnly(hostport string) string {
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return host
	}
	return hostport
}
