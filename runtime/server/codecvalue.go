package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"time"
)

// CodecJSON returns v, the value a codec decoded a body into (an any:
// maps, slices and scalars, as libraries of formats such as msgpack and
// CBOR give them), as JSON, so that the JSON decoder reads it with its
// rules: required properties, read-only and write-only ones, defaults,
// and problems at JSON pointers. Map keys are strings, or integers and
// booleans written as their text; []byte is base64, as format byte is in
// JSON; times are RFC 3339; numbers must be finite.
func CodecJSON(v any) ([]byte, error) {
	j, err := jsonValue(reflect.ValueOf(v), 0)
	if err != nil {
		return nil, err
	}
	return json.Marshal(j)
}

// maxCodecDepth bounds the nesting of a codec's value, as JSON's is.
const maxCodecDepth = 10000

// jsonValue converts v to a value json.Marshal writes as JSON values do.
func jsonValue(v reflect.Value, depth int) (any, error) {
	if depth > maxCodecDepth {
		return nil, errors.New("the value is nested too deeply")
	}
	for v.IsValid() && (v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer) {
		if v.IsNil() {
			return nil, nil
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return nil, nil
	}
	if t, ok := v.Interface().(time.Time); ok {
		return t.Format(time.RFC3339Nano), nil
	}
	switch v.Kind() {
	case reflect.Bool:
		return v.Bool(), nil
	case reflect.String:
		return v.String(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return json.Number(strconv.FormatInt(v.Int(), 10)), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return json.Number(strconv.FormatUint(v.Uint(), 10)), nil
	case reflect.Float32, reflect.Float64:
		f := v.Float()
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, fmt.Errorf("%v is not a JSON number", f)
		}
		return json.Number(strconv.FormatFloat(f, 'g', -1, v.Type().Bits())), nil
	case reflect.Slice, reflect.Array:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			b := make([]byte, v.Len())
			reflect.Copy(reflect.ValueOf(b), v)
			return base64.StdEncoding.EncodeToString(b), nil
		}
		if v.Kind() == reflect.Slice && v.IsNil() {
			return nil, nil
		}
		out := make([]any, v.Len())
		for i := range out {
			item, err := jsonValue(v.Index(i), depth+1)
			if err != nil {
				return nil, fmt.Errorf("item %d: %w", i, err)
			}
			out[i] = item
		}
		return out, nil
	case reflect.Map:
		if v.IsNil() {
			return nil, nil
		}
		out := make(map[string]any, v.Len())
		for it := v.MapRange(); it.Next(); {
			key, err := mapKey(it.Key())
			if err != nil {
				return nil, err
			}
			value, err := jsonValue(it.Value(), depth+1)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			out[key] = value
		}
		return out, nil
	}
	return nil, fmt.Errorf("a %s cannot be a JSON value", v.Type())
}

// mapKey returns a key of a map as the name of a JSON property.
func mapKey(k reflect.Value) (string, error) {
	for k.Kind() == reflect.Interface && !k.IsNil() {
		k = k.Elem()
	}
	switch k.Kind() {
	case reflect.String:
		return k.String(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(k.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(k.Uint(), 10), nil
	case reflect.Bool:
		return strconv.FormatBool(k.Bool()), nil
	}
	return "", fmt.Errorf("a key of type %s cannot name a property", k.Type())
}

// CodecValue returns the JSON data as the value a codec encodes: maps of
// strings, slices, strings, booleans, nil, int64 for integers that fit and
// float64 for other numbers; a number too large for a float64 is an
// error.
func CodecValue(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return codecNumbers(v)
}

func codecNumbers(v any) (any, error) {
	var err error
	switch x := v.(type) {
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n, nil
		}
		f, err := x.Float64()
		if err != nil {
			return nil, fmt.Errorf("the number %s is out of range", x)
		}
		return f, nil
	case []any:
		for i := range x {
			if x[i], err = codecNumbers(x[i]); err != nil {
				return nil, err
			}
		}
	case map[string]any:
		for k := range x {
			if x[k], err = codecNumbers(x[k]); err != nil {
				return nil, err
			}
		}
	}
	return v, nil
}
