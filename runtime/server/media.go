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

// maxMultipartMemory is how much of a multipart body is kept in memory;
// the rest of the parts go to temporary files.
const maxMultipartMemory = 32 << 20

// DecodeMultipart decodes a multipart/form-data body into T. Values are
// converted like ObjectJSON; parts of properties of type "file" are read
// into the oapi.File fields of T (File, Optional[File], *File, []File or
// Optional[[]File]) with the matching JSON name.
func DecodeMultipart[T any](body []byte, contentType string, props map[string]Property) (T, error) {
	var v T
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil || params["boundary"] == "" {
		return v, &oapi.ValidationError{Message: "multipart body without a boundary"}
	}
	form, err := multipart.NewReader(bytes.NewReader(body), params["boundary"]).ReadForm(maxMultipartMemory)
	if err != nil {
		return v, &oapi.ValidationError{Message: "invalid multipart body: " + err.Error()}
	}
	defer form.RemoveAll()

	fields := make(map[string][]string, len(form.Value)+len(form.File))
	for name, vals := range form.Value {
		fields[name] = vals
	}
	files := map[string][]oapi.File{}
	for name, headers := range form.File {
		if props[name].Type != "file" {
			continue
		}
		for _, fh := range headers {
			f, err := readPart(fh)
			if err != nil {
				return v, &oapi.ValidationError{Path: oapi.Field("", name), Message: err.Error()}
			}
			files[name] = append(files[name], f)
			// A placeholder, so that decoding sees the property as present.
			fields[name] = append(fields[name], "")
		}
	}
	data, err := ObjectJSON(fields, props)
	if err != nil {
		return v, err
	}
	if err := oapi.DecodeRequest(data, &v); err != nil {
		return v, err
	}
	return v, setFiles(&v, files)
}

func readPart(fh *multipart.FileHeader) (oapi.File, error) {
	f, err := fh.Open()
	if err != nil {
		return oapi.File{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return oapi.File{}, err
	}
	return oapi.File{Name: fh.Filename, ContentType: fh.Header.Get("Content-Type"), Data: data}, nil
}

var (
	fileType      = reflect.TypeFor[oapi.File]()
	fileSliceType = reflect.TypeFor[[]oapi.File]()
)

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
		if ev.ID != "" {
			fmt.Fprintf(bw, "id: %s\n", ev.ID)
		}
		if ev.Event != "" {
			fmt.Fprintf(bw, "event: %s\n", ev.Event)
		}
		if ev.Retry > 0 {
			fmt.Fprintf(bw, "retry: %d\n", ev.Retry)
		}
		for line := range strings.SplitSeq(ev.Data, "\n") {
			fmt.Fprintf(bw, "data: %s\n", line)
		}
		bw.WriteByte('\n')
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
