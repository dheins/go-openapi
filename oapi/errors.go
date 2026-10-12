package oapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// ValidationError is a problem with one value. Path is a JSON pointer to
// the value, relative to the value being decoded or validated.
type ValidationError struct {
	Path    string
	Message string
}

func (e *ValidationError) Error() string {
	if e.Path == "" {
		return e.Message
	}
	return e.Path + ": " + e.Message
}

// ValidationErrors lists every problem found in a value.
type ValidationErrors []*ValidationError

func (es ValidationErrors) Error() string {
	msgs := make([]string, len(es))
	for i, e := range es {
		msgs[i] = e.Error()
	}
	return strings.Join(msgs, "; ")
}

// Add records a problem at path.
func (es *ValidationErrors) Add(path, message string) {
	*es = append(*es, &ValidationError{Path: path, Message: message})
}

// Addf records a problem at path with a formatted message.
func (es *ValidationErrors) Addf(path, format string, args ...any) {
	es.Add(path, fmt.Sprintf(format, args...))
}

// Nested records the problems of err, a failure to decode or validate the
// value at prefix, with their paths made relative to the enclosing value.
// A nil err records nothing.
func (es *ValidationErrors) Nested(prefix string, err error) {
	if err == nil {
		return
	}
	var list ValidationErrors
	var single *ValidationError
	switch {
	case errors.As(err, &list):
		for _, e := range list {
			es.Add(prefix+e.Path, e.Message)
		}
	case errors.As(err, &single):
		es.Add(prefix+single.Path, single.Message)
	default:
		es.Add(prefix, describe(err))
	}
}

// Err returns the errors as an error, or nil if there are none.
func (es ValidationErrors) Err() error {
	if len(es) == 0 {
		return nil
	}
	return es
}

// Field returns the JSON pointer of property name below path.
func Field(path, name string) string {
	return path + "/" + pointerEscaper.Replace(name)
}

var pointerEscaper = strings.NewReplacer("~", "~0", "/", "~1")

// Index returns the JSON pointer of array element i below path.
func Index(path string, i int) string {
	return path + "/" + strconv.Itoa(i)
}

// describe turns a decoding error from encoding/json into a message about
// JSON types rather than Go types.
func describe(err error) string {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return fmt.Sprintf("expected %s, got %s", jsonType(typeErr.Type), typeErr.Value)
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return "invalid JSON: " + syntaxErr.Error()
	}
	return err.Error()
}

// jsonType names the JSON type that decodes into t.
func jsonType(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map, reflect.Struct:
		return "object"
	}
	return t.String()
}
