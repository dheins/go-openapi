package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"testing"

	"github.com/dheins/go-openapi/oapi"
)

// FuzzParamStyles checks that splitting parameter values never panics,
// for every style, explode setting and shape.
func FuzzParamStyles(f *testing.F) {
	for _, seed := range []string{";id=3;id=4", ".a.b", "role,admin", "a=1&id[x]=2", `"a, b", c`, ";", "", "=,="} {
		f.Add(seed)
	}
	styles := []string{"simple", "label", "matrix", "form", "spaceDelimited", "pipeDelimited", "deepObject"}
	f.Fuzz(func(t *testing.T, raw string) {
		q, _ := url.ParseQuery(raw)
		h := http.Header{"X-P": {raw}}
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Cookie", "id="+raw)
		for _, style := range styles {
			for _, explode := range []bool{false, true} {
				for _, shape := range []Shape{Scalar, Array, Object} {
					p := Param{Name: "id", Style: style, Explode: explode, Shape: shape, Properties: []string{"a", "x"}}
					_, _ = p.PathValues(raw)
					_, _, _ = p.QueryValues(q)
					p.Name = "X-P"
					_, _, _ = p.HeaderValues(h)
					p.Name = "id"
					_, _, _ = p.CookieValues(r)
				}
			}
		}
	})
}

// FuzzNegotiate checks that Accept parsing never panics.
func FuzzNegotiate(f *testing.F) {
	for _, seed := range []string{"text/*;q=0.9, application/json", "*/*", ";;;", "a/b;q=x", ""} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, accept string) {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Accept", accept)
		got := Negotiate(r, "application/json", "text/csv")
		if got != "" && got != "application/json" && got != "text/csv" {
			t.Fatalf("Negotiate returned %q, which was not offered", got)
		}
	})
}

// FuzzMultipart checks that decoding arbitrary multipart bodies never
// panics.
func FuzzMultipart(f *testing.F) {
	f.Add([]byte("--b\r\nContent-Disposition: form-data; name=\"name\"\r\n\r\nRex\r\n--b--\r\n"), "b")
	f.Add([]byte("--b\r\nContent-Disposition: form-data; name=\"photo\"; filename=\"a\"\r\n\r\nPNG\r\n--b--\r\n"), "b")
	f.Fuzz(func(t *testing.T, body []byte, boundary string) {
		type upload struct {
			Name  string `json:"name"`
			Photo []byte `json:"photo"`
		}
		props := Property{Type: "object", Properties: map[string]Property{"name": {Type: "string"}, "photo": {Type: "file"}}}
		_, _ = DecodeMultipart[upload](bytes.Clone(body), "multipart/form-data; boundary="+boundary, props)
		// The same body as positional parts, of each kind of item.
		ct := "multipart/mixed; boundary=" + boundary
		rules := Parts{Prefix: []Property{{ContentTypes: []string{"application/json"}, Headers: []string{"X-Seq"}}}}
		rules.Item = "json"
		_, _ = DecodeParts[upload](bytes.Clone(body), ct, rules)
		rules.Item = "text"
		_, _ = DecodeParts[string](bytes.Clone(body), ct, rules)
		rules.Item = "file"
		_, _ = DecodeParts[oapi.File](bytes.Clone(body), ct, rules)
	})
}

// FuzzObjectJSON checks that converting fields with bracketed keys never
// panics and only produces valid JSON.
func FuzzObjectJSON(f *testing.F) {
	f.Add("owner[age]=3&owner[name]=Ann&ids[]=1&ids[1]=2&meta[x][y]=z")
	f.Add("a[b]=1&owner=%7B%22age%22%3A1%7D&x[=1&[c]=3")
	f.Add("items[0][name]=a&items[0][tags][1]=x&items[3][qty]=2&tree[children][0][children][0][name]=leaf")
	f.Add("items[0]=v&items[0][name]=b&tree[children][x]=1&items[01][qty]=1")
	tree := &Property{}
	*tree = Property{Type: "object", Properties: map[string]Property{"name": {Type: "string"}, "children": {Array: true, Ref: tree}}}
	obj := Property{Type: "object", Properties: map[string]Property{
		"owner": {Type: "object", Properties: map[string]Property{"age": {Type: "integer"}}},
		"ids":   {Type: "integer", Array: true},
		"meta":  {Type: "object", Additional: &Property{Type: "any"}},
		"a[b]":  {Type: "string"},
		"items": {Type: "object", Array: true, Properties: map[string]Property{"name": {Type: "string"}, "tags": {Type: "string", Array: true}, "qty": {Type: "integer"}}},
		"tree":  {Ref: tree},
	}}
	f.Fuzz(func(t *testing.T, query string) {
		q, err := url.ParseQuery(query)
		if err != nil {
			return
		}
		data, err := ObjectJSON(q, obj)
		if err == nil && !json.Valid(data) {
			t.Fatalf("invalid JSON %s for %q", data, query)
		}
	})
}

// FuzzXMLToJSON checks that converting XML never panics, gives valid
// JSON, and that what it gives is written back as XML that reads the
// same.
func FuzzXMLToJSON(f *testing.F) {
	f.Add([]byte(`<p:pet xmlns:p="urn:pets" id="7"><p:name>Rex</p:name><photoUrls><photoUrl>a</photoUrl></photoUrls><tag><name>x</name></tag><category><id>1</id>Dogs</category></p:pet>`))
	f.Add([]byte(`<p:pet xmlns:p="urn:pets"><extra a="1"><b>2</b><b>3</b>t</extra><vip xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:nil="true"/></p:pet>`))
	f.Fuzz(func(t *testing.T, data []byte) {
		out, err := XMLToJSON(data, petXML)
		if err != nil {
			return
		}
		if !json.Valid(out) {
			t.Fatalf("invalid JSON %s from %q", out, data)
		}
		doc, err := JSONToXML(out, petXML)
		if err != nil {
			t.Fatalf("writing %s: %v", out, err)
		}
		back, err := XMLToJSON(doc, petXML)
		if err != nil || string(back) != string(out) {
			t.Fatalf("%q\n-> %s\n-> %s\n-> %s (%v)", data, out, doc, back, err)
		}
	})
}

// FuzzStreamBodies checks that reading streamed request bodies never
// panics, and that what is read is written back as a stream that reads
// the same: JSON Lines items, and server-sent events.
func FuzzStreamBodies(f *testing.F) {
	f.Add([]byte("{\"a\":1}\n\n[1,2]\n\"x\"\n"))
	f.Add([]byte("event: a\r\ndata: 1\rdata: 2\n\n: c\nretry: 30\nid: 7\ndata\n\n"))
	c := NewConfig(WithMaxBodyBytes(1 << 10))
	f.Fuzz(func(t *testing.T, data []byte) {
		read := func(body []byte) ([]any, error) {
			var items []any
			for item, err := range JSONLinesBody[any](c, httptest.NewRequest("POST", "/", bytes.NewReader(body)), nil) {
				if err != nil {
					return items, err
				}
				items = append(items, item)
			}
			return items, nil
		}
		items, _ := read(data)
		var lines []byte
		for _, item := range items {
			line, err := oapi.EncodeRequest(item)
			if err != nil {
				t.Fatalf("encoding %v: %v", item, err)
			}
			lines = append(append(lines, line...), '\n')
		}
		if back, err := read(lines); err != nil || !reflect.DeepEqual(back, items) {
			t.Fatalf("%q\n-> %q\n-> %v (%v)", data, lines, back, err)
		}

		readEvents := func(body []byte) []Event {
			var events []Event
			for ev, err := range EventsBody(c, httptest.NewRequest("POST", "/", bytes.NewReader(body))) {
				if err != nil {
					break
				}
				events = append(events, ev)
			}
			return events
		}
		events := readEvents(data)
		var stream []byte
		for _, ev := range events {
			stream = AppendEvent(stream, ev)
		}
		if back := readEvents(stream); !slices.Equal(back, events) {
			t.Fatalf("%q\n-> %q\n-> %+v, want %+v", data, stream, back, events)
		}
	})
}
