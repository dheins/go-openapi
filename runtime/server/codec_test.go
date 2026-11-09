package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dheins/go-openapi/oapi"
)

// cborish stands in for a codec of a format goapi does not know, giving
// values as CBOR libraries do: maps of any keys, []byte for strings
// written "bin:...", and int64 or float64 numbers. Its encoding is JSON,
// with the same marks.
type cborish struct{}

func (cborish) Marshal(v any) ([]byte, error) { return json.Marshal(v) }

func (cborish) Unmarshal(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var x any
	if err := dec.Decode(&x); err != nil {
		return err
	}
	*(v.(*any)) = cborValue(x)
	return nil
}

func cborValue(x any) any {
	switch v := x.(type) {
	case map[string]any:
		m := map[any]any{}
		for k, e := range v {
			m[k] = cborValue(e)
		}
		return m
	case []any:
		for i := range v {
			v[i] = cborValue(v[i])
		}
		return v
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return n
		}
		f, _ := v.Float64()
		return f
	case string:
		if b, ok := strings.CutPrefix(v, "bin:"); ok {
			return []byte(b)
		}
	}
	return x
}

type codecPet struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func TestCodecs(t *testing.T) {
	c := NewConfig(WithCodec("Application/X-Cbor; v=1", cborish{}))
	if CodecKey("Application/X-Cbor; v=1") != "application/x-cbor" {
		t.Errorf("CodecKey = %q", CodecKey("Application/X-Cbor; v=1"))
	}
	pet, err := DecodeCodec[codecPet](c, "application/x-cbor", []byte(`{"name":"Rex","age":3}`))
	if err != nil || pet != (codecPet{Name: "Rex", Age: 3}) {
		t.Errorf("decoded %+v, %v", pet, err)
	}
	// A body the codec cannot decode is answered with 400, and so is a
	// value of the wrong type, as a JSON body's (at its JSON pointer, for a
	// generated type).
	_, err = DecodeCodec[codecPet](c, "application/x-cbor", []byte("nope"))
	var ve *oapi.ValidationError
	if !errors.As(err, &ve) || !strings.HasPrefix(ve.Message, "invalid application/x-cbor: ") {
		t.Errorf("invalid body: %v", err)
	}
	_, err = DecodeCodec[codecPet](c, "application/x-cbor", []byte(`{"name":"Rex","age":"old"}`))
	if err == nil || err.Error() != "expected integer, got string" {
		t.Errorf("wrong type: %v", err)
	}

	w := httptest.NewRecorder()
	if err := c.WriteCodec(w, 201, "application/x-cbor", codecPet{Name: "Tom"}, nil); err != nil {
		t.Fatal(err)
	}
	if w.Code != 201 || w.Header().Get("Content-Type") != "application/x-cbor" || w.Body.String() != `{"age":0,"name":"Tom"}` {
		t.Errorf("written %d %q %s", w.Code, w.Header().Get("Content-Type"), w.Body)
	}
	// An unregistered media type is a server error, before anything is
	// written.
	w = httptest.NewRecorder()
	if err := c.WriteCodec(w, 200, "application/cbor", codecPet{}, nil); err == nil || w.Body.Len() != 0 {
		t.Errorf("unregistered: %v %q", err, w.Body)
	}

	c.RequireCodecs("application/x-cbor")
	defer func() {
		if r := recover(); r != "server: no codec for application/cbor, application/msgpack; register one with server.WithCodec" {
			t.Errorf("recovered %v", r)
		}
	}()
	c.RequireCodecs("application/cbor", "application/x-cbor; v=2", "application/msgpack", "application/cbor")
}

// TestCodecJSON checks what a codec's values are as JSON, and that what
// the encoder gives a codec are plain values.
func TestCodecJSON(t *testing.T) {
	when := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		value any
		json  string
	}{
		{map[any]any{"a": int64(1), 2: true, false: nil}, `{"2":true,"a":1,"false":null}`},
		{[]any{[]byte("hi"), 1.5, uint8(7), "x"}, `["aGk=",1.5,7,"x"]`},
		{map[string][]int{"n": {1, 2}}, `{"n":[1,2]}`},
		{when, `"2026-11-09T12:00:00Z"`},
		{(*int)(nil), `null`},
	} {
		got, err := CodecJSON(tt.value)
		if err != nil || string(got) != tt.json {
			t.Errorf("%#v: %s %v, want %s", tt.value, got, err, tt.json)
		}
	}
	for _, bad := range []any{map[any]any{1.5: 1}, []any{make(chan int)}, map[string]any{"f": []float64{1, 0}[0] / 0}} {
		if got, err := CodecJSON(bad); err == nil {
			t.Errorf("%#v: %s, no error", bad, got)
		}
	}
	v, err := CodecValue([]byte(`{"a":[1,2.5,"s",true,null],"b":{"c":9007199254740993}}`))
	want := map[string]any{"a": []any{int64(1), 2.5, "s", true, nil}, "b": map[string]any{"c": int64(9007199254740993)}}
	if err != nil || !reflect.DeepEqual(v, want) {
		t.Errorf("CodecValue = %#v %v", v, err)
	}
}

func TestCodecContent(t *testing.T) {
	codecs := map[string]Codec{"application/x-cbor": cborish{}}
	pet, err := DecodeCodecContent[codecPet](codecs, "application/x-cbor", `{"name":"Rex","age":2}`, oapi.Request)
	if err != nil || pet != (codecPet{Name: "Rex", Age: 2}) {
		t.Errorf("decoded %+v %v", pet, err)
	}
	if _, err := DecodeCodecContent[codecPet](codecs, "application/x-cbor", "{", oapi.Request); err == nil {
		t.Error("invalid: no error")
	}
	text, err := EncodeCodecContent(codecs, "application/x-cbor", codecPet{Name: "Tom"}, oapi.Response)
	if err != nil || text != `{"age":0,"name":"Tom"}` {
		t.Errorf("encoded %q %v", text, err)
	}
	// A header holds a line of text, which a binary codec may not write.
	binary := map[string]Codec{"application/x-bin": binaryCodec{}}
	if _, err := EncodeCodecContent(binary, "application/x-bin", 1, oapi.Request); err == nil || !strings.Contains(err.Error(), "not a line of text") {
		t.Errorf("binary: %v", err)
	}
}

// binaryCodec writes bytes that are no text.
type binaryCodec struct{}

func (binaryCodec) Marshal(any) ([]byte, error)        { return []byte{0xff, 0x00, '\n'}, nil }
func (binaryCodec) Unmarshal(data []byte, v any) error { return errors.New("unused") }
