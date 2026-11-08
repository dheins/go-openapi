// Package client is the runtime of generated clients: it builds requests
// from their parameters and bodies, sends them, and reads the responses
// a document declares.
package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Doer sends an HTTP request, as *http.Client does. Middleware wraps one
// to add retries, logging or request signing.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// DoerFunc is a function that is a Doer.
type DoerFunc func(*http.Request) (*http.Response, error)

// Do calls f.
func (f DoerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

// DefaultMaxResponseBytes is how much of a response body is read by
// default; a longer body is an error.
const DefaultMaxResponseBytes = 32 << 20

// Config is what a generated client sends requests with.
type Config struct {
	// BaseURL is prefixed to every operation's path: the server URL,
	// with any base path.
	BaseURL *url.URL
	// OperationServers sends operations with servers of their own
	// (Request.Server) there rather than to BaseURL; see
	// WithOperationServers.
	OperationServers bool
	Doer             Doer
	// Header holds headers sent with every request.
	Header http.Header
	// ValidateRequests checks requests against the document before they
	// are sent; ValidateResponses checks the responses read.
	ValidateRequests, ValidateResponses bool
	// MaxResponseBytes bounds the response bodies read whole.
	MaxResponseBytes int64
	// ReplayLimit is how much of a streamed request body is kept to send
	// it again; see WithReplayLimit.
	ReplayLimit int64
	// Codecs encode and decode the bodies of media types goapi does not
	// know, by server.CodecKey; see WithCodec.
	Codecs map[string]Codec

	middleware []func(Doer) Doer
	// defaultBase says BaseURL is the document's server, from
	// WithDefaultBaseURL; operationServers is WithOperationServers', if
	// given.
	defaultBase      bool
	operationServers *bool
	err              error
}

// Option configures a client.
type Option func(*Config)

// NewConfig applies opts to the defaults: http.DefaultClient, request
// validation, DefaultMaxResponseBytes and DefaultReplayLimit.
func NewConfig(opts ...Option) (*Config, error) {
	c, err := newConfig(opts)
	if err != nil {
		return nil, err
	}
	if c.BaseURL == nil {
		return nil, errors.New("client: no base URL; use WithBaseURL")
	}
	return c, nil
}

// NewSenderConfig is NewConfig for sending webhooks and callbacks, which
// go to the URL given with each request (BuildTo), so it needs no base
// URL.
func NewSenderConfig(opts ...Option) (*Config, error) {
	return newConfig(opts)
}

func newConfig(opts []Option) (*Config, error) {
	c := &Config{Doer: http.DefaultClient, Header: http.Header{}, ValidateRequests: true, MaxResponseBytes: DefaultMaxResponseBytes,
		ReplayLimit: DefaultReplayLimit}
	for _, o := range opts {
		o(c)
	}
	if c.err != nil {
		return nil, c.err
	}
	c.OperationServers = c.defaultBase
	if c.operationServers != nil {
		c.OperationServers = *c.operationServers
	}
	// The first middleware is the outermost.
	for i := len(c.middleware) - 1; i >= 0; i-- {
		c.Doer = c.middleware[i](c.Doer)
	}
	return c, nil
}

// WithBaseURL sets the server URL requests are sent to, such as
// https://api.example.com/v1. All of them go there, even those of
// operations with servers of their own, unless WithOperationServers says
// otherwise.
func WithBaseURL(raw string) Option {
	return func(c *Config) { c.setBaseURL(raw, false) }
}

// WithDefaultBaseURL sets the document's server URL, which generated
// clients start with: operations with servers of their own are sent there
// instead, unless WithOperationServers says otherwise.
func WithDefaultBaseURL(raw string) Option {
	return func(c *Config) { c.setBaseURL(raw, true) }
}

func (c *Config) setBaseURL(raw string, isDefault bool) {
	u, err := url.Parse(raw)
	switch {
	case err != nil:
		c.err = fmt.Errorf("client: base URL: %w", err)
	case !u.IsAbs():
		c.err = fmt.Errorf("client: base URL %q is not absolute", raw)
	default:
		c.BaseURL, c.defaultBase = u, isDefault
	}
}

// WithOperationServers says whether operations with servers of their own
// are sent there (true) or to the base URL (false). By default they are
// while the base URL is the document's (WithDefaultBaseURL), and are not
// once WithBaseURL sets another. A relative operation server is resolved
// against the base URL, so with WithBaseURL and true it is sent to that
// base URL's host.
func WithOperationServers(on bool) Option {
	return func(c *Config) { c.operationServers = &on }
}

// WithHTTPClient sends requests with hc.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Config) { c.Doer = hc }
}

// WithDoer sends requests with d.
func WithDoer(d Doer) Option {
	return func(c *Config) { c.Doer = d }
}

// WithMiddleware wraps the Doer, in order: the first wraps the others.
func WithMiddleware(mw ...func(Doer) Doer) Option {
	return func(c *Config) { c.middleware = append(c.middleware, mw...) }
}

// WithHeader adds a header sent with every request.
func WithHeader(name, value string) Option {
	return func(c *Config) { c.Header.Add(name, value) }
}

// WithoutRequestValidation sends requests without checking them against
// the document first.
func WithoutRequestValidation() Option {
	return func(c *Config) { c.ValidateRequests = false }
}

// WithResponseValidation checks responses against the document; a
// response that does not match is an *InvalidResponse.
func WithResponseValidation() Option {
	return func(c *Config) { c.ValidateResponses = true }
}

// WithMaxResponseBytes bounds the response bodies read whole.
func WithMaxResponseBytes(n int64) Option {
	return func(c *Config) { c.MaxResponseBytes = n }
}

// Do sends req with the configured headers.
func (c *Config) Do(req *http.Request) (*http.Response, error) {
	for name, values := range c.Header {
		if _, set := req.Header[name]; !set {
			req.Header[name] = values
		}
	}
	resp, err := c.Doer.Do(req)
	stream, streamed := req.Body.(*replayReader)
	if err != nil {
		if streamed {
			stream.rec.stop()
		}
		return nil, fmt.Errorf("%s %s: %w", req.Method, req.URL.Redacted(), err)
	}
	if streamed {
		// The stream may go on while the response is read.
		resp.Body = &failedBody{ReadCloser: resp.Body, stream: stream.rec}
	}
	return resp, nil
}

type serverKey struct{}

// WithServer returns ctx with a server for the requests sent with it: an
// absolute URL, or one relative to the base URL. It takes the place of
// operations' own servers, under the same rule (see
// WithOperationServers): a client whose base URL is not the document's
// sends to its base URL. Following a link that names a server uses it.
func WithServer(ctx context.Context, url string) context.Context {
	return context.WithValue(ctx, serverKey{}, url)
}

// ServerFrom returns the server WithServer put in ctx.
func ServerFrom(ctx context.Context) (string, bool) {
	s, ok := ctx.Value(serverKey{}).(string)
	return s, ok
}

// Build turns r into an HTTP request to the base URL or, when
// OperationServers is set, to the server of ctx (WithServer) or of its
// operation (Request.Server).
func (c *Config) Build(ctx context.Context, r *Request) (*http.Request, error) {
	if r.err != nil {
		return nil, r.err
	}
	if c.BaseURL == nil {
		return nil, errors.New("client: no base URL; use WithBaseURL")
	}
	u := *c.BaseURL
	server := r.server
	if s, ok := ServerFrom(ctx); ok {
		server = s
	}
	if server != "" && c.OperationServers {
		s, err := url.Parse(server)
		if err != nil {
			return nil, fmt.Errorf("client: operation server: %w", err)
		}
		u = *c.BaseURL.ResolveReference(s)
	}
	// The path's parameter values are escaped already.
	escaped := strings.TrimSuffix(u.EscapedPath(), "/") + r.path.String()
	path, err := url.PathUnescape(escaped)
	if err != nil {
		return nil, fmt.Errorf("client: path %q: %w", escaped, err)
	}
	u.Path, u.RawPath = path, escaped
	if r.rawQuery != "" {
		u.RawQuery = r.rawQuery
	} else {
		u.RawQuery = r.query.Encode()
	}
	return r.httpRequest(ctx, &u, c.ReplayLimit)
}

// BuildTo turns r, a webhook or callback, into an HTTP request to target,
// an absolute URL whose query r's query parameters are added to.
func (c *Config) BuildTo(ctx context.Context, target string, r *Request) (*http.Request, error) {
	if r.err != nil {
		return nil, r.err
	}
	u, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("client: target URL: %w", err)
	}
	if !u.IsAbs() || u.Host == "" {
		return nil, fmt.Errorf("client: target URL %q is not absolute", target)
	}
	switch extra := r.query.Encode(); {
	case r.rawQuery != "" && u.RawQuery != "":
		u.RawQuery += "&" + r.rawQuery
	case r.rawQuery != "":
		u.RawQuery = r.rawQuery
	case extra != "" && u.RawQuery != "":
		u.RawQuery += "&" + extra
	case extra != "":
		u.RawQuery = extra
	}
	return r.httpRequest(ctx, u, c.ReplayLimit)
}

// httpRequest makes the HTTP request of r to u: its body, a stream kept
// up to replayLimit bytes to be sent again, and headers.
func (r *Request) httpRequest(ctx context.Context, u *url.URL, replayLimit int64) (*http.Request, error) {
	var body io.Reader
	var rec *recording
	switch {
	case r.stream != nil:
		rec = newRecording(r.stream, replayLimit)
		body = rec.body()
	case r.body != nil:
		body = bytes.NewReader(r.body)
	}
	req, err := http.NewRequestWithContext(ctx, r.method, u.String(), body)
	if err != nil {
		return nil, err
	}
	if rec != nil {
		if replayLimit > 0 {
			req.GetBody = rec.getBody
		}
		// A call given up on ends its stream's writer too.
		context.AfterFunc(ctx, rec.stop)
	}
	for name, values := range r.header {
		req.Header[name] = values
	}
	if r.contentType != "" {
		req.Header.Set("Content-Type", r.contentType)
	}
	if len(r.accept) > 0 {
		req.Header.Set("Accept", strings.Join(r.accept, ", "))
	}
	for _, ck := range r.cookies {
		req.AddCookie(ck)
	}
	return req, nil
}
