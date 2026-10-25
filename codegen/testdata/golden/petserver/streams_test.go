package petserver

// Tests of streamed request bodies, over HTTP and through the generated
// client. They are not generated; codegen's TestGeneratedCode runs them.

import (
	"bufio"
	"context"
	"errors"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/api"
	petclient "github.com/dheins/go-openapi/codegen/testdata/golden/petserver/client"
	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/model"
	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/client"
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

// pets yields a pet per name, then err if not nil.
func pets(err error, names ...string) iter.Seq2[model.Pet, error] {
	return func(yield func(model.Pet, error) bool) {
		for _, name := range names {
			if !yield(model.Pet{Name: name}, nil) {
				return
			}
		}
		if err != nil {
			yield(model.Pet{}, err)
		}
	}
}

func TestClientStreams(t *testing.T) {
	ctx := context.Background()
	s, c := newTestClient(t, petclient.Credentials{})
	lines := as[api.ImportLines200JSONResponse](t)
	if r := lines(c.ImportLines(ctx, &api.ImportLinesRequest{Body: pets(nil, "Ann", "Bo", "Cy")})); r.Body != 3 || s.pets[4].Name != "Cy" {
		t.Errorf("import: %+v %+v", r, s.pets)
	}
	if r := lines(c.ImportLines(ctx, &api.ImportLinesRequest{JSONBody: oapi.Some([]model.Pet{{Name: "Di"}})})); r.Body != 1 {
		t.Errorf("import as JSON: %+v", r)
	}
	if got := s.seen["ImportLines"].(*api.ImportLinesRequest); got.ContentType != "application/json" || got.Body != nil {
		t.Errorf("JSON import = %+v", got)
	}

	// An invalid item ends the request before it is sent.
	var invalid *client.InvalidRequest
	if _, err := c.ImportLines(ctx, &api.ImportLinesRequest{Body: pets(nil, "Ed", "")}); !errors.As(err, &invalid) ||
		len(invalid.Problem.Errors) != 1 || invalid.Problem.Errors[0].Pointer != "/1/name" {
		t.Errorf("invalid item: %v", err)
	}
	// So does an error of the iterator.
	stop := errors.New("stop")
	if _, err := c.ImportLines(ctx, &api.ImportLinesRequest{Body: pets(stop, "Fi")}); !errors.Is(err, stop) {
		t.Errorf("iterator error: %v", err)
	}
	// Without validation, the server finds the invalid item.
	_, lax := newTestClient(t, petclient.Credentials{}, client.WithoutRequestValidation())
	_, err := lax.ImportLines(ctx, &api.ImportLinesRequest{Body: pets(nil, "Gus", "")})
	if u := unexpected(t, err, 400); u.Problem == nil || len(u.Problem.Errors) != 1 || u.Problem.Errors[0].Pointer != "/1/name" {
		t.Errorf("server-side invalid item: %+v", u.Problem)
	}

	events := func(yield func(server.Event, error) bool) {
		_ = yield(server.Event{Event: "a", Data: "1\n2"}, nil) && yield(server.Event{ID: "7", Data: "x"}, nil)
	}
	if r := as[api.PostEvents200JSONResponse](t)(c.PostEvents(ctx, &api.PostEventsRequest{Body: events})); r.Body != 2 ||
		!slices.Equal(s.seen["PostEvents"].([]server.Event), []server.Event{{Event: "a", Data: "1\n2"}, {ID: "7", Data: "x"}}) {
		t.Errorf("events: %+v %+v", r, s.seen["PostEvents"])
	}
}

func TestClientFullDuplex(t *testing.T) {
	_, c := newTestClient(t, petclient.Credentials{})
	// Each pet is sent once the one before it has come back, so the
	// request and the response stream at the same time.
	echoed := make(chan string)
	items := func(yield func(model.Pet, error) bool) {
		for _, name := range []string{"Ann", "Bo"} {
			if !yield(model.Pet{Name: name}, nil) {
				return
			}
			select {
			case <-echoed:
			case <-time.After(10 * time.Second):
				yield(model.Pet{}, errors.New("no echo"))
				return
			}
		}
	}
	r := as[api.Echo200JSONLinesResponse](t)(c.Echo(context.Background(), &api.EchoRequest{Body: items}))
	var got []string
	for p, err := range r.Items {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, p.Name)
		echoed <- p.Name
	}
	if !slices.Equal(got, []string{"Ann", "Bo"}) {
		t.Errorf("echoed %q", got)
	}
}
