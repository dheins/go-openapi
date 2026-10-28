package server

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/dheins/go-openapi/oapi"
)

// FormFields parses an application/x-www-form-urlencoded body.
func FormFields(body []byte) (map[string][]string, error) {
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, &oapi.ValidationError{Message: "invalid form body: " + err.Error()}
	}
	return values, nil
}

// DecodeMultipart decodes a multipart/form-data body into T. Values are
// converted like ObjectJSON; parts of properties of type "file", with or
// without a file name, are read into the oapi.File fields of T (File,
// Optional[File], *File, []File or Optional[[]File]) with the matching
// JSON name. A part must have one of its property's ContentTypes, if it
// declares a content type, and carry its Headers.
func DecodeMultipart[T any](body []byte, contentType string, obj Property) (T, error) {
	var v T
	obj = obj.shape()
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil || params["boundary"] == "" {
		return v, &oapi.ValidationError{Message: "multipart body without a boundary"}
	}
	fields := map[string][]string{}
	files := map[string][]oapi.File{}
	var errs oapi.ValidationErrors
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return v, &oapi.ValidationError{Message: "invalid multipart body: " + err.Error()}
		}
		name := part.FormName()
		if name == "" {
			continue
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return v, &oapi.ValidationError{Message: "invalid multipart body: " + err.Error()}
		}
		prop := obj.Properties[name]
		if msg := checkPart(part.Header, prop); msg != "" {
			errs.Add(oapi.Field("", name), msg)
			continue
		}
		_, declared := obj.Properties[name]
		switch {
		case prop.Type == "file":
			files[name] = append(files[name], oapi.File{Name: part.FileName(), ContentType: part.Header.Get("Content-Type"), Header: part.Header, Data: data})
			// A placeholder, so that decoding sees the property as present.
			fields[name] = append(fields[name], "")
		case part.FileName() == "" || declared:
			// A part of a declared value is read as one even with a file
			// name: browsers' FormData names every Blob part a file, as
			// JavaScript clients send JSON values.
			fields[name] = append(fields[name], string(data))
		}
	}
	if err := errs.Err(); err != nil {
		return v, err
	}
	data, err := ObjectJSON(fields, obj)
	if err != nil {
		return v, err
	}
	if err := oapi.DecodeRequest(data, &v); err != nil {
		return v, err
	}
	return v, setFiles(&v, files)
}

// checkPart checks a part's headers against its encoding, returning what
// is wrong or "".
func checkPart(header textproto.MIMEHeader, prop Property) string {
	if ct := header.Get("Content-Type"); ct != "" && len(prop.ContentTypes) > 0 {
		mt, _, err := mime.ParseMediaType(ct)
		if err != nil || !matchMediaType(mt, prop.ContentTypes) {
			return fmt.Sprintf("content type %q is not allowed; expected %s", ct, strings.Join(prop.ContentTypes, ", "))
		}
	}
	for _, h := range prop.Headers {
		if msg := h.check(header); msg != "" {
			return msg
		}
	}
	return ""
}

var (
	fileType      = reflect.TypeFor[oapi.File]()
	fileSliceType = reflect.TypeFor[[]oapi.File]()
)

// CheckFileHeaders checks the headers of the files of v, a multipart body,
// against the encodings of the properties of obj, as a server reading the
// body does: in their oapi.File.Header. Problems are located at the
// property (/report), or its item (/scans/1).
func CheckFileHeaders(v any, obj Property) error {
	var errs oapi.ValidationErrors
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer && !rv.IsNil() {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil
	}
	obj = obj.shape()
	for i := range rv.NumField() {
		name, _, _ := strings.Cut(rv.Type().Field(i).Tag.Get("json"), ",")
		prop, ok := obj.Properties[name]
		if !ok || prop.Type != "file" || len(prop.Headers) == 0 {
			continue
		}
		files := heldFiles(rv.Field(i))
		for j, file := range files {
			at := oapi.Field("", name)
			if prop.Array || len(files) > 1 {
				at = oapi.Index(at, j)
			}
			if msg := checkPart(file.Header, Property{Headers: prop.Headers}); msg != "" {
				errs.Add(at, msg)
			}
		}
	}
	return errs.Err()
}

// CheckParts checks the headers of items, the parts of a multipart body of
// positional parts, against the encodings of p, by position: those of
// files (oapi.File.Header) and of oapi.Part items. Other items carry no
// headers to check.
func CheckParts[T any](items []T, p Parts) error {
	list := make([]partItem, len(items))
	for i := range items {
		list[i] = partItem{&items[i], p.Item}
	}
	return checkItems(list, p)
}

// CheckTuple checks the headers of the parts of v, a tuple as DecodeTuple
// reads it, against the encodings of p, by position, as CheckParts does.
func CheckTuple(v any, p Parts) error {
	items, err := tupleItems(v, p)
	if err != nil {
		return err
	}
	return checkItems(items, p)
}

func checkItems(items []partItem, p Parts) error {
	var errs oapi.ValidationErrors
	for i, item := range items {
		var header textproto.MIMEHeader
		switch v := item.ptr.(type) {
		case *oapi.File:
			header = v.Header
		case oapi.MIMEPart:
			h, _ := v.MIMEPart()
			header = *h
		default:
			continue
		}
		if msg := checkPart(header, Property{Headers: p.encoding(i).Headers}); msg != "" {
			errs.Add(oapi.Index("", i), msg)
		}
	}
	return errs.Err()
}

// heldFiles returns the files a field holds: one, several, or, inside an
// Optional that is not set or behind a nil pointer, none.
func heldFiles(f reflect.Value) []oapi.File {
	switch {
	case f.Type() == fileType:
		return []oapi.File{f.Interface().(oapi.File)}
	case f.Type() == fileSliceType:
		return f.Interface().([]oapi.File)
	case f.Kind() == reflect.Pointer:
		if f.IsNil() {
			return nil
		}
		return heldFiles(f.Elem())
	case f.Kind() == reflect.Struct && f.FieldByName("Set").IsValid() && f.FieldByName("Value").IsValid():
		if !f.FieldByName("Set").Bool() {
			return nil
		}
		return heldFiles(f.FieldByName("Value"))
	}
	return nil
}

// setFiles stores files in the fields of *dst whose JSON names match.
func setFiles(dst any, files map[string][]oapi.File) error {
	v := reflect.ValueOf(dst).Elem()
	if v.Kind() != reflect.Struct {
		return nil
	}
	for i := range v.NumField() {
		name, _, _ := strings.Cut(v.Type().Field(i).Tag.Get("json"), ",")
		got, ok := files[name]
		if !ok || len(got) == 0 {
			continue
		}
		if err := storeFiles(v.Field(i), got); err != nil {
			return &oapi.ValidationError{Path: oapi.Field("", name), Message: err.Error()}
		}
	}
	return nil
}

// storeFiles assigns files to a field holding one or more oapi.File,
// possibly inside an Optional (Value and Set) or behind a pointer.
func storeFiles(f reflect.Value, files []oapi.File) error {
	switch {
	case f.Type() == fileType:
		if len(files) != 1 {
			return fmt.Errorf("expected one file, got %d", len(files))
		}
		f.Set(reflect.ValueOf(files[0]))
	case f.Type() == fileSliceType:
		f.Set(reflect.ValueOf(files))
	case f.Kind() == reflect.Pointer && f.Type().Elem() == fileType:
		if len(files) != 1 {
			return fmt.Errorf("expected one file, got %d", len(files))
		}
		file := files[0]
		f.Set(reflect.ValueOf(&file))
	case f.Kind() == reflect.Struct && f.FieldByName("Set").IsValid() && f.FieldByName("Value").IsValid():
		if err := storeFiles(f.FieldByName("Value"), files); err != nil {
			return err
		}
		f.FieldByName("Set").SetBool(true)
	default:
		return fmt.Errorf("cannot store a file in %s", f.Type())
	}
	return nil
}

// WriteText sends a text body, as UTF-8 unless the content type names
// a charset.
func WriteText(w http.ResponseWriter, status int, contentType, body string, header http.Header) error {
	if !strings.Contains(strings.ToLower(contentType), "charset=") {
		contentType += "; charset=utf-8"
	}
	return WriteBytes(w, status, contentType, []byte(body), header)
}

// CheckContentType checks the content type a handler chose for a
// response declared with a media type range such as "image/*".
func CheckContentType(contentType, declared string) error {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil || !matchMediaType(mt, []string{declared}) {
		return fmt.Errorf("content type %q does not match the declared %q", contentType, declared)
	}
	return nil
}

// WriteBytes sends a binary body.
func WriteBytes(w http.ResponseWriter, status int, contentType string, body []byte, header http.Header) error {
	applyHeader(w, header)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	// The status line is sent; a failed write means the client is gone.
	_, _ = w.Write(body)
	return nil
}

// WriteJSONLines streams items as JSON Lines (one JSON value per line),
// flushing after each, leaving out write-only properties. Once the first
// item is sent the status cannot change, so a later error ends the
// stream and is logged.
func WriteJSONLines[T any](c *Config, w http.ResponseWriter, r *http.Request, status int, contentType string, items iter.Seq2[T, error], header http.Header) error {
	if items == nil {
		return errors.New("streaming response without items")
	}
	applyHeader(w, header)
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	bw := bufio.NewWriter(w)
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		// Send the headers now, so the client sees the stream open before
		// the first item, which may take a while.
		flusher.Flush()
	}
	for item, err := range items {
		if err != nil {
			c.Log(r.Context(), slog.LevelError, "streaming response failed", "path", r.URL.Path, "error", err)
			break
		}
		data, err := oapi.EncodeResponse(item)
		if err != nil {
			c.Log(r.Context(), slog.LevelError, "encoding streamed item", "path", r.URL.Path, "error", err)
			break
		}
		bw.Write(data)
		bw.WriteByte('\n')
		if bw.Flush() != nil {
			// The client is gone.
			return nil
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	return nil
}

// Event is a server-sent event (text/event-stream).
type Event struct {
	ID    string
	Event string
	// Data is sent as one or more data lines.
	Data string
	// Retry, if positive, tells the client how many milliseconds to wait
	// before reconnecting.
	Retry int
}

// JSONEvent returns an event whose data is v encoded as JSON for a
// response.
func JSONEvent(event string, v any) (Event, error) {
	data, err := oapi.EncodeResponse(v)
	if err != nil {
		return Event{}, err
	}
	return Event{Event: event, Data: string(data)}, nil
}

// WriteEvents streams server-sent events, flushing after each. A later
// error ends the stream and is logged.
func WriteEvents(c *Config, w http.ResponseWriter, r *http.Request, status int, events iter.Seq2[Event, error], header http.Header) error {
	if events == nil {
		return errors.New("streaming response without events")
	}
	applyHeader(w, header)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(status)
	bw := bufio.NewWriter(w)
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		// Send the headers now, so the client sees the stream open before
		// the first item, which may take a while.
		flusher.Flush()
	}
	for ev, err := range events {
		if err != nil {
			c.Log(r.Context(), slog.LevelError, "event stream failed", "path", r.URL.Path, "error", err)
			break
		}
		bw.Write(AppendEvent(nil, ev))
		if bw.Flush() != nil {
			return nil
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	return nil
}

// Negotiate returns the offer the request's Accept header prefers, or ""
// if it accepts none of them. Without an Accept header the first offer
// wins. Offers are media types such as "application/json".
func Negotiate(r *http.Request, offers ...string) string {
	accept := r.Header.Values("Accept")
	if len(accept) == 0 {
		if len(offers) > 0 {
			return offers[0]
		}
		return ""
	}
	type rangeQ struct {
		mediaRange string
		q          float64
	}
	var ranges []rangeQ
	for _, header := range accept {
		for part := range strings.SplitSeq(header, ",") {
			mt, params, err := mime.ParseMediaType(strings.TrimSpace(part))
			if err != nil {
				continue
			}
			q := 1.0
			if qs, ok := params["q"]; ok {
				if parsed, err := strconv.ParseFloat(qs, 64); err == nil {
					q = parsed
				}
			}
			ranges = append(ranges, rangeQ{mt, q})
		}
	}
	best, bestQ, bestSpecific := "", 0.0, -1
	for _, offer := range offers {
		for _, rq := range ranges {
			if rq.q <= 0 || !matchMediaType(offer, []string{rq.mediaRange}) {
				continue
			}
			// More specific ranges count first: type/subtype over type/* over */*.
			specific := 2 - strings.Count(rq.mediaRange, "*")
			if rq.q > bestQ || (rq.q == bestQ && specific > bestSpecific) {
				best, bestQ, bestSpecific = offer, rq.q, specific
			}
		}
	}
	return best
}
