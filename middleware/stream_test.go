package middleware

import (
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestTextAndStreams(t *testing.T) {
	h, got := newHandler(t, WithMaxBodyBytes(1<<10))
	post := func(contentType, body string) result {
		r := httptest.NewRequest("POST", "/v1/notes", strings.NewReader(body))
		r.Header.Set("Content-Type", contentType)
		return send(h, r)
	}
	long := strings.Repeat("x", 2000)
	for _, tt := range []struct {
		contentType, body string
		status            int
		errors            []string
		// read is what the handler read, if not the whole body.
		read *string
	}{
		{"text/plain", "ab", 204, nil, nil},
		{"text/plain", "a", 400, []string{"body : minLength: got 1, want 2"}, ptr("")},
		{"text/plain", "AB", 400, []string{"body : 'AB' does not match pattern '^[a-z]+$'"}, ptr("")},
		// Lines keep their line ends; blank lines are not items.
		{"application/jsonl", "{\"name\":\"Rex\"}\r\n\n{\"name\":\"Bo\"}", 204, nil, nil},
		{"application/jsonl", "{\"name\":\"Rex\"}\n\n{\"name\":\"\"}\n{\"name\":\"Al\"}\n", 400,
			[]string{"body /1/name: minLength: got 0, want 1"}, ptr("{\"name\":\"Rex\"}\n\n")},
		{"application/jsonl", "{\"name\":", 400, nil, ptr("")},
		{"application/jsonl", "{\"name\":\"Rex\"}\n{\"name\":\"" + long + "\"}\n", 413, nil, ptr("{\"name\":\"Rex\"}\n")},
		{"text/event-stream", "event: add\ndata: {\"name\":\"Rex\"}\n\n: a comment\r\n\r\nevent: note\rdata: hi\r\r", 204, nil, nil},
		{"text/event-stream", "event: add\ndata: {\"name\":\"Rex\"}\n\nevent: note\ndata: long\n\n", 400,
			[]string{"body /1/data: maxLength: got 4, want 3"}, ptr("event: add\ndata: {\"name\":\"Rex\"}\n\n")},
		{"text/event-stream", "event: add\ndata: {}\n\n", 400, []string{"body /0/data/name: required property is missing"}, ptr("")},
		{"text/event-stream", "event: what\ndata: 1\n\n", 400, []string{`body /0/event: unknown event "what"; expected "add", "note"`}, ptr("")},
		{"text/event-stream", "event: note\ndata: " + long + "\n\n", 413, nil, ptr("")},
		// An event the stream does not finish is not one.
		{"text/event-stream", "event: note\ndata: long", 204, nil, nil},
	} {
		got.body = ""
		res := post(tt.contentType, tt.body)
		slices.Sort(res.errors)
		read := tt.body
		if tt.read != nil {
			read = *tt.read
		}
		if res.status != tt.status || (tt.errors != nil && !slices.Equal(res.errors, tt.errors)) || got.body != read {
			t.Errorf("%s %.40q: %d %q, read %q; want %d %q, %q", tt.contentType, tt.body, res.status, res.errors, got.body, tt.status, tt.errors, read)
		}
	}
	if res := post("text/plain", "ab"); res.status != 204 || got.op.Body != "ab" {
		t.Errorf("text body = %#v", got.op.Body)
	}
	if res := post("application/jsonl", ""); res.status != 400 {
		t.Errorf("empty stream: %d", res.status)
	}
}

// TestSplitter cuts streams written a byte at a time as when written at
// once.
func TestSplitter(t *testing.T) {
	for _, tt := range []struct {
		events bool
		in     string
		want   []string
	}{
		{false, "a\nb\r\n\nc", []string{"a\n", "b\r\n", "\n", "c"}},
		{true, "a: 1\nb\r\n\r\n:x\r\rdata\n\nrest", []string{"a: 1\nb\r\n\r\n", ":x\r\r", "data\n\n", "rest"}},
		{true, "data: x\r", []string{"data: x\r"}},
	} {
		for _, step := range []int{1, len(tt.in)} {
			sp := &splitter{events: tt.events}
			var got []string
			for i := 0; i < len(tt.in); i += step {
				sp.write([]byte(tt.in[i:min(i+step, len(tt.in))]))
				for item, ok := sp.next(false); ok; item, ok = sp.next(false) {
					got = append(got, string(item))
				}
			}
			for item, ok := sp.next(true); ok; item, ok = sp.next(true) {
				got = append(got, string(item))
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("%q by %d: %q, want %q", tt.in, step, got, tt.want)
			}
		}
	}
}

func ptr[T any](v T) *T { return &v }
