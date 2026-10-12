package goapiotel

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/dheins/go-openapi/runtime/server"
)

// clearEnv unsets the variables Setup reads, so the developer's
// environment does not leak into the tests.
func clearEnv(t *testing.T) {
	for _, v := range []string{"OTEL_SDK_DISABLED", "OTEL_TRACES_EXPORTER", "OTEL_METRICS_EXPORTER", "OTEL_SERVICE_NAME", "OTEL_RESOURCE_ATTRIBUTES", "OTEL_EXPORTER_OTLP_PROTOCOL"} {
		t.Setenv(v, "")
	}
}

func TestSetupDisabled(t *testing.T) {
	clearEnv(t)
	for name, setup := range map[string]func() (*Telemetry, error){
		"nothing configured": func() (*Telemetry, error) { return Setup(context.Background(), Config{}) },
		"disabled": func() (*Telemetry, error) {
			return Setup(context.Background(), Config{Disabled: true, Traces: []Exporter{{Kind: "stdout"}}})
		},
		"none": func() (*Telemetry, error) {
			return Setup(context.Background(), Config{Traces: []Exporter{{Kind: "none"}}, Metrics: []Exporter{{Kind: "none"}}})
		},
		"OTEL_SDK_DISABLED": func() (*Telemetry, error) {
			t.Setenv("OTEL_SDK_DISABLED", "true")
			defer t.Setenv("OTEL_SDK_DISABLED", "")
			return Setup(context.Background(), Config{Traces: []Exporter{{Kind: "stdout"}}})
		},
		"OTEL_TRACES_EXPORTER=none": func() (*Telemetry, error) {
			t.Setenv("OTEL_TRACES_EXPORTER", "none")
			defer t.Setenv("OTEL_TRACES_EXPORTER", "")
			return Setup(context.Background(), Config{})
		},
	} {
		tel, err := setup()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if tel.Enabled() || tel.Prometheus != nil {
			t.Errorf("%s: enabled", name)
		}
		// A disabled Telemetry adds no observer: no overhead at all.
		if cfg := server.NewConfig(tel.ServerOption()); len(cfg.Observers) != 0 {
			t.Errorf("%s: %d observers", name, len(cfg.Observers))
		}
		if err := tel.Shutdown(context.Background()); err != nil {
			t.Errorf("%s: shutdown: %v", name, err)
		}
	}
}

func TestSetupSeveralExporters(t *testing.T) {
	clearEnv(t)
	// Two trace destinations at once, as for two vendors, and metrics for
	// a Prometheus scrape.
	var first, second bytes.Buffer
	tel, err := Setup(context.Background(), Config{
		ServiceName: "petstore",
		Traces:      []Exporter{{Kind: "stdout", Writer: &first}, {Kind: "console", Writer: &second}},
		Metrics:     []Exporter{{Kind: "prometheus"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !tel.Enabled() || tel.Prometheus == nil {
		t.Fatalf("enabled %t, prometheus %v", tel.Enabled(), tel.Prometheus)
	}
	h := newServer(tel.ServerOption())
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/pets/7", nil))

	scrape := httptest.NewRecorder()
	tel.Prometheus.ServeHTTP(scrape, httptest.NewRequest("GET", "/metrics", nil))
	if body := scrape.Body.String(); !strings.Contains(body, "http_server_request_duration_seconds") || !strings.Contains(body, `http_route="/pets/{petId}"`) {
		t.Errorf("prometheus scrape lacks the request metrics:\n%s", body)
	}

	// Shutdown flushes the batched spans to both writers.
	if err := tel.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	for name, out := range map[string]*bytes.Buffer{"first": &first, "second": &second} {
		var span struct {
			Name     string
			Resource []struct {
				Key   string
				Value struct{ Value any }
			}
		}
		if err := json.NewDecoder(out).Decode(&span); err != nil {
			t.Fatalf("%s exporter: %v", name, err)
		}
		if span.Name != "GET /pets/{petId}" {
			t.Errorf("%s exporter span = %q", name, span.Name)
		}
		service := ""
		for _, kv := range span.Resource {
			if kv.Key == "service.name" {
				service, _ = kv.Value.Value.(string)
			}
		}
		if service != "petstore" {
			t.Errorf("%s exporter service.name = %q", name, service)
		}
	}
}

func TestSetupFromEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("OTEL_METRICS_EXPORTER", "none, prometheus")
	t.Setenv("OTEL_SERVICE_NAME", "from-env")
	tel, err := Setup(context.Background(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer tel.Shutdown(context.Background())
	if !tel.Enabled() || tel.Prometheus == nil {
		t.Errorf("env exporters not used: enabled %t", tel.Enabled())
	}
}

func TestSetupOTLP(t *testing.T) {
	clearEnv(t)
	// OTLP exporters connect lazily; building and shutting them down
	// without data sends nothing.
	for _, protocol := range []string{"grpc", "http/protobuf"} {
		tel, err := Setup(context.Background(), Config{
			Traces:  []Exporter{{Kind: "otlp", Protocol: protocol, Endpoint: "localhost:4317", Insecure: true, Headers: map[string]string{"api-key": "k"}}},
			Metrics: []Exporter{{Kind: "otlp", Protocol: protocol, Endpoint: "http://localhost:4318", Insecure: true}},
		})
		if err != nil {
			t.Fatalf("%s: %v", protocol, err)
		}
		if !tel.Enabled() {
			t.Errorf("%s: not enabled", protocol)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 0)
		_ = tel.Shutdown(ctx) // nothing to flush; a timeout error is fine here
		cancel()
	}
}

func TestSetupErrors(t *testing.T) {
	clearEnv(t)
	for _, c := range []Config{
		{Traces: []Exporter{{Kind: "zipkin"}}},
		{Traces: []Exporter{{Kind: "prometheus"}}},
		{Metrics: []Exporter{{Kind: "otlp", Protocol: "carrier-pigeon"}}},
	} {
		if _, err := Setup(context.Background(), c); err == nil {
			t.Errorf("%+v: no error", c)
		}
	}
}

func TestLogHandler(t *testing.T) {
	var out bytes.Buffer
	logger := slog.New(LogHandler(slog.NewJSONHandler(&out, nil))).With("service", "petstore")

	spans := tracetest.NewSpanRecorder()
	ctx, span := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)).Tracer("test").Start(context.Background(), "op")
	logger.InfoContext(ctx, "in a span")
	span.End()
	logger.InfoContext(context.Background(), "outside")

	dec := json.NewDecoder(&out)
	var inSpan, outside map[string]any
	if err := dec.Decode(&inSpan); err != nil {
		t.Fatal(err)
	}
	if err := dec.Decode(&outside); err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if inSpan["trace_id"] != span.SpanContext().TraceID().String() || inSpan["span_id"] != span.SpanContext().SpanID().String() || inSpan["service"] != "petstore" {
		t.Errorf("record in a span = %v", inSpan)
	}
	if _, has := outside["trace_id"]; has {
		t.Errorf("record outside a span = %v", outside)
	}
}
