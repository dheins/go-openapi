package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/dheins/go-openapi/oapi"
)

// Shape is the shape of a parameter value: a single value, an array, or
// an object with named properties.
type Shape int

const (
	Scalar Shape = iota
	Array
	Object
)

// Param describes how a parameter is serialized (OpenAPI style values).
type Param struct {
	Name    string
	Style   string
	Explode bool
	Shape   Shape
	// Properties are the property names of an object value, needed where
	// they are not prefixed with the parameter name (query objects in the
	// exploded form style).
	Properties []string
	// AllowEmptyValue accepts an empty query value (?q=), which then
	// counts as absent. Otherwise an empty value is an error.
	AllowEmptyValue bool
}

// Values is a parsed parameter value: Items for a scalar (one item) or an
// array, Fields for an object.
type Values struct {
	Items  []string
	Fields map[string][]string
}

// PathValues parses a path parameter in the simple, label or matrix style
// from the text the route's wildcard matched.
func (p Param) PathValues(raw string) (Values, error) {
	switch p.Style {
	case "label":
		rest, ok := strings.CutPrefix(raw, ".")
		if !ok {
			return Values{}, fmt.Errorf("expected a value starting with \".\"")
		}
		sep := ","
		if p.Explode {
			sep = "."
		}
		return p.delimited(rest, sep, p.Explode)
	case "matrix":
		return p.matrix(raw)
	default:
		return p.delimited(raw, ",", p.Explode)
	}
}

// delimited parses items separated by sep. Objects are "k,v,k,v" or, when
// exploded, "k=v<sep>k=v".
func (p Param) delimited(s, sep string, exploded bool) (Values, error) {
	if p.Shape == Scalar {
		return Values{Items: []string{s}}, nil
	}
	return p.fromParts(split(s, sep), exploded)
}

// fromParts interprets the separated parts of an array or object value.
func (p Param) fromParts(parts []string, exploded bool) (Values, error) {
	if p.Shape == Array {
		return Values{Items: parts}, nil
	}
	fields := map[string][]string{}
	if exploded {
		for _, part := range parts {
			k, v, ok := strings.Cut(part, "=")
			if !ok {
				return Values{}, fmt.Errorf("expected key=value, got %q", part)
			}
			fields[k] = append(fields[k], v)
		}
		return Values{Fields: fields}, nil
	}
	if len(parts)%2 != 0 {
		return Values{}, fmt.Errorf("expected key,value pairs")
	}
	for i := 0; i < len(parts); i += 2 {
		fields[parts[i]] = append(fields[parts[i]], parts[i+1])
	}
	return Values{Fields: fields}, nil
}

// matrix parses ";name=v", ";name=a,b" (";name=a;name=b" exploded) or,
// for objects, ";name=k,v,k,v" (";k=v;k=v" exploded).
func (p Param) matrix(raw string) (Values, error) {
	rest, ok := strings.CutPrefix(raw, ";")
	if !ok {
		return Values{}, fmt.Errorf("expected a value starting with \";\"")
	}
	parts := strings.Split(rest, ";")
	if p.Shape == Object && p.Explode {
		fields := map[string][]string{}
		for _, part := range parts {
			k, v, _ := strings.Cut(part, "=")
			fields[k] = append(fields[k], v)
		}
		return Values{Fields: fields}, nil
	}
	var values []string
	for _, part := range parts {
		k, v, _ := strings.Cut(part, "=")
		if k != p.Name {
			return Values{}, fmt.Errorf("expected %q, got %q", ";"+p.Name+"=", ";"+k)
		}
		values = append(values, v)
	}
	if p.Shape == Array && p.Explode {
		return Values{Items: values}, nil
	}
	if len(values) != 1 {
		return Values{}, fmt.Errorf("expected one value")
	}
	return p.delimited(values[0], ",", false)
}

// QueryValues parses a query parameter in the form, spaceDelimited,
// pipeDelimited or deepObject style, and reports whether it is present.
func (p Param) QueryValues(q url.Values) (Values, bool, error) {
	switch {
	case p.Style == "deepObject":
		fields := map[string][]string{}
		prefix := p.Name + "["
		for key, vals := range q {
			if prop, ok := strings.CutPrefix(key, prefix); ok && strings.HasSuffix(prop, "]") {
				fields[strings.TrimSuffix(prop, "]")] = vals
			}
		}
		return Values{Fields: fields}, len(fields) > 0, nil
	case p.Shape == Object && p.Explode:
		// Each property is a query parameter of its own.
		fields := map[string][]string{}
		for _, prop := range p.Properties {
			if vals, ok := q[prop]; ok {
				fields[prop] = vals
			}
		}
		return Values{Fields: fields}, len(fields) > 0, nil
	}
	vals, ok := q[p.Name]
	if !ok {
		return Values{}, false, nil
	}
	if len(vals) == 1 && vals[0] == "" {
		if p.AllowEmptyValue {
			return Values{}, false, nil
		}
		return Values{}, true, fmt.Errorf("must not be empty")
	}
	if p.Shape == Array && p.Explode {
		return Values{Items: vals}, true, nil
	}
	if len(vals) != 1 {
		return Values{}, true, fmt.Errorf("expected a single value, got %d", len(vals))
	}
	sep := ","
	switch p.Style {
	case "spaceDelimited":
		sep = " "
	case "pipeDelimited":
		sep = "|"
	}
	v, err := p.delimited(vals[0], sep, false)
	return v, true, err
}

// HeaderValues parses a header parameter in the simple style and reports
// whether it is present.
func (p Param) HeaderValues(h http.Header) (Values, bool, error) {
	vals := h.Values(p.Name)
	if len(vals) == 0 {
		return Values{}, false, nil
	}
	if p.Shape == Scalar {
		return Values{Items: []string{strings.Join(vals, ", ")}}, true, nil
	}
	var items []string
	for _, v := range vals {
		items = append(items, splitHeaderList(v)...)
	}
	v, err := p.fromParts(items, p.Explode)
	return v, true, err
}

// CookieValues parses a cookie parameter in the form style and reports
// whether it is present. Exploded arrays repeat the cookie, and exploded
// objects send one cookie per property.
func (p Param) CookieValues(r *http.Request) (Values, bool, error) {
	if p.Shape == Object && p.Explode {
		fields := map[string][]string{}
		for _, prop := range p.Properties {
			for _, c := range r.CookiesNamed(prop) {
				fields[prop] = append(fields[prop], c.Value)
			}
		}
		return Values{Fields: fields}, len(fields) > 0, nil
	}
	cookies := r.CookiesNamed(p.Name)
	if len(cookies) == 0 {
		return Values{}, false, nil
	}
	if p.Shape == Array && p.Explode {
		items := make([]string, len(cookies))
		for i, c := range cookies {
			items[i] = c.Value
		}
		return Values{Items: items}, true, nil
	}
	if len(cookies) != 1 {
		return Values{}, true, fmt.Errorf("expected a single cookie, got %d", len(cookies))
	}
	v, err := p.delimited(cookies[0].Value, ",", false)
	return v, true, err
}

// split splits s at sep; an empty s has no items.
func split(s, sep string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Split(s, sep)
}

// DecodeFields decodes the fields of an object parameter (or the whole
// query string) into T, as a request: see ObjectJSON.
func DecodeFields[T any](fields map[string][]string, props map[string]Property) (T, error) {
	var v T
	data, err := ObjectJSON(fields, props)
	if err != nil {
		return v, err
	}
	err = oapi.DecodeRequest(data, &v)
	return v, err
}

// DecodeContent decodes a parameter serialized as JSON.
func DecodeContent[T any](raw string) (T, error) {
	var v T
	err := oapi.DecodeRequest([]byte(raw), &v)
	return v, err
}

// ContentValue returns the text of a query, header or cookie parameter
// serialized with a media type, and whether it is present.
func ContentValue(r *http.Request, query url.Values, in, name string) (string, bool) {
	switch in {
	case "query":
		if vals, ok := query[name]; ok && len(vals) > 0 {
			return vals[0], true
		}
	case "header":
		if vals := r.Header.Values(name); len(vals) > 0 {
			return strings.Join(vals, ", "), true
		}
	case "cookie":
		if c, err := r.Cookie(name); err == nil {
			return c.Value, true
		}
	}
	return "", false
}

// QueryFields returns every query parameter as the fields of a
// querystring parameter serialized as a form, and whether there are any.
func QueryFields(q url.Values) (Values, bool, error) {
	return Values{Fields: q}, len(q) > 0, nil
}

// QueryString returns the whole query string, percent-decoded, for a
// querystring parameter serialized as JSON, and whether there is one.
func QueryString(r *http.Request) (string, bool, error) {
	if r.URL.RawQuery == "" {
		return "", false, nil
	}
	s, err := url.QueryUnescape(r.URL.RawQuery)
	if err != nil {
		return "", true, fmt.Errorf("invalid query string: %w", err)
	}
	return s, true, nil
}

// Property describes the type of an object property for ObjectJSON: a
// JSON Schema type name, and whether the value is an array of it.
type Property struct {
	Type  string
	Array bool
}

// ObjectJSON converts the fields of an object parameter or form body to a
// JSON object, using the property types to decide which values are
// numbers and booleans. A value that does not parse as its type is kept
// as a string, so decoding reports it as the wrong type. Properties not
// in props are strings.
func ObjectJSON(fields map[string][]string, props map[string]Property) ([]byte, error) {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	slices.Sort(names)
	obj := make(map[string]json.RawMessage, len(fields))
	for _, name := range names {
		vals := fields[name]
		prop, ok := props[name]
		if !ok {
			prop = Property{Type: "string", Array: len(vals) > 1}
		}
		if prop.Array {
			items := make([]json.RawMessage, len(vals))
			for i, v := range vals {
				items[i] = scalarJSON(v, prop.Type)
			}
			data, err := json.Marshal(items)
			if err != nil {
				return nil, err
			}
			obj[name] = data
			continue
		}
		if len(vals) != 1 {
			return nil, &oapi.ValidationError{Path: oapi.Field("", name), Message: fmt.Sprintf("expected a single value, got %d", len(vals))}
		}
		obj[name] = scalarJSON(vals[0], prop.Type)
	}
	return json.Marshal(obj)
}

// scalarJSON renders a text value as JSON of the given type, or as a
// string when it does not parse.
func scalarJSON(v, typ string) json.RawMessage {
	switch typ {
	case "integer":
		if _, err := strconv.ParseInt(v, 10, 64); err == nil {
			return json.RawMessage(v)
		}
	case "number":
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return json.RawMessage(strconv.FormatFloat(f, 'g', -1, 64))
		}
	case "boolean":
		if v == "true" || v == "false" {
			return json.RawMessage(v)
		}
	}
	// Marshalling a string cannot fail.
	data, _ := json.Marshal(v)
	return data
}
