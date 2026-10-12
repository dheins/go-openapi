package oapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
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

// encodingContainer is implemented by Optional and Nullable, so that
// EncodeValue passes the direction on to the value they hold.
type encodingContainer interface {
	encodeWith(dir Direction) ([]byte, error)
}

func (o Optional[T]) encodeWith(dir Direction) ([]byte, error) {
	if !o.Set {
		return []byte("null"), nil
	}
	return EncodeValue(o.Value, dir)
}

func (n Nullable[T]) encodeWith(dir Direction) ([]byte, error) {
	if !n.Valid {
		return []byte("null"), nil
	}
	return EncodeValue(n.Value, dir)
}

var jsonMarshaler = reflect.TypeFor[json.Marshaler]()

// EncodeValue encodes v as JSON like json.Marshal, passing dir on to
// generated types inside it, through pointers, slices, maps, Optional and
// Nullable.
func EncodeValue(v any, dir Direction) ([]byte, error) {
	if v == nil {
		return []byte("null"), nil
	}
	// A nil pointer satisfies the interfaces below through its element's
	// value methods, which would then dereference it.
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Pointer && rv.IsNil() {
		return []byte("null"), nil
	}
	switch x := v.(type) {
	case DirectionalEncoder:
		return x.EncodeJSON(dir)
	case encodingContainer:
		return x.encodeWith(dir)
	// Fast paths for the most common values, avoiding reflection.
	case string:
		return appendString(nil, x), nil
	case bool:
		return strconv.AppendBool(nil, x), nil
	case int64:
		return strconv.AppendInt(nil, x, 10), nil
	case int32:
		return strconv.AppendInt(nil, int64(x), 10), nil
	case int:
		return strconv.AppendInt(nil, int64(x), 10), nil
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer:
		if rv.IsNil() {
			return []byte("null"), nil
		}
		return EncodeValue(rv.Elem().Interface(), dir)
	case reflect.Slice, reflect.Array:
		if rv.Type().Implements(jsonMarshaler) || rv.Type().Elem().Kind() == reflect.Uint8 {
			break
		}
		if rv.Kind() == reflect.Slice && rv.IsNil() {
			return []byte("null"), nil
		}
		var buf bytes.Buffer
		buf.WriteByte('[')
		for i := range rv.Len() {
			if i > 0 {
				buf.WriteByte(',')
			}
			item, err := EncodeValue(rv.Index(i).Interface(), dir)
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			buf.Write(item)
		}
		buf.WriteByte(']')
		return buf.Bytes(), nil
	case reflect.Map:
		if rv.Type().Implements(jsonMarshaler) || rv.Type().Key().Kind() != reflect.String {
			break
		}
		if rv.IsNil() {
			return []byte("null"), nil
		}
		keys := make([]string, 0, rv.Len())
		for _, k := range rv.MapKeys() {
			keys = append(keys, k.String())
		}
		slices.Sort(keys)
		enc := NewObjectEncoder(dir)
		for _, k := range keys {
			enc.Field(k, rv.MapIndex(reflect.ValueOf(k).Convert(rv.Type().Key())).Interface())
		}
		return enc.Bytes()
	}
	return json.Marshal(v)
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
	value, err := EncodeValue(v, e.dir)
	if err != nil {
		e.err = fmt.Errorf("%s: %w", name, err)
		return
	}
	if e.n > 0 {
		e.buf.WriteByte(',')
	}
	e.n++
	e.buf.Write(appendString(e.buf.AvailableBuffer(), name))
	e.buf.WriteByte(':')
	e.buf.Write(value)
}

// Inline adds the properties of v, which must encode as a JSON object, in
// their order, except those named in skip. Generated code uses it for
// unions whose properties belong to the enclosing object: skip lists the
// properties the object has written itself.
func (e *ObjectEncoder) Inline(v any, skip ...string) {
	if e.err != nil {
		return
	}
	data, err := EncodeValue(v, e.dir)
	if err != nil {
		e.err = err
		return
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		e.err = fmt.Errorf("inline value is not an object: %s", data)
		return
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			e.err = err
			return
		}
		name := tok.(string)
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			e.err = err
			return
		}
		if slices.Contains(skip, name) {
			continue
		}
		if e.n > 0 {
			e.buf.WriteByte(',')
		}
		e.n++
		e.buf.Write(appendString(e.buf.AvailableBuffer(), name))
		e.buf.WriteByte(':')
		e.buf.Write(value)
	}
}

// appendString appends s as a JSON string. Strings of printable ASCII
// without quotes, backslashes or HTML characters (what encoding/json
// escapes by default) are copied; others go through encoding/json.
func appendString(dst []byte, s string) []byte {
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
