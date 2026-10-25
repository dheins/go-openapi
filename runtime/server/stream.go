package server

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"iter"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/problem"
)

// BodyMediaType returns the pattern of mediaTypes (as ReadBody takes them)
// that the request's content type matches, without reading the body, for
// operations whose bodies may be streams. It returns false, recording why
// on b, for a missing required body or an unsupported content type. A body
// is absent only when the request has none (a Content-Length of 0, or
// http.NoBody): a stream is not read ahead to find out.
func (c *Config) BodyMediaType(r *http.Request, b *Binder, mediaTypes []string, required bool) (string, bool) {
	if r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0 {
		if required {
			b.errs = append(b.errs, problem.Error{In: "body", Detail: "request body is required"})
		}
		return "", false
	}
	return matchBody(r, b, mediaTypes)
}

// ReadAll reads a body BodyMediaType matched, for a media type that is not
// a stream, as ReadBody does.
func (c *Config) ReadAll(w http.ResponseWriter, r *http.Request, b *Binder, required bool) ([]byte, bool) {
	body, ok := c.readAll(w, r, b)
	if ok && len(body) == 0 {
		if required {
			b.errs = append(b.errs, problem.Error{In: "body", Detail: "request body is required"})
		}
		return nil, false
	}
	return body, ok
}

func (c *Config) readAll(w http.ResponseWriter, r *http.Request, b *Binder) ([]byte, bool) {
	if r.Body == nil {
		return nil, true
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, c.MaxBodyBytes))
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		b.fatal = problem.Newf(http.StatusRequestEntityTooLarge, "the request body is larger than %d bytes", tooLarge.Limit)
		return nil, false
	case err != nil:
		b.fatal = problem.New(http.StatusBadRequest, "the request body could not be read")
		return nil, false
	}
	return body, true
}

// matchBody returns the pattern of mediaTypes the request's content type
// matches, the first one if it names none.
func matchBody(r *http.Request, b *Binder, mediaTypes []string) (string, bool) {
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		return mediaTypes[0], true
	}
	mt, _, err := mime.ParseMediaType(ct)
	if err == nil {
		for _, pattern := range mediaTypes {
			if matchMediaType(mt, []string{pattern}) {
				return pattern, true
			}
		}
	}
	b.fatal = problem.Newf(http.StatusUnsupportedMediaType, "content type %q is not supported; use %s", ct, strings.Join(mediaTypes, " or "))
	return "", false
}

// JSONLinesBody returns the items of a JSON Lines request body, read as
// the handler ranges over them: each line is decoded as a request and
// checked with validate, if not nil. Blank lines are skipped. A line may
// be at most MaxBodyBytes long; the stream itself has no limit.
//
// A problem ends the loop with a *BindError, which the handler can
// return as it is: 400 for an invalid item, with pointers into it from
// its index ("/2/name"), or 413 for a line that is too long. The items
// can be ranged over once.
func JSONLinesBody[T any](c *Config, r *http.Request, validate func(T) error) iter.Seq2[T, error] {
	once := false
	return func(yield func(T, error) bool) {
		var zero T
		if once {
			yield(zero, errors.New("the request body was read already"))
			return
		}
		once = true
		sc := c.streamScanner(r.Body)
		for i := 0; sc.Scan(); {
			line := sc.Bytes()
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			var item T
			err := oapi.DecodeRequest(line, &item)
			if err == nil && validate != nil {
				err = validate(item)
			}
			if err != nil {
				var errs oapi.ValidationErrors
				errs.Nested("/"+strconv.Itoa(i), err)
				var b Binder
				b.Body(errs.Err())
				yield(zero, b.Err())
				return
			}
			if !yield(item, nil) {
				return
			}
			i++
		}
		if err := c.streamErr(sc.Err()); err != nil {
			yield(zero, err)
		}
	}
}

// EventsBody returns the server-sent events of a request body, read as
// the handler ranges over them. A line may be at most MaxBodyBytes long;
// a longer one ends the loop with a *BindError (413), as JSONLinesBody
// does. The events can be ranged over once.
func EventsBody(c *Config, r *http.Request) iter.Seq2[Event, error] {
	once := false
	return func(yield func(Event, error) bool) {
		if once {
			yield(Event{}, errors.New("the request body was read already"))
			return
		}
		once = true
		sc := c.streamScanner(r.Body)
		for ev, err := range ScanEvents(sc) {
			if err != nil {
				yield(Event{}, c.streamErr(err))
				return
			}
			if !yield(ev, nil) {
				return
			}
		}
	}
}

func (c *Config) streamScanner(body io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, min(64<<10, c.MaxBodyBytes+1)), int(c.MaxBodyBytes)+1)
	return sc
}

// streamErr is the *BindError of a stream that could not be read.
func (c *Config) streamErr(err error) error {
	var b Binder
	switch {
	case err == nil:
		return nil
	case errors.Is(err, bufio.ErrTooLong):
		b.fatal = problem.Newf(http.StatusRequestEntityTooLarge, "a line of the request body is longer than %d bytes", c.MaxBodyBytes)
	default:
		b.fatal = problem.New(http.StatusBadRequest, "the request body could not be read")
	}
	return b.Err()
}

// ScanEvents returns the server-sent events of the lines sc reads, as
// the text/event-stream format defines them, and then the error sc
// stopped with, if any. It splits sc's input at the format's line ends
// (CRLF, LF or CR), so sc must not have been scanned yet.
func ScanEvents(sc *bufio.Scanner) iter.Seq2[Event, error] {
	sc.Split(scanEventLines)
	return func(yield func(Event, error) bool) {
		var ev Event
		var data []string
		pending := false
		for sc.Scan() {
			line := sc.Text()
			if line == "" {
				if pending {
					ev.Data = strings.Join(data, "\n")
					if !yield(ev, nil) {
						return
					}
				}
				ev, data, pending = Event{}, nil, false
				continue
			}
			if strings.HasPrefix(line, ":") {
				continue
			}
			name, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			pending = true
			switch name {
			case "id":
				ev.ID = value
			case "event":
				ev.Event = value
			case "data":
				data = append(data, value)
			case "retry":
				// Only digits; Atoi would take signs.
				if value != "" && strings.Trim(value, "0123456789") == "" {
					if n, err := strconv.Atoi(value); err == nil {
						ev.Retry = n
					}
				}
			}
		}
		if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
			yield(Event{}, err)
		}
	}
}

// scanEventLines is a bufio.SplitFunc for the line ends of
// text/event-stream: CRLF, LF or CR.
func scanEventLines(data []byte, atEOF bool) (int, []byte, error) {
	i := bytes.IndexAny(data, "\r\n")
	switch {
	case i < 0 && atEOF && len(data) > 0:
		return len(data), data, nil
	case i < 0:
		return 0, nil, nil
	case data[i] == '\n':
		return i + 1, data[:i], nil
	case i+1 < len(data) && data[i+1] == '\n':
		return i + 2, data[:i], nil
	case i+1 == len(data) && !atEOF:
		// A LF may follow the CR.
		return 0, nil, nil
	}
	return i + 1, data[:i], nil
}

// eventLines splits a field value at the line ends of text/event-stream.
var eventLines = strings.NewReplacer("\r\n", "\n", "\r", "\n")

// AppendEvent appends ev in the text/event-stream format, ending with
// the blank line that dispatches it. Data is sent as a data line per
// line; line ends in ID and Event, which would start other fields, are
// sent as spaces.
func AppendEvent(dst []byte, ev Event) []byte {
	field := func(name, value string) {
		dst = append(dst, name...)
		dst = append(dst, ": "...)
		dst = append(dst, value...)
		dst = append(dst, '\n')
	}
	if ev.ID != "" {
		field("id", strings.ReplaceAll(eventLines.Replace(ev.ID), "\n", " "))
	}
	if ev.Event != "" {
		field("event", strings.ReplaceAll(eventLines.Replace(ev.Event), "\n", " "))
	}
	if ev.Retry > 0 {
		field("retry", strconv.Itoa(ev.Retry))
	}
	for line := range strings.SplitSeq(eventLines.Replace(ev.Data), "\n") {
		field("data", line)
	}
	return append(dst, '\n')
}

// FullDuplex lets a handler keep reading a streamed request body after it
// starts a streamed response, which HTTP/1.x servers do not allow by
// default. HTTP/2 is always full duplex, so not being able to enable it
// is not an error.
func FullDuplex(w http.ResponseWriter) {
	_ = http.NewResponseController(w).EnableFullDuplex()
}
