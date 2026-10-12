package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
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
		props := map[string]Property{"name": {Type: "string"}, "photo": {Type: "file"}}
		_, _ = DecodeMultipart[upload](bytes.Clone(body), "multipart/form-data; boundary="+boundary, props)
	})
}
