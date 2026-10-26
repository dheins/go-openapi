package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/dheins/go-openapi/oapi"
)

// The functions here compute callback URLs: the values of the runtime
// expressions in them, from the request of the operation declaring the
// callback. Values are put in the URL as they are, not escaped: a
// callback URL is often an expression on its own, holding a whole URL.

// ParamValue returns the text of v, a parameter's value: a scalar as
// text, an array's items joined by commas.
func ParamValue(v any) (string, error) {
	if k := reflect.ValueOf(v).Kind(); k != reflect.Slice && k != reflect.Array {
		return scalar(v)
	}
	items, err := arrayItems(v)
	if err != nil {
		return "", err
	}
	return strings.Join(items, ","), nil
}

// BodyValue returns the value at pointer, a JSON pointer ("" for the
// whole value), in body encoded as JSON: a string as it is, other values
// as JSON.
func BodyValue(body any, pointer string) (string, error) {
	data, err := oapi.EncodeRequest(body)
	if err != nil {
		return "", fmt.Errorf("callback URL: body: %w", err)
	}
	v, err := valueAt(data, pointer)
	if err != nil {
		return "", fmt.Errorf("callback URL: the body %w", err)
	}
	switch v := v.(type) {
	case string:
		return v, nil
	case json.Number:
		return v.String(), nil
	}
	text, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("callback URL: body: %w", err)
	}
	return string(text), nil
}

// valueAt returns the value at pointer, a JSON pointer ("" for the whole
// value), in data, a JSON document, with numbers as json.Numbers.
func valueAt(data []byte, pointer string) (any, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if pointer == "" {
		return v, nil
	}
	for _, tok := range strings.Split(pointer[1:], "/") {
		tok = strings.NewReplacer("~1", "/", "~0", "~").Replace(tok)
		switch c := v.(type) {
		case map[string]any:
			var ok bool
			if v, ok = c[tok]; !ok {
				return nil, fmt.Errorf("has no %s", pointer)
			}
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(c) {
				return nil, fmt.Errorf("has no %s", pointer)
			}
			v = c[i]
		default:
			return nil, fmt.Errorf("has no %s", pointer)
		}
	}
	return v, nil
}

// LinkValue sets *dst, a field of the request a link leads to, to the
// value at pointer ("" for all of it) in src encoded as JSON in direction
// dir: src is a value of the response the link starts from, or of the
// request it answers.
func LinkValue(dst, src any, dir oapi.Direction, pointer string) error {
	data, err := oapi.EncodeValue(src, dir)
	if err != nil {
		return err
	}
	v, err := valueAt(data, pointer)
	if err != nil {
		return fmt.Errorf("the value %w", err)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return LinkConst(dst, raw)
}

// LinkConst sets *dst, a field of the request a link leads to, to the
// JSON value raw.
func LinkConst(dst any, raw json.RawMessage) error {
	return oapi.DecodeValue(raw, dst, oapi.Request)
}

// LinkError is the error of a link whose value expr could not be given to
// the request it leads to.
func LinkError(link, expr string, err error) error {
	return fmt.Errorf("link %s: %s: %w", link, expr, err)
}

// MissingValue reports a runtime expression whose value the request does
// not have, such as an optional parameter that is not set.
func MissingValue(expr string) error {
	return fmt.Errorf("callback URL: %s is not set", expr)
}
