package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/dheins/go-openapi/runtime/problem"
)

type operationKey struct{}

// withOperation puts op in the context of every request to next, where
// logging and OperationFromContext find it.
func withOperation(op Operation, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), operationKey{}, op)))
	})
}

// OperationFromContext returns the operation a request is routed to, from
// the context of a request (or of a handler) of a generated server.
func OperationFromContext(ctx context.Context) (Operation, bool) {
	op, ok := ctx.Value(operationKey{}).(Operation)
	return op, ok
}

// operationID names op in logs: its operationId, or its method name if the
// document gives none.
func operationID(op Operation) string {
	if op.ID != "" {
		return op.ID
	}
	return op.Name
}

// Log logs msg at level with the server's Logger, adding the operation the
// request in ctx is routed to. args are slog key-value pairs. The caller
// of Log is recorded as the source, so handlers that record callers (such
// as zap's) point at the code that logged.
func (c *Config) Log(ctx context.Context, level slog.Level, msg string, args ...any) {
	if !c.Logger.Enabled(ctx, level) {
		return
	}
	var pcs [1]uintptr
	runtime.Callers(2, pcs[:])
	rec := slog.NewRecord(time.Now(), level, msg, pcs[0])
	if op, ok := OperationFromContext(ctx); ok {
		rec.AddAttrs(slog.String("operation", operationID(op)))
	}
	rec.Add(args...)
	// A logging failure has nowhere better to be reported.
	_ = c.Logger.Handler().Handle(ctx, rec)
}

// LogResponse logs, at Debug, the type of resp, the response a handler
// returned, such as "GetPet404Response".
func (c *Config) LogResponse(ctx context.Context, resp any) {
	if !c.Logger.Enabled(ctx, slog.LevelDebug) {
		return
	}
	name := "nil"
	if t := reflect.TypeOf(resp); t != nil {
		name = t.Name()
	}
	c.Log(ctx, slog.LevelDebug, "sending response", "response", name)
}

// logError logs err, which r is answered for, at a level that says whose
// problem it is: invalid requests and failed authentication at Info, the
// problems a handler answers with (4xx) at Debug, and server errors at
// Error. Request bodies, credentials and, except at Debug, the values in
// a request are not logged.
func (c *Config) logError(r *http.Request, err error) {
	ctx := r.Context()
	var bind *BindError
	var auth *AuthError
	var p *problem.Problem
	switch {
	case errors.As(err, &bind):
		// The details can quote the request's values, so only Debug logs
		// them; the client gets them in the response either way.
		args := []any{"status", bind.Problem.Status, "invalid", problemLocations(bind.Problem)}
		if c.Logger.Enabled(ctx, slog.LevelDebug) {
			args = append(args, "problems", problemList(bind.Problem))
		}
		c.Log(ctx, slog.LevelInfo, "invalid request", args...)
	case errors.As(err, &auth):
		args := []any{"status", auth.Problem.Status}
		if auth.Err != nil {
			args = append(args, "reason", auth.Err.Error())
		}
		c.Log(ctx, slog.LevelInfo, "request not authorized", args...)
	case errors.As(err, &p) && p.Status < 500:
		c.Log(ctx, slog.LevelDebug, "answering with a problem", "status", p.Status, "detail", p.Detail)
	default:
		c.Log(ctx, slog.LevelError, "request failed", "method", r.Method, "path", r.URL.Path, "error", err)
	}
}

// problemLocations lists where the problems of an invalid request are:
// "query limit", "body /name". The request itself (a body too large, of
// the wrong media type) is "request".
func problemLocations(p *problem.Problem) []string {
	if len(p.Errors) == 0 {
		return []string{"request"}
	}
	out := make([]string, 0, len(p.Errors))
	for _, e := range p.Errors {
		out = append(out, location(e))
	}
	return out
}

// problemList is problemLocations with each problem's detail.
func problemList(p *problem.Problem) []string {
	if len(p.Errors) == 0 {
		return []string{p.Detail}
	}
	out := make([]string, 0, len(p.Errors))
	for _, e := range p.Errors {
		out = append(out, location(e)+": "+e.Detail)
	}
	return out
}

func location(e problem.Error) string {
	if where := strings.TrimSpace(strings.Join([]string{e.In, e.Parameter, e.Pointer}, " ")); where != "" {
		return where
	}
	return "request"
}

// AuthError is the error Authorize returns: Problem is the response (401,
// or a problem a SecurityHandler returned, such as 403) and Err why the
// credentials were rejected, if a check said so. It wraps Problem, so
// errors.As finds a *problem.Problem in it. Err is logged, never sent.
type AuthError struct {
	Problem *problem.Problem
	Err     error
}

func (e *AuthError) Error() string { return e.Problem.Error() }

func (e *AuthError) Unwrap() error { return e.Problem }
