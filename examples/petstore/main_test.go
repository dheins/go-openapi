package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dheins/go-openapi/examples/petstore/gen/api"
	petclient "github.com/dheins/go-openapi/examples/petstore/gen/client"
	"github.com/dheins/go-openapi/examples/petstore/gen/model"
	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/client"
	"github.com/dheins/go-openapi/runtime/server"
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
	var page []model.Pet
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

// TestPetstoreClient drives the server through the generated client.
func TestPetstoreClient(t *testing.T) {
	srv := newTestServer(t)
	ctx := context.Background()
	newClient := func(key string) *petclient.Client {
		t.Helper()
		var creds petclient.Credentials
		if key != "" {
			creds.APIKey = func(context.Context) (string, error) { return key, nil }
		}
		c, err := petclient.New(creds, client.WithBaseURL(srv.URL+"/v1"), client.WithHTTPClient(srv.Client()))
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	anon, c := newClient(""), newClient("secret")

	rex := &api.CreatePetRequest{Body: model.Pet{Name: "Rex", Status: model.PetStatusAvailable}}
	if _, err := anon.CreatePet(ctx, rex); !errors.Is(err, client.ErrNoCredentials) {
		t.Fatalf("create without key: %v", err)
	}
	var unexpected *client.UnexpectedResponse
	if _, err := newClient("wrong").CreatePet(ctx, rex); !errors.As(err, &unexpected) || unexpected.StatusCode != 401 {
		t.Fatalf("create with a wrong key: %v", err)
	}
	for _, name := range []string{"Rex", "Tom", "Kit"} {
		resp, err := c.CreatePet(ctx, &api.CreatePetRequest{Body: model.Pet{Name: name, Status: model.PetStatusAvailable}})
		if created, ok := resp.(api.CreatePet201JSONResponse); err != nil || !ok || created.Body.Name != name || created.Body.ID == 0 {
			t.Fatalf("create %s: %+v %v", name, resp, err)
		}
	}
	resp, err := c.CreatePet(ctx, rex)
	if conflict, ok := resp.(api.CreatePet409JSONResponse); err != nil || !ok || !strings.Contains(conflict.Body.Detail.Value, "already exists") {
		t.Errorf("duplicate: %+v %v", resp, err)
	}

	list, err := anon.ListPets(ctx, &api.ListPetsRequest{Limit: 2})
	page, ok := list.(api.ListPets200JSONResponse)
	if err != nil || !ok || len(page.Body) != 2 || page.XNext.Value != 2 {
		t.Fatalf("first page: %+v %v", list, err)
	}
	list, err = anon.ListPets(ctx, &api.ListPetsRequest{Limit: 2, After: page.XNext})
	if page, ok := list.(api.ListPets200JSONResponse); err != nil || !ok || len(page.Body) != 1 || page.Body[0].Name != "Kit" || page.XNext.Set {
		t.Fatalf("second page: %+v %v", list, err)
	}
	// An invalid request is reported before it is sent.
	var invalid *client.InvalidRequest
	if _, err := anon.ListPets(ctx, &api.ListPetsRequest{Status: oapi.Some([]model.PetStatus{"lost"})}); !errors.As(err, &invalid) {
		t.Errorf("invalid status: %v", err)
	}

	if resp, err := c.DeletePet(ctx, &api.DeletePetRequest{PetID: 2}); err != nil || resp != (api.DeletePet204Response{}) {
		t.Errorf("delete: %+v %v", resp, err)
	}
	got, err := anon.GetPet(ctx, &api.GetPetRequest{PetID: 2})
	if missing, ok := got.(api.GetPet404JSONResponse); err != nil || !ok || !strings.Contains(missing.Body.Detail.Value, "no pet with id 2") {
		t.Errorf("deleted pet: %+v %v", got, err)
	}
	if health, err := anon.GetHealth(ctx, nil); err != nil || health != (api.GetHealth200TextResponse{Body: "OK"}) {
		t.Errorf("health: %+v %v", health, err)
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

func TestHealth(t *testing.T) {
	srv := newTestServer(t)
	// No API key is needed, and the body is plain text.
	resp, body := send(t, srv, "GET", "/v1/health", "", "")
	if resp.StatusCode != 200 || body != "OK" {
		t.Fatalf("health: %d %q", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content type = %q", ct)
	}
}

func TestLogging(t *testing.T) {
	for _, bad := range [][2]string{{"loud", "json"}, {"info", "xml"}} {
		if _, err := newLogger(bad[0], bad[1]); err == nil {
			t.Errorf("newLogger(%q, %q) accepted", bad[0], bad[1])
		}
	}
	if _, err := newLogger("debug", "text"); err != nil {
		t.Errorf("newLogger(debug, text): %v", err)
	}

	// The access log and the server's own entries, tagged with the
	// operation, as JSON.
	var out bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&out, nil)).With("service", "petstore")
	srv := httptest.NewServer(newHandler(newStore(), "secret", logger, server.WithAccessLog(slog.LevelInfo)))
	t.Cleanup(srv.Close)
	send(t, srv, "POST", "/v1/pets", "wrong", `{"name":"Rex","status":"available"}`)
	send(t, srv, "GET", "/v1/pets/42", "", "")

	var entries []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var e map[string]any
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("%v: %s", err, line)
		}
		entries = append(entries, e)
	}
	type entry struct{ msg, operation string }
	var got []entry
	for _, e := range entries {
		got = append(got, entry{e["msg"].(string), e["operation"].(string)})
	}
	want := []entry{{"request not authorized", "createPet"}, {"request", "createPet"}, {"request", "getPet"}}
	if !slices.Equal(got, want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
	if denied := entries[0]; denied["reason"] != "wrong API key" || denied["service"] != "petstore" {
		t.Errorf("denied = %v", denied)
	}
	if missing := entries[2]; missing["status"] != 404.0 || missing["route"] != "/pets/{petId}" {
		t.Errorf("access entry = %v", missing)
	}
}
