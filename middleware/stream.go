package middleware

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"iter"
	"strconv"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dheins/go-openapi/internal/schemas"
	"github.com/dheins/go-openapi/ir"
	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/oas"
	"github.com/dheins/go-openapi/runtime/server"
)

// stream checks the items of a JSON Lines or server-sent events body
// against their schemas, as generated servers do.
type stream struct {
	events bool
	// item checks a line of JSON Lines; nil checks none.
	item *jsonschema.Schema
	// kinds check the data of events by the event's name, or of every
	// event under "" for a stream of one kind; names are the names, in
	// document order, for the error of an unknown one.
	kinds map[string]eventKind
	names []string
}

// eventKind checks the data of a kind of event: as JSON, or as text.
type eventKind struct {
	schema *jsonschema.Schema
	text   bool
}

// stream returns how to check a stream body of kind (jsonl or sse), of
// media type m, typed by events for sse; nil if it has nothing to check.
func (b *builder) stream(kind string, m *oas.MediaType, events *ir.EventStream) *stream {
	switch kind {
	case "jsonl":
		item := m.ItemSchema
		if item == nil && m.Schema != nil {
			item = deref(m.Schema).Items
		}
		if item == nil {
			return nil
		}
		return &stream{item: b.set.Compile(item)}
	case "sse":
		item := deref(m.ItemSchema)
		if events == nil || item == nil {
			return nil
		}
		s := &stream{events: true, kinds: map[string]eventKind{}}
		if len(events.Kinds) == 0 {
			s.kinds[""] = eventKind{schema: b.set.Compile(eventData(item).ContentSchema)}
			return s
		}
		members := item.OneOf
		if len(members) == 0 {
			members = item.AnyOf
		}
		// The IR types the events only if every member is a kind, so
		// they are in the order of its kinds.
		for i, k := range events.Kinds {
			data := eventData(deref(members[i]))
			schema := data.ContentSchema
			if k.Text {
				schema = data
			}
			s.kinds[k.Name] = eventKind{schema: b.set.Compile(schema), text: k.Text}
			s.names = append(s.names, k.Name)
		}
		return s
	}
	return nil
}

// eventData returns the schema of the data property of an event schema,
// which the IR has checked is there.
func eventData(item *oas.Schema) *oas.Schema {
	data, _ := item.Properties.Get("data")
	return deref(data)
}

// deref returns the schema s refers to.
func deref(s *oas.Schema) *oas.Schema {
	for seen := map[*oas.Schema]bool{}; s != nil && s.Resolved != nil && !seen[s]; s = s.Resolved {
		seen[s] = true
	}
	return s
}

// check returns what is wrong with an item of the stream, the bytes of a
// line or of an event with the blank line ending it, sent in direction
// dir, located at its index; counted says whether it is an item, which
// blank lines and comments are not.
func (s *stream) check(set *schemas.Set, raw []byte, index int, dir schemas.Direction) (counted bool, err error) {
	if !s.events {
		if len(bytes.TrimSpace(raw)) == 0 {
			return false, nil
		}
		_, err = check(set, s.item, raw, dir)
		return true, nested(index, err)
	}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(nil, len(raw)+1)
	for ev, scanErr := range server.ScanEvents(sc) {
		if scanErr != nil {
			return true, scanErr
		}
		kind, ok := s.kinds[""]
		if len(s.names) > 0 {
			if kind, ok = s.kinds[ev.Event]; !ok {
				return true, nested(index, server.UnknownEvent(ev.Event, s.names...))
			}
		}
		data := []byte(ev.Data)
		if kind.text {
			data, _ = json.Marshal(ev.Data)
		}
		_, err = check(set, kind.schema, data, dir)
		if err != nil {
			var errs oapi.ValidationErrors
			errs.Nested("/data", err)
			err = errs.Err()
		}
		return true, nested(index, err)
	}
	return false, nil
}

// nested locates err at the item index of a stream.
func nested(index int, err error) error {
	var errs oapi.ValidationErrors
	errs.Nested("/"+strconv.Itoa(index), err)
	return errs.Err()
}

// splitter cuts a stream into its items as its bytes arrive: the lines of
// JSON Lines, or the events of server-sent events up to the blank line
// ending each, each with the line end it was sent with.
type splitter struct {
	events  bool
	pending []byte
	// line is where the line being read starts in pending, and searched
	// how far it has been searched for its end; longest is the length of
	// the longest line of the item being read, and of the last item once
	// it is cut, for the limit on lines.
	line, searched, longest int
}

func (s *splitter) write(p []byte) { s.pending = append(s.pending, p...) }

// lineLen is the length of the line being read, for the limit on lines.
func (s *splitter) lineLen() int { return len(s.pending) - s.line }

// next returns the next complete item, or false if pending holds none;
// at EOF, what is left is the last one.
func (s *splitter) next(atEOF bool) ([]byte, bool) {
	ends := "\n"
	if s.events {
		// Lines of text/event-stream end with CRLF, LF or CR.
		ends = "\r\n"
	}
	for {
		i := bytes.IndexAny(s.pending[s.searched:], ends)
		if i < 0 {
			s.searched = len(s.pending)
			break
		}
		i += s.searched
		end := i + 1
		if s.pending[i] == '\r' {
			if end == len(s.pending) && !atEOF {
				// A LF may follow the CR.
				s.searched = i
				break
			}
			if end < len(s.pending) && s.pending[end] == '\n' {
				end++
			}
		}
		blank := i == s.line
		s.longest = max(s.longest, i-s.line)
		s.line, s.searched = end, end
		if !s.events || blank {
			return s.cut(end), true
		}
	}
	if atEOF && len(s.pending) > 0 {
		s.longest = max(s.longest, s.lineLen())
		return s.cut(len(s.pending)), true
	}
	return nil, false
}

// cut returns the first n bytes of pending and removes them.
func (s *splitter) cut(n int) []byte {
	item := s.pending[:n:n]
	s.pending = s.pending[n:]
	s.line, s.searched = s.line-n, s.searched-n
	return item
}

// checkedBody is a request body whose items are checked as the handler
// reads it: it hands over an item only once it is checked, and ends with
// the *server.BindError of the first one that is invalid, or of a line
// longer than the limit, instead of reading on.
type checkedBody struct {
	body io.ReadCloser
	next func() ([]byte, error, bool)
	stop func()
	buf  []byte
	err  error
}

// newCheckedBody checks the items of body against s as they are read,
// with lines of at most cfg.MaxBodyBytes.
func newCheckedBody(cfg *server.Config, set *schemas.Set, s *stream, body io.ReadCloser) *checkedBody {
	next, stop := iter.Pull2(s.items(cfg, set, body))
	return &checkedBody{body: body, next: next, stop: stop}
}

// items reads body, yielding each item once it is checked.
func (s *stream) items(cfg *server.Config, set *schemas.Set, body io.Reader) iter.Seq2[[]byte, error] {
	return func(yield func([]byte, error) bool) {
		sp := &splitter{events: s.events}
		chunk := make([]byte, 32<<10)
		index := 0
		for eof := false; ; {
			item, ok := sp.next(eof)
			if !ok {
				if eof {
					return
				}
				if int64(sp.lineLen()) > cfg.MaxBodyBytes {
					yield(nil, cfg.StreamError(bufio.ErrTooLong))
					return
				}
				n, err := body.Read(chunk)
				sp.write(chunk[:n])
				switch {
				case err == io.EOF:
					eof = true
				case err != nil:
					yield(nil, cfg.StreamError(err))
					return
				}
				continue
			}
			if int64(sp.longest) > cfg.MaxBodyBytes {
				yield(nil, cfg.StreamError(bufio.ErrTooLong))
				return
			}
			sp.longest = 0
			counted, err := s.check(set, item, index, schemas.Request)
			if err != nil {
				var b server.Binder
				b.Body(err)
				yield(nil, b.Err())
				return
			}
			if counted {
				index++
			}
			if !yield(item, nil) {
				return
			}
		}
	}
}

func (c *checkedBody) Read(p []byte) (int, error) {
	for len(c.buf) == 0 {
		if c.err != nil {
			return 0, c.err
		}
		item, err, ok := c.next()
		switch {
		case !ok:
			c.err = io.EOF
		case err != nil:
			c.err = err
		default:
			c.buf = item
		}
	}
	n := copy(p, c.buf)
	c.buf = c.buf[n:]
	return n, nil
}

func (c *checkedBody) Close() error {
	c.stop()
	return c.body.Close()
}
