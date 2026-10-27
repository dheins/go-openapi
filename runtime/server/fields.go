package server

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"strconv"

	"github.com/dheins/go-openapi/oapi"
)

// Field is a property of an object written as fields: its path (one name,
// or more for a nested property) and its values (several for an array).
type Field struct {
	Path   []string
	Values []string
}

// Name returns the field's name in a form: a[b][c].
func (f Field) Name() string {
	name := f.Path[0]
	for _, seg := range f.Path[1:] {
		name += "[" + seg + "]"
	}
	return name
}

// ObjectFields returns the properties of v, encoded as JSON in direction
// dir, as fields in their order: the inverse of ObjectJSON. With nested,
// objects inside are fields of their own (a[b]); otherwise they, like
// arrays of objects, are JSON text, which servers read as such.
func ObjectFields(v any, nested bool, dir oapi.Direction) ([]Field, error) {
	data, err := oapi.EncodeValue(v, dir)
	if err != nil {
		return nil, err
	}
	dec := jsontext.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.ReadToken(); err != nil || tok.Kind() != '{' {
		return nil, fmt.Errorf("expected an object, got %s", data)
	}
	var fields []Field
	if err := readFields(dec, nil, nested, &fields); err != nil {
		return nil, err
	}
	return fields, nil
}

// readFields reads the members of an object, whose opening brace is read,
// into fields under prefix.
func readFields(dec *jsontext.Decoder, prefix []string, nested bool, fields *[]Field) error {
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
			if nested {
				indexed, err := indexedFields(raw, path, fields)
				if err != nil {
					return err
				}
				if indexed {
					continue
				}
			}
			values, err := arrayValues(raw)
			if err != nil {
				return err
			}
			*fields = append(*fields, Field{Path: path, Values: values})
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
			*fields = append(*fields, Field{Path: path, Values: []string{FieldText(raw)}})
		}
	}
	_, err := dec.ReadToken()
	return err
}

// indexedFields writes the JSON array raw at path with indices, if it
// holds an object: a[0][b]=x for an object item, a[1]=y for a scalar one.
// It reports whether it did; an array of scalars is left to the caller,
// as a=x&a=y.
func indexedFields(raw []byte, path []string, fields *[]Field) (bool, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return false, err
	}
	hasObject := false
	for _, it := range items {
		if len(it) > 0 && it[0] == '{' {
			hasObject = true
			break
		}
	}
	if !hasObject {
		return false, nil
	}
	for i, it := range items {
		at := append(append([]string{}, path...), strconv.Itoa(i))
		switch {
		case it[0] == '{':
			dec := jsontext.NewDecoder(bytes.NewReader(it))
			if _, err := dec.ReadToken(); err != nil {
				return false, err
			}
			if err := readFields(dec, at, true, fields); err != nil {
				return false, err
			}
		case string(it) == "null":
			// An absent item, as null members are: the server reads the
			// items that are given, in order.
		default:
			*fields = append(*fields, Field{Path: at, Values: []string{FieldText(it)}})
		}
	}
	return true, nil
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
		values[i] = FieldText(it)
	}
	return values, nil
}

// FieldText returns a JSON value as the text of a field: a string
// unquoted, anything else as written.
func FieldText(raw []byte) string {
	if len(raw) > 0 && raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
	}
	return string(raw)
}
