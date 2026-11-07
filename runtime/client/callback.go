package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/server"
)

// The functions here compute callback URLs: the values of the runtime
// expressions in them, from the request of the operation declaring the
// callback and the response its handler returns. Values are put in the URL as they are, not escaped: a
// callback URL is often an expression on its own, holding a whole URL.

// ParamValue returns the text of v, a parameter's value: a scalar as
// text, an array's items joined by commas.
func ParamValue(v any) (string, error) {
	if k := reflect.ValueOf(v).Kind(); k != reflect.Slice && k != reflect.Array {
		return scalar(v)
	}
	items, err := arrayItems(v, "")
	if err != nil {
		return "", err
	}
	return strings.Join(items, ","), nil
}

// BodyValue returns the value at pointer, a JSON pointer ("" for the
// whole value), in body, a request body, encoded as JSON: a string as it
// is, other values as JSON.
func BodyValue(body any, pointer string) (string, error) {
	return bodyText(body, oapi.Request, pointer, "body")
}

// ResponseValue is BodyValue for body, a response body.
func ResponseValue(body any, pointer string) (string, error) {
	return bodyText(body, oapi.Response, pointer, "response body")
}

// MissingResponseValue reports a runtime expression over the response
// that resp, a response variant, does not have, such as a header that
// only another status declares.
func MissingResponseValue(expr string, resp any) error {
	return fmt.Errorf("callback URL: %s is not in a %T", expr, resp)
}

func bodyText(body any, dir oapi.Direction, pointer, what string) (string, error) {
	data, err := oapi.EncodeValue(body, dir)
	if err != nil {
		return "", fmt.Errorf("callback URL: %s: %w", what, err)
	}
	v, err := valueAt(data, pointer)
	if err != nil {
		return "", fmt.Errorf("callback URL: the %s %w", what, err)
	}
	text, err := textOf(v)
	if err != nil {
		return "", fmt.Errorf("callback URL: %s: %w", what, err)
	}
	return text, nil
}

// textOf returns v, a decoded JSON value, as text: a string as it is,
// other values as JSON.
func textOf(v any) (string, error) {
	switch v := v.(type) {
	case string:
		return v, nil
	case json.Number:
		return v.String(), nil
	}
	text, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(text), nil
}

// errNoValue is the error of valueAt for a pointer the value does not
// have.
var errNoValue = errors.New("has no")

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
				return nil, fmt.Errorf("%w %s", errNoValue, pointer)
			}
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(c) {
				return nil, fmt.Errorf("%w %s", errNoValue, pointer)
			}
			v = c[i]
		default:
			return nil, fmt.Errorf("%w %s", errNoValue, pointer)
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

// LinkPart returns the value at pointer ("" for all of it) in src encoded
// as JSON in direction dir as text, for a link value with expressions
// embedded in text: a string as it is, other values as JSON.
func LinkPart(src any, dir oapi.Direction, pointer string) (string, error) {
	data, err := oapi.EncodeValue(src, dir)
	if err != nil {
		return "", err
	}
	v, err := valueAt(data, pointer)
	if err != nil {
		return "", fmt.Errorf("the value %w", err)
	}
	return textOf(v)
}

// LinkText sets *dst, a scalar field of the request a link leads to, to
// text, a value with expressions embedded in it: as a string or, for a
// field that is not one, as the number or boolean text spells.
func LinkText(dst any, text string) error {
	quoted, err := json.Marshal(text)
	if err != nil {
		return err
	}
	err = LinkConst(dst, quoted)
	if err == nil {
		return nil
	}
	if _, numErr := strconv.ParseFloat(text, 64); json.Valid([]byte(text)) && (numErr == nil || text == "true" || text == "false") {
		if LinkConst(dst, json.RawMessage(text)) == nil {
			return nil
		}
	}
	return err
}

// LinkParsed sets *dst, a field of the request a link leads to that is
// not a string, to text, a value with expressions embedded in it, which
// is JSON.
func LinkParsed(dst any, text string) error {
	if !json.Valid([]byte(text)) {
		return fmt.Errorf("%q is not JSON", text)
	}
	return LinkConst(dst, json.RawMessage(text))
}

// LinkArray sets *dst, an array parameter of the request a link leads
// to, to text, a value with expressions embedded in it: JSON or, if it
// is not JSON of the array, items as p's style writes them
// (server.Param.TextValues).
func LinkArray[T any](dst any, text string, p server.Param) error {
	if json.Valid([]byte(text)) && LinkConst(dst, json.RawMessage(text)) == nil {
		return nil
	}
	vals, err := p.TextValues(text)
	if err != nil {
		return err
	}
	items, err := server.ParseArray[T](vals.Items)
	if err != nil {
		return err
	}
	return linkGo(dst, items)
}

// LinkObject is LinkArray for an object parameter, whose properties
// props describes: JSON or "k,v,k,v".
func LinkObject[T any](dst any, text string, p server.Param, props server.Property) error {
	if json.Valid([]byte(text)) && LinkConst(dst, json.RawMessage(text)) == nil {
		return nil
	}
	vals, err := p.TextValues(text)
	if err != nil {
		return err
	}
	obj, err := server.DecodeFields[T](vals.Fields, props)
	if err != nil {
		return err
	}
	return linkGo(dst, obj)
}

// linkGo sets *dst to v, a Go value of its type or of the type it
// holds.
func linkGo(dst, v any) error {
	data, err := oapi.EncodeValue(v, oapi.Request)
	if err != nil {
		return err
	}
	return LinkConst(dst, data)
}

// Fill is a member of a link's constant filled in from the response or
// request: the value at Pointer, an item of an array if Item, is Value,
// JSON, or not there if Value is nil.
type Fill struct {
	Pointer string
	Item    bool
	Value   json.RawMessage
}

// LinkJSON returns the value at pointer ("" for all of it) in src encoded
// as JSON in direction dir, for a member of a link's constant, or nil if
// src has no value there.
func LinkJSON(src any, dir oapi.Direction, pointer string) (json.RawMessage, error) {
	data, err := oapi.EncodeValue(src, dir)
	if err != nil {
		return nil, err
	}
	v, err := valueAt(data, pointer)
	if errors.Is(err, errNoValue) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// LinkString returns text, a member of a link's constant with expressions
// embedded in it, as JSON.
func LinkString(text string) json.RawMessage {
	// Marshalling a string cannot fail.
	data, _ := json.Marshal(text)
	return data
}

// LinkFill sets *dst, a field of the request a link leads to, to raw, a
// JSON object or array, with fills set in it. A fill that is not there
// leaves out an object's property; an array's item cannot be left out.
func LinkFill(dst any, raw json.RawMessage, fills ...Fill) error {
	var doc any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return err
	}
	for _, f := range fills {
		if f.Value == nil && f.Item {
			return fmt.Errorf("the item %s is not there", f.Pointer)
		}
		var v any
		if f.Value != nil {
			dec := json.NewDecoder(bytes.NewReader(f.Value))
			dec.UseNumber()
			if err := dec.Decode(&v); err != nil {
				return err
			}
		}
		var err error
		if doc, err = setAt(doc, f.Pointer, v, f.Value == nil); err != nil {
			return err
		}
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	return LinkConst(dst, data)
}

// setAt sets the value at pointer in doc, a decoded JSON value, to v, or
// deletes it if remove, and returns doc.
func setAt(doc any, pointer string, v any, remove bool) (any, error) {
	if pointer == "" {
		return v, nil
	}
	rest := pointer[1:]
	tok, rest, more := strings.Cut(rest, "/")
	tok = strings.NewReplacer("~1", "/", "~0", "~").Replace(tok)
	if more {
		rest = "/" + rest
	}
	switch c := doc.(type) {
	case map[string]any:
		if !more && remove {
			delete(c, tok)
			return c, nil
		}
		child := c[tok]
		if more {
			var err error
			if child, err = setAt(child, rest, v, remove); err != nil {
				return nil, err
			}
			c[tok] = child
			return c, nil
		}
		c[tok] = v
		return c, nil
	case []any:
		i, err := strconv.Atoi(tok)
		if err != nil || i < 0 || i >= len(c) {
			return nil, fmt.Errorf("has no %s", pointer)
		}
		if more {
			if c[i], err = setAt(c[i], rest, v, remove); err != nil {
				return nil, err
			}
			return c, nil
		}
		c[i] = v
		return c, nil
	}
	return nil, fmt.Errorf("has no %s", pointer)
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
