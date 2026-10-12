package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dheins/go-openapi/examples/petstore/api"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(newHandler(newStore(), "secret", slog.New(slog.DiscardHandler)))
	t.Cleanup(srv.Close)
	return srv
}

func send(t *testing.T, srv *httptest.Server, method, path, key, body string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(data)
}

func TestPetstore(t *testing.T) {
	srv := newTestServer(t)

	if resp, _ := send(t, srv, "POST", "/v1/pets", "", `{"name":"Rex","status":"available"}`); resp.StatusCode != 401 {
		t.Fatalf("create without key: %d", resp.StatusCode)
	}
	for _, name := range []string{"Rex", "Tom", "Kit"} {
		resp, body := send(t, srv, "POST", "/v1/pets", "secret", `{"name":"`+name+`","status":"available"}`)
		if resp.StatusCode != 201 {
			t.Fatalf("create %s: %d %s", name, resp.StatusCode, body)
		}
	}
	if resp, body := send(t, srv, "POST", "/v1/pets", "secret", `{"name":"Rex","status":"sold"}`); resp.StatusCode != 409 || !strings.Contains(body, "already exists") {
		t.Errorf("duplicate: %d %s", resp.StatusCode, body)
	}

	resp, body := send(t, srv, "GET", "/v1/pets?limit=2", "", "")
	var page []api.Pet
	if err := json.Unmarshal([]byte(body), &page); err != nil || len(page) != 2 || resp.Header.Get("X-Next") != "2" {
		t.Fatalf("first page: %v %s next=%q", err, body, resp.Header.Get("X-Next"))
	}
	_, body = send(t, srv, "GET", "/v1/pets?limit=2&after=2", "", "")
	if err := json.Unmarshal([]byte(body), &page); err != nil || len(page) != 1 || page[0].Name != "Kit" {
		t.Fatalf("second page: %v %s", err, body)
	}
	if resp, body := send(t, srv, "GET", "/v1/pets?status=sold,lost", "", ""); resp.StatusCode != 400 || !strings.Contains(body, `must be one of`) {
		t.Errorf("invalid status: %d %s", resp.StatusCode, body)
	}

	if resp, _ := send(t, srv, "DELETE", "/v1/pets/2", "secret", ""); resp.StatusCode != 204 {
		t.Errorf("delete: %d", resp.StatusCode)
	}
	if resp, body := send(t, srv, "GET", "/v1/pets/2", "", ""); resp.StatusCode != 404 || !strings.Contains(body, "no pet with id 2") {
		t.Errorf("deleted pet: %d %s", resp.StatusCode, body)
	}
	if resp, body := send(t, srv, "GET", "/v1/openapi.json", "", ""); resp.StatusCode != 200 || !strings.Contains(body, `"title":"Petstore example"`) {
		t.Errorf("document: %d %.60s", resp.StatusCode, body)
	}
}

func TestWatchPets(t *testing.T) {
	srv := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL+"/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content type %q", resp.Header.Get("Content-Type"))
	}

	if resp, body := send(t, srv, "POST", "/v1/pets", "secret", `{"name":"Rex","status":"available"}`); resp.StatusCode != 201 {
		t.Fatalf("create: %d %s", resp.StatusCode, body)
	}
	lines := bufio.NewScanner(resp.Body)
	var event []string
	for lines.Scan() && lines.Text() != "" {
		event = append(event, lines.Text())
	}
	want := []string{"id: 1", "event: pet", `data: {"id":1,"name":"Rex","status":"available"}`}
	if strings.Join(event, "\n") != strings.Join(want, "\n") {
		t.Errorf("event:\n%s\nwant\n%s", strings.Join(event, "\n"), strings.Join(want, "\n"))
	}
}
