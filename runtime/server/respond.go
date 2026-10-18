package server

import (
	"bytes"
	"encoding"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/dheins/go-openapi/oapi"
)

// WriteJSON sends body as JSON with the given status, content type and
// headers, leaving out write-only properties. The body is encoded before
// anything is written, so an error leaves w untouched (headers included)
// and the caller can still send an error response.
func (c *Config) WriteJSON(w http.ResponseWriter, status int, contentType string, body any, header http.Header) error {
	data, err := oapi.EncodeResponse(body)
	if err != nil {
		return fmt.Errorf("encoding the %d response: %w", status, err)
	}
	applyHeader(w, header)
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	// The status line is sent; a failed write means the client is gone and
	// there is no one left to tell.
	_, _ = w.Write(data)
	return nil
}

// InvalidResponse reports that a handler's response failed validation
// against the document (see WithResponseValidation).
type InvalidResponse struct {
	Status int
	Err    error
}

func (e *InvalidResponse) Error() string {
	return fmt.Sprintf("the %d response does not match the document: %v", e.Status, e.Err)
}

func (e *InvalidResponse) Unwrap() error { return e.Err }

// WriteEmpty sends a response without a body.
func WriteEmpty(w http.ResponseWriter, status int, header http.Header) error {
	applyHeader(w, header)
	w.WriteHeader(status)
	return nil
}

// applyHeader adds the headers of a response to w. Writers call it only
// once nothing can fail any more, so an error response never carries the
// headers meant for the success response.
func applyHeader(w http.ResponseWriter, header http.Header) {
	for name, values := range header {
		w.Header()[name] = values
	}
}

// CheckStatus checks a status code chosen by a handler for a response
// declared as a range ("4XX") or as "default".
func CheckStatus(code int, declared string) error {
	if declared == "default" {
		if code < 100 || code > 599 {
			return fmt.Errorf("status code %d is not a valid HTTP status", code)
		}
		return nil
	}
	class, ok := strings.CutSuffix(strings.ToUpper(declared), "XX")
	if !ok || len(class) != 1 || code/100 != int(class[0]-'0') {
		return fmt.Errorf("status code %d does not match the declared response %q", code, declared)
	}
	return nil
}

// FormatScalar formats a header value in the simple style.
func FormatScalar[T any](v T) (string, error) {
	if m, ok := any(v).(encoding.TextMarshaler); ok {
		text, err := m.MarshalText()
		if err != nil {
			return "", fmt.Errorf("formatting header value: %w", err)
		}
		return string(text), nil
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.String:
		return rv.String(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(rv.Float(), 'g', -1, rv.Type().Bits()), nil
	case reflect.Bool:
		return strconv.FormatBool(rv.Bool()), nil
	}
	return "", fmt.Errorf("server: cannot format %T as a header value", v)
}

// FormatArray formats an array header value in the simple style.
func FormatArray[T any](vs []T) (string, error) {
	items := make([]string, len(vs))
	for i, v := range vs {
		s, err := FormatScalar(v)
		if err != nil {
			return "", err
		}
		items[i] = s
	}
	return strings.Join(items, ","), nil
}

// FormatJSON formats the value of a header serialized as JSON, leaving
// out write-only properties as in response bodies.
func FormatJSON[T any](v T) (string, error) {
	data, err := oapi.EncodeResponse(v)
	if err != nil {
		return "", fmt.Errorf("formatting header value: %w", err)
	}
	// A header value cannot hold line breaks a custom encoder may write.
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		return "", fmt.Errorf("formatting header value: %w", err)
	}
	return compact.String(), nil
}
