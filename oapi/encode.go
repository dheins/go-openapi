package oapi

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"time"
)

// DirectionalEncoder is implemented by generated object and union types.
// EncodeJSON leaves read-only properties out of requests and write-only
// properties out of responses.
type DirectionalEncoder interface {
	EncodeJSON(dir Direction) ([]byte, error)
}

// EncodeRequest encodes a request body, leaving out read-only properties.
func EncodeRequest(v any) ([]byte, error) {
	return EncodeValue(v, Request)
}

// EncodeResponse encodes a response body, leaving out write-only
// properties.
func EncodeResponse(v any) ([]byte, error) {
	return EncodeValue(v, Response)
}

// Appender is implemented by generated types that append their JSON to a
// buffer, which avoids a buffer per nested value.
type Appender interface {
	AppendJSON(dst []byte, dir Direction) ([]byte, error)
}

// appendingContainer is implemented by Optional and Nullable, so that
// AppendValue passes the direction on to the value they hold.
type appendingContainer interface {
	appendWith(dst []byte, dir Direction) ([]byte, error)
}

func (o Optional[T]) appendWith(dst []byte, dir Direction) ([]byte, error) {
	if !o.Set {
		return append(dst, "null"...), nil
	}
	return AppendValue(dst, o.Value, dir)
}

func (n Nullable[T]) appendWith(dst []byte, dir Direction) ([]byte, error) {
	if !n.Valid {
		return append(dst, "null"...), nil
	}
	return AppendValue(dst, n.Value, dir)
}

var jsonMarshaler = reflect.TypeFor[json.Marshaler]()

// EncodeValue encodes v as JSON like json.Marshal, passing dir on to
// generated types inside it, through pointers, slices, maps, Optional and
// Nullable.
func EncodeValue(v any, dir Direction) ([]byte, error) {
	return AppendValue(nil, v, dir)
}

// AppendValue appends v to dst as EncodeValue encodes it. On an error,
// the returned slice is nil.
func AppendValue(dst []byte, v any, dir Direction) ([]byte, error) {
	if v == nil {
		return append(dst, "null"...), nil
	}
	// A nil pointer satisfies the interfaces below through its element's
	// value methods, which would then dereference it.
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Pointer && rv.IsNil() {
		return append(dst, "null"...), nil
	}
	switch x := v.(type) {
	case Appender:
		return x.AppendJSON(dst, dir)
	case DirectionalEncoder:
		data, err := x.EncodeJSON(dir)
		if err != nil {
			return nil, err
		}
		return append(dst, data...), nil
	case appendingContainer:
		return x.appendWith(dst, dir)
	// Fast paths for the most common values, avoiding reflection.
	case string:
		return AppendString(dst, x), nil
	case bool:
		return strconv.AppendBool(dst, x), nil
	case int64:
		return strconv.AppendInt(dst, x, 10), nil
	case int32:
		return strconv.AppendInt(dst, int64(x), 10), nil
	case int:
		return strconv.AppendInt(dst, int64(x), 10), nil
	case float64:
		return AppendFloat(dst, x, 64)
	case float32:
		return AppendFloat(dst, float64(x), 32)
	case time.Time:
		if out, err := x.AppendText(append(dst, '"')); err == nil {
			return append(out, '"'), nil
		}
	case Date:
		if out, err := x.AppendText(append(dst, '"')); err == nil {
			return append(out, '"'), nil
		}
	case []string:
		if x == nil {
			return append(dst, "null"...), nil
		}
		dst = append(dst, '[')
		for i, s := range x {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = AppendString(dst, s)
		}
		return append(dst, ']'), nil
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer:
		return AppendValue(dst, rv.Elem().Interface(), dir)
	case reflect.Slice, reflect.Array:
		if rv.Type().Implements(jsonMarshaler) || rv.Type().Elem().Kind() == reflect.Uint8 {
			break
		}
		if rv.Kind() == reflect.Slice && rv.IsNil() {
			return append(dst, "null"...), nil
		}
		dst = append(dst, '[')
		for i := range rv.Len() {
			if i > 0 {
				dst = append(dst, ',')
			}
			var err error
			if dst, err = AppendValue(dst, rv.Index(i).Interface(), dir); err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
		}
		return append(dst, ']'), nil
	case reflect.Map:
		if rv.Type().Implements(jsonMarshaler) || rv.Type().Key().Kind() != reflect.String {
			break
		}
		if rv.IsNil() {
			return append(dst, "null"...), nil
		}
		keys := make([]string, 0, rv.Len())
		for _, k := range rv.MapKeys() {
			keys = append(keys, k.String())
		}
		slices.Sort(keys)
		dst = append(dst, '{')
		for i, k := range keys {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = append(AppendString(dst, k), ':')
			var err error
			if dst, err = AppendValue(dst, rv.MapIndex(reflect.ValueOf(k).Convert(rv.Type().Key())).Interface(), dir); err != nil {
				return nil, fmt.Errorf("%s: %w", k, err)
			}
		}
		return append(dst, '}'), nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return append(dst, data...), nil
}

// EndObject closes the object a generated AppendJSON started at
// dst[start], whose properties are each followed by a comma.
func EndObject(dst []byte, start int) []byte {
	if len(dst) > start+1 {
		dst[len(dst)-1] = '}'
		return dst
	}
	return append(dst, '}')
}

// AppendFloat appends f as encoding/json writes a float of the given bit
// size: the shortest representation that reads back the same, with an
// exponent only for very small or large values.
func AppendFloat(dst []byte, f float64, bits int) ([]byte, error) {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		// For encoding/json's error.
		var v any = f
		if bits == 32 {
			v = float32(f)
		}
		_, err := json.Marshal(v)
		return nil, err
	}
	format := byte('f')
	if abs := math.Abs(f); abs != 0 {
		if bits == 64 && (abs < 1e-6 || abs >= 1e21) || bits == 32 && (float32(abs) < 1e-6 || float32(abs) >= 1e21) {
			format = 'e'
		}
	}
	dst = strconv.AppendFloat(dst, f, format, -1, bits)
	if format == 'e' {
		// e-09 is written e-9.
		if n := len(dst); n >= 4 && dst[n-4] == 'e' && dst[n-3] == '-' && dst[n-2] == '0' {
			dst[n-2] = dst[n-1]
			dst = dst[:n-1]
		}
	}
	return dst, nil
}

// ObjectEncoder writes a JSON object property by property. Generated
// EncodeJSON methods use it.
type ObjectEncoder struct {
	dir Direction
	buf bytes.Buffer
	n   int
	err error
}

// NewObjectEncoder starts an object encoded in direction dir.
func NewObjectEncoder(dir Direction) *ObjectEncoder {
	e := &ObjectEncoder{dir: dir}
	e.buf.WriteByte('{')
	return e
}

// Field adds the property name with value v. The first error is kept and
// returned by Bytes.
func (e *ObjectEncoder) Field(name string, v any) {
	if e.err != nil {
		return
	}
	if e.n > 0 {
		e.buf.WriteByte(',')
	}
	e.buf.Write(AppendString(e.buf.AvailableBuffer(), name))
	e.buf.WriteByte(':')
	value, err := AppendValue(e.buf.AvailableBuffer(), v, e.dir)
	if err != nil {
		e.err = fmt.Errorf("%s: %w", name, err)
		return
	}
	e.n++
	e.buf.Write(value)
}

// Inline adds the properties of v, which must encode as a JSON object, in
// their order, except those named in skip. The first error is kept and
// returned by Bytes.
func (e *ObjectEncoder) Inline(v any, skip ...string) {
	if e.err != nil {
		return
	}
	members, err := AppendInline(nil, v, e.dir, skip...)
	if err != nil {
		e.err = err
		return
	}
	if len(members) == 0 {
		return
	}
	if e.n > 0 {
		e.buf.WriteByte(',')
	}
	e.n++
	e.buf.Write(members[:len(members)-1])
}

// AppendInline appends the properties of v, which must encode as a JSON
// object, to dst in their order, each followed by a comma, except those
// named in skip. Generated code uses it for unions whose properties
// belong to the enclosing object: skip lists the properties the object
// has written itself.
func AppendInline(dst []byte, v any, dir Direction, skip ...string) ([]byte, error) {
	data, err := AppendValue(nil, v, dir)
	if err != nil {
		return nil, err
	}
	dec := jsontext.NewDecoder(bytes.NewReader(data), decoderOptions...)
	if tok, err := dec.ReadToken(); err != nil || tok.Kind() != '{' {
		return nil, fmt.Errorf("inline value is not an object: %s", data)
	}
	for dec.PeekKind() != '}' {
		tok, err := dec.ReadToken()
		if err != nil {
			return nil, err
		}
		// The token is only valid until the next read.
		name := tok.String()
		value, err := dec.ReadValue()
		if err != nil {
			return nil, err
		}
		if !slices.Contains(skip, name) {
			dst = append(AppendString(dst, name), ':')
			dst = append(append(dst, value...), ',')
		}
	}
	return dst, nil
}

// AppendString appends s as a JSON string. Strings of printable ASCII
// without quotes, backslashes or HTML characters (what encoding/json
// escapes by default) are copied; others go through encoding/json.
func AppendString(dst []byte, s string) []byte {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c >= 0x7f || c == '"' || c == '\\' || c == '<' || c == '>' || c == '&' {
			// Marshalling a string cannot fail.
			quoted, _ := json.Marshal(s)
			return append(dst, quoted...)
		}
	}
	dst = append(dst, '"')
	dst = append(dst, s...)
	return append(dst, '"')
}

// Bytes returns the encoded object.
func (e *ObjectEncoder) Bytes() ([]byte, error) {
	if e.err != nil {
		return nil, e.err
	}
	e.buf.WriteByte('}')
	return e.buf.Bytes(), nil
}
