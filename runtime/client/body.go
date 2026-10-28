package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"mime/multipart"
	"net/textproto"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/server"
)

// JSONBody sets the body to v encoded as JSON, leaving out read-only
// properties.
func (r *Request) JSONBody(contentType string, v any) {
	data, err := oapi.EncodeRequest(v)
	if err != nil {
		r.fail(fmt.Errorf("body: %w", err))
		return
	}
	r.body, r.contentType = data, contentType
}

// FormBody sets the body to v, an object, as form fields: nested objects
// with brackets (a[b]=v), arrays as repeated fields.
func (r *Request) FormBody(contentType string, v any) {
	fields, err := server.ObjectFields(v, true, oapi.Request)
	if err != nil {
		r.fail(fmt.Errorf("body: %w", err))
		return
	}
	form := url.Values{}
	for _, f := range fields {
		for _, val := range f.Values {
			form.Add(f.Name(), val)
		}
	}
	r.body, r.contentType = []byte(form.Encode()), contentType
}

// TextBody sets the body to s.
func (r *Request) TextBody(contentType, s string) {
	if !strings.Contains(strings.ToLower(contentType), "charset=") {
		contentType += "; charset=utf-8"
	}
	r.body, r.contentType = []byte(s), contentType
}

// BytesBody sets the body to data.
func (r *Request) BytesBody(contentType string, data []byte) {
	r.body, r.contentType = data, contentType
}

// XMLBody sets the body to v encoded as JSON, leaving out read-only
// properties, and written as the XML document root describes.
func (r *Request) XMLBody(contentType string, v any, root *server.XMLNode) {
	data, err := oapi.EncodeRequest(v)
	if err == nil {
		data, err = server.JSONToXML(data, root)
	}
	if err != nil {
		r.fail(fmt.Errorf("body: %w", err))
		return
	}
	r.body, r.contentType = data, contentType
}

// JSONLinesBody sets the body to items as JSON Lines, each encoded
// (leaving out read-only properties) and, if c validates requests,
// checked with validate as it is sent. An invalid item, or an error items
// yields, ends the request with that error: a *InvalidRequest, with
// pointers into the item from its index ("/2/name"), for an invalid one.
// A stream cannot be sent again, so middleware cannot retry it.
func JSONLinesBody[T any](c *Config, r *Request, contentType string, items iter.Seq2[T, error], validate func(T) error) {
	if !c.ValidateRequests {
		validate = nil
	}
	r.contentType = contentType
	r.stream = func(w io.Writer) error {
		i := 0
		for item, err := range items {
			if err != nil {
				return err
			}
			if validate != nil {
				if err := validate(item); err != nil {
					var errs oapi.ValidationErrors
					errs.Nested("/"+strconv.Itoa(i), err)
					var b server.Binder
					b.Body(errs.Err())
					return &InvalidRequest{Problem: b.Problem()}
				}
			}
			data, err := oapi.EncodeRequest(item)
			if err != nil {
				return fmt.Errorf("body: item %d: %w", i, err)
			}
			if _, err := w.Write(append(data, '\n')); err != nil {
				return err
			}
			i++
		}
		return nil
	}
}

// EventsBody sets the body to events as server-sent events, written as
// they are sent. An error events yields ends the request with it.
func (r *Request) EventsBody(events iter.Seq2[server.Event, error]) {
	r.contentType = "text/event-stream"
	r.stream = func(w io.Writer) error {
		for ev, err := range events {
			if err != nil {
				return err
			}
			if _, err := w.Write(server.AppendEvent(nil, ev)); err != nil {
				return err
			}
		}
		return nil
	}
}

// TypedEventsBody sets the body to events as server-sent events, each
// written by encode, or with its data as JSON if encode is nil, and, if c
// validates requests, its data checked with check first. An invalid
// event, or an error events yields, ends the request with that error: a
// *InvalidRequest, with pointers into the event from its index
// ("/2/data/name"), for an invalid one.
func TypedEventsBody[T any](c *Config, r *Request, events iter.Seq2[server.TypedEvent[T], error], encode func(server.TypedEvent[T]) (server.Event, error), check func(T) error) {
	if !c.ValidateRequests {
		check = nil
	}
	if encode == nil {
		encode = func(ev server.TypedEvent[T]) (server.Event, error) {
			return server.EncodeEvent(ev, "", ev.Data, oapi.Request)
		}
	}
	r.EventsBody(func(yield func(server.Event, error) bool) {
		i := 0
		for typed, err := range events {
			if err == nil && check != nil {
				if verr := check(typed.Data); verr != nil {
					var errs oapi.ValidationErrors
					errs.Nested("/"+strconv.Itoa(i)+"/data", verr)
					var b server.Binder
					b.Body(errs.Err())
					err = &InvalidRequest{Problem: b.Problem()}
				}
			}
			var ev server.Event
			if err == nil {
				if ev, err = encode(typed); err != nil {
					err = fmt.Errorf("body: event %d: %w", i, err)
				}
			}
			if !yield(ev, err) || err != nil {
				return
			}
			i++
		}
	})
}

// streamBody is a request body written as the transport reads it. The
// writer starts on the first read, so a request that is never sent
// leaves nothing running, and Close stops it.
type streamBody struct {
	write  func(io.Writer) error
	once   sync.Once
	reader *io.PipeReader
}

func (s *streamBody) Read(p []byte) (int, error) {
	s.once.Do(s.start)
	if s.reader == nil {
		return 0, io.ErrClosedPipe
	}
	return s.reader.Read(p)
}

func (s *streamBody) Close() error {
	// Closed before it is read, it never starts.
	s.once.Do(func() {})
	if s.reader == nil {
		return nil
	}
	return s.reader.Close()
}

func (s *streamBody) start() {
	pr, pw := io.Pipe()
	s.reader = pr
	go func() { pw.CloseWithError(s.write(pw)) }()
}

// PartsBody sets the body to items as a multipart body of positional
// parts (multipart/mixed and the like), as p describes.
func PartsBody[T any](r *Request, contentType string, items []T, p server.Parts) {
	data, ct, err := server.EncodeParts(items, contentType, p, oapi.Request)
	if err != nil {
		r.fail(fmt.Errorf("body: %w", err))
		return
	}
	r.body, r.contentType = data, ct
}

// TupleBody sets the body to v, a tuple of parts (prefixItems) as
// server.DecodeTuple reads it, as a multipart body of positional parts.
func TupleBody(r *Request, contentType string, v any, p server.Parts) {
	data, ct, err := server.EncodeTuple(v, contentType, p, oapi.Request)
	if err != nil {
		r.fail(fmt.Errorf("body: %w", err))
		return
	}
	r.body, r.contentType = data, ct
}

// MultipartBody sets the body to v, a struct or a map with string keys, as
// multipart/form-data: a part per property, files (properties of type file
// in obj, or oapi.File values) with their name, content type and headers,
// others as text, or as JSON for objects, with the first content type the
// property's encoding names. Absent optional properties are left out.
func (r *Request) MultipartBody(v any, obj server.Property) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	write := func(name string, fv reflect.Value, prop server.Property) bool {
		fv, present := presentValue(fv)
		if !present {
			return true
		}
		var err error
		if prop.Type == "file" || fv.Type() == fileType || (fv.Kind() == reflect.Slice && fv.Type().Elem() == fileType) {
			err = writeFiles(mw, name, fv, prop)
		} else {
			err = writeValue(mw, name, fv, prop)
		}
		if err != nil {
			r.fail(fmt.Errorf("body: %s: %w", name, err))
			return false
		}
		return true
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	switch {
	case rv.Kind() == reflect.Struct:
		for i := range rv.NumField() {
			name, _, _ := strings.Cut(rv.Type().Field(i).Tag.Get("json"), ",")
			if name == "" || name == "-" {
				continue
			}
			if !write(name, rv.Field(i), obj.Properties[name]) {
				return
			}
		}
	case rv.Kind() == reflect.Map && rv.Type().Key().Kind() == reflect.String:
		// Sorted, so that the same value gives the same body.
		keys := rv.MapKeys()
		slices.SortFunc(keys, func(a, b reflect.Value) int { return strings.Compare(a.String(), b.String()) })
		for _, k := range keys {
			prop, ok := obj.Properties[k.String()]
			if !ok && obj.Additional != nil {
				prop = *obj.Additional
			}
			fv := rv.MapIndex(k)
			for fv.Kind() == reflect.Interface && !fv.IsNil() {
				fv = fv.Elem()
			}
			// A null value has no part.
			if fv.Kind() == reflect.Interface {
				continue
			}
			if !write(k.String(), fv, prop) {
				return
			}
		}
	default:
		r.fail(fmt.Errorf("body: a multipart body must be a struct or a map, not %T", v))
		return
	}
	if err := mw.Close(); err != nil {
		r.fail(err)
		return
	}
	r.body, r.contentType = buf.Bytes(), mw.FormDataContentType()
}

// presentValue returns the value a field holds: through Optional (if
// Set) and pointers (if not nil); false when there is none.
func presentValue(v reflect.Value) (reflect.Value, bool) {
	for {
		switch {
		case v.Kind() == reflect.Pointer:
			if v.IsNil() {
				return v, false
			}
			v = v.Elem()
		case v.Kind() == reflect.Struct && v.FieldByName("Set").IsValid() && v.FieldByName("Value").IsValid() && v.Type().PkgPath() == optionalPkg:
			if !v.FieldByName("Set").Bool() {
				return v, false
			}
			v = v.FieldByName("Value")
		default:
			return v, true
		}
	}
}

var optionalPkg = reflect.TypeFor[oapi.Optional[int]]().PkgPath()

var fileType = reflect.TypeFor[oapi.File]()

// writeFiles writes a file part per oapi.File in v (a File or []File).
func writeFiles(mw *multipart.Writer, name string, v reflect.Value, prop server.Property) error {
	var files []oapi.File
	switch {
	case v.Type() == fileType:
		files = []oapi.File{v.Interface().(oapi.File)}
	case v.Kind() == reflect.Slice && v.Type().Elem() == fileType:
		files = v.Interface().([]oapi.File)
	default:
		return fmt.Errorf("expected oapi.File, got %s", v.Type())
	}
	for _, f := range files {
		h := textproto.MIMEHeader{}
		for k, vs := range f.Header {
			h[k] = vs
		}
		disposition := fmt.Sprintf("form-data; name=%q", name)
		if f.Name != "" {
			disposition += fmt.Sprintf("; filename=%q", f.Name)
		}
		h.Set("Content-Disposition", disposition)
		ct := f.ContentType
		if ct == "" {
			ct = declaredType(prop, "application/octet-stream")
		}
		h.Set("Content-Type", ct)
		pw, err := mw.CreatePart(h)
		if err != nil {
			return err
		}
		// Writing to a buffer cannot fail.
		_, _ = pw.Write(f.Data)
	}
	return nil
}

// writeValue writes the part (or parts, for an array) of a value that is
// not a file: its JSON text, unquoted for a string, JSON for an object.
func writeValue(mw *multipart.Writer, name string, v reflect.Value, prop server.Property) error {
	data, err := oapi.EncodeRequest(v.Interface())
	if err != nil {
		return err
	}
	items := []json.RawMessage{data}
	if len(data) > 0 && data[0] == '[' {
		// One part per item, as an array is sent in multipart bodies.
		if err := json.Unmarshal(data, &items); err != nil {
			return err
		}
	}
	for _, item := range items {
		val := server.FieldText(item)
		ct := "text/plain; charset=utf-8"
		if len(item) > 0 && item[0] == '{' {
			ct = "application/json"
		}
		ct = declaredType(prop, ct)
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf("form-data; name=%q", name))
		h.Set("Content-Type", ct)
		pw, err := mw.CreatePart(h)
		if err != nil {
			return err
		}
		// Writing to a buffer cannot fail.
		_, _ = pw.Write([]byte(val))
	}
	return nil
}

// declaredType returns the first content type a part's encoding names, if
// it is not a range, or def.
func declaredType(prop server.Property, def string) string {
	if len(prop.ContentTypes) > 0 && !strings.Contains(prop.ContentTypes[0], "*") {
		return prop.ContentTypes[0]
	}
	return def
}
