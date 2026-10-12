package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/problem"
)

// logRecords decodes the JSON lines a slog JSON handler wrote.
func logRecords(t *testing.T, out *bytes.Buffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("%v: %s", err, line)
		}
		records = append(records, rec)
	}
	return records
}

// loggingServer answers each path with a different kind of failure.
func loggingServer(level slog.Level) (*Config, *bytes.Buffer) {
	var out bytes.Buffer
	cfg := NewConfig(WithLogger(slog.New(slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: level, AddSource: true}))))
	cfg.Handle(Operation{ID: "getPet", Method: "GET", Path: "/pets/{petId}"}, "/pets/{p0}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.PathValue("p0") {
		case "invalid":
			var b Binder
			b.Param("query", "limit", &oapi.ValidationError{Message: `expected integer, got "s3cr3t"`})
			cfg.HandleError(w, r, b.Err())
		case "denied":
			_, err := Authorize(r.Context(), w, []Requirement{{{Check: func(context.Context) (context.Context, error) {
				return nil, errors.New("key revoked")
			}}}})
			cfg.HandleError(w, r, err)
		case "missing":
			cfg.HandleError(w, r, problem.New(http.StatusNotFound, "no such pet"))
		case "broken":
			cfg.HandleError(w, r, errors.New("database down"))
		default:
			cfg.Log(r.Context(), slog.LevelInfo, "found", "id", r.PathValue("p0"))
		}
	}))
	return cfg, &out
}

func TestErrorLogLevels(t *testing.T) {
	cfg, out := loggingServer(slog.LevelInfo)
	h := cfg.Handler()
	for _, id := range []string{"invalid", "denied", "missing", "broken"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/pets/"+id, nil))
	}
	records := logRecords(t, out)
	// At Info, the 404 a handler chose is not logged.
	if len(records) != 3 {
		t.Fatalf("records:\n%s", out)
	}
	invalid, denied, broken := records[0], records[1], records[2]
	if invalid["level"] != "INFO" || invalid["msg"] != "invalid request" || invalid["operation"] != "getPet" || invalid["status"] != 400.0 {
		t.Errorf("invalid request = %v", invalid)
	}
	if locs, _ := invalid["invalid"].([]any); len(locs) != 1 || locs[0] != "query limit" {
		t.Errorf("invalid locations = %v", invalid["invalid"])
	}
	// The request's values stay out of the log above Debug.
	if strings.Contains(out.String(), "s3cr3t") {
		t.Errorf("a request value was logged at Info:\n%s", out)
	}
	if denied["level"] != "INFO" || denied["msg"] != "request not authorized" || denied["status"] != 401.0 || denied["reason"] != "key revoked" {
		t.Errorf("denied = %v", denied)
	}
	if broken["level"] != "ERROR" || broken["msg"] != "request failed" || broken["error"] != "database down" || broken["operation"] != "getPet" {
		t.Errorf("broken = %v", broken)
	}
}

func TestErrorLogDebug(t *testing.T) {
	cfg, out := loggingServer(slog.LevelDebug)
	h := cfg.Handler()
	for _, id := range []string{"invalid", "missing"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/pets/"+id, nil))
	}
	records := logRecords(t, out)
	if len(records) != 2 {
		t.Fatalf("records:\n%s", out)
	}
	// Debug adds the details of invalid requests, and the problems a
	// handler answers with.
	if problems, _ := records[0]["problems"].([]any); len(problems) != 1 || problems[0] != `query limit: expected integer, got "s3cr3t"` {
		t.Errorf("problems = %v", records[0]["problems"])
	}
	if missing := records[1]; missing["level"] != "DEBUG" || missing["status"] != 404.0 || missing["detail"] != "no such pet" {
		t.Errorf("missing = %v", missing)
	}
}

func TestLogAddsOperationAndCaller(t *testing.T) {
	cfg, out := loggingServer(slog.LevelInfo)
	cfg.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/pets/7", nil))
	rec := logRecords(t, out)[0]
	if rec["msg"] != "found" || rec["operation"] != "getPet" || rec["id"] != "7" {
		t.Errorf("record = %v", rec)
	}
	// The source is the code that called Log, not Log itself.
	if src, _ := rec["source"].(map[string]any); src == nil || !strings.HasSuffix(src["file"].(string), "log_test.go") {
		t.Errorf("source = %v", rec["source"])
	}
}

func TestOperationFromContext(t *testing.T) {
	cfg := NewConfig()
	var got Operation
	cfg.Handle(Operation{ID: "listPets", Name: "ListPets", Method: "GET", Path: "/pets"}, "/pets", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = OperationFromContext(RequestContext(r))
	}))
	cfg.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/pets", nil))
	if got.ID != "listPets" || got.Path != "/pets" {
		t.Errorf("operation = %+v", got)
	}
	if _, ok := OperationFromContext(context.Background()); ok {
		t.Error("operation found outside a request")
	}
}

func TestAuthErrorWrapsProblem(t *testing.T) {
	// Error handlers that look for a *problem.Problem keep working.
	_, err := Authorize(context.Background(), httptest.NewRecorder(), []Requirement{{{Check: func(context.Context) (context.Context, error) {
		return nil, ErrNoCredentials
	}}}})
	var p *problem.Problem
	var auth *AuthError
	if !errors.As(err, &p) || p.Status != 401 || !errors.As(err, &auth) || auth.Err != nil {
		t.Errorf("err = %#v", err)
	}
}
