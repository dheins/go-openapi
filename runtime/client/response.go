package client

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/problem"
	"github.com/dheins/go-openapi/runtime/server"
)

// MediaType returns the media type of resp, without parameters, in lower
// case; "" if it has none.
func MediaType(resp *http.Response) string {
	mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		return ""
	}
	return mt
}

// StatusIs reports whether code is the status of a response declared as
// declared: a code ("201"), a range ("4XX") or "default".
func StatusIs(code int, declared string) bool {
	switch {
	case declared == "default":
		return true
	case len(declared) == 3 && strings.HasSuffix(strings.ToUpper(declared), "XX"):
		return code/100 == int(declared[0]-'0')
	}
	n, err := strconv.Atoi(declared)
	return err == nil && n == code
}

// MediaTypeIs reports whether mt, the media type of a response, is one
// declared as declared, which may be a range. A response without a media
// type matches when the status has a single declared media type (single).
func MediaTypeIs(mt, declared string, single bool) bool {
	if mt == "" {
		return single
	}
	declared, _, _ = strings.Cut(declared, ";")
	return server.MatchMediaType(mt, strings.TrimSpace(strings.ToLower(declared)))
}

// ReadBody reads the whole body of resp, up to MaxResponseBytes, and
// closes it.
func (c *Config) ReadBody(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	limit := c.MaxResponseBytes
	if limit <= 0 {
		limit = DefaultMaxResponseBytes
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, &InvalidResponse{StatusCode: resp.StatusCode, Err: fmt.Errorf("reading the body: %w", err)}
	}
	if int64(len(data)) > limit {
		return nil, &InvalidResponse{StatusCode: resp.StatusCode, Err: fmt.Errorf("the body is longer than %d bytes", limit)}
	}
	return data, nil
}

// Unexpected reads a response the operation does not declare, and
// returns it as an *UnexpectedResponse.
func (c *Config) Unexpected(resp *http.Response) error {
	defer resp.Body.Close()
	// A little of the body, to report what went wrong.
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	e := &UnexpectedResponse{StatusCode: resp.StatusCode, Header: resp.Header, Body: data}
	if MediaType(resp) == problem.ContentType {
		var p problem.Problem
		if json.Unmarshal(data, &p) == nil {
			e.Problem = &p
		}
	}
	return e
}

// DecodeJSON decodes a JSON response body into dst, without requiring
// write-only properties.
func DecodeJSON(resp *http.Response, body []byte, dst any) error {
	if err := oapi.DecodeResponse(body, dst); err != nil {
		return &InvalidResponse{StatusCode: resp.StatusCode, Err: err}
	}
	return nil
}

// DecodeXML decodes an XML response body into dst, as root describes.
func DecodeXML(resp *http.Response, body []byte, root *server.XMLNode, dst any) error {
	data, err := server.XMLToJSON(body, root)
	if err == nil {
		err = oapi.DecodeResponse(data, dst)
	}
	if err != nil {
		return &InvalidResponse{StatusCode: resp.StatusCode, Err: err}
	}
	return nil
}

// DecodeParts decodes a response of positional parts into items.
func DecodeParts[T any](resp *http.Response, body []byte, p server.Parts) ([]T, error) {
	items, err := server.DecodePartsAs[T](body, resp.Header.Get("Content-Type"), p, oapi.Response)
	if err != nil {
		return nil, &InvalidResponse{StatusCode: resp.StatusCode, Err: err}
	}
	return items, nil
}

// Invalid reports a response that failed validation.
func Invalid(resp *http.Response, err error) error {
	if err == nil {
		return nil
	}
	return &InvalidResponse{StatusCode: resp.StatusCode, Err: err}
}

// MissingHeader reports a response without the required header name.
func MissingHeader(resp *http.Response, name string) error {
	return &InvalidResponse{StatusCode: resp.StatusCode, Err: fmt.Errorf("header %s is required", name)}
}

// Header decodes the response header name into dst with parse
// (server.ParseScalar or ParseArray), reporting whether it was present.
func Header[T any](resp *http.Response, name string, parse func([]string) (T, error), dst *T) (bool, error) {
	vals := resp.Header.Values(name)
	if len(vals) == 0 {
		return false, nil
	}
	v, err := parse(vals)
	if err != nil {
		return true, &InvalidResponse{StatusCode: resp.StatusCode, Err: fmt.Errorf("header %s: %w", name, err)}
	}
	*dst = v
	return true, nil
}

// SplitHeader splits the values of a header into list items.
func SplitHeader(vals []string) []string {
	var items []string
	for _, v := range vals {
		for item := range strings.SplitSeq(v, ",") {
			items = append(items, strings.TrimSpace(item))
		}
	}
	return items
}

// JSONHeader decodes the response header name, serialized as JSON, into
// dst, reporting whether it was present.
func JSONHeader(resp *http.Response, name string, dst any) (bool, error) {
	v := resp.Header.Get(name)
	if v == "" {
		return false, nil
	}
	if err := oapi.DecodeResponse([]byte(v), dst); err != nil {
		return true, &InvalidResponse{StatusCode: resp.StatusCode, Err: fmt.Errorf("header %s: %w", name, err)}
	}
	return true, nil
}

// ContentHeader reads the header name of resp, serialized with a media
// type, into dst with decode, reporting whether it is present.
func ContentHeader[T any](resp *http.Response, name string, decode func(string) (T, error), dst *T) (bool, error) {
	return Header(resp, name, func(vals []string) (T, error) { return decode(strings.Join(vals, ", ")) }, dst)
}

// JSONLines returns the items of a JSON Lines response as they arrive.
// The body is closed when the loop ends; a loop that is never run leaves
// it open, so range over the items or close resp.Body.
func JSONLines[T any](resp *http.Response) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
		for sc.Scan() {
			line := sc.Bytes()
			if len(strings.TrimSpace(string(line))) == 0 {
				continue
			}
			var item T
			err := oapi.DecodeResponse(line, &item)
			if !yield(item, err) || err != nil {
				return
			}
		}
		if err := sc.Err(); err != nil {
			var zero T
			yield(zero, err)
		}
	}
}

// Events returns the events of a server-sent event response as they
// arrive. The body is closed when the loop ends, as with JSONLines.
func Events(resp *http.Response) iter.Seq2[server.Event, error] {
	return func(yield func(server.Event, error) bool) {
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
		for ev, err := range server.ScanEvents(sc) {
			if !yield(ev, err) {
				return
			}
		}
	}
}

// TypedEvents returns the events of a server-sent event response whose
// data is typed, as they arrive: decode turns each into its TypedEvent,
// checking its data if c validates responses. An event that does not
// decode ends the loop with a *InvalidResponse. The body is closed when
// the loop ends, as with JSONLines.
func TypedEvents[T any](c *Config, resp *http.Response, decode func(ev server.Event, validate bool) (server.TypedEvent[T], error)) iter.Seq2[server.TypedEvent[T], error] {
	return func(yield func(server.TypedEvent[T], error) bool) {
		i := 0
		for ev, err := range Events(resp) {
			if err != nil {
				yield(server.TypedEvent[T]{}, err)
				return
			}
			typed, err := decode(ev, c.ValidateResponses)
			if err != nil {
				yield(server.TypedEvent[T]{}, &InvalidResponse{StatusCode: resp.StatusCode, Err: fmt.Errorf("event %d: %w", i, err)})
				return
			}
			if !yield(typed, nil) {
				return
			}
			i++
		}
	}
}
