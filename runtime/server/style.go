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
	// AllowEmptyValue accepts an empty query value (?q=) as the empty
	// string, or for an array as no items. EmptyIsAbsent accepts it as if
	// the parameter were absent, for types an empty string is no value of
	// (numbers, booleans, objects). Otherwise an empty value is an error.
	AllowEmptyValue bool
	EmptyIsAbsent   bool
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
			// filter[author][name] is the field author[name].
			if rest, ok := strings.CutPrefix(key, prefix); ok {
				if prop, nested, ok := strings.Cut(rest, "]"); ok && prop != "" {
					fields[prop+nested] = vals
				}
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
		switch {
		case p.AllowEmptyValue && p.Shape == Array:
			return Values{}, true, nil
		case p.AllowEmptyValue:
			return Values{Items: []string{""}}, true, nil
		case p.EmptyIsAbsent:
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
func DecodeFields[T any](fields map[string][]string, obj Property) (T, error) {
	var v T
	data, err := ObjectJSON(fields, obj)
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
// JSON Schema type name ("string", "integer", "number", "boolean",
// "object", "file", or "any" for a value of any type), and whether the
// value is an array of it. An object lists its Properties and the type
// of any others (Additional; strings if nil). For a multipart part, it
// also says which content types the part may declare (any if empty) and
// which headers it must carry, from its encoding.
//
// A property whose Ref is set has the shape of *Ref: its Type,
// Properties and Additional. It is how a recursive type refers to itself;
// Array, ContentTypes and Headers stay the property's own.
type Property struct {
	Type         string
	Array        bool
	Properties   map[string]Property
	Additional   *Property
	ContentTypes []string
	Headers      []string
	Ref          *Property
}

// shape returns p with the shape of its Ref, if it has one.
func (p Property) shape() Property {
	if p.Ref == nil {
		return p
	}
	s := p.Ref.shape()
	s.Array, s.ContentTypes, s.Headers = p.Array, p.ContentTypes, p.Headers
	return s
}

// ObjectJSON converts the fields of an object parameter or form body to a
// JSON object, using the property types of obj to decide which values are
// numbers and booleans. A value that does not parse as its type is kept
// as a string, so decoding reports it as the wrong type.
//
// Nested objects are written with brackets, as in deepObject parameters:
// "a[b][c]=v". An array may be written "a=1&a=2", "a[]=1&a[]=2" or
// "a[0]=1&a[1]=2", and an array of objects with indices:
// "a[0][b]=1&a[1][b]=2". An object given as a single value is read as JSON,
// the default content type of objects in form and multipart bodies. A
// field named exactly like a declared property is that property, even if
// it contains brackets.
func ObjectJSON(fields map[string][]string, obj Property) ([]byte, error) {
	obj = obj.shape()
	root := &fieldNode{}
	for key, vals := range fields {
		path := []string{key}
		if _, declared := obj.Properties[key]; !declared {
			path = splitKey(key)
		}
		root.insert(path, vals)
	}
	return root.json("", obj)
}

// fieldNode is a field value, or the fields below a bracketed key.
type fieldNode struct {
	values   []string
	children map[string]*fieldNode
}

func (n *fieldNode) insert(path []string, vals []string) {
	for _, name := range path {
		if n.children == nil {
			n.children = map[string]*fieldNode{}
		}
		child, ok := n.children[name]
		if !ok {
			child = &fieldNode{}
			n.children[name] = child
		}
		n = child
	}
	n.values = append(n.values, vals...)
}

// splitKey splits "a[b][c]" into a, b and c; a trailing "[]" only marks
// an array. A key that is not of this form is a name of its own.
func splitKey(key string) []string {
	name, rest, ok := strings.Cut(key, "[")
	if !ok || name == "" || rest == "" {
		return []string{key}
	}
	path := []string{name}
	for rest != "" {
		seg, after, ok := strings.Cut(rest, "]")
		if !ok || (after != "" && !strings.HasPrefix(after, "[")) || strings.Contains(seg, "[") {
			return []string{key}
		}
		if seg == "" && after != "" {
			// "a[][b]": arrays of objects are not written this way here.
			return []string{key}
		}
		if seg != "" {
			path = append(path, seg)
		}
		rest = strings.TrimPrefix(after, "[")
	}
	return path
}

// json renders n, found at the JSON pointer path, as a value of prop.
func (n *fieldNode) json(path string, prop Property) (json.RawMessage, error) {
	prop = prop.shape()
	if len(n.children) > 0 && len(n.values) > 0 {
		return nil, &oapi.ValidationError{Path: path, Message: "both a value and nested properties"}
	}
	if len(n.children) > 0 {
		if prop.Array {
			return n.indexedJSON(path, prop)
		}
		if prop.Type != "object" && prop.Type != "any" {
			return nil, &oapi.ValidationError{Path: path, Message: "expected a value, got nested properties"}
		}
		return n.objectJSON(path, prop)
	}
	vals := n.values
	if prop.Array {
		items := make([]json.RawMessage, len(vals))
		for i, v := range vals {
			items[i] = scalarJSON(v, prop.Type)
		}
		return json.Marshal(items)
	}
	if len(vals) != 1 {
		return nil, &oapi.ValidationError{Path: path, Message: fmt.Sprintf("expected a single value, got %d", len(vals))}
	}
	return scalarJSON(vals[0], prop.Type), nil
}

func (n *fieldNode) objectJSON(path string, prop Property) (json.RawMessage, error) {
	names := make([]string, 0, len(n.children))
	for name := range n.children {
		names = append(names, name)
	}
	slices.Sort(names)
	obj := make(map[string]json.RawMessage, len(names))
	for _, name := range names {
		child := n.children[name]
		cp, ok := prop.Properties[name]
		switch {
		case ok:
		case prop.Additional != nil:
			cp = *prop.Additional
		case len(child.children) > 0:
			cp = Property{Type: "any"}
		default:
			cp = Property{Type: "string", Array: len(child.values) > 1}
		}
		data, err := child.json(oapi.Field(path, name), cp)
		if err != nil {
			return nil, err
		}
		obj[name] = data
	}
	return json.Marshal(obj)
}

// indexedJSON renders "a[0]=x&a[1]=y", or "a[0][b]=x" for objects, as an
// array in index order; missing indices are skipped.
func (n *fieldNode) indexedJSON(path string, prop Property) (json.RawMessage, error) {
	type item struct {
		index int
		node  *fieldNode
	}
	var items []item
	for key, child := range n.children {
		i, err := strconv.Atoi(key)
		if err != nil || i < 0 || key != strconv.Itoa(i) {
			return nil, &oapi.ValidationError{Path: oapi.Field(path, key), Message: "expected an array item with an index"}
		}
		items = append(items, item{i, child})
	}
	slices.SortFunc(items, func(a, b item) int { return a.index - b.index })
	elem := prop
	elem.Array = false
	out := make([]json.RawMessage, len(items))
	for i, it := range items {
		data, err := it.node.json(oapi.Index(path, i), elem)
		if err != nil {
			return nil, err
		}
		out[i] = data
	}
	return json.Marshal(out)
}

// scalarJSON renders a text value as JSON of the given type, or as a
// string when it does not parse.
func scalarJSON(v, typ string) json.RawMessage {
	switch typ {
	case "integer":
		// Written as parsed: "007" and "+7" are 7, which JSON can hold.
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return strconv.AppendInt(nil, n, 10)
		}
	case "number":
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return json.RawMessage(strconv.FormatFloat(f, 'g', -1, 64))
		}
	case "boolean":
		if v == "true" || v == "false" {
			return json.RawMessage(v)
		}
	case "object":
		if strings.HasPrefix(strings.TrimSpace(v), "{") && json.Valid([]byte(v)) {
			return json.RawMessage(v)
		}
	}
	// Marshalling a string cannot fail.
	data, _ := json.Marshal(v)
	return data
}
