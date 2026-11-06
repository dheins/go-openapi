package server

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"reflect"
	"strconv"
	"strings"

	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/problem"
)

// Parts describes a multipart body whose parts are positional, such as
// multipart/mixed or multipart/related: how each part becomes an item,
// and the encodings of the parts by position.
type Parts struct {
	// Item is "file" (the part itself, an oapi.File), "bytes" (its body as
	// a []byte, sent base64 with a Content-Transfer-Encoding and read as
	// its own says), "text" (its body as
	// a string) or "json" (its body decoded as JSON).
	Item string
	// Kinds are those of the first parts of a tuple (prefixItems), by
	// position, as Item is: see DecodeTuple. Item is then that of the
	// parts after them.
	Kinds []string
	// Prefix are the encodings of the first parts (prefixEncoding), Rest
	// that of the others (itemEncoding): the content types each may
	// declare and the headers it must carry.
	Prefix []Property
	Rest   Property
}

// encoding returns the encoding of the part at index i.
func (p Parts) encoding(i int) Property {
	if i < len(p.Prefix) {
		return p.Prefix[i]
	}
	return p.Rest
}

// kind returns how the part at index i of a tuple becomes an item.
func (p Parts) kind(i int) string {
	if i < len(p.Kinds) {
		return p.Kinds[i]
	}
	return p.Item
}

// DecodeParts decodes a multipart body of positional parts into items of
// type T, as p describes, checking each part against its encoding.
// Problems are reported at the index of the part.
func DecodeParts[T any](body []byte, contentType string, p Parts) ([]T, error) {
	return DecodePartsAs[T](body, contentType, p, oapi.Request)
}

// DecodePartsAs decodes positional parts like DecodeParts, with text and
// JSON items decoded in direction dir: oapi.Response for a client reading
// a response.
func DecodePartsAs[T any](body []byte, contentType string, p Parts, dir oapi.Direction) ([]T, error) {
	if _, ok := any(new(T)).(*oapi.File); p.Item == "file" && !ok {
		return nil, fmt.Errorf("parts of type file need oapi.File items, not %T", *new(T))
	}
	items := []T{}
	var errs oapi.ValidationErrors
	err := eachPart(body, contentType, func(i int, part *multipart.Part, data []byte) {
		path := oapi.Index("", i)
		if msg := checkPart(part.Header, p.encoding(i)); msg != "" {
			errs.Add(path, msg)
			return
		}
		var item T
		errs.Nested(path, decodeItem(&item, p.Item, part, data, dir))
		items = append(items, item)
	})
	if err != nil {
		return nil, err
	}
	if err := errs.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// DecodeTuple decodes a multipart body of positional parts with
// prefixItems into T: a struct with a field per position, tagged
// part:"0", part:"1" and so on, and, if more parts may follow, a slice
// field tagged part:"rest". Each position is a file, text or JSON as
// p.Kinds says, and the rest as p.Item says. A position whose field is an
// oapi.Optional may be absent; a missing part, or one more than T holds,
// is reported at its index, like other problems.
func DecodeTuple[T any](body []byte, contentType string, p Parts, dir oapi.Direction) (T, error) {
	var v T
	rv := reflect.ValueOf(&v).Elem()
	prefix, rest, err := tupleFields(rv.Type(), p)
	if err != nil {
		return v, err
	}
	var errs oapi.ValidationErrors
	count := 0
	err = eachPart(body, contentType, func(i int, part *multipart.Part, data []byte) {
		count = i + 1
		path := oapi.Index("", i)
		if msg := checkPart(part.Header, p.encoding(i)); msg != "" {
			errs.Add(path, msg)
			return
		}
		var dst reflect.Value
		switch {
		case i < len(prefix):
			dst = rv.Field(prefix[i])
			if inner, ok := optionalValue(dst); ok {
				dst.FieldByName("Set").SetBool(true)
				dst = inner
			}
		case rest >= 0:
			items := rv.Field(rest)
			items.Set(reflect.Append(items, reflect.Zero(items.Type().Elem())))
			dst = items.Index(items.Len() - 1)
		default:
			errs.Add(path, fmt.Sprintf("unexpected part: the body has at most %d", len(prefix)))
			return
		}
		errs.Nested(path, decodeItem(dst.Addr().Interface(), p.kind(i), part, data, dir))
	})
	if err != nil {
		return v, err
	}
	for i := count; i < len(prefix); i++ {
		if _, ok := optionalValue(rv.Field(prefix[i])); !ok {
			errs.Add(oapi.Index("", i), "part is missing")
		}
	}
	return v, errs.Err()
}

// eachPart reads the parts of a multipart body, calling fn with each
// part's index, the part and its data.
func eachPart(body []byte, contentType string, fn func(i int, part *multipart.Part, data []byte)) error {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil || params["boundary"] == "" {
		return &oapi.ValidationError{Message: "multipart body without a boundary"}
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for i := 0; ; i++ {
		part, err := reader.NextPart()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return &oapi.ValidationError{Message: "invalid multipart body: " + err.Error()}
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return &oapi.ValidationError{Message: "invalid multipart body: " + err.Error()}
		}
		fn(i, part, data)
	}
}

// decodeItem decodes a part into dst, a pointer, as kind says: the part
// itself for a file (dst is an *oapi.File), its text, or its body as JSON.
// An oapi.Part keeps the part's headers next to its value.
func decodeItem(dst any, kind string, part *multipart.Part, data []byte, dir oapi.Direction) error {
	if mp, ok := dst.(oapi.MIMEPart); ok && kind != "file" {
		var header *textproto.MIMEHeader
		header, dst = mp.MIMEPart()
		*header = part.Header
	}
	switch kind {
	case "file":
		*dst.(*oapi.File) = oapi.File{Name: part.FileName(), ContentType: part.Header.Get("Content-Type"), Header: part.Header, Data: data}
		return nil
	case "text":
		// Through JSON, so that enums and formats are checked as usual.
		return oapi.DecodeValue(oapi.AppendString(nil, string(data)), dst, dir)
	case "bytes":
		raw, err := transferDecoded(part.Header, data)
		if err != nil {
			return &oapi.ValidationError{Message: err.Error()}
		}
		// Through JSON too, where bytes are base64, for types of their own.
		return oapi.DecodeValue(oapi.AppendString(nil, base64.StdEncoding.EncodeToString(raw)), dst, dir)
	}
	return oapi.DecodeValue(data, dst, dir)
}

// tupleFields returns the indices of the fields of t, a struct, by
// position (part:"0", ...), and that of its rest field (part:"rest"), or
// -1. Positions of files must be oapi.File fields, inside an Optional or
// not.
func tupleFields(t reflect.Type, p Parts) ([]int, int, error) {
	if t.Kind() != reflect.Struct {
		return nil, -1, fmt.Errorf("a tuple of parts needs a struct, not %s", t)
	}
	var prefix []int
	rest := -1
	byPosition := map[int]int{}
	for i := range t.NumField() {
		tag, ok := t.Field(i).Tag.Lookup("part")
		if !ok {
			continue
		}
		if tag == "rest" {
			rest = i
			continue
		}
		n, err := strconv.Atoi(tag)
		if err != nil || n < 0 {
			return nil, -1, fmt.Errorf("%s.%s: part tag %q is not a position", t, t.Field(i).Name, tag)
		}
		byPosition[n] = i
	}
	for n := range len(byPosition) {
		i, ok := byPosition[n]
		if !ok {
			return nil, -1, fmt.Errorf("%s: no field for part %d", t, n)
		}
		f := t.Field(i).Type
		if inner, ok := optionalType(f); ok {
			f = inner
		}
		if p.kind(n) == "file" && f != fileType {
			return nil, -1, fmt.Errorf("%s.%s: parts of type file need oapi.File fields, not %s", t, t.Field(i).Name, f)
		}
		prefix = append(prefix, i)
	}
	if rest >= 0 && (t.Field(rest).Type.Kind() != reflect.Slice || p.Item == "file" && t.Field(rest).Type.Elem() != fileType) {
		return nil, -1, fmt.Errorf("%s.%s: the rest of the parts need a slice of the items", t, t.Field(rest).Name)
	}
	return prefix, rest, nil
}

// optionalType returns the type an oapi.Optional of type t holds, and
// whether t is one.
func optionalType(t reflect.Type) (reflect.Type, bool) {
	if t.Kind() != reflect.Struct {
		return nil, false
	}
	set, ok := t.FieldByName("Set")
	value, okValue := t.FieldByName("Value")
	if !ok || !okValue || set.Type.Kind() != reflect.Bool || t.NumField() != 2 {
		return nil, false
	}
	return value.Type, true
}

// optionalValue returns the Value field of f if it is an oapi.Optional.
func optionalValue(f reflect.Value) (reflect.Value, bool) {
	if _, ok := optionalType(f.Type()); !ok {
		return reflect.Value{}, false
	}
	return f.FieldByName("Value"), true
}

// WriteParts sends items as a multipart body of positional parts, with
// the subtype of contentType (such as multipart/mixed) and a new
// boundary: see EncodeParts. JSON items leave out write-only properties.
// As with WriteJSON, nothing is written when encoding fails.
func WriteParts[T any](w http.ResponseWriter, status int, contentType string, items []T, p Parts, header http.Header) error {
	body, ct, err := EncodeParts(items, contentType, p, oapi.Response)
	return writeMultipart(w, status, body, ct, header, err)
}

// WriteTuple sends v, a tuple of parts as DecodeTuple reads it, as a
// multipart body of positional parts, like WriteParts.
func WriteTuple(w http.ResponseWriter, status int, contentType string, v any, p Parts, header http.Header) error {
	body, ct, err := EncodeTuple(v, contentType, p, oapi.Response)
	return writeMultipart(w, status, body, ct, header, err)
}

func writeMultipart(w http.ResponseWriter, status int, body []byte, ct string, header http.Header, err error) error {
	if err != nil {
		return fmt.Errorf("encoding the %d response: %w", status, err)
	}
	applyHeader(w, header)
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(status)
	// The status line is sent; a failed write means the client is gone.
	_, _ = w.Write(body)
	return nil
}

// partItem is an item to write as a part: a pointer to it, and its kind.
type partItem struct {
	ptr  any
	kind string
}

// EncodeParts encodes items as a multipart body of positional parts, in
// direction dir, and returns it with its content type: the subtype of
// contentType with a new boundary. Each part has the first content type
// its encoding names, else the file's own (application/octet-stream if
// none), text/plain for text and application/json for JSON.
func EncodeParts[T any](items []T, contentType string, p Parts, dir oapi.Direction) ([]byte, string, error) {
	if _, ok := any(new(T)).(*oapi.File); p.Item == "file" && !ok {
		return nil, "", fmt.Errorf("parts of type file need oapi.File items, not %T", *new(T))
	}
	list := make([]partItem, len(items))
	for i := range items {
		list[i] = partItem{&items[i], p.Item}
	}
	return encodeParts(list, contentType, p, dir)
}

// EncodeTuple encodes v, a tuple of parts as DecodeTuple reads it, like
// EncodeParts. An optional position that is not set ends the parts: the
// positions after it cannot be set.
func EncodeTuple(v any, contentType string, p Parts, dir oapi.Direction) ([]byte, string, error) {
	items, err := tupleItems(v, p)
	if err != nil {
		return nil, "", err
	}
	return encodeParts(items, contentType, p, dir)
}

// tupleItems returns the parts of v, a tuple, in order.
func tupleItems(v any, p Parts) ([]partItem, error) {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer && !rv.IsNil() {
		rv = rv.Elem()
	}
	// An addressable copy, whose oapi.Part fields give their headers.
	tuple := reflect.New(rv.Type()).Elem()
	tuple.Set(rv)
	prefix, rest, err := tupleFields(tuple.Type(), p)
	if err != nil {
		return nil, err
	}
	var items []partItem
	gap := -1
	for n, i := range prefix {
		f := tuple.Field(i)
		if inner, ok := optionalValue(f); ok {
			if !f.FieldByName("Set").Bool() {
				if gap < 0 {
					gap = n
				}
				continue
			}
			f = inner
		}
		if gap >= 0 {
			return nil, fmt.Errorf("part %d is set, but part %d before it is not", n, gap)
		}
		items = append(items, partItem{f.Addr().Interface(), p.kind(n)})
	}
	if rest >= 0 {
		more := tuple.Field(rest)
		if gap >= 0 && more.Len() > 0 {
			return nil, fmt.Errorf("parts after %d are set, but part %d is not", len(prefix)-1, gap)
		}
		for j := range more.Len() {
			items = append(items, partItem{more.Index(j).Addr().Interface(), p.Item})
		}
	}
	return items, nil
}

func encodeParts(items []partItem, contentType string, p Parts, dir oapi.Direction) ([]byte, string, error) {
	ct, boundary, err := PartsContentType(contentType)
	if err != nil {
		return nil, "", err
	}
	var buf bytes.Buffer
	pw := NewPartWriter(&buf, boundary, p, dir)
	for _, item := range items {
		if err := pw.write(item); err != nil {
			return nil, "", err
		}
	}
	if err := pw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), ct, nil
}

// PartsContentType returns the content type of a multipart body of the
// subtype of contentType with a new boundary, and the boundary, for a
// PartWriter.
func PartsContentType(contentType string) (string, string, error) {
	mt, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", "", fmt.Errorf("content type %q: %w", contentType, err)
	}
	params["boundary"] = multipart.NewWriter(io.Discard).Boundary()
	return mime.FormatMediaType(mt, params), params["boundary"], nil
}

// PartWriter writes positional parts one at a time, for a body streamed
// as its items are produced; each part is encoded as EncodeParts encodes
// it, by its position.
type PartWriter struct {
	mw  *multipart.Writer
	p   Parts
	dir oapi.Direction
	n   int
}

// NewPartWriter returns a PartWriter writing to w with boundary, from
// PartsContentType, the parts p describes in direction dir.
func NewPartWriter(w io.Writer, boundary string, p Parts, dir oapi.Direction) *PartWriter {
	mw := multipart.NewWriter(w)
	// A boundary from PartsContentType is valid.
	_ = mw.SetBoundary(boundary)
	return &PartWriter{mw: mw, p: p, dir: dir}
}

// Write writes the item ptr points to, of the kind p.Item names, as the
// next part.
func (pw *PartWriter) Write(ptr any) error {
	return pw.write(partItem{ptr, pw.p.Item})
}

// Check checks the headers of the item ptr points to against the
// encoding of the next part, as CheckParts does; problems are located at
// its index.
func (pw *PartWriter) Check(ptr any) error {
	var errs oapi.ValidationErrors
	if msg := checkItem(partItem{ptr, pw.p.Item}, pw.p.encoding(pw.n)); msg != "" {
		errs.Add(oapi.Index("", pw.n), msg)
	}
	return errs.Err()
}

// Close writes the end of the body.
func (pw *PartWriter) Close() error {
	return pw.mw.Close()
}

func (pw *PartWriter) write(item partItem) error {
	h, data, partType, err := encodeItem(item.ptr, item.kind, pw.dir)
	if err != nil {
		return fmt.Errorf("part %d: %w", pw.n, err)
	}
	if declared := pw.p.encoding(pw.n).ContentTypes; len(declared) > 0 && !strings.Contains(declared[0], "*") {
		partType = declared[0]
	}
	h.Set("Content-Type", partType)
	// Its length lets a reader take the part as soon as it arrives,
	// rather than when the next one starts: see ScanParts.
	h.Set("Content-Length", strconv.Itoa(len(data)))
	w, err := pw.mw.CreatePart(h)
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	pw.n++
	return nil
}

// ErrPartTooLarge ends ScanParts at a part longer than its limit.
var ErrPartTooLarge = errors.New("the part is too large")

// ScanParts returns the positional parts of a multipart body of
// contentType read from body, as items of type T, each read when the loop
// asks for it and decoded as DecodePartsAs decodes it. A part with a
// Content-Length, as PartWriter writes them, is complete when its data
// has arrived; others when the delimiter after them has. A part may be
// at most limit bytes long. The loop ends at the first problem: an
// oapi.ValidationErrors for an invalid body or part, located at the
// part's index, an error wrapping ErrPartTooLarge, or the error reading
// body failed with.
func ScanParts[T any](body io.Reader, contentType string, p Parts, dir oapi.Direction, limit int64) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		var zero T
		if _, ok := any(new(T)).(*oapi.File); p.Item == "file" && !ok {
			yield(zero, fmt.Errorf("parts of type file need oapi.File items, not %T", zero))
			return
		}
		var errs oapi.ValidationErrors
		_, params, err := mime.ParseMediaType(contentType)
		if err != nil || params["boundary"] == "" {
			errs.Add("", "multipart body without a boundary")
			yield(zero, errs.Err())
			return
		}
		sc := newPartScanner(body, params["boundary"], limit)
		for i := 0; ; i++ {
			header, data, err := sc.next()
			var malformed errMalformed
			switch {
			case err == io.EOF:
				return
			case errors.Is(err, ErrPartTooLarge):
				yield(zero, fmt.Errorf("part %d: %w", i, err))
				return
			case errors.As(err, &malformed):
				errs.Add("", "invalid multipart body: "+malformed.Error())
				yield(zero, errs.Err())
				return
			case err != nil:
				yield(zero, err)
				return
			}
			part := &multipart.Part{Header: header}
			var item T
			path := oapi.Index("", i)
			if msg := checkPart(part.Header, p.encoding(i)); msg != "" {
				errs.Add(path, msg)
			} else {
				errs.Nested(path, decodeItem(&item, p.Item, part, data, dir))
			}
			if err := errs.Err(); err != nil {
				yield(zero, err)
				return
			}
			if !yield(item, nil) {
				return
			}
		}
	}
}

// readRecorder records the error reading r failed with, which tells a
// body that could not be read from a malformed one.
type readRecorder struct {
	r   io.Reader
	err error
}

func (rr *readRecorder) Read(p []byte) (int, error) {
	n, err := rr.r.Read(p)
	if err != nil {
		rr.err = err
	}
	return n, err
}

// PartStreamBody returns the positional parts of a multipart request
// body, read as the handler ranges over them, as JSONLinesBody reads
// lines: each part is checked against its encoding, decoded as p.Item
// says, and checked with validate, if not nil. A part may be at most
// MaxBodyBytes long; the stream itself has no limit.
//
// A problem ends the loop with a *BindError, which the handler can
// return as it is: 400 for an invalid part, with pointers into it from
// its index ("/2/name"), or 413 for a part that is too long. The items
// can be ranged over once.
func PartStreamBody[T any](c *Config, r *http.Request, p Parts, validate func(T) error) iter.Seq2[T, error] {
	once := false
	return func(yield func(T, error) bool) {
		var zero T
		if once {
			yield(zero, errors.New("the request body was read already"))
			return
		}
		once = true
		i := 0
		for item, err := range ScanParts[T](r.Body, r.Header.Get("Content-Type"), p, oapi.Request, c.MaxBodyBytes) {
			if err == nil && validate != nil {
				var errs oapi.ValidationErrors
				errs.Nested(oapi.Index("", i), validate(item))
				err = errs.Err()
			}
			if err != nil {
				yield(zero, c.partErr(err))
				return
			}
			if !yield(item, nil) {
				return
			}
			i++
		}
	}
}

// partErr is the *BindError of a stream of parts that ScanParts, or
// validating a part, ended with.
func (c *Config) partErr(err error) error {
	var b Binder
	switch {
	case errors.Is(err, ErrPartTooLarge):
		b.fatal = problem.Newf(http.StatusRequestEntityTooLarge, "a part of the request body is longer than %d bytes", c.MaxBodyBytes)
	case errors.As(err, new(oapi.ValidationErrors)):
		b.Body(err)
	default:
		b.fatal = problem.New(http.StatusBadRequest, "the request body could not be read")
	}
	return b.Err()
}

// WritePartStream sends items as a multipart body of positional parts,
// as WriteParts does, writing and flushing each part as it is produced,
// as WriteJSONLines does. With c.ValidateResponses, the headers of each
// part are checked before it is sent. The status line is sent before the
// first item, so a problem after it (an error items yields, or a part
// that does not encode or is invalid) is logged and ends the body without
// its closing boundary, which tells the client it was cut short.
func WritePartStream[T any](c *Config, w http.ResponseWriter, r *http.Request, status int, contentType string, items iter.Seq2[T, error], p Parts, header http.Header) error {
	if items == nil {
		return errors.New("streaming response without items")
	}
	ct, boundary, err := PartsContentType(contentType)
	if err != nil {
		return fmt.Errorf("encoding the %d response: %w", status, err)
	}
	applyHeader(w, header)
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(status)
	bw := bufio.NewWriter(w)
	pw := NewPartWriter(bw, boundary, p, oapi.Response)
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}
	complete := true
	for item, err := range items {
		if err != nil {
			c.Log(r.Context(), slog.LevelError, "streaming response failed", "path", r.URL.Path, "error", err)
			complete = false
			break
		}
		if c.ValidateResponses {
			err = pw.Check(&item)
		}
		if err == nil {
			err = pw.Write(&item)
		}
		if err != nil {
			c.Log(r.Context(), slog.LevelError, "encoding streamed part", "path", r.URL.Path, "error", err)
			complete = false
			break
		}
		if bw.Flush() != nil {
			// The client is gone.
			return nil
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	if complete {
		_ = pw.Close()
	}
	// The status line is sent; a failed write means the client is gone.
	_ = bw.Flush()
	return nil
}

// encodeItem encodes the item ptr points to as a part of kind: its
// headers (a file's own, or an oapi.Part's), its data, and the content
// type it has unless its encoding names one.
func encodeItem(ptr any, kind string, dir oapi.Direction) (textproto.MIMEHeader, []byte, string, error) {
	h := textproto.MIMEHeader{}
	src := ptr
	if mp, ok := ptr.(oapi.MIMEPart); ok && kind != "file" {
		var header *textproto.MIMEHeader
		header, src = mp.MIMEPart()
		for k, vs := range *header {
			h[k] = vs
		}
	}
	switch kind {
	case "file":
		file, ok := ptr.(*oapi.File)
		if !ok {
			return nil, nil, "", fmt.Errorf("parts of type file need oapi.File items, not %T", ptr)
		}
		for k, vs := range file.Header {
			h[k] = vs
		}
		partType := file.ContentType
		if partType == "" {
			partType = "application/octet-stream"
		}
		return h, file.Data, partType, nil
	case "text":
		text, err := oapi.EncodeValue(src, dir)
		if err != nil {
			return nil, nil, "", err
		}
		var s string
		if err := oapi.Decode(text, &s); err != nil {
			return nil, nil, "", err
		}
		return h, []byte(s), "text/plain; charset=utf-8", nil
	case "bytes":
		raw, err := encodedBytes(src, dir)
		if err != nil {
			return nil, nil, "", err
		}
		h.Set("Content-Transfer-Encoding", "base64")
		return h, base64Lines(raw), "application/octet-stream", nil
	}
	data, err := oapi.EncodeValue(src, dir)
	if err != nil {
		return nil, nil, "", err
	}
	return h, data, "application/json", nil
}
