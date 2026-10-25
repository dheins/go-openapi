package server

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
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

// TypedEvent is a server-sent event whose data is a T, sent as JSON. For
// a stream of named kinds of event, Event names the kind: it is set when
// reading, and may be left "" when writing, as the data's variant names
// it.
type TypedEvent[T any] struct {
	ID    string
	Event string
	Retry int
	Data  T
}

// EventData decodes the data of ev in direction dir into a T, checking it
// with check if validate is set. Problems are located in the event, as
// "/data/name".
func EventData[T any](ev Event, dir oapi.Direction, validate bool, check func(T) error) (T, error) {
	var v T
	err := oapi.DecodeValue([]byte(ev.Data), &v, dir)
	if err == nil && validate && check != nil {
		err = check(v)
	}
	if err != nil {
		var errs oapi.ValidationErrors
		errs.Nested("/data", err)
		return v, errs.Err()
	}
	return v, nil
}

// EventText returns the data of ev, text, as a T, checking it with check
// if validate is set, with problems located as EventData's.
func EventText[T ~string](ev Event, validate bool, check func(T) error) (T, error) {
	v := T(ev.Data)
	if validate && check != nil {
		if err := check(v); err != nil {
			var errs oapi.ValidationErrors
			errs.Nested("/data", err)
			return v, errs.Err()
		}
	}
	return v, nil
}

// EncodeTextEvent returns ev with text as its data, of the kind of event
// kind, as EncodeEvent does for JSON.
func EncodeTextEvent[T any](ev TypedEvent[T], kind, text string) (Event, error) {
	if ev.Event != "" && ev.Event != kind {
		return Event{}, fmt.Errorf("the event %q holds the data of %q", ev.Event, kind)
	}
	return Event{ID: ev.ID, Event: kind, Retry: ev.Retry, Data: text}, nil
}

// UnknownEvent is the error of an event named none of the kinds of its
// stream.
func UnknownEvent(name string, kinds ...string) error {
	quoted := make([]string, len(kinds))
	for i, k := range kinds {
		quoted[i] = strconv.Quote(k)
	}
	return &oapi.ValidationError{Path: "/event", Message: fmt.Sprintf("unknown event %q; expected %s", name, strings.Join(quoted, ", "))}
}

// EncodeEvent returns ev with data, its data, as JSON in direction dir.
// kind is the name of the data's kind of event, or "" for a stream of one
// kind; ev.Event, if set, must be it.
func EncodeEvent[T any](ev TypedEvent[T], kind string, data any, dir oapi.Direction) (Event, error) {
	name := ev.Event
	switch {
	case kind == "":
	case name == "":
		name = kind
	case name != kind:
		return Event{}, fmt.Errorf("the event %q holds the data of %q", name, kind)
	}
	b, err := oapi.EncodeValue(data, dir)
	if err != nil {
		return Event{}, err
	}
	return Event{ID: ev.ID, Event: name, Retry: ev.Retry, Data: string(b)}, nil
}

// TypedEventsBody returns the events of a request body whose data is
// typed, read as the handler ranges over them like EventsBody's. decode
// turns each into its TypedEvent: it chooses the kind by the event's
// name, then decodes and validates the data. A problem ends the loop with
// a *BindError, 400 with pointers into the event from its index
// ("/2/data/name"). The events can be ranged over once.
func TypedEventsBody[T any](c *Config, r *http.Request, decode func(ev Event, validate bool) (TypedEvent[T], error)) iter.Seq2[TypedEvent[T], error] {
	events := EventsBody(c, r)
	return func(yield func(TypedEvent[T], error) bool) {
		i := 0
		for ev, err := range events {
			if err != nil {
				yield(TypedEvent[T]{}, err)
				return
			}
			typed, err := decode(ev, true)
			if err != nil {
				var errs oapi.ValidationErrors
				errs.Nested("/"+strconv.Itoa(i), err)
				var b Binder
				b.Body(errs.Err())
				yield(TypedEvent[T]{}, b.Err())
				return
			}
			if !yield(typed, nil) {
				return
			}
			i++
		}
	}
}

// WriteTypedEvents streams events as WriteEvents does, each written by
// encode, or with its data as JSON if encode is nil. An event that cannot
// be written ends the stream, and is logged.
func WriteTypedEvents[T any](c *Config, w http.ResponseWriter, r *http.Request, status int, events iter.Seq2[TypedEvent[T], error], header http.Header, encode func(TypedEvent[T]) (Event, error)) error {
	if events == nil {
		return errors.New("streaming response without events")
	}
	if encode == nil {
		encode = func(ev TypedEvent[T]) (Event, error) { return EncodeEvent(ev, "", ev.Data, oapi.Response) }
	}
	return WriteEvents(c, w, r, status, func(yield func(Event, error) bool) {
		for typed, err := range events {
			var ev Event
			if err == nil {
				ev, err = encode(typed)
			}
			if !yield(ev, err) || err != nil {
				return
			}
		}
	}, header)
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
