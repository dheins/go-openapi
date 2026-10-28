package server

import (
	"bufio"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"slices"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/problem"
)

type streamPet struct {
	Name string `json:"name"`
}

func TestJSONLinesBody(t *testing.T) {
	c := NewConfig()
	r := httptest.NewRequest("POST", "/", strings.NewReader("{\"name\":\"a\"}\n\n{\"name\":\"\"}\n{\"name\":\"c\"}\n"))
	nonEmpty := func(p streamPet) error {
		if p.Name == "" {
			return &oapi.ValidationError{Path: "/name", Message: "must not be empty"}
		}
		return nil
	}
	items := JSONLinesBody(c, r, nonEmpty)
	var names []string
	var last error
	for p, err := range items {
		if err != nil {
			last = err
			break
		}
		names = append(names, p.Name)
	}
	// The blank line is not an item, so the invalid one is item 1.
	var p *problem.Problem
	if !slices.Equal(names, []string{"a"}) || !errors.As(last, &p) || p.Status != http.StatusBadRequest ||
		len(p.Errors) != 1 || p.Errors[0].In != "body" || p.Errors[0].Pointer != "/1/name" {
		t.Errorf("names %q, error %v", names, last)
	}
	for _, err := range items {
		if err == nil || !strings.Contains(err.Error(), "read already") {
			t.Errorf("second range: %v", err)
		}
	}
}

func TestBodyMediaType(t *testing.T) {
	c := NewConfig()
	types := []string{"application/jsonl", "application/json"}
	for _, tc := range []struct {
		contentType, body, want, problem string
	}{
		{"application/jsonl; charset=utf-8", "x", "application/jsonl", ""},
		{"", "x", "application/jsonl", ""},
		{"application/json", "x", "application/json", ""},
		{"text/csv", "x", "", "415"},
		{"application/jsonl", "", "", "body: request body is required"},
	} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
		if tc.contentType != "" {
			r.Header.Set("Content-Type", tc.contentType)
		}
		var b Binder
		got, ok := c.BodyMediaType(r, &b, types, true)
		var msg string
		if p := b.Problem(); b.fatal != nil {
			msg = p.Title
			if p.Status == http.StatusUnsupportedMediaType {
				msg = "415"
			}
		} else if len(p.Errors) > 0 {
			msg = p.Errors[0].In + ": " + p.Errors[0].Detail
		}
		if got != tc.want || ok != (tc.want != "") || msg != tc.problem {
			t.Errorf("%q %q: %q %v %q", tc.contentType, tc.body, got, ok, msg)
		}
	}
}

func TestEvents(t *testing.T) {
	// CR, LF and CRLF all end lines; retry is digits only.
	in := "event: a\r\ndata: 1\rdata: 2\n\n: comment\nretry: -5\nid: 7\ndata\n\nretry: 30\ndata: x\r\r"
	var got []Event
	for ev, err := range ScanEvents(bufio.NewScanner(strings.NewReader(in))) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ev)
	}
	want := []Event{{Event: "a", Data: "1\n2"}, {ID: "7"}, {Retry: 30, Data: "x"}}
	if !slices.Equal(got, want) {
		t.Errorf("events = %+v", got)
	}

	// Line ends cannot start other fields.
	out := string(AppendEvent(nil, Event{ID: "1\nevent: x", Event: "e\r", Data: "a\r\nb"}))
	if out != "id: 1 event: x\nevent: e \ndata: a\ndata: b\n\n" {
		t.Errorf("appended %q", out)
	}
}

func TestTypedEventHelpers(t *testing.T) {
	ev := TypedEvent[streamPet]{ID: "1", Data: streamPet{Name: "a"}}
	if got, err := EncodeEvent(ev, "added", ev.Data, oapi.Response); err != nil || got != (Event{ID: "1", Event: "added", Data: `{"name":"a"}`}) {
		t.Errorf("encoded %+v %v", got, err)
	}
	// The event's own name must be its data's kind.
	ev.Event = "removed"
	if _, err := EncodeEvent(ev, "added", ev.Data, oapi.Response); err == nil {
		t.Error("a removed event with added data was encoded")
	}
	if _, err := EncodeTextEvent(ev, "ping", "hi"); err == nil {
		t.Error("a removed event with ping data was encoded")
	}

	nonEmpty := func(p streamPet) error {
		if p.Name == "" {
			return &oapi.ValidationError{Path: "/name", Message: "must not be empty"}
		}
		return nil
	}
	var errs oapi.ValidationErrors
	if _, err := EventData(Event{Data: `{"name":""}`}, oapi.Request, true, nonEmpty); !errors.As(err, &errs) || errs[0].Path != "/data/name" {
		t.Errorf("invalid data: %v", err)
	}
	// Without validate, only decoding counts.
	if _, err := EventData(Event{Data: `{"name":""}`}, oapi.Request, false, nonEmpty); err != nil {
		t.Errorf("unchecked data: %v", err)
	}
	var single *oapi.ValidationError
	if err := UnknownEvent("x", "added", "ping"); !errors.As(err, &single) || single.Path != "/event" || single.Message != `unknown event "x"; expected "added", "ping"` {
		t.Errorf("unknown event: %v", err)
	}
}

func TestWriteTypedEvents(t *testing.T) {
	c := NewConfig(WithLogger(slog.New(slog.DiscardHandler)))
	events := func(yield func(TypedEvent[streamPet], error) bool) {
		_ = yield(TypedEvent[streamPet]{Data: streamPet{Name: "a"}}, nil) && yield(TypedEvent[streamPet]{Event: "bad"}, nil) &&
			yield(TypedEvent[streamPet]{Data: streamPet{Name: "c"}}, nil)
	}
	// An event that cannot be written ends the stream.
	encode := func(ev TypedEvent[streamPet]) (Event, error) { return EncodeEvent(ev, "pet", ev.Data, oapi.Response) }
	w := httptest.NewRecorder()
	if err := WriteTypedEvents(c, w, httptest.NewRequest("GET", "/", nil), 200, events, nil, encode); err != nil {
		t.Fatal(err)
	}
	if got := w.Body.String(); got != "event: pet\ndata: {\"name\":\"a\"}\n\n" {
		t.Errorf("written %q", got)
	}
}

func TestCheckFileHeaders(t *testing.T) {
	type upload struct {
		One  oapi.File                  `json:"one"`
		Many oapi.Optional[[]oapi.File] `json:"many"`
		None *oapi.File                 `json:"none"`
	}
	seq := []PartHeader{{Name: "X-Seq", Required: true, Check: func(vals []string) error {
		if vals[0] != "1" {
			return &oapi.ValidationError{Message: "must be 1"}
		}
		return nil
	}}}
	obj := Property{Type: "object", Properties: map[string]Property{
		"one":  {Type: "file", Headers: seq},
		"many": {Type: "file", Array: true, Headers: seq},
		"none": {Type: "file", Headers: seq},
	}}
	good := oapi.File{Header: textproto.MIMEHeader{"X-Seq": {"1"}}}
	v := upload{One: good, Many: oapi.Some([]oapi.File{good, {Header: textproto.MIMEHeader{"X-Seq": {"2"}}}, {}})}
	var errs oapi.ValidationErrors
	if err := CheckFileHeaders(v, obj); !errors.As(err, &errs) || len(errs) != 2 ||
		errs[0].Path != "/many/1" || errs[0].Message != `part header "X-Seq": must be 1` || errs[1].Path != "/many/2" || errs[1].Message != `part header "X-Seq" is missing` {
		t.Errorf("errors = %v", err)
	}
	// A file that is not set has no headers to check.
	if err := CheckFileHeaders(&upload{One: good}, obj); err != nil {
		t.Errorf("unset files: %v", err)
	}
}

func TestPartValues(t *testing.T) {
	seq := []PartHeader{{Name: "X-Seq", Required: true, Check: func(vals []string) error {
		if vals[0] != "1" {
			return &oapi.ValidationError{Message: "must be 1"}
		}
		return nil
	}}}
	rules := Parts{Item: "json", Prefix: []Property{{Headers: seq}}}
	items := []oapi.Part[streamPet]{
		{Header: textproto.MIMEHeader{"X-Seq": {"1"}, "Content-Type": {"text/plain"}}, Value: streamPet{Name: "a"}},
		{Value: streamPet{Name: "b"}},
	}
	if err := CheckParts(items, rules); err != nil {
		t.Fatal(err)
	}
	// The part's headers are written with it, and read back next to its
	// value; Content-Type is the encoding's, not the item's.
	body, ct, err := EncodeParts(items, "multipart/mixed", rules, oapi.Request)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeParts[oapi.Part[streamPet]](body, ct, rules)
	if err != nil || len(got) != 2 || got[0].Value.Name != "a" || got[0].Header.Get("X-Seq") != "1" ||
		got[0].Header.Get("Content-Type") != "application/json" || got[1].Value.Name != "b" {
		t.Fatalf("decoded %+v, %v", got, err)
	}
	// Text items too.
	text := Parts{Item: "text", Rest: Property{Headers: seq}}
	body, ct, err = EncodeParts([]oapi.Part[string]{{Header: textproto.MIMEHeader{"X-Seq": {"1"}}, Value: "hi"}}, "multipart/mixed", text, oapi.Request)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := DecodeParts[oapi.Part[string]](body, ct, text); err != nil || len(got) != 1 || got[0].Value != "hi" || got[0].Header.Get("X-Seq") != "1" {
		t.Fatalf("decoded %+v, %v", got, err)
	}
	// Bad and missing headers are found by position, before sending.
	items[0].Header.Set("X-Seq", "2")
	var errs oapi.ValidationErrors
	if err := CheckParts(items, Parts{Item: "json", Rest: Property{Headers: seq}}); !errors.As(err, &errs) || len(errs) != 2 ||
		errs[0].Path != "/0" || errs[0].Message != `part header "X-Seq": must be 1` || errs[1].Path != "/1" || errs[1].Message != `part header "X-Seq" is missing` {
		t.Errorf("errors = %v", err)
	}
	// Items without headers have none to check.
	if err := CheckParts([]streamPet{{}}, rules); err != nil {
		t.Errorf("plain items: %v", err)
	}
}

func TestTuple(t *testing.T) {
	type report struct {
		Meta streamPet                        `part:"0"`
		Scan oapi.File                        `part:"1"`
		Note oapi.Optional[oapi.Part[string]] `part:"2"`
		More []oapi.File                      `part:"rest"`
	}
	seq := []PartHeader{{Name: "X-Seq", Required: true}}
	p := Parts{Item: "file", Kinds: []string{"json", "file", "text"},
		Prefix: []Property{{ContentTypes: []string{"application/json"}}, {Headers: seq}, {}}}
	scan := oapi.File{Name: "a.png", ContentType: "image/png", Header: textproto.MIMEHeader{"X-Seq": {"1"}}, Data: []byte("PNG")}
	in := report{Meta: streamPet{Name: "Rex"}, Scan: scan,
		Note: oapi.Some(oapi.Part[string]{Header: textproto.MIMEHeader{"X-Lang": {"en"}}, Value: "hi"}),
		More: []oapi.File{{ContentType: "text/csv", Data: []byte("a,b")}}}
	if err := CheckTuple(in, p); err != nil {
		t.Fatal(err)
	}
	body, ct, err := EncodeTuple(in, "multipart/mixed", p, oapi.Request)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeTuple[report](body, ct, p, oapi.Request)
	if err != nil || got.Meta.Name != "Rex" || string(got.Scan.Data) != "PNG" || got.Scan.Header.Get("X-Seq") != "1" ||
		got.Note.Value.Value != "hi" || got.Note.Value.Header.Get("X-Lang") != "en" || len(got.More) != 1 || string(got.More[0].Data) != "a,b" {
		t.Fatalf("decoded %+v, %v", got, err)
	}
	// An optional position may be left out, at the end only.
	short := report{Meta: in.Meta, Scan: scan}
	if body, ct, err = EncodeTuple(&short, "multipart/mixed", p, oapi.Request); err != nil {
		t.Fatal(err)
	}
	if got, err := DecodeTuple[report](body, ct, p, oapi.Request); err != nil || got.Note.Set || len(got.More) != 0 {
		t.Errorf("short = %+v, %v", got, err)
	}
	short.More = in.More
	if _, _, err := EncodeTuple(short, "multipart/mixed", p, oapi.Request); err == nil {
		t.Error("parts after an unset one")
	}
	// Missing, extra and invalid parts are reported at their index.
	type pair struct {
		Meta streamPet `part:"0"`
		Scan oapi.File `part:"1"`
	}
	pp := Parts{Kinds: []string{"json", "file"}, Prefix: p.Prefix[:2]}
	body, ct, _ = EncodeParts([]oapi.File{{ContentType: "application/json", Data: []byte(`{"name": 1}`)}}, "multipart/mixed", Parts{Item: "file"}, oapi.Request)
	var errs oapi.ValidationErrors
	if _, err := DecodeTuple[pair](body, ct, pp, oapi.Request); !errors.As(err, &errs) || len(errs) != 2 ||
		errs[0].Path != "/0" || errs[1].Path != "/1" || errs[1].Message != "part is missing" {
		t.Errorf("missing: %v", err)
	}
	body, ct, _ = EncodeTuple(in, "multipart/mixed", p, oapi.Request)
	if _, err := DecodeTuple[pair](body, ct, pp, oapi.Request); !errors.As(err, &errs) || len(errs) != 2 ||
		errs[0].Path != "/2" || errs[0].Message != "unexpected part: the body has at most 2" {
		t.Errorf("extra: %v", err)
	}
	unsigned := in
	unsigned.Scan.Header = nil
	if err := CheckTuple(unsigned, p); !errors.As(err, &errs) || len(errs) != 1 || errs[0].Path != "/1" {
		t.Errorf("header: %v", err)
	}
	// A file position needs an oapi.File field.
	if _, err := DecodeTuple[pair](body, ct, Parts{Kinds: []string{"file", "file"}}, oapi.Request); err == nil || errors.As(err, &errs) {
		t.Errorf("wrong field type: %v", err)
	}
}
