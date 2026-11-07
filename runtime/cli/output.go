package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"reflect"
	"slices"
	"strings"

	"github.com/dheins/go-openapi/runtime/client"
	"github.com/dheins/go-openapi/runtime/problem"
)

// captured is the last response the client read, and its body, kept as
// the client reads it; streams are not kept, as their items are printed.
type captured struct {
	resp *http.Response
	body bytes.Buffer
}

func (c *captured) middleware(next client.Doer) client.Doer {
	return client.DoerFunc(func(r *http.Request) (*http.Response, error) {
		resp, err := next.Do(r)
		if err != nil {
			return nil, err
		}
		c.resp = resp
		c.body.Reset()
		if !streaming(resp.Header.Get("Content-Type")) {
			resp.Body = struct {
				io.Reader
				io.Closer
			}{io.TeeReader(resp.Body, &c.body), resp.Body}
		}
		return resp, nil
	})
}

// streaming reports whether a media type is a stream: JSON Lines or
// server-sent events.
func streaming(contentType string) bool {
	mt, _, _ := mime.ParseMediaType(contentType)
	return mt == "application/jsonl" || mt == "application/x-ndjson" || mt == "text/event-stream"
}

// describe writes the status line and headers of the response.
func (c *captured) describe(w io.Writer) {
	fmt.Fprintf(w, "%s %s\n", c.resp.Proto, c.resp.Status)
	names := make([]string, 0, len(c.resp.Header))
	for name := range c.resp.Header {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		for _, v := range c.resp.Header[name] {
			fmt.Fprintf(w, "%s: %s\n", name, v)
		}
	}
	fmt.Fprintln(w)
}

// print writes the body of resp, a response the client read: the items
// of a stream, one JSON value a line, as they come; otherwise the body
// as it was sent, JSON indented.
func print(resp any, c *captured, w io.Writer) error {
	if stream, ok := streamOf(resp); ok {
		return printStream(stream, w)
	}
	body := c.body.Bytes()
	if len(body) == 0 {
		return nil
	}
	if c.resp != nil && isJSON(c.resp.Header.Get("Content-Type")) {
		var out bytes.Buffer
		if json.Indent(&out, body, "", "  ") == nil {
			out.WriteByte('\n')
			_, err := w.Write(out.Bytes())
			return err
		}
	}
	_, err := w.Write(body)
	return err
}

func isJSON(contentType string) bool {
	mt, _, _ := mime.ParseMediaType(contentType)
	return mt == "application/json" || strings.HasSuffix(mt, "+json")
}

// streamOf returns the field of resp holding an iter.Seq2 of items and
// errors, as streamed responses have, if it has one.
func streamOf(resp any) (reflect.Value, bool) {
	v := reflect.ValueOf(resp)
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return reflect.Value{}, false
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return reflect.Value{}, false
	}
	for i := range v.NumField() {
		f := v.Field(i)
		t := f.Type()
		if t.Kind() == reflect.Func && t.NumIn() == 1 && t.NumOut() == 0 {
			yield := t.In(0)
			if yield.Kind() == reflect.Func && yield.NumIn() == 2 && yield.In(1) == reflect.TypeFor[error]() && !f.IsNil() {
				return f, true
			}
		}
	}
	return reflect.Value{}, false
}

// printStream writes the items of stream, an iter.Seq2[T, error], until
// it ends or yields an error.
func printStream(stream reflect.Value, w io.Writer) error {
	var failed error
	yield := reflect.MakeFunc(stream.Type().In(0), func(args []reflect.Value) []reflect.Value {
		if err, _ := args[1].Interface().(error); err != nil {
			failed = err
			return []reflect.Value{reflect.ValueOf(false)}
		}
		line, err := json.Marshal(args[0].Interface())
		if err == nil {
			_, err = w.Write(append(line, '\n'))
		}
		if err != nil {
			failed = err
			return []reflect.Value{reflect.ValueOf(false)}
		}
		return []reflect.Value{reflect.ValueOf(true)}
	})
	stream.Call([]reflect.Value{yield})
	return failed
}

// failure reports err, from sending a request, and returns the exit code:
// an invalid request with its problems; a response the document does not
// declare with its status and body.
func failure(prefix string, err error, stdout, stderr io.Writer) int {
	var invalid *client.InvalidRequest
	var unexpected *client.UnexpectedResponse
	switch {
	case errors.As(err, &invalid):
		fmt.Fprintf(stderr, "%s: the request is invalid\n", prefix)
		writeProblem(stderr, invalid.Problem)
	case errors.As(err, &unexpected):
		fmt.Fprintf(stderr, "%s: %v\n", prefix, err)
		if unexpected.Problem != nil {
			writeProblem(stderr, unexpected.Problem)
		} else if len(unexpected.Body) > 0 {
			_, _ = stdout.Write(unexpected.Body)
		}
	default:
		fmt.Fprintf(stderr, "%s: %v\n", prefix, err)
	}
	return ExitFailure
}

// writeProblem writes the errors of a problem, one a line.
func writeProblem(w io.Writer, p *problem.Problem) {
	if p == nil {
		return
	}
	for _, e := range p.Errors {
		where := e.In
		if e.Parameter != "" {
			where += " " + e.Parameter
		}
		if e.Pointer != "" {
			where += " " + e.Pointer
		}
		fmt.Fprintf(w, "  %s: %s\n", strings.TrimSpace(where), e.Detail)
	}
}
