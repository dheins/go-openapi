package client

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// jsonCodec stands in for a codec of a format goapi does not know; its
// encoding is JSON.
type jsonCodec struct{}

func (jsonCodec) Marshal(v any) ([]byte, error)      { return json.Marshal(v) }
func (jsonCodec) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

// codecOwner has a read-only property, which requests leave out.
type codecOwner struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func TestCodecs(t *testing.T) {
	c, err := NewConfig(WithBaseURL("http://pets.test"), WithCodec("application/x-json", jsonCodec{}))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RequireCodecs("application/x-json", "Application/X-Json; v=1"); err != nil {
		t.Error(err)
	}
	if err := c.RequireCodecs("application/cbor"); err == nil || err.Error() != "client: no codec for application/cbor; register one with client.WithCodec" {
		t.Errorf("missing: %v", err)
	}

	r := NewRequest("POST")
	r.CodecBody(c, "application/x-json", map[string]int{"a": 1})
	if r.err != nil || r.contentType != "application/x-json" || string(r.body) != `{"a":1}` {
		t.Errorf("body %q %q %v", r.contentType, r.body, r.err)
	}
	r = NewRequest("POST")
	r.CodecBody(c, "application/cbor", 1)
	if r.err == nil {
		t.Error("no codec: no error")
	}

	resp := &http.Response{StatusCode: 200}
	var got codecOwner
	if err := c.DecodeCodec(resp, []byte(`{"id":2,"name":"Ann"}`), "application/x-json", &got); err != nil || got != (codecOwner{2, "Ann"}) {
		t.Errorf("decoded %+v %v", got, err)
	}
	var invalid *InvalidResponse
	if err := c.DecodeCodec(resp, []byte("x"), "application/x-json", &got); !errors.As(err, &invalid) || invalid.StatusCode != 200 {
		t.Errorf("invalid: %v", err)
	}
	// A value of the wrong type is an invalid response too, at its pointer.
	if err := c.DecodeCodec(resp, []byte(`{"id":"two"}`), "application/x-json", &got); !errors.As(err, &invalid) {
		t.Errorf("wrong type: %v", err)
	}
}

func TestMediaPattern(t *testing.T) {
	for _, tt := range []struct{ mt, want string }{
		{"application/json", "application/json"},
		{"Application/X-Cbor; v=1", "application/*"},
		{"text/plain", "text/plain"},
	} {
		if got := MediaPattern(tt.mt, "application/json", "application/*"); got != tt.want {
			t.Errorf("%s: %s, want %s", tt.mt, got, tt.want)
		}
	}
	c, err := NewConfig(WithBaseURL("http://pets.test"), WithCodec("application/x-json", jsonCodec{}))
	if err != nil {
		t.Fatal(err)
	}
	// A range is not a media type to send a body as.
	r := NewRequest("POST")
	r.CodecBody(c, "application/*", 1)
	if r.err == nil || !strings.Contains(r.err.Error(), "ContentType") {
		t.Errorf("range: %v", r.err)
	}
}
