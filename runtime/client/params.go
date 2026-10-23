package client

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
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
	method      string
	path        strings.Builder
	query       url.Values
	rawQuery    string
	header      http.Header
	cookies     []*http.Cookie
	body        []byte
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
	fields, err := objectFields(v, false)
	if err != nil {
		return "", err
	}
	pairs := make([]string, 0, 2*len(fields))
	var exploded []string
	for _, f := range fields {
		val := url.PathEscape(strings.Join(f.values, ","))
		pairs = append(pairs, url.PathEscape(f.name()), val)
		exploded = append(exploded, url.PathEscape(f.name())+"="+val)
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
		fields, err := objectFields(v, true)
		if err != nil {
			return err
		}
		for _, f := range fields {
			key := p.Name
			for _, seg := range f.path {
				key += "[" + seg + "]"
			}
			for _, val := range f.values {
				q.Add(key, val)
			}
		}
		return nil
	}
	fields, err := objectFields(v, false)
	if err != nil {
		return err
	}
	if p.Explode {
		// Each property is a parameter of its own.
		for _, f := range fields {
			for _, val := range f.values {
				q.Add(f.name(), val)
			}
		}
		return nil
	}
	var pairs []string
	for _, f := range fields {
		pairs = append(pairs, f.name(), strings.Join(f.values, ","))
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
	fields, err := objectFields(v, false)
	if err != nil {
		return "", err
	}
	var parts []string
	for _, f := range fields {
		val := strings.Join(f.values, ",")
		if p.Explode {
			parts = append(parts, item(f.name()+"="+val))
		} else {
			parts = append(parts, item(f.name()), item(val))
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
		fields, err := objectFields(v, false)
		if err != nil {
			r.fail(fmt.Errorf("cookie %q: %w", p.Name, err))
			return
		}
		for _, f := range fields {
			add(f.name(), strings.Join(f.values, ","))
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
// query, a header or a cookie.
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
	switch in {
	case "query":
		r.query.Set(name, compact.String())
	case "header":
		r.header.Set(name, compact.String())
	case "cookie":
		r.cookies = append(r.cookies, &http.Cookie{Name: name, Value: compact.String(), Quoted: true})
	}
}

// QueryFields sets the whole query string from v, an object serialized as
// a form (a querystring parameter).
func (r *Request) QueryFields(v any) {
	fields, err := objectFields(v, true)
	if err != nil {
		r.fail(fmt.Errorf("querystring: %w", err))
		return
	}
	for _, f := range fields {
		for _, val := range f.values {
			r.query.Add(f.name(), val)
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

// field is a property of an object written as fields: its path (one name,
// or more for a nested property) and its values (several for an array).
type field struct {
	path   []string
	values []string
}

// name returns the field's name in a form: a[b][c].
func (f field) name() string {
	name := f.path[0]
	for _, seg := range f.path[1:] {
		name += "[" + seg + "]"
	}
	return name
}

// objectFields returns the properties of v, encoded as JSON in the request
// direction, as fields in their order. With nested, objects inside are
// fields of their own (a[b]); otherwise they, like arrays of objects, are
// JSON text, which servers read as such.
func objectFields(v any, nested bool) ([]field, error) {
	data, err := oapi.EncodeRequest(v)
	if err != nil {
		return nil, err
	}
	dec := jsontext.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.ReadToken(); err != nil || tok.Kind() != '{' {
		return nil, fmt.Errorf("expected an object, got %s", data)
	}
	var fields []field
	if err := readFields(dec, nil, nested, &fields); err != nil {
		return nil, err
	}
	return fields, nil
}

// readFields reads the members of an object, whose opening brace is read,
// into fields under prefix.
func readFields(dec *jsontext.Decoder, prefix []string, nested bool, fields *[]field) error {
	for dec.PeekKind() != '}' {
		tok, err := dec.ReadToken()
		if err != nil {
			return err
		}
		path := append(append([]string{}, prefix...), tok.String())
		switch kind := dec.PeekKind(); {
		case kind == '{' && nested:
			if _, err := dec.ReadToken(); err != nil {
				return err
			}
			if err := readFields(dec, path, nested, fields); err != nil {
				return err
			}
		case kind == '[':
			raw, err := dec.ReadValue()
			if err != nil {
				return err
			}
			values, err := arrayValues(raw)
			if err != nil {
				return err
			}
			*fields = append(*fields, field{path: path, values: values})
		case kind == 'n':
			// An absent value: null is not sent as a field.
			if err := dec.SkipValue(); err != nil {
				return err
			}
		default:
			raw, err := dec.ReadValue()
			if err != nil {
				return err
			}
			*fields = append(*fields, field{path: path, values: []string{textOf(raw)}})
		}
	}
	_, err := dec.ReadToken()
	return err
}

// arrayValues returns the items of a JSON array as text: scalars as
// themselves, objects and arrays as JSON.
func arrayValues(raw []byte) ([]string, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	values := make([]string, len(items))
	for i, it := range items {
		values[i] = textOf(it)
	}
	return values, nil
}

// textOf returns a JSON value as text: a string unquoted, anything else as
// written.
func textOf(raw []byte) string {
	if len(raw) > 0 && raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
	}
	return string(raw)
}
