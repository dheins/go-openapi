package server

import (
	"encoding"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/problem"
)

// Binder collects the problems found while binding a request, so that a
// client learns about all of them at once.
type Binder struct {
	errs []problem.Error
	// fatal is a problem that replaces the 400 response, such as 415 for
	// an unsupported content type.
	fatal *problem.Problem
}

// Param records err, a problem with parameter name in in (query, path,
// header or cookie). A nil err records nothing.
func (b *Binder) Param(in, name string, err error) {
	b.add(in, name, err)
}

// Missing records that a required parameter is absent.
func (b *Binder) Missing(in, name string) {
	b.errs = append(b.errs, problem.Error{In: in, Parameter: name, Detail: "required parameter is missing"})
}

// Body records err, a problem with the request body. A nil err records
// nothing.
func (b *Binder) Body(err error) {
	b.add("body", "", err)
}

func (b *Binder) add(in, name string, err error) {
	if err == nil {
		return
	}
	var list oapi.ValidationErrors
	var single *oapi.ValidationError
	switch {
	case errors.As(err, &list):
		for _, e := range list {
			b.errs = append(b.errs, problem.Error{In: in, Parameter: name, Pointer: e.Path, Detail: e.Message})
		}
	case errors.As(err, &single):
		b.errs = append(b.errs, problem.Error{In: in, Parameter: name, Pointer: single.Path, Detail: single.Message})
	default:
		b.errs = append(b.errs, problem.Error{In: in, Parameter: name, Detail: err.Error()})
	}
}

// Failed reports whether binding found a problem.
func (b *Binder) Failed() bool {
	return b.fatal != nil || len(b.errs) > 0
}

// Problem returns the response for a failed binding.
func (b *Binder) Problem() *problem.Problem {
	if b.fatal != nil {
		return b.fatal
	}
	p := problem.New(http.StatusBadRequest, "the request is invalid")
	p.Errors = b.errs
	return p
}

// splitHeaderList splits a header value at commas outside double-quoted
// strings (RFC 9110, section 5.6.1), trimming whitespace and unquoting
// items that are quoted strings.
func splitHeaderList(v string) []string {
	var items []string
	var cur strings.Builder
	quoted, escaped := false, false
	flush := func() {
		item := strings.TrimSpace(cur.String())
		if len(item) >= 2 && item[0] == '"' && item[len(item)-1] == '"' {
			if unq, err := strconv.Unquote(item); err == nil {
				item = unq
			}
		}
		items = append(items, item)
		cur.Reset()
	}
	for _, r := range v {
		switch {
		case escaped:
			escaped = false
		case quoted && r == '\\':
			escaped = true
		case r == '"':
			quoted = !quoted
		case r == ',' && !quoted:
			flush()
			continue
		}
		cur.WriteRune(r)
	}
	flush()
	return items
}

// ParseScalar parses the single value of a parameter as T: a string,
// integer, number or boolean kind, or a type that implements
// encoding.TextUnmarshaler (time.Time, oapi.Date).
func ParseScalar[T any](vals []string) (T, error) {
	var v T
	if len(vals) != 1 {
		return v, &oapi.ValidationError{Message: fmt.Sprintf("expected a single value, got %d", len(vals))}
	}
	err := parseInto(vals[0], &v)
	return v, err
}

// ParseArray parses every value of a parameter as T; problems carry the
// index of the item.
func ParseArray[T any](vals []string) ([]T, error) {
	out := make([]T, len(vals))
	var errs oapi.ValidationErrors
	for i, s := range vals {
		errs.Nested(oapi.Index("", i), parseInto(s, &out[i]))
	}
	return out, errs.Err()
}

func parseInto(s string, dst any) error {
	if u, ok := dst.(encoding.TextUnmarshaler); ok {
		if err := u.UnmarshalText([]byte(s)); err != nil {
			return &oapi.ValidationError{Message: err.Error()}
		}
		return nil
	}
	v := reflect.ValueOf(dst).Elem()
	switch v.Kind() {
	case reflect.String:
		v.SetString(s)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(s, 10, v.Type().Bits())
		if err != nil {
			return &oapi.ValidationError{Message: fmt.Sprintf("expected integer, got %q", s)}
		}
		v.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(s, 10, v.Type().Bits())
		if err != nil {
			return &oapi.ValidationError{Message: fmt.Sprintf("expected non-negative integer, got %q", s)}
		}
		v.SetUint(n)
	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseFloat(s, v.Type().Bits())
		if err != nil {
			return &oapi.ValidationError{Message: fmt.Sprintf("expected number, got %q", s)}
		}
		v.SetFloat(n)
	case reflect.Bool:
		switch s {
		case "true":
			v.SetBool(true)
		case "false":
			v.SetBool(false)
		default:
			return &oapi.ValidationError{Message: fmt.Sprintf("expected true or false, got %q", s)}
		}
	default:
		return fmt.Errorf("server: cannot parse a parameter into %s", v.Type())
	}
	return nil
}

// ReadBody reads the request body of an operation accepting mediaTypes
// (patterns such as "application/json" or "image/*"). It returns the body
// and the pattern the request's content type matched (the first one if
// the request names none), and true when there is a body to decode.
// Otherwise it records why on b: a missing required body (400), an
// unsupported content type (415) or a body over the size limit (413). An
// optional body that is absent returns false without a problem.
func (c *Config) ReadBody(w http.ResponseWriter, r *http.Request, b *Binder, mediaTypes []string, required bool) ([]byte, string, bool) {
	var body []byte
	if r.Body != nil {
		var err error
		body, err = io.ReadAll(http.MaxBytesReader(w, r.Body, c.MaxBodyBytes))
		var tooLarge *http.MaxBytesError
		switch {
		case errors.As(err, &tooLarge):
			b.fatal = problem.Newf(http.StatusRequestEntityTooLarge, "the request body is larger than %d bytes", tooLarge.Limit)
			return nil, "", false
		case err != nil:
			b.fatal = problem.New(http.StatusBadRequest, "the request body could not be read")
			return nil, "", false
		}
	}
	if len(body) == 0 {
		if required {
			b.errs = append(b.errs, problem.Error{In: "body", Detail: "request body is required"})
		}
		return nil, "", false
	}
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		return body, mediaTypes[0], true
	}
	mt, _, err := mime.ParseMediaType(ct)
	if err == nil {
		for _, pattern := range mediaTypes {
			if matchMediaType(mt, []string{pattern}) {
				return body, pattern, true
			}
		}
	}
	b.fatal = problem.Newf(http.StatusUnsupportedMediaType, "content type %q is not supported; use %s", ct, strings.Join(mediaTypes, " or "))
	return nil, "", false
}

// matchMediaType reports whether mt matches one of the patterns, which
// may use wildcards ("application/*", "*/*", "application/*+json").
func matchMediaType(mt string, patterns []string) bool {
	mt = strings.ToLower(mt)
	typ, sub, _ := strings.Cut(mt, "/")
	for _, p := range patterns {
		pt, ps, _ := strings.Cut(strings.ToLower(p), "/")
		if pt != "*" && pt != typ {
			continue
		}
		if ps == "*" || ps == sub {
			return true
		}
		if suffix, ok := strings.CutPrefix(ps, "*"); ok && strings.HasSuffix(sub, suffix) {
			return true
		}
	}
	return false
}
