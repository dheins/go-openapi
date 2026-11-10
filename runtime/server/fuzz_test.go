package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
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
		q := ParseQuery(&http.Request{URL: &url.URL{RawQuery: raw}})
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
	f.Add([]byte("--b\r\nX-Seq: 1\r\nX-Tags: 1, \"2\"\r\nContent-Disposition: form-data; name=\"name\"\r\n\r\nRex\r\n--b--\r\n"), "b")
	f.Fuzz(func(t *testing.T, body []byte, boundary string) {
		type upload struct {
			Name  string `json:"name"`
			Photo []byte `json:"photo"`
		}
		// Part headers are parsed: an integer, and an array.
		seq := PartHeader{Name: "X-Seq", Required: true, Check: func(vals []string) error {
			_, err := ParseScalar[int64](vals)
			return err
		}}
		tags := PartHeader{Name: "X-Tags", Array: true, Check: func(vals []string) error {
			_, err := ParseArray[int32](vals)
			return err
		}}
		props := Property{Type: "object", Properties: map[string]Property{"name": {Type: "string", Headers: []PartHeader{tags}}, "photo": {Type: "file"}}}
		_, _ = DecodeMultipart[upload](bytes.Clone(body), "multipart/form-data; boundary="+boundary, props)
		// The same body as positional parts, of each kind of item.
		ct := "multipart/mixed; boundary=" + boundary
		rules := Parts{Prefix: []Property{{ContentTypes: []string{"application/json"}, Headers: []PartHeader{seq}}}, Rest: Property{Headers: []PartHeader{tags}}}
		rules.Item = "json"
		_, _ = DecodeParts[upload](bytes.Clone(body), ct, rules)
		_, _ = DecodeParts[oapi.Part[upload]](bytes.Clone(body), ct, rules)
		rules.Item = "text"
		_, _ = DecodeParts[string](bytes.Clone(body), ct, rules)
		_, _ = DecodeParts[oapi.Part[string]](bytes.Clone(body), ct, rules)
		// As bytes, by each part's transfer encoding, which read back as
		// they were written.
		rules.Item = "bytes"
		if raw, err := DecodeParts[[]byte](bytes.Clone(body), ct, rules); err == nil && len(raw) > 0 {
			plain := Parts{Item: "bytes"}
			again, ct2, err := EncodeParts(raw, "multipart/mixed", plain, oapi.Request)
			if err != nil {
				t.Fatalf("encoding %q: %v", raw, err)
			}
			back, err := DecodeParts[[]byte](again, ct2, plain)
			if err != nil || !reflect.DeepEqual(back, raw) {
				t.Fatalf("%q read back as %q, %v", raw, back, err)
			}
		}
		rules.Item = "file"
		files, err := DecodeParts[oapi.File](bytes.Clone(body), ct, rules)
		// Read as a stream, the parts are the same, unless they end at
		// their Content-Length, which mime/multipart ignores.
		streamed := []oapi.File{}
		var streamErr error
		for f, err := range ScanParts[oapi.File](bytes.NewReader(body), ct, rules, oapi.Request, int64(len(body))) {
			if err != nil {
				streamErr = err
				break
			}
			streamed = append(streamed, f)
		}
		lengths := bytes.Contains(bytes.ToLower(body), []byte("content-length"))
		if err == nil && !lengths && (streamErr != nil || !reflect.DeepEqual(streamed, files)) {
			t.Fatalf("streamed %+v, %v; want %+v", streamed, streamErr, files)
		}
		if err == nil && len(files) > 0 {
			var buf bytes.Buffer
			mt, boundary, err := PartsContentType(ct)
			if err != nil {
				t.Fatal(err)
			}
			pw := NewPartWriter(&buf, boundary, rules, oapi.Request)
			for i := range files {
				if err := pw.Write(&files[i]); err != nil {
					t.Fatalf("writing %+v: %v", files[i], err)
				}
			}
			if err := pw.Close(); err != nil {
				t.Fatal(err)
			}
			back, err := DecodeParts[oapi.File](buf.Bytes(), mt, rules)
			if err != nil || len(back) != len(files) {
				t.Fatalf("written %q, read back %+v, %v", buf.Bytes(), back, err)
			}
			var rescanned []oapi.File
			for f, err := range ScanParts[oapi.File](bytes.NewReader(buf.Bytes()), mt, rules, oapi.Request, int64(buf.Len())) {
				if err != nil {
					t.Fatalf("written %q, scanned: %v", buf.Bytes(), err)
				}
				rescanned = append(rescanned, f)
			}
			if !reflect.DeepEqual(rescanned, back) {
				t.Fatalf("written %q, scanned %+v, want %+v", buf.Bytes(), rescanned, back)
			}
			for i := range back {
				if !bytes.Equal(back[i].Data, files[i].Data) {
					t.Fatalf("part %d: %q, want %q", i, back[i].Data, files[i].Data)
				}
			}
		}
		// As a tuple: JSON, text and a file by position, then files.
		type tuple struct {
			Meta upload                       `part:"0"`
			Note oapi.Optional[string]        `part:"1"`
			Scan oapi.Optional[oapi.File]     `part:"2"`
			More []oapi.Part[json.RawMessage] `part:"rest"`
		}
		rules.Kinds, rules.Item = []string{"json", "text", "file"}, "json"
		if v, err := DecodeTuple[tuple](bytes.Clone(body), ct, rules, oapi.Request); err == nil {
			if _, _, err := EncodeTuple(v, ct, rules, oapi.Response); err != nil {
				t.Fatalf("encoding %+v: %v", v, err)
			}
		}
	})
}

// FuzzObjectJSON checks that converting fields with bracketed keys never
// panics and only produces valid JSON.
func FuzzObjectJSON(f *testing.F) {
	f.Add("owner[age]=3&owner[name]=Ann&ids[]=1&ids[1]=2&meta[x][y]=z")
	f.Add("a[b]=1&owner=%7B%22age%22%3A1%7D&x[=1&[c]=3")
	f.Add("items[0][name]=a&items[0][tags][1]=x&items[3][qty]=2&tree[children][0][children][0][name]=leaf")
	f.Add("items[0]=v&items[0][name]=b&tree[children][x]=1&items[01][qty]=1")
	f.Add("items[][name]=a&items[][qty]=1&items[][name]=b&items[][tags][]=x&grid[0][]=1&grid[1][1]=2&grid[2]=3")
	f.Add("items[][name]=a&items[0][qty]=1&grid=1&grid[][]=2&tree[children][][name]=x&meta[][]=y")
	tree := &Property{}
	*tree = Property{Type: "object", Properties: map[string]Property{"name": {Type: "string"}, "children": {Array: true, Ref: tree}}}
	obj := Property{Type: "object", Properties: map[string]Property{
		"owner": {Type: "object", Properties: map[string]Property{"age": {Type: "integer"}}},
		"ids":   {Type: "integer", Array: true},
		"meta":  {Type: "object", Additional: &Property{Type: "any"}},
		"a[b]":  {Type: "string"},
		"items": {Type: "object", Array: true, Properties: map[string]Property{"name": {Type: "string"}, "tags": {Type: "string", Array: true}, "qty": {Type: "integer"}}},
		"tree":  {Ref: tree},
		"grid":  {Type: "integer", Array: true, Depth: 1},
	}}
	f.Fuzz(func(t *testing.T, query string) {
		q, err := ParseFields(query)
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
// same, for documents with maps, shape references and inline objects
// too, maps of arrays, unions of scalars and objects, and additional
// properties.
func FuzzXMLToJSON(f *testing.F) {
	f.Add([]byte(`<p:pet xmlns:p="urn:pets" id="7"><p:name>Rex</p:name><photoUrls><photoUrl>a</photoUrl></photoUrls><tag><name>x</name></tag><category><id>1</id>Dogs</category></p:pet>`))
	f.Add([]byte(`<p:pet xmlns:p="urn:pets"><extra a="1"><b>2</b><b>3</b>t</extra><vip xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:nil="true"/></p:pet>`))
	f.Add([]byte(`<tree label="r" owner="o"><size>2</size><child><counts><x>1</x></counts></child><child label="b"><child/></child></tree>`))
	f.Add([]byte(`<tree info="x" children="y"><info>z</info><counts/><p:pet xmlns:p="urn:pets" photoUrls="a" tags="b"/></tree>`))
	f.Add([]byte(`<labels><name>a</name><tags><c>r</c><c>b</c></tags><value unit="kg"><amount>1</amount></value><n>2</n></labels>`))
	f.Add([]byte(`<labels><value>7</value><tags/><x>y</x></labels>`))
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, root := range []*XMLNode{petXML, treeXML, labelsXML} {
			out, err := XMLToJSON(data, root)
			if err != nil {
				continue
			}
			if !json.Valid(out) {
				t.Fatalf("invalid JSON %s from %q", out, data)
			}
			doc, err := JSONToXML(out, root)
			if err != nil {
				t.Fatalf("writing %s: %v", out, err)
			}
			back, err := XMLToJSON(doc, root)
			if err != nil || string(back) != string(out) {
				t.Fatalf("%q\n-> %s\n-> %s\n-> %s (%v)", data, out, doc, back, err)
			}
		}
	})
}

// FuzzStreamBodies checks that reading streamed request bodies never
// panics, and that what is read is written back as a stream that reads
// the same: JSON Lines items, server-sent events, and events with JSON
// data.
func FuzzStreamBodies(f *testing.F) {
	f.Add([]byte("{\"a\":1}\n\n[1,2]\n\"x\"\n"))
	f.Add([]byte("event: a\r\ndata: 1\rdata: 2\n\n: c\nretry: 30\nid: 7\ndata\n\n"))
	f.Add([]byte("event: added\ndata: {\"a\":\ndata: [1,2]}\n\nid: x\ndata: \"s\"\n\n"))
	f.Add([]byte("event: card\ndata: <p:card xmlns:p=\"urn:x\" id=\"3\"><p:name>Kit</p:name></p:card>\n\nevent: size\ndata: w=2&h=3\n\n"))
	f.Add([]byte("id: 7\nretry: 100\ndata: TC{\"w\":5}\n\nevent: count\ndata: 4\n\n"))
	c := NewConfig(WithMaxBodyBytes(1 << 10))
	// What is written back is read with a larger limit: escaping (of & as
	// \u0026, for one) can make a line longer than it was.
	big := NewConfig(WithMaxBodyBytes(1 << 20))
	f.Fuzz(func(t *testing.T, data []byte) {
		read := func(c *Config, body []byte) ([]any, error) {
			var items []any
			for item, err := range JSONLinesBody[any](c, httptest.NewRequest("POST", "/", bytes.NewReader(body)), nil) {
				if err != nil {
					return items, err
				}
				items = append(items, item)
			}
			return items, nil
		}
		items, _ := read(c, data)
		var lines []byte
		for _, item := range items {
			line, err := oapi.EncodeRequest(item)
			if err != nil {
				t.Fatalf("encoding %v: %v", item, err)
			}
			lines = append(append(lines, line...), '\n')
		}
		if back, err := read(big, lines); err != nil || !reflect.DeepEqual(back, items) {
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

		decode := func(ev Event, validate bool) (TypedEvent[any], error) {
			v, err := EventData[any](ev, oapi.Request, validate, nil)
			return TypedEvent[any]{ID: ev.ID, Event: ev.Event, Retry: ev.Retry, Data: v}, err
		}
		readTyped := func(c *Config, body []byte) []TypedEvent[any] {
			var typed []TypedEvent[any]
			for ev, err := range TypedEventsBody(c, httptest.NewRequest("POST", "/", bytes.NewReader(body)), decode) {
				if err != nil {
					break
				}
				typed = append(typed, ev)
			}
			return typed
		}
		typed := readTyped(c, data)
		stream = nil
		for _, ev := range typed {
			raw, err := EncodeEvent(ev, "", ev.Data, oapi.Request)
			if err != nil {
				t.Fatalf("encoding %+v: %v", ev, err)
			}
			stream = AppendEvent(stream, raw)
		}
		if back := readTyped(big, stream); !reflect.DeepEqual(back, typed) {
			t.Fatalf("%q\n-> %q\n-> %+v, want %+v", data, stream, back, typed)
		}
	})
}

// FuzzRange checks that the ranges of any Range header are within the
// representation, and that the response written for them reads back as
// those bytes.
func FuzzRange(f *testing.F) {
	f.Add("bytes=0-4", uint16(10))
	f.Add("bytes=-3, 5-", uint16(10))
	f.Add("bytes=0-0,2-2,4-", uint16(7))
	f.Add("bytes=20-", uint16(3))
	all := Ranges{Single: true, Multi: true, Unsatisfiable: true}
	f.Fuzz(func(t *testing.T, header string, n uint16) {
		size := int64(n)
		ranges, err := ParseRange(header, size)
		for _, r := range ranges {
			if r.Start < 0 || r.End < r.Start || r.End >= size {
				t.Fatalf("%q of %d: range %+v", header, size, r)
			}
		}
		body := make([]byte, size)
		for i := range body {
			body[i] = byte(i)
		}
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Range", header)
		rec := httptest.NewRecorder()
		if werr := WriteBytesRanges(rec, req, 200, "application/octet-stream", body, nil, all); werr != nil {
			t.Fatal(werr)
		}
		switch {
		case err != nil:
			if rec.Code != http.StatusRequestedRangeNotSatisfiable {
				t.Fatalf("%q of %d: %d, want 416", header, size, rec.Code)
			}
		case len(ranges) == 0:
			if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), body) {
				t.Fatalf("%q of %d: %d", header, size, rec.Code)
			}
		case len(ranges) == 1:
			start, end, total, err := ParseContentRange(rec.Header().Get("Content-Range"))
			if rec.Code != 206 || err != nil || start != ranges[0].Start || end != ranges[0].End || total != size ||
				!bytes.Equal(rec.Body.Bytes(), body[start:end+1]) {
				t.Fatalf("%q of %d: %d %s, %v", header, size, rec.Code, rec.Header().Get("Content-Range"), err)
			}
		default:
			got, err := DecodeByteRanges(rec.Body.Bytes(), rec.Header().Get("Content-Type"))
			if rec.Code != 206 || err != nil || len(got) != len(ranges) {
				t.Fatalf("%q of %d: %d %v", header, size, rec.Code, err)
			}
			for i, br := range got {
				if br.Start != ranges[i].Start || br.End != ranges[i].End || br.Size != size || !bytes.Equal(br.Data, body[br.Start:br.End+1]) {
					t.Fatalf("%q of %d: part %d is %+v", header, size, i, br)
				}
			}
		}
	})
}

// FuzzCodecValue checks that a JSON document goes to the value a codec
// encodes and back to the same JSON, also as the values a CBOR library
// gives (maps of any keys, int64 and float64 numbers).
func FuzzCodecValue(f *testing.F) {
	f.Add([]byte(`{"a":[1,2.5,"s",true,null],"b":{"c":-3}}`))
	f.Add([]byte(`[{"":{}},[],1e300,"é"]`))
	f.Add([]byte(`9007199254740993`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if !json.Valid(data) {
			return
		}
		same := func(a, b []byte) bool {
			var x, y any
			return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
		}
		v, err := CodecValue(data)
		if err != nil {
			if !strings.Contains(err.Error(), "out of range") {
				t.Fatalf("CodecValue(%s): %v", data, err)
			}
			return
		}
		back, err := CodecJSON(v)
		if err != nil || !same(data, back) {
			t.Fatalf("%s -> %#v -> %s (%v)", data, v, back, err)
		}
		var raw any
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		if dec.Decode(&raw) != nil {
			return
		}
		if back, err := CodecJSON(cborValue(raw)); err != nil || !same(data, back) {
			t.Fatalf("%s as CBOR values -> %s (%v)", data, back, err)
		}
	})
}
