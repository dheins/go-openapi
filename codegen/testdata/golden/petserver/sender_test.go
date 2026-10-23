package petserver

// End-to-end tests of the generated sender of webhooks and callbacks.
// They are not generated; codegen's TestGeneratedCode runs them.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/api"
	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/model"
	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/client"
)

// received is a request a receiver got.
type received struct {
	method, path, query, event, contentType, body string
}

// newReceiver starts a server recording the requests it gets and
// answering them with respond.
func newReceiver(t *testing.T, respond func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, func() []received) {
	t.Helper()
	var mu sync.Mutex
	var got []received
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, received{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("X-Event"), r.Header.Get("Content-Type"), string(body)})
		mu.Unlock()
		respond(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []received {
		mu.Lock()
		defer mu.Unlock()
		return append([]received(nil), got...)
	}
}

func newSender(t *testing.T, opts ...client.Option) *Sender {
	t.Helper()
	s, err := NewSender(opts...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSendWebhook(t *testing.T) {
	ctx := context.Background()
	srv, got := newReceiver(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	})
	s := newSender(t)

	req := &api.PetAdoptedWebhookRequest{Body: model.PetAdoptedWebhookRequestBody{Pet: model.Pet{ID: 1, Name: "Rex", Secret: oapi.Some("s")}, By: "Ann"}}
	resp, err := s.PetAdoptedWebhook(ctx, srv.URL+"/hooks/adopted", req)
	if ack, ok := resp.(api.PetAdoptedWebhook200JSONResponse); err != nil || !ok || !ack.Body.Ok.Value {
		t.Fatalf("response %+v %v", resp, err)
	}
	r := got()
	if len(r) != 1 || r[0].method != "POST" || r[0].path != "/hooks/adopted" || r[0].contentType != "application/json" {
		t.Fatalf("received %+v", r)
	}
	// Sent like a request: write-only properties in, read-only ones out.
	var body map[string]any
	if err := json.Unmarshal([]byte(r[0].body), &body); err != nil {
		t.Fatal(err)
	}
	if pet := body["pet"].(map[string]any); body["by"] != "Ann" || pet["secret"] != "s" || pet["id"] != nil {
		t.Errorf("body = %s", r[0].body)
	}

	// An invalid webhook is not sent.
	var invalid *client.InvalidRequest
	if _, err := s.PetAdoptedWebhook(ctx, srv.URL, &api.PetAdoptedWebhookRequest{Body: model.PetAdoptedWebhookRequestBody{Pet: model.Pet{Name: "Rex"}}}); !errors.As(err, &invalid) {
		t.Errorf("invalid webhook: %v", err)
	}
	if len(got()) != 1 {
		t.Error("an invalid webhook was sent")
	}
	if _, err := s.PetAdoptedWebhook(ctx, "/relative", req); err == nil {
		t.Error("sent to a relative URL")
	}
}

func TestSendCallbacks(t *testing.T) {
	ctx := context.Background()
	status := 204
	srv, got := newReceiver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.Header().Set("Content-Type", "text/plain")
			io.WriteString(w, "pong "+r.URL.Query().Get("n"))
			return
		}
		w.WriteHeader(status)
	})
	s := newSender(t)

	// The subscription, as the server received it, gives the URLs.
	sub := &api.SubscribeRequest{XCallback: oapi.Some(srv.URL + "/ping"), Body: model.SubscribeRequestBody{URL: srv.URL + "/hooks"}}
	eventURL, err := api.SubscribePetEventCallbackURL(sub)
	if err != nil || eventURL != srv.URL+"/hooks/pets/POST" {
		t.Fatalf("event URL %q, %v", eventURL, err)
	}
	pingURL, err := api.SubscribePingCallbackURL(sub)
	if err != nil || pingURL != srv.URL+"/ping" {
		t.Fatalf("ping URL %q, %v", pingURL, err)
	}
	if _, err := api.SubscribePingCallbackURL(&api.SubscribeRequest{}); err == nil || !strings.Contains(err.Error(), "$request.header.X-Callback is not set") {
		t.Errorf("ping URL without the header: %v", err)
	}

	event := &api.SubscribePetEventCallbackRequest{XEvent: model.SubscribePetEventCallbackXEventCreated, Body: model.Pet{Name: "Rex"}}
	if resp, err := s.SubscribePetEventCallback(ctx, eventURL, event); err != nil || resp != (api.SubscribePetEventCallback204Response{}) {
		t.Fatalf("event: %+v %v", resp, err)
	}
	if r := got(); r[0].path != "/hooks/pets/POST" || r[0].event != "created" || r[0].body != `{"name":"Rex"}` {
		t.Errorf("received %+v", r[0])
	}
	status = 410
	if resp, err := s.SubscribePetEventCallback(ctx, eventURL, event); err != nil || resp != (api.SubscribePetEventCallback410Response{}) {
		t.Errorf("unsubscribed: %+v %v", resp, err)
	}
	status = 500
	var unexpected *client.UnexpectedResponse
	if _, err := s.SubscribePetEventCallback(ctx, eventURL, event); !errors.As(err, &unexpected) || unexpected.StatusCode != 500 {
		t.Errorf("unexpected: %v", err)
	}
	// The required header is checked before sending.
	var invalid *client.InvalidRequest
	if _, err := s.SubscribePetEventCallback(ctx, eventURL, &api.SubscribePetEventCallbackRequest{Body: model.Pet{Name: "Rex"}}); !errors.As(err, &invalid) {
		t.Errorf("event without X-Event: %v", err)
	}

	resp, err := s.SubscribePingCallback(ctx, pingURL, &api.SubscribePingCallbackRequest{N: oapi.Some(int64(3))})
	if pong, ok := resp.(api.SubscribePingCallback200TextResponse); err != nil || !ok || pong.Body != "pong 3" {
		t.Errorf("ping: %+v %v", resp, err)
	}
	if r := got(); r[len(r)-1].method != "GET" || r[len(r)-1].query != "n=3" {
		t.Errorf("ping received %+v", r[len(r)-1])
	}
}
