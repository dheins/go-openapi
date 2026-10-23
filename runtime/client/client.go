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
	Doer    Doer
	// Header holds headers sent with every request.
	Header http.Header
	// ValidateRequests checks requests against the document before they
	// are sent; ValidateResponses checks the responses read.
	ValidateRequests, ValidateResponses bool
	// MaxResponseBytes bounds the response bodies read whole.
	MaxResponseBytes int64

	middleware []func(Doer) Doer
	err        error
}

// Option configures a client.
type Option func(*Config)

// NewConfig applies opts to the defaults: http.DefaultClient, request
// validation, and DefaultMaxResponseBytes.
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
	c := &Config{Doer: http.DefaultClient, Header: http.Header{}, ValidateRequests: true, MaxResponseBytes: DefaultMaxResponseBytes}
	for _, o := range opts {
		o(c)
	}
	if c.err != nil {
		return nil, c.err
	}
	// The first middleware is the outermost.
	for i := len(c.middleware) - 1; i >= 0; i-- {
		c.Doer = c.middleware[i](c.Doer)
	}
	return c, nil
}

// WithBaseURL sets the server URL requests are sent to, such as
// https://api.example.com/v1.
func WithBaseURL(raw string) Option {
	return func(c *Config) {
		u, err := url.Parse(raw)
		switch {
		case err != nil:
			c.err = fmt.Errorf("client: base URL: %w", err)
		case !u.IsAbs():
			c.err = fmt.Errorf("client: base URL %q is not absolute", raw)
		default:
			c.BaseURL = u
		}
	}
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
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", req.Method, req.URL.Redacted(), err)
	}
	return resp, nil
}

// Build turns r into an HTTP request to the base URL.
func (c *Config) Build(ctx context.Context, r *Request) (*http.Request, error) {
	if r.err != nil {
		return nil, r.err
	}
	if c.BaseURL == nil {
		return nil, errors.New("client: no base URL; use WithBaseURL")
	}
	u := *c.BaseURL
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
	return r.httpRequest(ctx, &u)
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
	return r.httpRequest(ctx, u)
}

// httpRequest makes the HTTP request of r to u: its body and headers.
func (r *Request) httpRequest(ctx context.Context, u *url.URL) (*http.Request, error) {
	var body io.Reader
	if r.body != nil {
		body = bytes.NewReader(r.body)
	}
	req, err := http.NewRequestWithContext(ctx, r.method, u.String(), body)
	if err != nil {
		return nil, err
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
