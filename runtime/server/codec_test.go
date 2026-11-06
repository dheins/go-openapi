package server

import (
	"bytes"
	"encoding/gob"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/dheins/go-openapi/oapi"
)

// gobCodec stands in for a codec of a format goapi does not know.
type gobCodec struct{}

func (gobCodec) Marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	err := gob.NewEncoder(&b).Encode(v)
	return b.Bytes(), err
}

func (gobCodec) Unmarshal(data []byte, v any) error {
	return gob.NewDecoder(bytes.NewReader(data)).Decode(v)
}

type codecPet struct {
	Name string
	Age  int
}

func TestCodecs(t *testing.T) {
	c := NewConfig(WithCodec("Application/X-Gob; v=1", gobCodec{}))
	if CodecKey("Application/X-Gob; v=1") != "application/x-gob" {
		t.Errorf("CodecKey = %q", CodecKey("Application/X-Gob; v=1"))
	}
	body, _ := gobCodec{}.Marshal(codecPet{Name: "Rex", Age: 3})
	pet, err := DecodeCodec[codecPet](c, "application/x-gob", body)
	if err != nil || pet != (codecPet{Name: "Rex", Age: 3}) {
		t.Errorf("decoded %+v, %v", pet, err)
	}
	// A body the codec cannot decode is answered with 400.
	_, err = DecodeCodec[codecPet](c, "application/x-gob", []byte("nope"))
	var ve *oapi.ValidationError
	if !errors.As(err, &ve) || ve.Message[:len("invalid application/x-gob: ")] != "invalid application/x-gob: " {
		t.Errorf("invalid body: %v", err)
	}

	w := httptest.NewRecorder()
	if err := c.WriteCodec(w, 201, "application/x-gob", codecPet{Name: "Tom"}, nil); err != nil {
		t.Fatal(err)
	}
	var back codecPet
	if err := (gobCodec{}).Unmarshal(w.Body.Bytes(), &back); w.Code != 201 || w.Header().Get("Content-Type") != "application/x-gob" || err != nil || back.Name != "Tom" {
		t.Errorf("written %d %q %+v %v", w.Code, w.Header().Get("Content-Type"), back, err)
	}
	// An unregistered media type is a server error, before anything is
	// written.
	w = httptest.NewRecorder()
	if err := c.WriteCodec(w, 200, "application/cbor", codecPet{}, nil); err == nil || w.Body.Len() != 0 {
		t.Errorf("unregistered: %v %q", err, w.Body)
	}

	c.RequireCodecs("application/x-gob")
	defer func() {
		if r := recover(); r != "server: no codec for application/cbor, application/msgpack; register one with server.WithCodec" {
			t.Errorf("recovered %v", r)
		}
	}()
	c.RequireCodecs("application/cbor", "application/x-gob; v=2", "application/msgpack", "application/cbor")
}
