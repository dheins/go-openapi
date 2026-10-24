package petserver

// End-to-end tests of the generated sender and receivers of webhooks and
// callbacks. They are not generated; codegen's TestGeneratedCode runs
// them.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/api"
	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/model"
	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/client"
	"github.com/dheins/go-openapi/runtime/problem"
	"github.com/dheins/go-openapi/runtime/server"
)

// receiver implements WebhookHandler and CallbackHandler, recording the
// requests it gets.
type receiver struct {
	mu   sync.Mutex
	seen []any
	// gone makes pet events answer 410, and fail an error.
	gone, fail bool
}

func (r *receiver) record(req any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, req)
}

func (r *receiver) last() any {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.seen) == 0 {
		return nil
	}
	return r.seen[len(r.seen)-1]
}

func (r *receiver) PetAdoptedWebhook(_ context.Context, req *api.PetAdoptedWebhookRequest) (api.PetAdoptedWebhookResponse, error) {
	r.record(req)
	return api.PetAdoptedWebhook200JSONResponse{Body: model.PetAdoptedWebhook200ResponseBody{Ok: oapi.Some(req.Body.By != "nobody")}}, nil
}

func (r *receiver) SubscribePetEventCallback(_ context.Context, req *api.SubscribePetEventCallbackRequest) (api.SubscribePetEventCallbackResponse, error) {
	r.record(req)
	switch {
	case r.fail:
		return nil, problem.New(http.StatusServiceUnavailable, "try later")
	case r.gone:
		return api.SubscribePetEventCallback410Response{}, nil
	}
	return api.SubscribePetEventCallback204Response{}, nil
}

func (r *receiver) SubscribePingCallback(_ context.Context, req *api.SubscribePingCallbackRequest) (api.SubscribePingCallbackResponse, error) {
	r.record(req)
	return api.SubscribePingCallback200TextResponse{Body: "pong " + strconv.FormatInt(req.N.Value, 10)}, nil
}

// newReceiver serves r's handlers at the paths a consumer would register:
// the webhook at /hooks/adopted, pet events below /hooks (the URL of the
// subscription) and pings at /ping.
func newReceiver(t *testing.T, r *receiver) *httptest.Server {
	t.Helper()
	cfg := server.NewConfig(server.WithLogger(slog.New(slog.DiscardHandler)))
	cfg.Handle(api.PetAdoptedWebhookOperation, "/hooks/adopted", api.HandlePetAdoptedWebhook(cfg, r))
	cfg.Handle(api.SubscribePetEventCallbackOperation, "/hooks/pets/POST", api.HandleSubscribePetEventCallback(cfg, r))
	cfg.Handle(api.SubscribePingCallbackOperation, "/ping", api.HandleSubscribePingCallback(cfg, r))
	srv := httptest.NewServer(cfg.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func newSender(t *testing.T, opts ...client.Option) *Sender {
	t.Helper()
	s, err := NewSender(opts...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestWebhookRoundTrip(t *testing.T) {
	ctx := context.Background()
	r := &receiver{}
	srv := newReceiver(t, r)
	s := newSender(t)

	req := &api.PetAdoptedWebhookRequest{Body: model.PetAdoptedWebhookRequestBody{Pet: model.Pet{ID: 1, Name: "Rex", Secret: oapi.Some("s")}, By: "Ann"}}
	resp, err := s.PetAdoptedWebhook(ctx, srv.URL+"/hooks/adopted", req)
	if ack, ok := resp.(api.PetAdoptedWebhook200JSONResponse); err != nil || !ok || !ack.Body.Ok.Value {
		t.Fatalf("response %+v %v", resp, err)
	}
	// Sent as a request: write-only properties in, read-only ones out.
	got := r.last().(*api.PetAdoptedWebhookRequest)
	if got.Body.By != "Ann" || got.Body.Pet.Name != "Rex" || got.Body.Pet.Secret.Value != "s" || got.Body.Pet.ID != 0 {
		t.Errorf("received %+v", got.Body)
	}

	// An invalid webhook is not sent; sent anyway, the receiver rejects it.
	invalid := &api.PetAdoptedWebhookRequest{Body: model.PetAdoptedWebhookRequestBody{Pet: model.Pet{Name: "Rex"}}}
	var invalidReq *client.InvalidRequest
	if _, err := s.PetAdoptedWebhook(ctx, srv.URL+"/hooks/adopted", invalid); !errors.As(err, &invalidReq) || invalidReq.Problem.Errors[0].Pointer != "/by" {
		t.Errorf("invalid webhook: %v", err)
	}
	if r.last() != any(got) {
		t.Error("an invalid webhook was sent")
	}
	_, err = newSender(t, client.WithoutRequestValidation()).PetAdoptedWebhook(ctx, srv.URL+"/hooks/adopted", invalid)
	var unexpected *client.UnexpectedResponse
	if !errors.As(err, &unexpected) || unexpected.StatusCode != 400 || unexpected.Problem == nil || unexpected.Problem.Errors[0].Pointer != "/by" {
		t.Errorf("rejected webhook: %v", err)
	}

	// Routes the receiver does not serve.
	if _, err := s.PetAdoptedWebhook(ctx, srv.URL+"/hooks/other", req); !errors.As(err, &unexpected) || unexpected.StatusCode != 404 {
		t.Errorf("unknown route: %v", err)
	}
	if _, err := s.PetAdoptedWebhook(ctx, "/relative", req); err == nil {
		t.Error("sent to a relative URL")
	}
}

func TestCallbackRoundTrip(t *testing.T) {
	ctx := context.Background()
	r := &receiver{}
	srv := newReceiver(t, r)
	s := newSender(t)

	// The subscription, as the API received it, gives the URLs.
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
	if got := r.last().(*api.SubscribePetEventCallbackRequest); got.XEvent != model.SubscribePetEventCallbackXEventCreated || got.Body.Name != "Rex" {
		t.Errorf("received %+v", got)
	}
	r.gone = true
	if resp, err := s.SubscribePetEventCallback(ctx, eventURL, event); err != nil || resp != (api.SubscribePetEventCallback410Response{}) {
		t.Errorf("unsubscribed: %+v %v", resp, err)
	}
	r.fail = true
	var unexpected *client.UnexpectedResponse
	if _, err := s.SubscribePetEventCallback(ctx, eventURL, event); !errors.As(err, &unexpected) || unexpected.StatusCode != 503 || unexpected.Problem.Detail != "try later" {
		t.Errorf("problem: %v", err)
	}
	// The required header is checked before sending, and by the receiver.
	noEvent := &api.SubscribePetEventCallbackRequest{Body: model.Pet{Name: "Rex"}}
	var invalid *client.InvalidRequest
	if _, err := s.SubscribePetEventCallback(ctx, eventURL, noEvent); !errors.As(err, &invalid) {
		t.Errorf("event without X-Event: %v", err)
	}
	_, err = newSender(t, client.WithoutRequestValidation()).SubscribePetEventCallback(ctx, eventURL, noEvent)
	if !errors.As(err, &unexpected) || unexpected.StatusCode != 400 || unexpected.Problem.Errors[0].Parameter != "X-Event" {
		t.Errorf("rejected event: %v", err)
	}

	resp, err := s.SubscribePingCallback(ctx, pingURL, &api.SubscribePingCallbackRequest{N: oapi.Some(int64(3))})
	if pong, ok := resp.(api.SubscribePingCallback200TextResponse); err != nil || !ok || pong.Body != "pong 3" {
		t.Errorf("ping: %+v %v", resp, err)
	}
	if got := r.last().(*api.SubscribePingCallbackRequest); got.N.Value != 3 {
		t.Errorf("ping received %+v", got)
	}
	// The receiver answers the method it does not serve.
	if _, err := s.PetAdoptedWebhook(ctx, pingURL, &api.PetAdoptedWebhookRequest{Body: model.PetAdoptedWebhookRequestBody{Pet: model.Pet{Name: "Rex"}, By: "Ann"}}); !errors.As(err, &unexpected) || unexpected.StatusCode != 405 {
		t.Errorf("wrong method: %v", err)
	}
}
