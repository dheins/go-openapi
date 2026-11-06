package middleware

import (
	"bytes"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dheins/go-openapi/internal/schemas"
	"github.com/dheins/go-openapi/oas"
	"github.com/dheins/go-openapi/runtime/problem"
	"github.com/dheins/go-openapi/runtime/server"
)

// ResponseMode says what WithResponseValidation does with a response
// that does not match the document.
type ResponseMode int

const (
	// Unchecked sends responses as the handler writes them, the default.
	Unchecked ResponseMode = iota
	// LogResponses logs what is wrong with a response, at warning level,
	// and sends it.
	LogResponses
	// RejectResponses logs what is wrong with a response and sends a 500
	// problem instead.
	RejectResponses
)

// WithResponseValidation checks the responses of handlers against the
// document: the status, the media type, a JSON body against its schema
// (write-only properties need not be there) and the required headers.
// Responses are buffered to be checked, until the handler flushes one,
// which sends it as it is from then on, for streams.
func WithResponseValidation(mode ResponseMode) Option {
	return func(s *settings) { s.responses = mode }
}

// response is a response of an operation, as the middleware checks it.
type response struct {
	media   []responseMedia
	headers []string
}

type responseMedia struct {
	mediaType string
	// schema checks a JSON body; nil checks nothing.
	schema *jsonschema.Schema
}

// responses returns the responses of op by status ("200", "4XX",
// "default").
func (b *builder) responses(op *oas.Operation) map[string]*response {
	out := map[string]*response{}
	if op.Responses == nil {
		return out
	}
	for status, r := range op.Responses.Items.All() {
		r = r.Deref()
		if r == nil {
			continue
		}
		resp := &response{}
		for mt, m := range r.Content.All() {
			rm := responseMedia{mediaType: mt}
			if m = m.Deref(); m != nil && m.Schema != nil && isJSON(mt) {
				rm.schema = b.set.Compile(m.Schema)
			}
			resp.media = append(resp.media, rm)
		}
		for name, h := range r.Headers.All() {
			if h = h.Deref(); h != nil && h.Required && !strings.EqualFold(name, "Content-Type") {
				resp.headers = append(resp.headers, name)
			}
		}
		out[strings.ToUpper(status)] = resp
	}
	return out
}

// isJSON reports whether a media type is JSON: application/json, a +json
// suffix or a range of them.
func isJSON(mt string) bool {
	base, _, _ := strings.Cut(strings.ToLower(mt), ";")
	base = strings.TrimSpace(base)
	return base == "application/json" || strings.HasSuffix(base, "+json")
}

// declared returns the response of a status: its own, its range's
// ("4XX"), or the default one.
func declared(responses map[string]*response, status int) (*response, bool) {
	code := strconv.Itoa(status)
	for _, key := range []string{code, code[:1] + "XX", "DEFAULT"} {
		if r, ok := responses[key]; ok {
			return r, true
		}
	}
	return nil, false
}

// checkResponse hands the request to next, buffering what it writes, and
// sends the response if it matches the document; otherwise it logs why,
// and with RejectResponses sends a 500 problem instead.
func (op *operation) checkResponse(cfg *server.Config, set *schemas.Set, mode ResponseMode, w http.ResponseWriter, r *http.Request, next http.Handler) {
	bw := &bufferedWriter{w: w, header: http.Header{}}
	next.ServeHTTP(bw, r)
	if bw.streaming {
		return
	}
	if bw.status == 0 {
		bw.status = http.StatusOK
	}
	problems := op.responseProblems(set, bw.status, bw.header, bw.body.Bytes())
	if len(problems) > 0 {
		cfg.Log(r.Context(), slog.LevelWarn, "the response does not match the document",
			"method", r.Method, "path", r.URL.Path, "status", bw.status, "problems", strings.Join(problems, "; "))
		if mode == RejectResponses {
			cfg.HandleError(w, r, problem.New(http.StatusInternalServerError, ""))
			return
		}
	}
	bw.send()
}

// responseProblems returns what is wrong with a response of op.
func (op *operation) responseProblems(set *schemas.Set, status int, header http.Header, body []byte) []string {
	resp, ok := declared(op.responses, status)
	if !ok {
		return []string{fmt.Sprintf("status %d is not declared", status)}
	}
	var problems []string
	for _, name := range resp.headers {
		if header.Get(name) == "" {
			problems = append(problems, fmt.Sprintf("header %s is required", name))
		}
	}
	if len(resp.media) == 0 {
		if len(body) > 0 {
			problems = append(problems, "the response declares no content")
		}
		return problems
	}
	if len(body) == 0 && header.Get("Content-Type") == "" {
		return problems
	}
	mt, _, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil {
		return append(problems, fmt.Sprintf("content type %q is not a media type", header.Get("Content-Type")))
	}
	for _, m := range resp.media {
		if !server.MatchMediaType(mt, m.mediaType) {
			continue
		}
		if m.schema != nil {
			if _, err := check(set, m.schema, body, schemas.Response); err != nil {
				problems = append(problems, "body: "+err.Error())
			}
		}
		return problems
	}
	return append(problems, fmt.Sprintf("content type %s is not declared for status %d", mt, status))
}

// bufferedWriter holds a response until it is checked, or until the
// handler flushes it, which streams it from then on.
type bufferedWriter struct {
	w         http.ResponseWriter
	header    http.Header
	status    int
	body      bytes.Buffer
	streaming bool
}

func (b *bufferedWriter) Header() http.Header {
	if b.streaming {
		return b.w.Header()
	}
	return b.header
}

func (b *bufferedWriter) WriteHeader(status int) {
	switch {
	case b.streaming:
		b.w.WriteHeader(status)
	case b.status == 0 && status >= 200:
		b.status = status
	}
}

func (b *bufferedWriter) Write(p []byte) (int, error) {
	if b.streaming {
		return b.w.Write(p)
	}
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(p)
}

// Flush sends what is buffered and streams the rest unchecked.
func (b *bufferedWriter) Flush() {
	if !b.streaming {
		if b.status == 0 {
			b.status = http.StatusOK
		}
		b.send()
		b.streaming = true
	}
	if f, ok := b.w.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap gives http.ResponseController access to the underlying writer.
func (b *bufferedWriter) Unwrap() http.ResponseWriter { return b.w }

// send writes the buffered response.
func (b *bufferedWriter) send() {
	for k, v := range b.header {
		b.w.Header()[k] = v
	}
	b.w.WriteHeader(b.status)
	// The status line is sent; a failed write means the client is gone.
	_, _ = b.w.Write(b.body.Bytes())
}
