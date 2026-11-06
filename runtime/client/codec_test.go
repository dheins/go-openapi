package client

import (
	"bytes"
	"encoding/gob"
	"errors"
	"net/http"
	"testing"
)

type gobCodec struct{}

func (gobCodec) Marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	err := gob.NewEncoder(&b).Encode(v)
	return b.Bytes(), err
}

func (gobCodec) Unmarshal(data []byte, v any) error {
	return gob.NewDecoder(bytes.NewReader(data)).Decode(v)
}

func TestCodecs(t *testing.T) {
	c, err := NewConfig(WithBaseURL("http://pets.test"), WithCodec("application/x-gob", gobCodec{}))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RequireCodecs("application/x-gob", "Application/X-Gob; v=1"); err != nil {
		t.Error(err)
	}
	if err := c.RequireCodecs("application/cbor"); err == nil || err.Error() != "client: no codec for application/cbor; register one with client.WithCodec" {
		t.Errorf("missing: %v", err)
	}

	r := NewRequest("POST")
	r.CodecBody(c, "application/x-gob", map[string]int{"a": 1})
	var sent map[string]int
	if r.err != nil || r.contentType != "application/x-gob" || (gobCodec{}).Unmarshal(r.body, &sent) != nil || sent["a"] != 1 {
		t.Errorf("body %q %q %v", r.contentType, r.body, r.err)
	}
	r = NewRequest("POST")
	r.CodecBody(c, "application/cbor", 1)
	if r.err == nil {
		t.Error("no codec: no error")
	}

	resp := &http.Response{StatusCode: 200}
	var got map[string]int
	body, _ := gobCodec{}.Marshal(map[string]int{"b": 2})
	if err := c.DecodeCodec(resp, body, "application/x-gob", &got); err != nil || got["b"] != 2 {
		t.Errorf("decoded %v %v", got, err)
	}
	var invalid *InvalidResponse
	if err := c.DecodeCodec(resp, []byte("x"), "application/x-gob", &got); !errors.As(err, &invalid) || invalid.StatusCode != 200 {
		t.Errorf("invalid: %v", err)
	}
}
