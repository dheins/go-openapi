package server

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"reflect"
	"strconv"
	"strings"

	"github.com/dheins/go-openapi/oapi"
)

// Parts describes a multipart body whose parts are positional, such as
// multipart/mixed or multipart/related: how each part becomes an item,
// and the encodings of the parts by position.
type Parts struct {
	// Item is "file" (the part itself, an oapi.File), "text" (its body as
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
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for i, item := range items {
		h, data, partType, err := encodeItem(item.ptr, item.kind, dir)
		if err != nil {
			return nil, "", fmt.Errorf("part %d: %w", i, err)
		}
		if declared := p.encoding(i).ContentTypes; len(declared) > 0 && !strings.Contains(declared[0], "*") {
			partType = declared[0]
		}
		h.Set("Content-Type", partType)
		pw, err := mw.CreatePart(h)
		if err != nil {
			return nil, "", err
		}
		// Writing to a buffer cannot fail.
		_, _ = pw.Write(data)
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	mt, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, "", fmt.Errorf("content type %q: %w", contentType, err)
	}
	params["boundary"] = mw.Boundary()
	return buf.Bytes(), mime.FormatMediaType(mt, params), nil
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
		file := ptr.(*oapi.File)
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
	}
	data, err := oapi.EncodeValue(src, dir)
	if err != nil {
		return nil, nil, "", err
	}
	return h, data, "application/json", nil
}
