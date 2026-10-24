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
	var v any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return "", fmt.Errorf("callback URL: body: %w", err)
	}
	if pointer != "" {
		for _, tok := range strings.Split(pointer[1:], "/") {
			tok = strings.NewReplacer("~1", "/", "~0", "~").Replace(tok)
			switch c := v.(type) {
			case map[string]any:
				var ok bool
				if v, ok = c[tok]; !ok {
					return "", fmt.Errorf("callback URL: the body has no %s", pointer)
				}
			case []any:
				i, err := strconv.Atoi(tok)
				if err != nil || i < 0 || i >= len(c) {
					return "", fmt.Errorf("callback URL: the body has no %s", pointer)
				}
				v = c[i]
			default:
				return "", fmt.Errorf("callback URL: the body has no %s", pointer)
			}
		}
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

// MissingValue reports a runtime expression whose value the request does
// not have, such as an optional parameter that is not set.
func MissingValue(expr string) error {
	return fmt.Errorf("callback URL: %s is not set", expr)
}
