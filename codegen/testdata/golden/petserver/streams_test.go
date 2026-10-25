package petserver

// Tests of streamed request bodies over HTTP. They are not generated;
// codegen's TestGeneratedCode runs them.

import (
	"bufio"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dheins/go-openapi/runtime/server"
)

func TestStreamedRequestBodies(t *testing.T) {
	s, h := newTestServer()
	r := do(t, h, "POST", "/pets/lines", "application/jsonl", "{\"name\":\"Ann\"}\n\n{\"name\":\"Bo\",\"tag\":\"dog\"}\n")
	if r.status != 200 || r.body != "2" || s.pets[2].Name != "Ann" || s.pets[3].Tag.Value != "dog" || s.pets[3].ID != 4 {
		t.Errorf("jsonl: %d %s %+v", r.status, r.body, s.pets)
	}
	// The same operation takes the pets all at once.
	if r := do(t, h, "POST", "/pets/lines", "application/json", `[{"name":"Cy"}]`); r.status != 200 || r.body != "1" || len(s.pets) != 5 {
		t.Errorf("json: %d %s", r.status, r.body)
	}
	// An invalid item ends the stream: the items before it were read.
	r = do(t, h, "POST", "/pets/lines", "application/jsonl", "{\"name\":\"Di\"}\n{\"name\":\"\"}\n{\"name\":\"Ed\"}\n")
	if got := problemErrors(t, r); r.status != 400 || !slices.Equal(got, []string{"body /1/name: length must be at least 1"}) || s.pets[5].Name != "Di" || len(s.pets) != 6 {
		t.Errorf("invalid item: %d %q", r.status, got)
	}
	if r := do(t, h, "POST", "/pets/lines", "application/jsonl", "{\"name\":\n"); r.status != 400 {
		t.Errorf("malformed item: %d %s", r.status, r.body)
	}
	if got := problemErrors(t, do(t, h, "POST", "/pets/lines", "application/jsonl", "")); !slices.Equal(got, []string{"body: request body is required"}) {
		t.Errorf("no body: %q", got)
	}
	if r := do(t, h, "POST", "/pets/lines", "text/csv", "Ann\n"); r.status != 415 {
		t.Errorf("csv: %d %s", r.status, r.body)
	}

	r = do(t, h, "POST", "/events", "text/event-stream", "event: a\ndata: 1\ndata: 2\n\n: comment\nid: 7\ndata: x\n\n")
	want := []server.Event{{Event: "a", Data: "1\n2"}, {ID: "7", Data: "x"}}
	if got := s.seen["PostEvents"].([]server.Event); r.status != 200 || r.body != "2" || !slices.Equal(got, want) {
		t.Errorf("events: %d %s %+v", r.status, r.body, got)
	}
}

func TestStreamedLineLimit(t *testing.T) {
	// MaxBodyBytes limits each line, not the stream.
	h := NewServer(newStore(), auth{}, server.WithLogger(slog.New(slog.DiscardHandler)), server.WithMaxBodyBytes(32))
	short := strings.Repeat("{\"name\":\"Ann\"}\n", 10)
	if r := do(t, h, "POST", "/pets/lines", "application/jsonl", short); r.status != 200 || r.body != "10" {
		t.Errorf("short lines: %d %s", r.status, r.body)
	}
	long := "{\"name\":\"" + strings.Repeat("a", 40) + "\"}\n"
	if r := do(t, h, "POST", "/pets/lines", "application/jsonl", long); r.status != 413 {
		t.Errorf("long line: %d %s", r.status, r.body)
	}
	if r := do(t, h, "POST", "/events", "text/event-stream", "data: "+strings.Repeat("a", 40)+"\n\n"); r.status != 413 {
		t.Errorf("long event line: %d %s", r.status, r.body)
	}
}

func TestStreamedFullDuplex(t *testing.T) {
	_, h := newTestServer()
	srv := httptest.NewServer(h)
	defer srv.Close()

	// Each pet comes back before the next one is sent, over HTTP/1.1.
	pr, pw := io.Pipe()
	req, err := http.NewRequest("POST", srv.URL+"/v1/echo", pr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	// A stuck exchange fails instead of hanging.
	timer := time.AfterFunc(10*time.Second, func() { pw.CloseWithError(io.ErrNoProgress) })
	defer timer.Stop()
	// The server answers before reading the body, so Do returns first.
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	lines := bufio.NewScanner(resp.Body)
	for _, name := range []string{"Ann", "Bo"} {
		if _, err := io.WriteString(pw, "{\"name\":\""+name+"\"}\n"); err != nil {
			t.Fatal(err)
		}
		if !lines.Scan() || lines.Text() != "{\"id\":0,\"name\":\""+name+"\"}" {
			t.Fatalf("echo of %s: %q %v", name, lines.Text(), lines.Err())
		}
	}
	pw.Close()
	if lines.Scan() {
		t.Errorf("extra line %q", lines.Text())
	}
}
