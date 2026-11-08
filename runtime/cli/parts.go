package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/dheins/go-openapi/oapi"
)

// partArg is a -part flag and the -part-header flags after it.
type partArg struct {
	source string
	header textproto.MIMEHeader
}

// partValue is a part to send: its headers, and its file name and media
// type when it is read from a file.
type partValue struct {
	header      textproto.MIMEHeader
	name        string
	contentType string
	data        []byte
}

// readParts reads the parts of -part flags: @file, - for stdin, or the
// text itself.
func readParts(args []partArg) ([]partValue, error) {
	out := make([]partValue, len(args))
	for i, a := range args {
		data, err := readSource(a.source)
		if err != nil {
			return nil, fmt.Errorf("-part %d: %w", i+1, err)
		}
		p := partValue{header: a.header, data: data}
		if path, ok := strings.CutPrefix(a.source, "@"); ok {
			p.name = filepath.Base(path)
			p.contentType = mime.TypeByExtension(filepath.Ext(path))
		}
		if ct := p.header.Get("Content-Type"); ct != "" {
			p.contentType = ct
		}
		out[i] = p
	}
	return out, nil
}

// multipartParts reads the parts of data, a multipart document: its
// boundary is contentType's, or else that of its first line.
func multipartParts(data []byte, contentType string) ([]partValue, error) {
	boundary := ""
	if _, params, err := mime.ParseMediaType(contentType); err == nil {
		boundary = params["boundary"]
	}
	if boundary == "" {
		line, _, _ := bytes.Cut(bytes.TrimLeft(data, "\r\n"), []byte("\n"))
		b, ok := strings.CutPrefix(strings.TrimRight(string(line), "\r"), "--")
		if !ok || b == "" {
			return nil, errors.New("the body is not a multipart document: give its boundary with -content-type")
		}
		boundary = b
	}
	r := multipart.NewReader(bytes.NewReader(data), boundary)
	var out []partValue
	for {
		p, err := r.NextRawPart()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("part %d: %w", len(out)+1, err)
		}
		content, err := io.ReadAll(p)
		if err != nil {
			return nil, fmt.Errorf("part %d: %w", len(out)+1, err)
		}
		out = append(out, partValue{header: p.Header, name: p.FileName(), contentType: p.Header.Get("Content-Type"), data: content})
	}
}

// setParts sets target, the body of positional parts (a struct of
// positions tagged part:"0", ... and part:"rest", a slice, or an
// iter.Seq2 of items), from parts in order.
func setParts(target reflect.Value, parts []partValue) error {
	t := target.Type()
	switch {
	case t.Kind() == reflect.Slice:
		items := reflect.MakeSlice(t, len(parts), len(parts))
		for i, p := range parts {
			if err := setPart(items.Index(i), p); err != nil {
				return fmt.Errorf("part %d: %w", i+1, err)
			}
		}
		target.Set(items)
		return nil
	case isSeq2(t):
		items := reflect.MakeSlice(reflect.SliceOf(t.In(0).In(0)), len(parts), len(parts))
		for i, p := range parts {
			if err := setPart(items.Index(i), p); err != nil {
				return fmt.Errorf("part %d: %w", i+1, err)
			}
		}
		target.Set(seqOf(t, items))
		return nil
	case t.Kind() == reflect.Struct:
		return setTuple(target, parts)
	}
	return fmt.Errorf("cannot send parts as %s", t)
}

// setTuple sets the positions of a struct of parts in order, and its
// rest field to the parts after them.
func setTuple(target reflect.Value, parts []partValue) error {
	t := target.Type()
	type position struct {
		index int
		field reflect.Value
	}
	var positions []position
	rest := reflect.Value{}
	for i := range t.NumField() {
		tag, ok := t.Field(i).Tag.Lookup("part")
		switch {
		case !ok:
		case tag == "rest":
			rest = target.Field(i)
		default:
			n, err := strconv.Atoi(tag)
			if err != nil {
				return fmt.Errorf("the field %s has the part tag %q", t.Field(i).Name, tag)
			}
			positions = append(positions, position{n, target.Field(i)})
		}
	}
	slices.SortFunc(positions, func(a, b position) int { return a.index - b.index })
	for i, p := range parts {
		if i < len(positions) {
			if err := setPart(positions[i].field, p); err != nil {
				return fmt.Errorf("part %d: %w", i+1, err)
			}
			continue
		}
		if !rest.IsValid() {
			return fmt.Errorf("%d parts given; the body has at most %d", len(parts), len(positions))
		}
		item := reflect.New(rest.Type().Elem()).Elem()
		if err := setPart(item, p); err != nil {
			return fmt.Errorf("part %d: %w", i+1, err)
		}
		rest.Set(reflect.Append(rest, item))
	}
	return nil
}

// setPart sets v, an item of parts, from p: a file, a part with its
// headers, text, bytes, or else a value given as JSON.
func setPart(v reflect.Value, p partValue) error {
	v = settable(v)
	if v.CanAddr() {
		if mp, ok := v.Addr().Interface().(oapi.MIMEPart); ok {
			header, value := mp.MIMEPart()
			*header = p.header
			return setPart(reflect.ValueOf(value).Elem(), partValue{name: p.name, contentType: p.contentType, data: p.data})
		}
	}
	switch {
	case v.Type() == reflect.TypeFor[oapi.File]():
		v.Set(reflect.ValueOf(oapi.File{Name: p.name, ContentType: p.contentType, Header: p.header, Data: p.data}))
	case v.Kind() == reflect.String:
		v.SetString(string(p.data))
	case v.Kind() == reflect.Slice && v.Type().Elem().Kind() == reflect.Uint8:
		v.SetBytes(p.data)
	default:
		return oapi.DecodeRequest(p.data, v.Addr().Interface())
	}
	return nil
}

// isSeq2 reports whether t is an iter.Seq2[T, error].
func isSeq2(t reflect.Type) bool {
	if t.Kind() != reflect.Func || t.NumIn() != 1 || t.NumOut() != 0 {
		return false
	}
	yield := t.In(0)
	return yield.Kind() == reflect.Func && yield.NumIn() == 2 && yield.In(1) == reflect.TypeFor[error]() &&
		yield.NumOut() == 1 && yield.Out(0).Kind() == reflect.Bool
}

// seqOf returns an iter.Seq2 of type t yielding the items of a slice.
func seqOf(t reflect.Type, items reflect.Value) reflect.Value {
	noErr := reflect.Zero(reflect.TypeFor[error]())
	return reflect.MakeFunc(t, func(args []reflect.Value) []reflect.Value {
		yield := args[0]
		for i := range items.Len() {
			if !yield.Call([]reflect.Value{items.Index(i), noErr})[0].Bool() {
				break
			}
		}
		return nil
	})
}
