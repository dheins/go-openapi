package goapiotel

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/dheins/go-openapi/runtime/problem"
	"github.com/dheins/go-openapi/runtime/server"
)

// newServer is a generated-like server with three outcomes per pet id.
func newServer(opts ...server.Option) http.Handler {
	cfg := server.NewConfig(append([]server.Option{server.WithLogger(slog.New(slog.DiscardHandler))}, opts...)...)
	cfg.Handle(server.Operation{ID: "getPet", Name: "GetPet", Method: "GET", Path: "/pets/{petId}"}, "/pets/{p0}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.PathValue("p0") {
		case "0":
			cfg.HandleError(w, r, errors.New("database down"))
		case "404":
			cfg.HandleError(w, r, problem.New(http.StatusNotFound, "no such pet"))
		case "666":
			panic("kaboom")
		default:
			w.Write([]byte(`{"id":7}`))
		}
	}))
	return cfg.Handler()
}

func attrs(kvs []attribute.KeyValue) map[attribute.Key]attribute.Value {
	m := make(map[attribute.Key]attribute.Value, len(kvs))
	for _, kv := range kvs {
		m[kv.Key] = kv.Value
	}
	return m
}

func TestObserverSpans(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	h := newServer(WithTelemetry(tp, mp, WithPropagator(propagation.TraceContext{})))

	// The first request continues a trace the caller started.
	const parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	req := httptest.NewRequest("GET", "http://pets.example:8080/pets/7", nil)
	req.Header.Set("traceparent", parent)
	h.ServeHTTP(httptest.NewRecorder(), req)
	for _, id := range []string{"0", "404", "666"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/pets/"+id, nil))
	}

	ended := spans.Ended()
	if len(ended) != 4 {
		t.Fatalf("spans = %d", len(ended))
	}
	ok := ended[0]
	a := attrs(ok.Attributes())
	if ok.Name() != "GET /pets/{petId}" || ok.SpanKind() != trace.SpanKindServer {
		t.Errorf("span = %s %v", ok.Name(), ok.SpanKind())
	}
	for k, want := range map[attribute.Key]string{
		"http.request.method": "GET", "http.route": "/pets/{petId}", "url.path": "/pets/7",
		"server.address": "pets.example", "goapi.operation.id": "getPet",
	} {
		if a[k].AsString() != want {
			t.Errorf("%s = %q, want %q", k, a[k].AsString(), want)
		}
	}
	if a["http.response.status_code"].AsInt64() != 200 || ok.Status().Code == codes.Error {
		t.Errorf("ok span status = %v %v", a["http.response.status_code"], ok.Status())
	}
	if got := ok.Parent().TraceID().String(); got != "4bf92f3577b34da6a3ce929d0e0e4736" || ok.SpanContext().TraceID().String() != got {
		t.Errorf("span is not in the caller's trace: parent %s, span %s", got, ok.SpanContext().TraceID())
	}

	failed := ended[1]
	if failed.Status().Code != codes.Error || attrs(failed.Attributes())["error.type"].AsString() != "500" || len(failed.Events()) != 1 {
		t.Errorf("500 span = %v %v events %d", failed.Status(), failed.Attributes(), len(failed.Events()))
	}
	// 4xx are the client's error, not the server's.
	if notFound := ended[2]; notFound.Status().Code == codes.Error || attrs(notFound.Attributes())["http.response.status_code"].AsInt64() != 404 {
		t.Errorf("404 span = %v %v", notFound.Status(), notFound.Attributes())
	}
	if panicked := ended[3]; panicked.Status().Code != codes.Error || attrs(panicked.Attributes())["error.type"].AsString() != "panic" {
		t.Errorf("panic span = %v %v", panicked.Status(), panicked.Attributes())
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	var durations int
	statuses := map[int64]bool{}
	activeLeft := int64(-1)
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch m.Name {
			case "http.server.request.duration":
				for _, dp := range m.Data.(metricdata.Histogram[float64]).DataPoints {
					durations += int(dp.Count)
					if v, ok := dp.Attributes.Value("http.response.status_code"); ok {
						statuses[v.AsInt64()] = true
					}
					if route, _ := dp.Attributes.Value("http.route"); route.AsString() != "/pets/{petId}" {
						t.Errorf("histogram route = %v", route)
					}
				}
			case "http.server.active_requests":
				activeLeft = 0
				for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
					activeLeft += dp.Value
				}
			}
		}
	}
	if durations != 4 || !statuses[200] || !statuses[500] || !statuses[404] {
		t.Errorf("duration points = %d, statuses %v", durations, statuses)
	}
	if activeLeft != 0 {
		t.Errorf("active requests left = %d", activeLeft)
	}
}
