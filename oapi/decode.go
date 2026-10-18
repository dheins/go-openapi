package oapi

import (
	"bytes"
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// Direction says which way a value travels. It decides whether required
// properties marked readOnly or writeOnly must be present: read-only
// properties are only sent in responses, write-only ones only in requests.
type Direction int

const (
	// AnyDirection requires neither read-only nor write-only properties.
	// json.Unmarshal decodes generated types this way.
	AnyDirection Direction = iota
	// Request requires write-only properties but not read-only ones.
	Request
	// Response requires read-only properties but not write-only ones.
	Response
)

// DirectionalDecoder is implemented by generated object types.
type DirectionalDecoder interface {
	DecodeJSON(data []byte, dir Direction) error
}

// Decode decodes a JSON document into dst, a pointer, reporting problems
// as ValidationErrors with JSON pointer paths. Unlike json.Unmarshal it
// keeps the path of a problem inside an array or map, and it rejects null
// where the target type cannot hold it.
func Decode(data []byte, dst any) error {
	return decodeDocument(data, dst, AnyDirection)
}

// DecodeRequest decodes a request body like Decode, without requiring
// read-only properties.
func DecodeRequest(data []byte, dst any) error {
	return decodeDocument(data, dst, Request)
}

// DecodeResponse decodes a response body like Decode, without requiring
// write-only properties.
func DecodeResponse(data []byte, dst any) error {
	return decodeDocument(data, dst, Response)
}

func decodeDocument(data []byte, dst any, dir Direction) error {
	var errs ValidationErrors
	errs.Nested("", DecodeField(data, dst, false, dir))
	return errs.Err()
}

// DecodeObject parses data as a JSON object and returns its properties.
func DecodeObject(data []byte) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, &ValidationError{Message: "expected object, got " + valueType(trimmed)}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return nil, &ValidationError{Message: describe(err)}
	}
	return fields, nil
}

// UnknownFields returns the properties of fields not listed in known, in
// sorted order.
func UnknownFields(fields map[string]json.RawMessage, known ...string) []string {
	var unknown []string
	for _, name := range sortedKeys(fields) {
		if !slices.Contains(known, name) {
			unknown = append(unknown, name)
		}
	}
	return unknown
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// container is implemented by Optional and Nullable so that DecodeField
// can see through them to the value they hold.
type container interface {
	decodeInto(raw json.RawMessage, dir Direction) error
}

func (o *Optional[T]) decodeInto(raw json.RawMessage, dir Direction) error {
	o.Set = true
	return DecodeField(raw, &o.Value, false, dir)
}

func (n *Nullable[T]) decodeInto(raw json.RawMessage, dir Direction) error {
	if isNull(raw) {
		*n = Nullable[T]{}
		return nil
	}
	n.Valid = true
	return DecodeField(raw, &n.Value, false, dir)
}

var (
	containerType   = reflect.TypeFor[container]()
	jsonUnmarshaler = reflect.TypeFor[json.Unmarshaler]()
	textUnmarshaler = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// DecodeField decodes raw into dst, a pointer. Null is accepted when
// nullable is set or the target can represent it (Nullable, any);
// otherwise it is an error. dir is passed on to generated types. Problems
// are returned as ValidationErrors with paths relative to raw.
func DecodeField(raw json.RawMessage, dst any, nullable bool, dir Direction) error {
	if c, ok := dst.(container); ok {
		return c.decodeInto(raw, dir)
	}
	if done := decodeScalar(raw, dst); done {
		return nil
	}
	if _, directional := dst.(DirectionalDecoder); !directional && !isNull(raw) {
		// Calling the method directly skips encoding/json's checks of a
		// value that is known to be valid; a failure goes the long way
		// below, for the error encoding/json reports.
		if u, ok := dst.(json.Unmarshaler); ok && u.UnmarshalJSON(raw) == nil {
			return nil
		}
	}
	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("oapi: DecodeField needs a non-nil pointer, got %T", dst)
	}
	v := rv.Elem()
	if v.Kind() == reflect.Interface {
		return wrap(json.Unmarshal(raw, dst))
	}
	if isNull(raw) {
		if v.Kind() == reflect.Pointer && reflect.PointerTo(v.Type().Elem()).Implements(containerType) {
			// A pointer to a Nullable: present, and null.
			v.Set(reflect.New(v.Type().Elem()))
			return DecodeField(raw, v.Interface(), false, dir)
		}
		if !nullable {
			return &ValidationError{Message: "must not be null"}
		}
		v.SetZero()
		return nil
	}
	if d, ok := dst.(DirectionalDecoder); ok {
		return wrap(d.DecodeJSON(raw, dir))
	}
	if rv.Type().Implements(jsonUnmarshaler) || rv.Type().Implements(textUnmarshaler) {
		return wrap(json.Unmarshal(raw, dst))
	}

	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		return DecodeField(raw, v.Interface(), false, dir)
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			// []byte is base64 in JSON.
			return wrap(json.Unmarshal(raw, dst))
		}
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return &ValidationError{Message: "expected array, got " + valueType(raw)}
		}
		out := reflect.MakeSlice(v.Type(), len(items), len(items))
		var errs ValidationErrors
		for i, item := range items {
			// Build the path only for a problem.
			if err := DecodeField(item, out.Index(i).Addr().Interface(), false, dir); err != nil {
				errs.Nested(Index("", i), err)
			}
		}
		v.Set(out)
		return errs.Err()
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			break
		}
		fields, err := DecodeObject(raw)
		if err != nil {
			return err
		}
		out := reflect.MakeMapWithSize(v.Type(), len(fields))
		var errs ValidationErrors
		for _, name := range sortedKeys(fields) {
			elem := reflect.New(v.Type().Elem())
			if err := DecodeField(fields[name], elem.Interface(), false, dir); err != nil {
				errs.Nested(Field("", name), err)
			}
			out.SetMapIndex(reflect.ValueOf(name).Convert(v.Type().Key()), elem.Elem())
		}
		v.Set(out)
		return errs.Err()
	}
	return wrap(json.Unmarshal(raw, dst))
}

// decodeScalar decodes the simplest cases of common scalar targets without
// reflection: a string without escapes, a plain integer, a boolean or a
// number. It reports false for anything else (including errors and null),
// which DecodeField then handles in full.
func decodeScalar(raw json.RawMessage, dst any) bool {
	switch d := dst.(type) {
	case *string:
		if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
			return false
		}
		body := raw[1 : len(raw)-1]
		for _, c := range body {
			if c < 0x20 || c == '"' || c == '\\' || c >= 0x80 {
				return false
			}
		}
		*d = string(body)
		return true
	case *int64:
		n, err := strconv.ParseInt(string(raw), 10, 64)
		if err != nil || !plainInteger(raw) {
			return false
		}
		*d = n
		return true
	case *int32:
		n, err := strconv.ParseInt(string(raw), 10, 32)
		if err != nil || !plainInteger(raw) {
			return false
		}
		*d = int32(n)
		return true
	case *bool:
		switch string(raw) {
		case "true":
			*d = true
		case "false":
			*d = false
		default:
			return false
		}
		return true
	case *float64:
		if !plainNumber(raw) {
			return false
		}
		f, err := strconv.ParseFloat(string(raw), 64)
		if err != nil {
			return false
		}
		*d = f
		return true
	}
	return false
}

// plainInteger reports whether raw is a JSON integer: an optional minus
// and digits without a leading zero. strconv accepts more ("+1", "0x1").
func plainInteger(raw []byte) bool {
	if len(raw) > 0 && raw[0] == '-' {
		raw = raw[1:]
	}
	if len(raw) == 0 || (raw[0] == '0' && len(raw) > 1) {
		return false
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// plainNumber reports whether raw is a JSON number in the form
// -?int(.digits)?([eE][+-]?digits)?, which strconv.ParseFloat parses the
// same way.
func plainNumber(raw []byte) bool {
	i := 0
	if i < len(raw) && raw[i] == '-' {
		i++
	}
	start := i
	for i < len(raw) && raw[i] >= '0' && raw[i] <= '9' {
		i++
	}
	if i == start || (raw[start] == '0' && i-start > 1) {
		return false
	}
	if i < len(raw) && raw[i] == '.' {
		i++
		frac := i
		for i < len(raw) && raw[i] >= '0' && raw[i] <= '9' {
			i++
		}
		if i == frac {
			return false
		}
	}
	if i < len(raw) && (raw[i] == 'e' || raw[i] == 'E') {
		i++
		if i < len(raw) && (raw[i] == '+' || raw[i] == '-') {
			i++
		}
		exp := i
		for i < len(raw) && raw[i] >= '0' && raw[i] <= '9' {
			i++
		}
		if i == exp {
			return false
		}
	}
	return i == len(raw)
}

// wrap converts an encoding/json error, leaving ValidationErrors from
// nested decoders untouched.
func wrap(err error) error {
	if err == nil {
		return nil
	}
	var errs ValidationErrors
	errs.Nested("", err)
	return errs
}

// valueType names the JSON type of raw for messages.
func valueType(raw []byte) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "nothing"
	}
	switch c := raw[0]; {
	case c == '{':
		return "object"
	case c == '[':
		return "array"
	case c == '"':
		return "string"
	case c == 't' || c == 'f':
		return "boolean"
	case c == 'n':
		return "null"
	case c == '-' || (c >= '0' && c <= '9'):
		return "number"
	}
	return strings.TrimSpace(string(raw[:min(len(raw), 10)]))
}
