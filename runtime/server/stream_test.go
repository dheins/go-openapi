package server

import (
	"bufio"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/problem"
)

type streamPet struct {
	Name string `json:"name"`
}

func TestJSONLinesBody(t *testing.T) {
	c := NewConfig()
	r := httptest.NewRequest("POST", "/", strings.NewReader("{\"name\":\"a\"}\n\n{\"name\":\"\"}\n{\"name\":\"c\"}\n"))
	nonEmpty := func(p streamPet) error {
		if p.Name == "" {
			return &oapi.ValidationError{Path: "/name", Message: "must not be empty"}
		}
		return nil
	}
	items := JSONLinesBody(c, r, nonEmpty)
	var names []string
	var last error
	for p, err := range items {
		if err != nil {
			last = err
			break
		}
		names = append(names, p.Name)
	}
	// The blank line is not an item, so the invalid one is item 1.
	var p *problem.Problem
	if !slices.Equal(names, []string{"a"}) || !errors.As(last, &p) || p.Status != http.StatusBadRequest ||
		len(p.Errors) != 1 || p.Errors[0].In != "body" || p.Errors[0].Pointer != "/1/name" {
		t.Errorf("names %q, error %v", names, last)
	}
	for _, err := range items {
		if err == nil || !strings.Contains(err.Error(), "read already") {
			t.Errorf("second range: %v", err)
		}
	}
}

func TestBodyMediaType(t *testing.T) {
	c := NewConfig()
	types := []string{"application/jsonl", "application/json"}
	for _, tc := range []struct {
		contentType, body, want, problem string
	}{
		{"application/jsonl; charset=utf-8", "x", "application/jsonl", ""},
		{"", "x", "application/jsonl", ""},
		{"application/json", "x", "application/json", ""},
		{"text/csv", "x", "", "415"},
		{"application/jsonl", "", "", "body: request body is required"},
	} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
		if tc.contentType != "" {
			r.Header.Set("Content-Type", tc.contentType)
		}
		var b Binder
		got, ok := c.BodyMediaType(r, &b, types, true)
		var msg string
		if p := b.Problem(); b.fatal != nil {
			msg = p.Title
			if p.Status == http.StatusUnsupportedMediaType {
				msg = "415"
			}
		} else if len(p.Errors) > 0 {
			msg = p.Errors[0].In + ": " + p.Errors[0].Detail
		}
		if got != tc.want || ok != (tc.want != "") || msg != tc.problem {
			t.Errorf("%q %q: %q %v %q", tc.contentType, tc.body, got, ok, msg)
		}
	}
}

func TestEvents(t *testing.T) {
	// CR, LF and CRLF all end lines; retry is digits only.
	in := "event: a\r\ndata: 1\rdata: 2\n\n: comment\nretry: -5\nid: 7\ndata\n\nretry: 30\ndata: x\r\r"
	var got []Event
	for ev, err := range ScanEvents(bufio.NewScanner(strings.NewReader(in))) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ev)
	}
	want := []Event{{Event: "a", Data: "1\n2"}, {ID: "7"}, {Retry: 30, Data: "x"}}
	if !slices.Equal(got, want) {
		t.Errorf("events = %+v", got)
	}

	// Line ends cannot start other fields.
	out := string(AppendEvent(nil, Event{ID: "1\nevent: x", Event: "e\r", Data: "a\r\nb"}))
	if out != "id: 1 event: x\nevent: e \ndata: a\ndata: b\n\n" {
		t.Errorf("appended %q", out)
	}
}
