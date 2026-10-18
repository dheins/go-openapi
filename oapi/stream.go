package oapi

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
)

// StreamDecoder is implemented by generated types that decode themselves
// from a stream of JSON tokens, without first collecting their value.
type StreamDecoder interface {
	DecodeJSONFrom(dec *jsontext.Decoder, dir Direction) error
}

// streamContainer is implemented by Optional and Nullable so that
// DecodeFrom can see through them to the value they hold.
type streamContainer interface {
	decodeFrom(dec *jsontext.Decoder, dir Direction) error
}

func (o *Optional[T]) decodeFrom(dec *jsontext.Decoder, dir Direction) error {
	o.Set = true
	return DecodeFrom(dec, &o.Value, false, dir)
}

func (n *Nullable[T]) decodeFrom(dec *jsontext.Decoder, dir Direction) error {
	if dec.PeekKind() == 'n' {
		if _, err := dec.ReadToken(); err != nil {
			return err
		}
		*n = Nullable[T]{}
		return nil
	}
	n.Valid = true
	return DecodeFrom(dec, &n.Value, false, dir)
}

// decoderOptions match encoding/json's Unmarshal, which DecodeObject used:
// a repeated name keeps its last value, and invalid UTF-8 is accepted.
var decoderOptions = []jsontext.Options{jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true)}

// DecodeJSONWith runs decode, a generated decoder, on data, which must
// hold exactly one JSON value. Syntax errors are described as
// encoding/json describes them, whatever decode reported meanwhile.
func DecodeJSONWith(data []byte, decode func(dec *jsontext.Decoder) error) error {
	dec := jsontext.NewDecoder(bytes.NewReader(data), decoderOptions...)
	err := decode(dec)
	if !isSyntaxError(err) {
		// Data after the value makes the text invalid, which outweighs
		// any problem with the value.
		if _, terr := dec.ReadToken(); terr != io.EOF {
			err = errTrailingData
		}
	}
	if isSyntaxError(err) {
		var v json.RawMessage
		if uerr := json.Unmarshal(data, &v); uerr != nil {
			return &ValidationError{Message: describe(uerr)}
		}
	}
	return err
}

var errTrailingData = errors.New("unexpected data after the value")

// DecodeObjectWith runs decode on data like DecodeJSONWith, for a value
// that must be an object: data that does not start with one is reported
// as such before its syntax is checked, as DecodeObject reports it.
func DecodeObjectWith(data []byte, decode func(dec *jsontext.Decoder) error) error {
	if trimmed := bytes.TrimSpace(data); len(trimmed) == 0 || trimmed[0] != '{' {
		return &ValidationError{Message: "expected object, got " + valueType(trimmed)}
	}
	return DecodeJSONWith(data, decode)
}

// isSyntaxError reports whether err comes from reading the JSON text
// rather than from a value that does not fit.
func isSyntaxError(err error) bool {
	if err == nil {
		return false
	}
	var syntax *jsontext.SyntacticError
	return errors.As(err, &syntax) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) || errors.Is(err, errTrailingData)
}

// DecodeFrom decodes the next value of dec into dst, a pointer, like
// DecodeField: null is accepted when nullable is set or the target can
// represent it, dir is passed on to generated types, and problems are
// ValidationErrors with paths relative to the value. Objects, arrays and
// generated types are decoded token by token; anything else from the
// value's text.
func DecodeFrom(dec *jsontext.Decoder, dst any, nullable bool, dir Direction) error {
	if c, ok := dst.(streamContainer); ok {
		return c.decodeFrom(dec, dir)
	}
	// Only objects and arrays are worth reading token by token.
	if kind := dec.PeekKind(); kind == '{' || kind == '[' {
		if d, ok := dst.(StreamDecoder); ok {
			return d.DecodeJSONFrom(dec, dir)
		}
		if v := reflect.ValueOf(dst); v.Kind() == reflect.Pointer && !v.IsNil() && !hasUnmarshaler(v.Type()) {
			switch e := v.Elem(); {
			case e.Kind() == reflect.Slice && e.Type().Elem().Kind() != reflect.Uint8 && kind == '[':
				return decodeSliceFrom(dec, e, dir)
			case e.Kind() == reflect.Map && e.Type().Key().Kind() == reflect.String && kind == '{':
				return decodeMapFrom(dec, e, dir)
			case e.Kind() == reflect.Pointer:
				if e.IsNil() {
					e.Set(reflect.New(e.Type().Elem()))
				}
				return DecodeFrom(dec, e.Interface(), false, dir)
			}
		}
	}
	raw, err := dec.ReadValue()
	if err != nil {
		return err
	}
	return DecodeField(raw, dst, nullable, dir)
}

func hasUnmarshaler(t reflect.Type) bool {
	return t.Implements(jsonUnmarshaler) || t.Implements(textUnmarshaler) || t.Implements(directionalDecoder)
}

var directionalDecoder = reflect.TypeFor[DirectionalDecoder]()

func decodeSliceFrom(dec *jsontext.Decoder, v reflect.Value, dir Direction) error {
	if _, err := dec.ReadToken(); err != nil {
		return err
	}
	out := reflect.MakeSlice(v.Type(), 0, 4)
	var errs ValidationErrors
	for i := 0; dec.PeekKind() != ']'; i++ {
		if dec.PeekKind() == 0 {
			_, err := dec.ReadToken()
			return err
		}
		out = reflect.Append(out, reflect.Zero(v.Type().Elem()))
		if err := DecodeFrom(dec, out.Index(i).Addr().Interface(), false, dir); err != nil {
			if isSyntaxError(err) {
				return err
			}
			errs.Nested(Index("", i), err)
		}
	}
	if _, err := dec.ReadToken(); err != nil {
		return err
	}
	v.Set(out)
	return errs.Err()
}

func decodeMapFrom(dec *jsontext.Decoder, v reflect.Value, dir Direction) error {
	if _, err := dec.ReadToken(); err != nil {
		return err
	}
	out := reflect.MakeMap(v.Type())
	var problems []NamedError
	for dec.PeekKind() != '}' {
		tok, err := dec.ReadToken()
		if err != nil {
			return err
		}
		name := tok.String()
		elem := reflect.New(v.Type().Elem())
		err = DecodeFrom(dec, elem.Interface(), false, dir)
		if isSyntaxError(err) {
			return err
		}
		problems = append(problems, NamedError{name, err})
		out.SetMapIndex(reflect.ValueOf(name).Convert(v.Type().Key()), elem.Elem())
	}
	if _, err := dec.ReadToken(); err != nil {
		return err
	}
	v.Set(out)
	var errs ValidationErrors
	AddNamed(&errs, problems)
	return errs.Err()
}

// NamedError is a problem with the property Name of an object.
type NamedError struct {
	Name string
	Err  error
}

// AddNamed adds the problems of an object's properties to errs in the
// order of their names, as when they were decoded in sorted order. A
// repeated name counts with its last value only, whose Err may be nil.
func AddNamed(errs *ValidationErrors, problems []NamedError) {
	slices.SortStableFunc(problems, func(a, b NamedError) int { return strings.Compare(a.Name, b.Name) })
	for i, p := range problems {
		if i+1 < len(problems) && problems[i+1].Name == p.Name || p.Err == nil {
			continue
		}
		errs.Nested(Field("", p.Name), p.Err)
	}
}

// ObjectDecoder reads the members of a JSON object for a generated
// decoder:
//
//	obj := oapi.BeginObject(dec)
//	for obj.Next() {
//		switch obj.Name() { ... }
//	}
//	if err := obj.Err(); err != nil { ... }
//
// Each member's value must be read (or skipped with Skip) before the
// next call to Next.
type ObjectDecoder struct {
	dec  *jsontext.Decoder
	name string
	err  error
	done bool
}

// BeginObject starts reading an object. If the next value is something
// else, it is consumed and Err reports "expected object, got ...".
func BeginObject(dec *jsontext.Decoder) ObjectDecoder {
	o := ObjectDecoder{dec: dec}
	if kind := dec.PeekKind(); kind != '{' {
		o.done = true
		raw, err := dec.ReadValue()
		switch {
		case err == io.EOF:
			o.err = &ValidationError{Message: "expected object, got nothing"}
		case err != nil:
			o.err = err
		default:
			o.err = &ValidationError{Message: "expected object, got " + valueType(raw)}
		}
		return o
	}
	if _, err := dec.ReadToken(); err != nil {
		o.err, o.done = err, true
	}
	return o
}

// Next reads the next member's name, reporting false at the end of the
// object or on an error.
func (o *ObjectDecoder) Next() bool {
	if o.done {
		return false
	}
	tok, err := o.dec.ReadToken()
	switch {
	case err != nil:
		o.err, o.done = err, true
		return false
	case tok.Kind() == '}':
		o.done = true
		return false
	}
	o.name = tok.String()
	return true
}

// Name returns the name of the current member.
func (o *ObjectDecoder) Name() string { return o.name }

// Skip skips the current member's value.
func (o *ObjectDecoder) Skip() {
	if err := o.dec.SkipValue(); err != nil && o.err == nil {
		o.err, o.done = err, true
	}
}

// Decode decodes the current member's value into dst like DecodeFrom. A
// syntax error ends the object; other problems are returned.
func (o *ObjectDecoder) Decode(dst any, nullable bool, dir Direction) error {
	err := DecodeFrom(o.dec, dst, nullable, dir)
	if isSyntaxError(err) {
		if o.err == nil {
			o.err, o.done = err, true
		}
		return nil
	}
	return err
}

// Err returns the error that ended reading the object, if any.
func (o *ObjectDecoder) Err() error { return o.err }
