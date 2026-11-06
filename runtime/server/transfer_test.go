package server

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/textproto"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/oapi"
)

func TestTransferDecoded(t *testing.T) {
	for _, tc := range []struct {
		enc, data, want, err string
	}{
		{"", "raw\x00", "raw\x00", ""},
		{"binary", "raw", "raw", ""},
		{"BASE64", "aGVs\r\nbG8=", "hello", ""},
		{"quoted-printable", "caf=C3=A9", "café", ""},
		{"base64", "!!", "", "invalid base64 data"},
		{"x-gzip", "x", "", `Content-Transfer-Encoding "x-gzip" is not supported`},
	} {
		h := textproto.MIMEHeader{}
		if tc.enc != "" {
			h.Set("Content-Transfer-Encoding", tc.enc)
		}
		got, err := transferDecoded(h, []byte(tc.data))
		if string(got) != tc.want || (err == nil) != (tc.err == "") || err != nil && !strings.Contains(err.Error(), tc.err) {
			t.Errorf("%q %q: %q, %v", tc.enc, tc.data, got, err)
		}
	}
}

func TestBytesParts(t *testing.T) {
	long := bytes.Repeat([]byte{0, 1, 2, 0xff}, 30)
	p := Parts{Item: "bytes"}
	body, ct, err := EncodeParts([][]byte{[]byte("hi"), long}, "multipart/mixed", p, oapi.Request)
	if err != nil {
		t.Fatal(err)
	}
	// Sent base64, in lines of 76.
	if !bytes.Contains(body, []byte("Content-Transfer-Encoding: base64")) {
		t.Fatalf("body %q", body)
	}
	for line := range strings.Lines(string(body)) {
		if len(strings.TrimRight(line, "\r\n")) > 76 {
			t.Errorf("line of %d characters", len(line))
		}
	}
	got, err := DecodeParts[[]byte](body, ct, p)
	if err != nil || len(got) != 2 || string(got[0]) != "hi" || !bytes.Equal(got[1], long) {
		t.Errorf("decoded %q, %v", got, err)
	}
	// A part without a transfer encoding is its bytes; one with headers
	// keeps them.
	body, ct = rawParts(t, textproto.MIMEHeader{"X-Seq": {"1"}}, "raw")
	parts, err := DecodeParts[oapi.Part[[]byte]](body, ct, p)
	if err != nil || len(parts) != 1 || string(parts[0].Value) != "raw" || parts[0].Header.Get("X-Seq") != "1" {
		t.Errorf("raw part %+v, %v", parts, err)
	}
	body, ct = rawParts(t, textproto.MIMEHeader{"Content-Transfer-Encoding": {"base64"}}, "!!")
	var errs oapi.ValidationErrors
	if _, err := DecodeParts[[]byte](body, ct, p); !errors.As(err, &errs) || errs[0].Path != "/0" || !strings.Contains(errs[0].Message, "invalid base64") {
		t.Errorf("invalid: %v", err)
	}
}

// rawParts is a multipart/mixed body of one part with header and data.
func rawParts(t *testing.T, header textproto.MIMEHeader, data string) ([]byte, string) {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	w, err := mw.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(data))
	_ = mw.Close()
	return b.Bytes(), "multipart/mixed; boundary=" + mw.Boundary()
}

func TestBytesFormField(t *testing.T) {
	type upload struct {
		Doc  []byte   `json:"doc"`
		Docs [][]byte `json:"docs"`
	}
	obj := Property{Type: "object", Properties: map[string]Property{"doc": {Type: "bytes"}, "docs": {Type: "bytes", Array: true}}}
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	add := func(name, enc, data string) {
		h := textproto.MIMEHeader{"Content-Disposition": {`form-data; name="` + name + `"`}}
		if enc != "" {
			h.Set("Content-Transfer-Encoding", enc)
		}
		w, _ := mw.CreatePart(h)
		_, _ = w.Write([]byte(data))
	}
	add("doc", "base64", "aGk=")
	add("docs", "", "raw")
	add("docs", "base64", "eA==")
	_ = mw.Close()
	got, err := DecodeMultipart[upload](b.Bytes(), mw.FormDataContentType(), obj)
	if err != nil || string(got.Doc) != "hi" || len(got.Docs) != 2 || string(got.Docs[0]) != "raw" || string(got.Docs[1]) != "x" {
		t.Errorf("decoded %+v, %v", got, err)
	}
	b.Reset()
	mw = multipart.NewWriter(&b)
	add("doc", "base64", "!!")
	_ = mw.Close()
	var errs oapi.ValidationErrors
	if _, err := DecodeMultipart[upload](b.Bytes(), mw.FormDataContentType(), obj); !errors.As(err, &errs) || errs[0].Path != "/doc" {
		t.Errorf("invalid: %v", err)
	}
}

func TestObjectHeader(t *testing.T) {
	type rate struct {
		Limit int64  `json:"limit"`
		Note  string `json:"note,omitempty"`
	}
	obj := Property{Type: "object", Properties: map[string]Property{"limit": {Type: "integer"}, "note": {Type: "string"}}}
	for _, explode := range []bool{false, true} {
		s, err := FormatObject(rate{Limit: 100, Note: "a, b"}, explode, oapi.Response)
		if err != nil {
			t.Fatal(err)
		}
		want := `limit,100,note,"a, b"`
		if explode {
			want = `limit=100,"note=a, b"`
		}
		if s != want {
			t.Errorf("explode %t: %q, want %q", explode, s, want)
		}
		got, err := ParseObjectHeader[rate]([]string{s}, explode, obj, oapi.Response)
		if err != nil || got.Limit != 100 || got.Note != "a, b" {
			t.Errorf("explode %t: %+v, %v", explode, got, err)
		}
	}
	// Repeated header lines are one list.
	if got, err := ParseObjectHeader[rate]([]string{"limit=5", "note=x"}, true, obj, oapi.Response); err != nil || got.Limit != 5 || got.Note != "x" {
		t.Errorf("lines: %+v, %v", got, err)
	}
	for _, bad := range []string{"limit,100,note", "limit,many"} {
		if _, err := ParseObjectHeader[rate]([]string{bad}, false, obj, oapi.Response); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}
