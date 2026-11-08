package cli

import (
	"iter"
	"net/textproto"
	"reflect"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/oapi"
)

type tuple struct {
	Pet   filter                           `part:"0"`
	Photo oapi.File                        `part:"1"`
	Note  oapi.Optional[oapi.Part[string]] `part:"2"`
	Rest  []string                         `part:"rest"`
}

type pair struct {
	A string `part:"0"`
	B string `part:"1"`
}

func TestMultipartParts(t *testing.T) {
	// Line ends of LF only, the boundary from the first line.
	doc := "--xy\nContent-Type: text/plain\nX-A: 1\n\none\n--xy\nContent-Disposition: attachment; filename=\"b.txt\"\n\ntwo\n--xy--\n"
	parts, err := multipartParts([]byte(doc), "")
	if err != nil || len(parts) != 2 || string(parts[0].data) != "one" || parts[0].header.Get("X-A") != "1" || parts[1].name != "b.txt" || string(parts[1].data) != "two" {
		t.Fatalf("parts %+v, %v", parts, err)
	}
	// The boundary of the media type.
	doc = "preamble\r\n--q\r\n\r\nx\r\n--q--\r\n"
	if parts, err := multipartParts([]byte(doc), "multipart/mixed; boundary=q"); err != nil || len(parts) != 1 || string(parts[0].data) != "x" {
		t.Errorf("parts %+v, %v", parts, err)
	}
	if _, err := multipartParts([]byte("plain text"), ""); err == nil {
		t.Error("plain text is a multipart document")
	}
}

func TestSetParts(t *testing.T) {
	text := func(s string, header ...string) partValue {
		h := textproto.MIMEHeader{}
		for i := 0; i+1 < len(header); i += 2 {
			h.Add(header[i], header[i+1])
		}
		return partValue{header: h, data: []byte(s)}
	}
	var tp tuple
	parts := []partValue{text(`{"age":3,"name":"Rex"}`), {name: "rex.png", contentType: "image/png", data: []byte("PNG")}, text("hi", "X-Lang", "en"), text("r1"), text("r2")}
	if err := setParts(reflect.ValueOf(&tp).Elem(), parts); err != nil {
		t.Fatal(err)
	}
	want := tuple{
		Pet:   filter{Age: 3, Name: "Rex"},
		Photo: oapi.File{Name: "rex.png", ContentType: "image/png", Data: []byte("PNG")},
		Note:  oapi.Some(oapi.Part[string]{Header: textproto.MIMEHeader{"X-Lang": {"en"}}, Value: "hi"}),
		Rest:  []string{"r1", "r2"},
	}
	if !reflect.DeepEqual(tp, want) {
		t.Errorf("tuple:\n got %+v\nwant %+v", tp, want)
	}
	// An optional position may be left out at the end.
	tp = tuple{}
	if err := setParts(reflect.ValueOf(&tp).Elem(), parts[:2]); err != nil || tp.Note.Set {
		t.Errorf("two parts: %+v, %v", tp, err)
	}
	var p pair
	if err := setParts(reflect.ValueOf(&p).Elem(), []partValue{text("a"), text("b"), text("c")}); err == nil || !strings.Contains(err.Error(), "at most 2") {
		t.Errorf("too many parts: %v", err)
	}
	if err := setParts(reflect.ValueOf(&tp).Elem(), []partValue{text("{")}); err == nil || !strings.Contains(err.Error(), "part 1") {
		t.Errorf("invalid JSON part: %v", err)
	}
	// A stream of parts yields them in order.
	var seq iter.Seq2[oapi.Part[filter], error]
	if err := setParts(reflect.ValueOf(&seq).Elem(), []partValue{text(`{"age":1}`, "X-Seq", "1"), text(`{"age":2}`)}); err != nil {
		t.Fatal(err)
	}
	var ages []int
	for item, err := range seq {
		if err != nil {
			t.Fatal(err)
		}
		ages = append(ages, item.Value.Age)
	}
	if !reflect.DeepEqual(ages, []int{1, 2}) {
		t.Errorf("stream %v", ages)
	}
}
