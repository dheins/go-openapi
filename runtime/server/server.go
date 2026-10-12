// Package server is the runtime of generated servers: configuration,
// request binding, response writing and error handling. Applications use
// it through the options passed to a generated NewServer.
package server

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"strings"

	"github.com/dheins/go-openapi/runtime/problem"
)

// Mux routes requests. *http.ServeMux implements it; another router can
// be used if it accepts ServeMux patterns ("GET /pets/{p0}") and sets path
// values with Request.SetPathValue.
type Mux interface {
	http.Handler
	Handle(pattern string, handler http.Handler)
}

// Config holds the settings of a generated server.
type Config struct {
	// Mux receives a route for every operation.
	Mux Mux
	// MaxBodyBytes limits the size of request bodies.
	MaxBodyBytes int64
	// ErrorHandler writes the response for an error returned by a handler
	// or found while binding a request.
	ErrorHandler func(w http.ResponseWriter, r *http.Request, err error)
	// Logger receives errors that are not sent to the client.
	Logger *slog.Logger
	// BasePath prefixes every route, such as "/v1". Generated servers
	// default it to the path of the document's first server URL.
	BasePath string
	// ValidateResponses makes generated servers check response bodies
	// against the document before sending them.
	ValidateResponses bool
	// Middleware wraps the handler of every operation, the first outermost.
	Middleware []Middleware
	// DocumentPath, if set, serves the OpenAPI document there, below the
	// base path.
	DocumentPath string
}

// Operation identifies the operation a handler serves, for middleware.
type Operation struct {
	// ID is the operationId, or "" if the document gives none, and Name
	// the generated method name.
	ID, Name string
	Method   string
	// Path is the path template as written in the document.
	Path string
}

// Middleware wraps the handler of one operation. It runs before the
// request is authorized and bound, so it sees every request routed to
// the operation.
type Middleware func(op Operation, next http.Handler) http.Handler

// DefaultMaxBodyBytes is the default request body limit, 10 MiB.
const DefaultMaxBodyBytes = 10 << 20

// Option changes the Config of a generated server.
type Option func(*Config)

// WithMux routes requests with m instead of a new http.ServeMux.
func WithMux(m Mux) Option {
	return func(c *Config) { c.Mux = m }
}

// WithMaxBodyBytes sets the request body limit; larger bodies are
// answered with 413.
func WithMaxBodyBytes(n int64) Option {
	return func(c *Config) { c.MaxBodyBytes = n }
}

// WithErrorHandler replaces how errors are written.
func WithErrorHandler(h func(w http.ResponseWriter, r *http.Request, err error)) Option {
	return func(c *Config) { c.ErrorHandler = h }
}

// WithBasePath serves every route below prefix, such as "/v1"; "" serves
// them at the root.
func WithBasePath(prefix string) Option {
	return func(c *Config) { c.BasePath = strings.TrimSuffix(prefix, "/") }
}

// WithResponseValidation checks every response body against the document
// before it is sent. An invalid response is not sent; the handler's
// request fails with a logged 500 instead.
func WithResponseValidation() Option {
	return func(c *Config) { c.ValidateResponses = true }
}

// WithMiddleware adds middleware that wraps every operation's handler,
// the first outermost.
func WithMiddleware(mw ...Middleware) Option {
	return func(c *Config) { c.Middleware = append(c.Middleware, mw...) }
}

// WithDocument serves the OpenAPI document (as JSON) at path, such as
// "/openapi.json", below the base path.
func WithDocument(path string) Option {
	return func(c *Config) { c.DocumentPath = path }
}

// WithLogger sets the logger for errors that are not sent to the client.
func WithLogger(l *slog.Logger) Option {
	return func(c *Config) { c.Logger = l }
}

// NewConfig applies opts to the defaults.
func NewConfig(opts ...Option) *Config {
	c := &Config{Mux: http.NewServeMux(), MaxBodyBytes: DefaultMaxBodyBytes, Logger: slog.Default()}
	for _, o := range opts {
		o(c)
	}
	if c.ErrorHandler == nil {
		c.ErrorHandler = c.defaultErrorHandler
	}
	return c
}

// Handle routes op, at route (a ServeMux path pattern) below the base
// path, to h wrapped in the middleware.
func (c *Config) Handle(op Operation, route string, h http.Handler) {
	for i := len(c.Middleware) - 1; i >= 0; i-- {
		h = c.Middleware[i](op, h)
	}
	c.Mux.Handle(op.Method+" "+c.BasePath+route, h)
}

// ServeDocument serves doc, the document as JSON, at DocumentPath if it
// is set.
func (c *Config) ServeDocument(doc []byte) {
	if c.DocumentPath == "" {
		return
	}
	c.Mux.Handle("GET "+c.BasePath+c.DocumentPath, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// The status line is sent; a failed write means the client is gone.
		_, _ = w.Write(doc)
	}))
}

// Handler returns the server's http.Handler. With an *http.ServeMux,
// requests no route matches are answered with problem details: 404, or
// 405 with the Allow header when only the method does not match.
func (c *Config) Handler() http.Handler {
	type finder interface {
		Handler(r *http.Request) (http.Handler, string)
	}
	f, ok := c.Mux.(finder)
	if !ok {
		return c.Mux
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, pattern := f.Handler(r)
		if pattern != "" {
			c.Mux.ServeHTTP(w, r)
			return
		}
		// Let the mux decide (404, 405 or a redirect), then answer
		// 404 and 405 with problem details.
		rec := &recorder{header: http.Header{}}
		h.ServeHTTP(rec, r)
		switch rec.status {
		case http.StatusNotFound:
			c.HandleError(w, r, problem.Newf(http.StatusNotFound, "no route for %s", r.URL.Path))
		case http.StatusMethodNotAllowed:
			w.Header().Set("Allow", rec.header.Get("Allow"))
			c.HandleError(w, r, problem.Newf(http.StatusMethodNotAllowed, "%s is not allowed for %s", r.Method, r.URL.Path))
		default:
			maps.Copy(w.Header(), rec.header)
			w.WriteHeader(rec.status)
			// The status line is sent; a failed write means the client
			// is gone.
			_, _ = w.Write(rec.body.Bytes())
		}
	})
}

// recorder captures what the mux answers for a request without a route.
type recorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(b)
}

func (r *recorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

// HandleError writes the response for err.
func (c *Config) HandleError(w http.ResponseWriter, r *http.Request, err error) {
	c.ErrorHandler(w, r, err)
}

// defaultErrorHandler sends a *problem.Problem as it is. Any other error
// is logged and answered with a bare 500, so internal details do not leak.
func (c *Config) defaultErrorHandler(w http.ResponseWriter, r *http.Request, err error) {
	var p *problem.Problem
	if !errors.As(err, &p) {
		c.Logger.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "error", err)
		p = problem.New(http.StatusInternalServerError, "")
	}
	if werr := problem.Write(w, p); werr != nil {
		c.Logger.ErrorContext(r.Context(), "writing error response", "error", werr)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}

// NotImplemented answers every request with 501, for operations the
// generator could not support yet.
func NotImplemented(reason string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// problem.Write only fails to encode a Problem, which cannot happen
		// for this one.
		_ = problem.Write(w, problem.New(http.StatusNotImplemented, reason))
	})
}

type requestKey struct{}

// RequestContext returns the context passed to handlers, which carries r.
func RequestContext(r *http.Request) context.Context {
	return context.WithValue(r.Context(), requestKey{}, r)
}

// HTTPRequest returns the request a handler is serving, for what the
// document does not describe (TLS state, remote address, ...).
func HTTPRequest(ctx context.Context) (*http.Request, bool) {
	r, ok := ctx.Value(requestKey{}).(*http.Request)
	return r, ok
}
