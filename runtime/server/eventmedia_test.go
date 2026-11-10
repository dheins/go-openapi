package server

import (
	"errors"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/oapi"
)

type eventPet struct {
	Name string `json:"name"`
}

func TestEventMedia(t *testing.T) {
	pet := &XMLNode{Name: "pet", Type: "object", Properties: []XMLProperty{{"name", &XMLNode{Name: "name", Type: "string"}}}}
	form := Property{Type: "object", Properties: map[string]Property{"name": {Type: "string"}}}
	codecs := map[string]Codec{"application/x-cbor": cborish{}}
	for _, tt := range []struct {
		m    EventMedia
		data string
	}{
		{EventMedia{Kind: "json"}, `{"name":"Rex"}`},
		{EventMedia{Kind: "xml", XML: pet}, `<pet><name>Rex</name></pet>`},
		{EventMedia{Kind: "form", Form: form}, `name=Rex`},
		{EventMedia{Kind: "codec", MediaType: "application/x-cbor", Codecs: codecs}, `{"name":"Rex"}`},
	} {
		got, err := EventValue[eventPet](Event{Data: tt.data}, tt.m, oapi.Request, true, nil)
		if err != nil || got.Name != "Rex" {
			t.Errorf("%s: %+v %v", tt.m.Kind, got, err)
			continue
		}
		ev, err := EncodeEventValue(TypedEvent[eventPet]{ID: "1"}, "added", got, tt.m, oapi.Request)
		if err != nil || ev.Event != "added" || ev.ID != "1" || !strings.Contains(ev.Data, "Rex") {
			t.Errorf("%s: wrote %+v %v", tt.m.Kind, ev, err)
		}
		back, err := EventValue[eventPet](ev, tt.m, oapi.Request, true, nil)
		if err != nil || back != got {
			t.Errorf("%s: read back %+v %v", tt.m.Kind, back, err)
		}
	}
	// A number as text, and problems at the data.
	n, err := EventValue[int64](Event{Data: " 42 "}, EventMedia{Kind: "scalar", Type: "integer"}, oapi.Request, true, nil)
	if err != nil || n != 42 {
		t.Errorf("scalar: %d %v", n, err)
	}
	if ev, err := EncodeEventValue(TypedEvent[int64]{}, "", int64(7), EventMedia{Kind: "scalar", Type: "integer"}, oapi.Response); err != nil || ev.Data != "7" {
		t.Errorf("scalar written: %+v %v", ev, err)
	}
	_, err = EventValue[int64](Event{Data: "many"}, EventMedia{Kind: "scalar", Type: "integer"}, oapi.Request, true, nil)
	var errs oapi.ValidationErrors
	if !errors.As(err, &errs) || errs[0].Path != "/data" {
		t.Errorf("not a number: %v", err)
	}
	positive := func(n int64) error {
		if n < 1 {
			return &oapi.ValidationError{Message: "must be at least 1"}
		}
		return nil
	}
	if err := CheckEventField("/retry", int64(0), positive); err == nil || !strings.Contains(err.Error(), "/retry") {
		t.Errorf("retry: %v", err)
	}
	if err := CheckEventField[int64]("/retry", 0, nil); err != nil {
		t.Errorf("no check: %v", err)
	}
	if _, err := EncodeEventValue(TypedEvent[eventPet]{Event: "other"}, "added", eventPet{}, EventMedia{Kind: "form", Form: form}, oapi.Request); err == nil {
		t.Error("the event of another kind: no error")
	}
	err = NoEventKind("what", "added")
	if !errors.As(err, &errs) || len(errs) != 2 || errs[0].Path != "/event" || errs[1].Path != "/data" {
		t.Errorf("no kind: %v", err)
	}
	if err := NoEventKind(""); !strings.Contains(err.Error(), "no kind of event") {
		t.Errorf("no kind, unnamed: %v", err)
	}
}
