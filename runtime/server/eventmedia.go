package server

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/dheins/go-openapi/oapi"
)

// EventMedia says how the data of a kind of server-sent event is read
// and written: Kind is json (JSON), text (the text itself), scalar (the
// text of a number, boolean or enum, of JSON Type), xml (an XML document
// XML describes), form (form fields of an object, Form) or codec (with
// the codec of MediaType, of Codecs).
type EventMedia struct {
	Kind, Type, MediaType string
	XML                   *XMLNode
	Form                  Property
	Codecs                map[string]Codec
}

// EventValue decodes the data of ev, written as m says, into a T in
// direction dir, checking it with check if validate is set. Problems are
// located in the event, as "/data/name", as EventData's.
func EventValue[T any](ev Event, m EventMedia, dir oapi.Direction, validate bool, check func(T) error) (T, error) {
	var v T
	doc, err := EventJSON(ev.Data, m)
	if err == nil {
		err = oapi.DecodeValue(doc, &v, dir)
	}
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

// EventJSON returns data, the data of an event written as m says, as
// JSON.
func EventJSON(data string, m EventMedia) ([]byte, error) {
	switch m.Kind {
	case "text":
		return oapi.AppendString(nil, data), nil
	case "scalar":
		return appendScalar(nil, data, m.Type), nil
	case "xml":
		return XMLToJSON([]byte(data), m.XML)
	case "form":
		fields, err := ParseFields(data)
		if err != nil {
			return nil, &oapi.ValidationError{Message: "invalid form: " + err.Error()}
		}
		return ObjectJSON(fields, m.Form)
	case "codec":
		return codecJSON(m.Codecs, m.MediaType, []byte(data))
	}
	return []byte(data), nil
}

// EncodeEventValue returns ev with data, its data, written as m says in
// direction dir, of the kind of event kind, as EncodeEvent does for JSON:
// kind is "" for a stream of one kind or an unnamed kind, whose event is
// ev's.
func EncodeEventValue[T any](ev TypedEvent[T], kind string, data any, m EventMedia, dir oapi.Direction) (Event, error) {
	name := ev.Event
	switch {
	case kind == "":
	case name == "":
		name = kind
	case name != kind:
		return Event{}, fmt.Errorf("the event %q holds the data of %q", name, kind)
	}
	text, err := eventText(data, m, dir)
	if err != nil {
		return Event{}, err
	}
	return Event{ID: ev.ID, Event: name, Retry: ev.Retry, Data: text}, nil
}

// eventText writes data as the data of an event, as m says.
func eventText(data any, m EventMedia, dir oapi.Direction) (string, error) {
	switch m.Kind {
	case "text", "scalar":
		b, err := oapi.EncodeValue(data, dir)
		if err != nil {
			return "", err
		}
		// The text of a string, or a number or boolean as JSON writes it.
		return FieldText(b), nil
	case "xml":
		return EncodeXMLContent(data, m.XML, dir)
	case "form":
		return EncodeFormContent(data, dir)
	case "codec":
		b, err := EncodeCodec(m.Codecs, m.MediaType, data, dir)
		if err != nil {
			return "", err
		}
		if !utf8.Valid(b) || strings.ContainsRune(string(b), '\r') {
			return "", fmt.Errorf("the %s codec wrote bytes that are not text, which an event's data must be", CodecKey(m.MediaType))
		}
		return string(b), nil
	}
	b, err := oapi.EncodeValue(data, dir)
	return string(b), err
}

// CheckEventField checks v, the id or retry field of an event at, such as
// "/id", with check, which may be nil.
func CheckEventField[T any](at string, v T, check func(T) error) error {
	if check == nil {
		return nil
	}
	if err := check(v); err != nil {
		var errs oapi.ValidationErrors
		errs.Nested(at, err)
		return errs.Err()
	}
	return nil
}

// NoEventKind returns the error of an event of name whose data is none of
// the unnamed kinds of its stream, whose named kinds are names: the name
// is unknown too, if it is not "".
func NoEventKind(name string, names ...string) error {
	none := &oapi.ValidationError{Path: "/data", Message: "the data is no kind of event of the stream"}
	var unknown *oapi.ValidationError
	if name != "" && len(names) > 0 && errors.As(UnknownEvent(name, names...), &unknown) {
		return oapi.ValidationErrors{unknown, none}
	}
	return none
}

// EventKinds returns the error of an event whose data is more than one
// of the unnamed kinds of its stream, of which it may be only one
// (oneOf).
func EventKinds() error {
	return &oapi.ValidationError{Path: "/data", Message: "the data is more than one kind of event of the stream"}
}
