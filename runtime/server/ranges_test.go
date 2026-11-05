package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/oapi"
)

func TestParseRange(t *testing.T) {
	for _, tc := range []struct {
		header string
		size   int64
		want   []Range
		err    error
	}{
		{"bytes=0-4", 10, []Range{{0, 4}}, nil},
		{"bytes=5-", 10, []Range{{5, 9}}, nil},
		{"bytes=-3", 10, []Range{{7, 9}}, nil},
		{"bytes=-30", 10, []Range{{0, 9}}, nil},
		{"bytes=8-20", 10, []Range{{8, 9}}, nil},
		{"Bytes = 0-1 , ,4-5", 10, []Range{{0, 1}, {4, 5}}, nil},
		// Ranges outside it are dropped, and with none left it fails.
		{"bytes=20-30, 0-0", 10, []Range{{0, 0}}, nil},
		{"bytes=20-30", 10, nil, ErrRangeNotSatisfiable},
		{"bytes=-0", 10, nil, ErrRangeNotSatisfiable},
		{"bytes=0-", 0, nil, ErrRangeNotSatisfiable},
		// Others are ignored.
		{"", 10, nil, nil},
		{"items=0-1", 10, nil, nil},
		{"bytes=", 10, nil, nil},
		{"bytes=5-4", 10, nil, nil},
		{"bytes=a-b", 10, nil, nil},
		{"bytes=1", 10, nil, nil},
		{"bytes=+1-2", 10, nil, nil},
		{"bytes=99999999999999999999-", 10, nil, nil},
		{"bytes=0-9,0-9", 10, nil, nil},
		{"bytes=" + strings.Repeat("0-0,", maxRanges+1), 100, nil, nil},
	} {
		got, err := ParseRange(tc.header, tc.size)
		if !reflect.DeepEqual(got, tc.want) || !errors.Is(err, tc.err) {
			t.Errorf("ParseRange(%q, %d) = %v, %v; want %v, %v", tc.header, tc.size, got, err, tc.want, tc.err)
		}
	}
}

func TestParseContentRange(t *testing.T) {
	for _, tc := range []struct {
		in               string
		start, end, size int64
		ok               bool
	}{
		{"bytes 0-4/10", 0, 4, 10, true},
		{"bytes 3-3/*", 3, 3, -1, true},
		{"BYTES  2-5/6", 2, 5, 6, true},
		{"bytes 4-3/10", 0, 0, 0, false},
		{"bytes 0-10/10", 0, 0, 0, false},
		{"bytes */10", 0, 0, 0, false},
		{"items 0-1/2", 0, 0, 0, false},
		{"", 0, 0, 0, false},
	} {
		start, end, size, err := ParseContentRange(tc.in)
		if (err == nil) != tc.ok || tc.ok && (start != tc.start || end != tc.end || size != tc.size) {
			t.Errorf("ParseContentRange(%q) = %d, %d, %d, %v", tc.in, start, end, size, err)
		}
	}
}

func TestWriteBytesRanges(t *testing.T) {
	body := []byte("0123456789")
	all := Ranges{Single: true, Multi: true, Unsatisfiable: true}
	validators := http.Header{"Etag": {`"v1"`}, "Last-Modified": {"Mon, 02 Jan 2006 15:04:05 GMT"}}
	send := func(method string, rs Ranges, headers ...string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/", nil)
		for i := 0; i+1 < len(headers); i += 2 {
			r.Header.Set(headers[i], headers[i+1])
		}
		rec := httptest.NewRecorder()
		if err := WriteBytesRanges(rec, r, 200, "text/plain", body, validators, rs); err != nil {
			t.Fatal(err)
		}
		return rec
	}
	describe := func(rec *httptest.ResponseRecorder) string {
		return fmt.Sprintf("%d %s %q", rec.Code, rec.Header().Get("Content-Range"), rec.Body.String())
	}
	for _, tc := range []struct {
		name    string
		method  string
		rs      Ranges
		headers []string
		want    string
	}{
		{"whole", "GET", all, nil, `200  "0123456789"`},
		{"one", "GET", all, []string{"Range", "bytes=2-4"}, `206 bytes 2-4/10 "234"`},
		{"suffix", "GET", all, []string{"Range", "bytes=-2"}, `206 bytes 8-9/10 "89"`},
		{"one undeclared", "GET", Ranges{Multi: true}, []string{"Range", "bytes=2-4"}, `200  "0123456789"`},
		{"several undeclared", "GET", Ranges{Single: true}, []string{"Range", "bytes=0-0,2-2"}, `200  "0123456789"`},
		{"not GET", "POST", all, []string{"Range", "bytes=2-4"}, `200  "0123456789"`},
		{"ETag", "GET", all, []string{"Range", "bytes=0-0", "If-Range", `"v1"`}, `206 bytes 0-0/10 "0"`},
		{"other ETag", "GET", all, []string{"Range", "bytes=0-0", "If-Range", `"v2"`}, `200  "0123456789"`},
		{"weak ETag", "GET", all, []string{"Range", "bytes=0-0", "If-Range", `W/"v1"`}, `200  "0123456789"`},
		{"date", "GET", all, []string{"Range", "bytes=0-0", "If-Range", "Mon, 02 Jan 2006 15:04:05 GMT"}, `206 bytes 0-0/10 "0"`},
		{"other date", "GET", all, []string{"Range", "bytes=0-0", "If-Range", "Tue, 03 Jan 2006 15:04:05 GMT"}, `200  "0123456789"`},
		{"unsatisfiable undeclared", "GET", Ranges{Single: true}, []string{"Range", "bytes=20-"}, `200  "0123456789"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := send(tc.method, tc.rs, tc.headers...)
			if got := describe(rec); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
			if tc.method == "GET" && rec.Header().Get("Accept-Ranges") != "bytes" && (tc.rs.Single || tc.rs.Multi) {
				t.Error("no Accept-Ranges")
			}
		})
	}

	// Several ranges are parts of a multipart/byteranges body.
	rec := send("GET", all, "Range", "bytes=0-1, 8-")
	if rec.Code != 206 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "multipart/byteranges; boundary=") {
		t.Fatalf("%d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	got, err := DecodeByteRanges(rec.Body.Bytes(), rec.Header().Get("Content-Type"))
	want := []oapi.ByteRange{{Start: 0, End: 1, Size: 10, ContentType: "text/plain", Data: []byte("01")}, {Start: 8, End: 9, Size: 10, ContentType: "text/plain", Data: []byte("89")}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("ranges %+v, %v", got, err)
	}

	// One that cannot be satisfied is a problem, with the size.
	rec = send("GET", all, "Range", "bytes=20-")
	if rec.Code != 416 || rec.Header().Get("Content-Range") != "bytes */10" || !strings.Contains(rec.Body.String(), "outside the 10 bytes") {
		t.Errorf("%d %s %s", rec.Code, rec.Header().Get("Content-Range"), rec.Body)
	}
}

func TestByteRanges(t *testing.T) {
	ranges := []oapi.ByteRange{{Start: 2, End: 3, Size: -1, ContentType: "image/png", Data: []byte("PN")}}
	body, ct, err := EncodeByteRanges(ranges)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Content-Range: bytes 2-3/*") {
		t.Errorf("body %q", body)
	}
	if got, err := DecodeByteRanges(body, ct); err != nil || !reflect.DeepEqual(got, ranges) {
		t.Errorf("decoded %+v, %v", got, err)
	}
	// A range is checked against its data, when written and when read.
	for _, br := range []oapi.ByteRange{{Start: 3, End: 2}, {Start: 0, End: 1, Size: 1, Data: []byte("ab")}, {Start: 0, End: 1, Size: 5, Data: []byte("a")}} {
		if _, _, err := EncodeByteRanges([]oapi.ByteRange{br}); err == nil {
			t.Errorf("encoded %+v", br)
		}
	}
	body, ct, _ = EncodeParts([]oapi.File{
		{Data: []byte("x")},
		{Header: map[string][]string{"Content-Range": {"bytes 0-1/5"}}, Data: []byte("x")},
	}, "multipart/byteranges", Parts{Item: "file"}, oapi.Response)
	var errs oapi.ValidationErrors
	if _, err := DecodeByteRanges(body, ct); !errors.As(err, &errs) || len(errs) != 2 || errs[0].Path != "/0" ||
		errs[0].Message != `part header "Content-Range" is missing` || errs[1].Message != "bytes 0-1 hold 1 bytes of data" {
		t.Errorf("invalid parts: %v", err)
	}
}
