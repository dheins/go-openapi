package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/server"
)

// Request is a request being built by a generated client: its path,
// parameters and body. The first error is kept and returned by
// Config.Build.
type Request struct {
	method   string
	path     strings.Builder
	query    url.Values
	rawQuery string
	header   http.Header
	cookies  []*http.Cookie
	body     []byte
	// stream, if not nil, writes the body as it is sent.
	stream      func(io.Writer) error
	contentType string
	accept      []string
	err         error
}

// NewRequest starts a request with the given method.
func NewRequest(method string) *Request {
	return &Request{method: method, query: url.Values{}, header: http.Header{}}
}

func (r *Request) fail(err error) {
	if r.err == nil {
		r.err = err
	}
}

// Path appends text of the path template, as written.
func (r *Request) Path(text string) { r.path.WriteString(text) }

// Accept sets the media types the response may have.
func (r *Request) Accept(mediaTypes ...string) { r.accept = mediaTypes }

// PathParam appends the value v of path parameter p, in its style
// (simple, label or matrix), escaped.
func (r *Request) PathParam(p server.Param, v any) {
	s, err := pathValue(p, v)
	if err != nil {
		r.fail(fmt.Errorf("path parameter %q: %w", p.Name, err))
		return
	}
	r.path.WriteString(s)
}

func pathValue(p server.Param, v any) (string, error) {
	switch p.Shape {
	case server.Scalar:
		s, err := scalar(v)
		if err != nil {
			return "", err
		}
		s = url.PathEscape(s)
		switch p.Style {
		case "label":
			return "." + s, nil
		case "matrix":
			return ";" + p.Name + "=" + s, nil
		}
		return s, nil
	case server.Array:
		items, err := arrayItems(v)
		if err != nil {
			return "", err
		}
		for i := range items {
			items[i] = url.PathEscape(items[i])
		}
		switch p.Style {
		case "label":
			if p.Explode {
				return "." + strings.Join(items, "."), nil
			}
			return "." + strings.Join(items, ","), nil
		case "matrix":
			if p.Explode {
				var b strings.Builder
				for _, it := range items {
					b.WriteString(";" + p.Name + "=" + it)
				}
				return b.String(), nil
			}
			return ";" + p.Name + "=" + strings.Join(items, ","), nil
		}
		return strings.Join(items, ","), nil
	}
	fields, err := server.ObjectFields(v, false, oapi.Request)
	if err != nil {
		return "", err
	}
	pairs := make([]string, 0, 2*len(fields))
	var exploded []string
	for _, f := range fields {
		val := url.PathEscape(strings.Join(f.Values, ","))
		pairs = append(pairs, url.PathEscape(f.Name()), val)
		exploded = append(exploded, url.PathEscape(f.Name())+"="+val)
	}
	switch p.Style {
	case "label":
		if p.Explode {
			return "." + strings.Join(exploded, "."), nil
		}
		return "." + strings.Join(pairs, ","), nil
	case "matrix":
		if p.Explode {
			return ";" + strings.Join(exploded, ";"), nil
		}
		return ";" + p.Name + "=" + strings.Join(pairs, ","), nil
	}
	if p.Explode {
		return strings.Join(exploded, ","), nil
	}
	return strings.Join(pairs, ","), nil
}

// QueryParam adds the value v of query parameter p, in its style (form,
// spaceDelimited, pipeDelimited or deepObject).
func (r *Request) QueryParam(p server.Param, v any) {
	if err := queryValue(r.query, p, v); err != nil {
		r.fail(fmt.Errorf("query parameter %q: %w", p.Name, err))
	}
}

func queryValue(q url.Values, p server.Param, v any) error {
	switch p.Shape {
	case server.Scalar:
		s, err := scalar(v)
		if err != nil {
			return err
		}
		q.Add(p.Name, s)
		return nil
	case server.Array:
		items, err := arrayItems(v)
		if err != nil {
			return err
		}
		switch {
		case p.Style == "spaceDelimited":
			q.Add(p.Name, strings.Join(items, " "))
		case p.Style == "pipeDelimited":
			q.Add(p.Name, strings.Join(items, "|"))
		case p.Explode:
			for _, it := range items {
				q.Add(p.Name, it)
			}
		default:
			q.Add(p.Name, strings.Join(items, ","))
		}
		return nil
	}
	if p.Style == "deepObject" {
		fields, err := server.ObjectFields(v, true, oapi.Request)
		if err != nil {
			return err
		}
		for _, f := range fields {
			key := p.Name
			for _, seg := range f.Path {
				key += "[" + seg + "]"
			}
			for _, val := range f.Values {
				q.Add(key, val)
			}
		}
		return nil
	}
	fields, err := server.ObjectFields(v, false, oapi.Request)
	if err != nil {
		return err
	}
	if p.Explode {
		// Each property is a parameter of its own.
		for _, f := range fields {
			for _, val := range f.Values {
				q.Add(f.Name(), val)
			}
		}
		return nil
	}
	var pairs []string
	for _, f := range fields {
		pairs = append(pairs, f.Name(), strings.Join(f.Values, ","))
	}
	q.Add(p.Name, strings.Join(pairs, ","))
	return nil
}

// HeaderParam sets the value v of header parameter p, in the simple style.
// List items that hold a comma or a quote, or begin or end with space,
// are quoted strings, as HTTP header lists have them.
func (r *Request) HeaderParam(p server.Param, v any) {
	s, err := simpleValue(p, v, quoteListItem)
	if err != nil {
		r.fail(fmt.Errorf("header %q: %w", p.Name, err))
		return
	}
	r.header.Set(p.Name, s)
}

// simpleValue formats v in the simple style, with the list items of an
// array or object passed through item.
func simpleValue(p server.Param, v any, item func(string) string) (string, error) {
	switch p.Shape {
	case server.Scalar:
		return scalar(v)
	case server.Array:
		items, err := arrayItems(v)
		for i := range items {
			items[i] = item(items[i])
		}
		return strings.Join(items, ","), err
	}
	fields, err := server.ObjectFields(v, false, oapi.Request)
	if err != nil {
		return "", err
	}
	var parts []string
	for _, f := range fields {
		val := strings.Join(f.Values, ",")
		if p.Explode {
			parts = append(parts, item(f.Name()+"="+val))
		} else {
			parts = append(parts, item(f.Name()), item(val))
		}
	}
	return strings.Join(parts, ","), nil
}

// quoteListItem returns an item of a header list as a quoted string when
// it would otherwise not read back as one item.
func quoteListItem(s string) string {
	if !strings.ContainsAny(s, `",`) && strings.TrimSpace(s) == s {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func plainItem(s string) string { return s }

// CookieParam adds the value v of cookie parameter p, in the form style:
// an exploded array repeats the cookie, an exploded object sends one
// cookie per property.
func (r *Request) CookieParam(p server.Param, v any) {
	add := func(name, value string) { r.cookies = append(r.cookies, &http.Cookie{Name: name, Value: value}) }
	switch {
	case p.Shape == server.Array && p.Explode:
		items, err := arrayItems(v)
		if err != nil {
			r.fail(fmt.Errorf("cookie %q: %w", p.Name, err))
			return
		}
		for _, it := range items {
			add(p.Name, it)
		}
	case p.Shape == server.Object && p.Explode:
		fields, err := server.ObjectFields(v, false, oapi.Request)
		if err != nil {
			r.fail(fmt.Errorf("cookie %q: %w", p.Name, err))
			return
		}
		for _, f := range fields {
			add(f.Name(), strings.Join(f.Values, ","))
		}
	default:
		s, err := simpleValue(server.Param{Name: p.Name, Shape: p.Shape}, v, plainItem)
		if err != nil {
			r.fail(fmt.Errorf("cookie %q: %w", p.Name, err))
			return
		}
		add(p.Name, s)
	}
}

// ContentParam sets a parameter serialized as JSON (content), in the
// query, a header or a cookie, or appends it to the path.
func (r *Request) ContentParam(in, name string, v any) {
	data, err := oapi.EncodeRequest(v)
	if err != nil {
		r.fail(fmt.Errorf("%s parameter %q: %w", in, name, err))
		return
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		r.fail(fmt.Errorf("%s parameter %q: %w", in, name, err))
		return
	}
	r.setContent(in, name, compact.String())
}

// TextContentParam sets a parameter serialized as text/plain: v, a
// primitive value, as its text.
func (r *Request) TextContentParam(in, name string, v any) {
	text, err := server.FormatScalar(v)
	if err != nil {
		r.fail(fmt.Errorf("%s parameter %q: %w", in, name, err))
		return
	}
	r.setContent(in, name, text)
}

// FormContentParam sets a parameter serialized as
// application/x-www-form-urlencoded: v, an object, as form fields.
func (r *Request) FormContentParam(in, name string, v any) {
	text, err := server.EncodeFormContent(v, oapi.Request)
	if err != nil {
		r.fail(fmt.Errorf("%s parameter %q: %w", in, name, err))
		return
	}
	r.setContent(in, name, text)
}

// XMLContentParam sets a parameter serialized as the XML document root
// describes.
func (r *Request) XMLContentParam(in, name string, v any, root *server.XMLNode) {
	text, err := server.EncodeXMLContent(v, root, oapi.Request)
	if err != nil {
		r.fail(fmt.Errorf("%s parameter %q: %w", in, name, err))
		return
	}
	r.setContent(in, name, text)
}

// setContent sets the text of a parameter serialized with a media type.
func (r *Request) setContent(in, name, text string) {
	switch in {
	case "path":
		r.path.WriteString(url.PathEscape(text))
	case "query":
		r.query.Set(name, text)
	case "header":
		r.header.Set(name, text)
	case "cookie":
		r.cookies = append(r.cookies, &http.Cookie{Name: name, Value: cookieEscape(text)})
	}
}

// cookieEscape percent-encodes the bytes a cookie value cannot hold
// (quotes, separators, spaces, controls, non-ASCII) and %, which servers
// decode.
func cookieEscape(s string) string {
	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		if c <= ' ' || c >= 0x7f || c == '"' || c == ',' || c == ';' || c == '\\' || c == '%' {
			fmt.Fprintf(&b, "%%%02X", c)
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// QueryFields sets the whole query string from v, an object serialized as
// a form (a querystring parameter).
func (r *Request) QueryFields(v any) {
	fields, err := server.ObjectFields(v, true, oapi.Request)
	if err != nil {
		r.fail(fmt.Errorf("querystring: %w", err))
		return
	}
	for _, f := range fields {
		for _, val := range f.Values {
			r.query.Add(f.Name(), val)
		}
	}
}

// QueryString sets the whole query string to v serialized as JSON (a
// querystring parameter with JSON content).
func (r *Request) QueryString(v any) {
	data, err := oapi.EncodeRequest(v)
	if err != nil {
		r.fail(fmt.Errorf("querystring: %w", err))
		return
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		r.fail(fmt.Errorf("querystring: %w", err))
		return
	}
	r.rawQuery = url.QueryEscape(compact.String())
}

// IsZero reports whether v is the zero value of its type: for a parameter
// with a default, that it is not given, so the server applies the default.
func IsZero(v any) bool {
	return v == nil || reflect.ValueOf(v).IsZero()
}

// scalar formats a parameter value as text.
func scalar(v any) (string, error) {
	return server.FormatScalar(v)
}

// arrayItems formats the items of a slice.
func arrayItems(v any) ([]string, error) {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil, fmt.Errorf("expected an array, got %T", v)
	}
	items := make([]string, rv.Len())
	for i := range rv.Len() {
		s, err := scalar(rv.Index(i).Interface())
		if err != nil {
			return nil, err
		}
		items[i] = s
	}
	return items, nil
}
