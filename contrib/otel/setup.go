package goapiotel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/dheins/go-openapi/runtime/server"
)

// Config says where telemetry goes. Its zero value reads the standard
// OpenTelemetry environment variables, and exports nothing unless they
// name exporters: telemetry is opt-in.
type Config struct {
	// Disabled turns telemetry off, as does OTEL_SDK_DISABLED=true.
	Disabled bool
	// ServiceName and ServiceVersion identify the service; OTEL_SERVICE_NAME
	// and OTEL_RESOURCE_ATTRIBUTES are read too, and these take precedence.
	ServiceName, ServiceVersion string
	// Traces and Metrics are the exporters, any number of each, to send to
	// several vendors at once. If empty, OTEL_TRACES_EXPORTER and
	// OTEL_METRICS_EXPORTER list them, comma-separated ("otlp", "console",
	// "prometheus" for metrics, "none").
	Traces, Metrics []Exporter
	// SampleRatio samples that share of new traces (0 to 1); traces a
	// caller started are followed as they were sampled. 0 samples all.
	SampleRatio float64
}

// Exporter is one destination for traces or metrics.
type Exporter struct {
	// Kind is "otlp", "stdout" (or "console"), "prometheus" (metrics
	// only) or "none".
	Kind string
	// Protocol is "grpc" or "http/protobuf" for OTLP; empty reads
	// OTEL_EXPORTER_OTLP_[TRACES_|METRICS_]PROTOCOL, then http/protobuf.
	Protocol string
	// Endpoint, Headers and Insecure configure OTLP; empty values are read
	// from the OTEL_EXPORTER_OTLP_* environment variables. Vendors take
	// their API key as a header.
	Endpoint string
	Headers  map[string]string
	Insecure bool
	// Writer receives stdout output; nil is os.Stdout.
	Writer io.Writer
}

// Telemetry holds the providers Setup built.
type Telemetry struct {
	TracerProvider trace.TracerProvider
	MeterProvider  metric.MeterProvider
	// Prometheus serves the metrics for scraping if a "prometheus" metrics
	// exporter is configured, and is nil otherwise. Mount it on a port of
	// its own, such as :9464/metrics.
	Prometheus http.Handler

	enabled  bool
	shutdown []func(context.Context) error
}

// Enabled reports whether anything is exported.
func (t *Telemetry) Enabled() bool { return t.enabled }

// ServerOption instruments a generated server with the providers (see
// Observer), or does nothing if telemetry is disabled.
func (t *Telemetry) ServerOption(opts ...Option) server.Option {
	if !t.enabled {
		return func(*server.Config) {}
	}
	return WithTelemetry(t.TracerProvider, t.MeterProvider, opts...)
}

// Shutdown flushes and stops the exporters; call it before the program
// exits.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	var errs []error
	for _, f := range t.shutdown {
		errs = append(errs, f(ctx))
	}
	return errors.Join(errs...)
}

// Setup builds the tracer and meter providers c describes and, when
// telemetry is enabled, installs them and the W3C trace context and
// baggage propagators as the OpenTelemetry globals, so other
// instrumentation in the program reports to the same place.
func Setup(ctx context.Context, c Config) (*Telemetry, error) {
	t := &Telemetry{TracerProvider: tracenoop.NewTracerProvider(), MeterProvider: metricnoop.NewMeterProvider()}
	if disabled, _ := strconv.ParseBool(os.Getenv("OTEL_SDK_DISABLED")); c.Disabled || disabled {
		return t, nil
	}
	traces := used(c.Traces, "OTEL_TRACES_EXPORTER")
	metrics := used(c.Metrics, "OTEL_METRICS_EXPORTER")
	if len(traces) == 0 && len(metrics) == 0 {
		return t, nil
	}

	res, err := newResource(ctx, c)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*Telemetry, error) {
		// Release what was built before the error.
		_ = t.Shutdown(ctx)
		return nil, err
	}
	if len(traces) > 0 {
		opts := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}
		if c.SampleRatio > 0 {
			opts = append(opts, sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(c.SampleRatio))))
		}
		for _, e := range traces {
			exp, err := newSpanExporter(ctx, e)
			if err != nil {
				return fail(fmt.Errorf("goapiotel: traces exporter %q: %w", e.Kind, err))
			}
			// Each exporter has a batcher of its own, so a slow vendor does
			// not hold up the others.
			opts = append(opts, sdktrace.WithBatcher(exp))
		}
		tp := sdktrace.NewTracerProvider(opts...)
		t.TracerProvider = tp
		t.shutdown = append(t.shutdown, tp.Shutdown)
	}
	if len(metrics) > 0 {
		opts := []sdkmetric.Option{sdkmetric.WithResource(res)}
		for _, e := range metrics {
			reader, handler, err := newMetricReader(ctx, e)
			if err != nil {
				return fail(fmt.Errorf("goapiotel: metrics exporter %q: %w", e.Kind, err))
			}
			if handler != nil {
				t.Prometheus = handler
			}
			opts = append(opts, sdkmetric.WithReader(reader))
		}
		mp := sdkmetric.NewMeterProvider(opts...)
		t.MeterProvider = mp
		t.shutdown = append(t.shutdown, mp.Shutdown)
	}
	t.enabled = true
	otel.SetTracerProvider(t.TracerProvider)
	otel.SetMeterProvider(t.MeterProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return t, nil
}

// used returns the exporters to build: configured ones, or those the
// environment variable lists, without "none".
func used(configured []Exporter, envVar string) []Exporter {
	if len(configured) == 0 {
		for _, kind := range strings.Split(os.Getenv(envVar), ",") {
			if kind = strings.ToLower(strings.TrimSpace(kind)); kind != "" {
				configured = append(configured, Exporter{Kind: kind})
			}
		}
	}
	var out []Exporter
	for _, e := range configured {
		if strings.ToLower(e.Kind) != "none" {
			out = append(out, e)
		}
	}
	return out
}

func newResource(ctx context.Context, c Config) (*resource.Resource, error) {
	var attrs []resource.Option
	if c.ServiceName != "" {
		attrs = append(attrs, resource.WithAttributes(semconv.ServiceName(c.ServiceName)))
	}
	if c.ServiceVersion != "" {
		attrs = append(attrs, resource.WithAttributes(semconv.ServiceVersion(c.ServiceVersion)))
	}
	// Later options take precedence: the Config over the environment.
	opts := append([]resource.Option{resource.WithFromEnv(), resource.WithTelemetrySDK()}, attrs...)
	res, err := resource.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("goapiotel: resource: %w", err)
	}
	return resource.Merge(resource.Default(), res)
}

// protocol returns the OTLP protocol of e for signal ("TRACES" or
// "METRICS").
func protocol(e Exporter, signal string) string {
	for _, p := range []string{e.Protocol, os.Getenv("OTEL_EXPORTER_OTLP_" + signal + "_PROTOCOL"), os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL")} {
		if p != "" {
			return p
		}
	}
	return "http/protobuf"
}

func newSpanExporter(ctx context.Context, e Exporter) (sdktrace.SpanExporter, error) {
	switch strings.ToLower(e.Kind) {
	case "otlp":
		switch p := protocol(e, "TRACES"); p {
		case "grpc":
			var opts []otlptracegrpc.Option
			if e.Endpoint != "" {
				if strings.Contains(e.Endpoint, "://") {
					opts = append(opts, otlptracegrpc.WithEndpointURL(e.Endpoint))
				} else {
					opts = append(opts, otlptracegrpc.WithEndpoint(e.Endpoint))
				}
			}
			if len(e.Headers) > 0 {
				opts = append(opts, otlptracegrpc.WithHeaders(e.Headers))
			}
			if e.Insecure {
				opts = append(opts, otlptracegrpc.WithInsecure())
			}
			return otlptracegrpc.New(ctx, opts...)
		case "http/protobuf":
			var opts []otlptracehttp.Option
			if e.Endpoint != "" {
				if strings.Contains(e.Endpoint, "://") {
					opts = append(opts, otlptracehttp.WithEndpointURL(e.Endpoint))
				} else {
					opts = append(opts, otlptracehttp.WithEndpoint(e.Endpoint))
				}
			}
			if len(e.Headers) > 0 {
				opts = append(opts, otlptracehttp.WithHeaders(e.Headers))
			}
			if e.Insecure {
				opts = append(opts, otlptracehttp.WithInsecure())
			}
			return otlptracehttp.New(ctx, opts...)
		default:
			return nil, fmt.Errorf("unknown OTLP protocol %q", p)
		}
	case "stdout", "console":
		return stdouttrace.New(stdouttrace.WithWriter(writer(e)))
	}
	return nil, fmt.Errorf("unknown exporter")
}

// newMetricReader returns the reader for e and, for Prometheus, the
// handler that serves its metrics.
func newMetricReader(ctx context.Context, e Exporter) (sdkmetric.Reader, http.Handler, error) {
	switch strings.ToLower(e.Kind) {
	case "otlp":
		var exp sdkmetric.Exporter
		var err error
		switch p := protocol(e, "METRICS"); p {
		case "grpc":
			var opts []otlpmetricgrpc.Option
			if e.Endpoint != "" {
				if strings.Contains(e.Endpoint, "://") {
					opts = append(opts, otlpmetricgrpc.WithEndpointURL(e.Endpoint))
				} else {
					opts = append(opts, otlpmetricgrpc.WithEndpoint(e.Endpoint))
				}
			}
			if len(e.Headers) > 0 {
				opts = append(opts, otlpmetricgrpc.WithHeaders(e.Headers))
			}
			if e.Insecure {
				opts = append(opts, otlpmetricgrpc.WithInsecure())
			}
			exp, err = otlpmetricgrpc.New(ctx, opts...)
		case "http/protobuf":
			var opts []otlpmetrichttp.Option
			if e.Endpoint != "" {
				if strings.Contains(e.Endpoint, "://") {
					opts = append(opts, otlpmetrichttp.WithEndpointURL(e.Endpoint))
				} else {
					opts = append(opts, otlpmetrichttp.WithEndpoint(e.Endpoint))
				}
			}
			if len(e.Headers) > 0 {
				opts = append(opts, otlpmetrichttp.WithHeaders(e.Headers))
			}
			if e.Insecure {
				opts = append(opts, otlpmetrichttp.WithInsecure())
			}
			exp, err = otlpmetrichttp.New(ctx, opts...)
		default:
			return nil, nil, fmt.Errorf("unknown OTLP protocol %q", p)
		}
		if err != nil {
			return nil, nil, err
		}
		return sdkmetric.NewPeriodicReader(exp), nil, nil
	case "stdout", "console":
		exp, err := stdoutmetric.New(stdoutmetric.WithWriter(writer(e)))
		if err != nil {
			return nil, nil, err
		}
		return sdkmetric.NewPeriodicReader(exp), nil, nil
	case "prometheus":
		// A registry of its own, so the handler serves exactly these
		// metrics.
		registry := prometheus.NewRegistry()
		exp, err := promexporter.New(promexporter.WithRegisterer(registry))
		if err != nil {
			return nil, nil, err
		}
		return exp, promhttp.HandlerFor(registry, promhttp.HandlerOpts{}), nil
	}
	return nil, nil, fmt.Errorf("unknown exporter")
}

func writer(e Exporter) io.Writer {
	if e.Writer != nil {
		return e.Writer
	}
	return os.Stdout
}
