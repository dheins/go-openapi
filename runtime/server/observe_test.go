package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/runtime/problem"
)

// recordingObserver keeps what it was told, and puts its name in the
// context so the handler and End can be checked to see it.
type recordingObserver struct {
	name   string
	events *[]string
	ends   []Result
}

type observerKey string

func (o *recordingObserver) Start(ctx context.Context, op Operation, r *http.Request) context.Context {
	*o.events = append(*o.events, o.name+" start "+op.ID)
	return context.WithValue(ctx, observerKey(o.name), true)
}

func (o *recordingObserver) End(ctx context.Context, op Operation, r *http.Request, res Result) {
	if ctx.Value(observerKey(o.name)) == nil {
		*o.events = append(*o.events, o.name+" end without its context")
	}
	*o.events = append(*o.events, o.name+" end "+op.ID)
	o.ends = append(o.ends, res)
}

func TestObservers(t *testing.T) {
	var events []string
	outer := &recordingObserver{name: "outer", events: &events}
	inner := &recordingObserver{name: "inner", events: &events}
	cfg := NewConfig(WithObserver(outer, inner), WithLogger(slog.New(slog.DiscardHandler)))
	cfg.Handle(Operation{ID: "ok", Method: "GET", Path: "/ok"}, "/ok", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The handler runs with the observers' context.
		if r.Context().Value(observerKey("outer")) == nil || r.Context().Value(observerKey("inner")) == nil {
			t.Error("handler context lacks the observers' values")
		}
		events = append(events, "handler")
		w.Write([]byte("hello"))
	}))
	cfg.Handle(Operation{ID: "fail", Method: "GET", Path: "/fail"}, "/fail", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg.HandleError(w, r, errors.New("database down"))
	}))
	cfg.Handle(Operation{ID: "missing", Method: "GET", Path: "/missing"}, "/missing", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg.HandleError(w, r, problem.New(http.StatusNotFound, "no such pet"))
	}))
	cfg.Handle(Operation{ID: "boom", Method: "GET", Path: "/boom"}, "/boom", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("kaboom")
	}))
	h := cfg.Handler()
	for _, path := range []string{"/ok", "/fail", "/missing", "/boom"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
	}

	// Start in order, End in reverse, each with its own context.
	if got, want := strings.Join(events[:5], ", "), "outer start ok, inner start ok, handler, inner end ok, outer end ok"; got != want {
		t.Errorf("events = %s\nwant %s", got, want)
	}
	if strings.Contains(strings.Join(events, ","), "without its context") {
		t.Errorf("End lost the context: %v", events)
	}
	res := outer.ends
	if len(res) != 4 {
		t.Fatalf("ends = %+v", res)
	}
	if r := res[0]; r.Status != 200 || r.Bytes != 5 || r.Err != nil || r.Panic != nil || r.Duration < 0 {
		t.Errorf("ok = %+v", r)
	}
	if r := res[1]; r.Status != 500 || r.Err == nil || r.Err.Error() != "database down" {
		t.Errorf("fail = %+v", r)
	}
	var p *problem.Problem
	if r := res[2]; r.Status != 404 || !errors.As(r.Err, &p) {
		t.Errorf("missing = %+v", r)
	}
	if r := res[3]; r.Status != 500 || r.Panic != "kaboom" {
		t.Errorf("boom = %+v", r)
	}
	if inner.ends[3].Status != 500 {
		t.Error("observers see different results")
	}
}

func TestObserversSeeBindErrors(t *testing.T) {
	var events []string
	o := &recordingObserver{name: "o", events: &events}
	cfg := NewConfig(WithObserver(o))
	cfg.Handle(Operation{ID: "bind", Method: "GET", Path: "/bind"}, "/bind", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b Binder
		b.Missing("query", "limit")
		cfg.HandleError(w, r, b.Err())
	}))
	cfg.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/bind", nil))
	var be *BindError
	if r := o.ends[0]; r.Status != 400 || !errors.As(r.Err, &be) {
		t.Errorf("bind = %+v", r)
	}
}

func TestObserverSeesAbortedRequests(t *testing.T) {
	var events []string
	o := &recordingObserver{name: "o", events: &events}
	cfg := NewConfig(WithObserver(o))
	cfg.Handle(Operation{ID: "abort", Method: "GET", Path: "/abort"}, "/abort", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	func() {
		defer func() {
			if v := recover(); v != http.ErrAbortHandler {
				t.Errorf("ErrAbortHandler should pass through, got %v", v)
			}
		}()
		cfg.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/abort", nil))
	}()
	if len(o.ends) != 1 || o.ends[0].Panic != http.ErrAbortHandler {
		t.Errorf("ends = %+v", o.ends)
	}
}

func TestAccessLog(t *testing.T) {
	var out strings.Builder
	// WithAccessLog before WithLogger: the logger is read per request.
	cfg := NewConfig(WithAccessLog(slog.LevelInfo), WithLogger(slog.New(slog.NewJSONHandler(&out, nil))))
	cfg.Handle(Operation{ID: "getPet", Method: "GET", Path: "/pets/{petId}"}, "/pets/{p0}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("p0") == "0" {
			cfg.HandleError(w, r, errors.New("no pet zero"))
			return
		}
		w.Write([]byte(`{"id":7}`))
	}))
	h := cfg.Handler()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/pets/7", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/pets/0", nil))

	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("%v: %s", err, line)
		}
		if rec["msg"] == "request" {
			records = append(records, rec)
		}
	}
	if len(records) != 2 {
		t.Fatalf("access log:\n%s", out.String())
	}
	ok, failed := records[0], records[1]
	for k, want := range map[string]any{"level": "INFO", "operation": "getPet", "method": "GET", "route": "/pets/{petId}", "path": "/pets/7", "status": 200.0, "bytes": 8.0} {
		if ok[k] != want {
			t.Errorf("%s = %v, want %v", k, ok[k], want)
		}
	}
	if _, has := ok["error"]; has {
		t.Errorf("successful request logged an error: %v", ok)
	}
	if failed["level"] != "ERROR" || failed["status"] != 500.0 || failed["error"] != "no pet zero" {
		t.Errorf("failed request = %v", failed)
	}
}

func TestNoObserversNoWrapping(t *testing.T) {
	// Without observers the request keeps its context: nothing is added.
	cfg := NewConfig()
	cfg.Handle(Operation{ID: "ctx", Method: "GET", Path: "/ctx"}, "/ctx", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Value(outcomeKey{}) != nil {
			t.Error("an outcome was recorded without observers")
		}
	}))
	cfg.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/ctx", nil))
}
