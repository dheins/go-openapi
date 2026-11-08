package cli

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"iter"
	"mime"
	"mime/multipart"
	"os"
	"reflect"
	"strings"

	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/server"
)

// maxLine is the longest line of a streamed body the CLI reads.
const maxLine = 64 << 20

// JSONLines returns the items of a JSON Lines body read from r as they
// are sent, for a command's Media.Stream: a line that is not a T ends
// them with an error. Blank lines are skipped.
func JSONLines[T any](r io.Reader) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		var zero T
		sc := bufio.NewScanner(r)
		sc.Buffer(nil, maxLine)
		for i := 0; sc.Scan(); {
			line := sc.Bytes()
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			var item T
			if err := oapi.DecodeRequest(line, &item); err != nil {
				yield(zero, fmt.Errorf("item %d: %w", i, err))
				return
			}
			if !yield(item, nil) {
				return
			}
			i++
		}
		if err := sc.Err(); err != nil {
			yield(zero, err)
		}
	}
}

// Events returns the server-sent events of a body in the
// text/event-stream format read from r as they are sent, for a command's
// Media.Stream.
func Events(r io.Reader) iter.Seq2[server.Event, error] {
	return func(yield func(server.Event, error) bool) {
		sc := bufio.NewScanner(r)
		sc.Buffer(nil, maxLine)
		for ev, err := range server.ScanEvents(sc) {
			if !yield(ev, err) || err != nil {
				return
			}
		}
	}
}

// TypedEvents returns the events of a body in the text/event-stream
// format read from r, each decoded by decode (the generated client's
// decoder of the operation's events), for a command's Media.Stream.
func TypedEvents[T any](r io.Reader, decode func(ev server.Event, validate bool) (server.TypedEvent[T], error)) iter.Seq2[server.TypedEvent[T], error] {
	return func(yield func(server.TypedEvent[T], error) bool) {
		i := 0
		for ev, err := range Events(r) {
			if err != nil {
				yield(server.TypedEvent[T]{}, err)
				return
			}
			// The client checks the events it sends.
			typed, err := decode(ev, false)
			if err != nil {
				yield(server.TypedEvent[T]{}, fmt.Errorf("event %d: %w", i, err))
				return
			}
			if !yield(typed, nil) {
				return
			}
			i++
		}
	}
}

// openSource opens a flag's value to be read as it is sent: @file, stdin
// for -, or the value itself. Closing it closes the file.
func openSource(v string) (io.ReadCloser, error) {
	switch {
	case v == "-":
		return io.NopCloser(os.Stdin), nil
	case strings.HasPrefix(v, "@"):
		return os.Open(v[1:])
	}
	return io.NopCloser(strings.NewReader(v)), nil
}

// partStream returns an iter.Seq2 of type t, a stream of parts, reading
// them from r, a multipart document, as they are sent: its boundary is
// contentType's, or that of its first line.
func partStream(t reflect.Type, r io.Reader, contentType string) reflect.Value {
	item := t.In(0).In(0)
	return reflect.MakeFunc(t, func(args []reflect.Value) []reflect.Value {
		yield := args[0]
		fail := func(err error) {
			yield.Call([]reflect.Value{reflect.Zero(item), reflect.ValueOf(&err).Elem()})
		}
		br := bufio.NewReader(r)
		boundary := ""
		if _, params, err := mime.ParseMediaType(contentType); err == nil {
			boundary = params["boundary"]
		}
		if boundary == "" {
			first, err := br.Peek(min(br.Size(), 200))
			if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
				fail(err)
				return nil
			}
			line, _, _ := bytes.Cut(bytes.TrimLeft(first, "\r\n"), []byte("\n"))
			b, ok := strings.CutPrefix(strings.TrimRight(string(line), "\r"), "--")
			if !ok || b == "" {
				fail(errors.New("-body is not a multipart document: give its boundary with -content-type"))
				return nil
			}
			boundary = b
		}
		mr := multipart.NewReader(br, boundary)
		noErr := reflect.Zero(reflect.TypeFor[error]())
		for i := 0; ; i++ {
			p, err := mr.NextRawPart()
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err == nil {
				var data []byte
				if data, err = io.ReadAll(p); err == nil {
					v := reflect.New(item).Elem()
					if err = setPart(v, partValue{header: p.Header, name: p.FileName(), contentType: p.Header.Get("Content-Type"), data: data}); err == nil {
						if !yield.Call([]reflect.Value{v, noErr})[0].Bool() {
							return nil
						}
						continue
					}
				}
			}
			fail(fmt.Errorf("part %d: %w", i+1, err))
			return nil
		}
	})
}
