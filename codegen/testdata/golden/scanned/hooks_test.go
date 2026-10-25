package scanned

// End-to-end tests of the webhooks, callbacks and multipart bodies goapi
// scan finds in ./service: the generated sender against the generated
// receivers, and the client against the server. They are not generated;
// codegen's TestGeneratedCode runs them.

import (
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"net/textproto"
	"sync"
	"testing"

	"github.com/dheins/go-openapi/codegen/testdata/golden/scanned/api"
	scannedclient "github.com/dheins/go-openapi/codegen/testdata/golden/scanned/client"
	"github.com/dheins/go-openapi/codegen/testdata/golden/scanned/model"
	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/client"
	"github.com/dheins/go-openapi/runtime/server"
)

// hooks implements WebhookHandler, CallbackHandler and
// ReceiverSecurityHandler: the consumer issued the key "consumer-key".
type hooks struct {
	mu       sync.Mutex
	adoption []model.Adoption
	events   []model.Pet
	// callers are who sent each request: "key", or nil without one.
	callers []any
}

type callerKey struct{}

func (h *hooks) APIKey(ctx context.Context, key string, _ []string) (context.Context, error) {
	if key != "consumer-key" {
		return nil, errors.New("unknown key")
	}
	return context.WithValue(ctx, callerKey{}, "key"), nil
}

func (h *hooks) PetAdoptedWebhook(ctx context.Context, req *api.PetAdoptedWebhookRequest) (api.PetAdoptedWebhookResponse, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.adoption = append(h.adoption, req.Body)
	h.callers = append(h.callers, ctx.Value(callerKey{}))
	return api.PetAdoptedWebhook204Response{}, nil
}

func (h *hooks) NotifyPetCallback(ctx context.Context, req *api.NotifyPetCallbackRequest) (api.NotifyPetCallbackResponse, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, req.Body)
	h.callers = append(h.callers, ctx.Value(callerKey{}))
	return api.NotifyPetCallback204Response{}, nil
}

func (h *hooks) lastCaller() any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.callers[len(h.callers)-1]
}

func TestScannedWebhooks(t *testing.T) {
	ctx := context.Background()
	h := &hooks{}
	cfg := server.NewConfig(server.WithLogger(slog.New(slog.DiscardHandler)))
	cfg.Handle(api.PetAdoptedWebhookOperation, "/hooks/adopted", api.HandlePetAdoptedWebhook(cfg, h, h))
	cfg.Handle(api.NotifyPetCallbackOperation, "/hooks/pets", api.HandleNotifyPetCallback(cfg, h, h))
	consumer := httptest.NewServer(cfg.Handler())
	defer consumer.Close()

	sender := func(creds SenderCredentials) *Sender {
		s, err := NewSender(creds)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	key := SenderCredentials{APIKey: func(context.Context, string) (string, error) { return "consumer-key", nil }}

	// The webhook, with the consumer's key.
	adoption := model.Adoption{Pet: model.Pet{Name: "Rex", Kind: model.KindDog}, By: "Ann"}
	if r, err := sender(key).PetAdoptedWebhook(ctx, consumer.URL+"/hooks/adopted", &api.PetAdoptedWebhookRequest{Body: adoption}); err != nil || r != (api.PetAdoptedWebhook204Response{}) {
		t.Fatalf("webhook: %+v %v", r, err)
	}
	if len(h.adoption) != 1 || h.adoption[0].By != "Ann" || h.lastCaller() != "key" {
		t.Errorf("received %+v from %v", h.adoption, h.lastCaller())
	}
	var invalid *client.InvalidRequest
	if _, err := sender(key).PetAdoptedWebhook(ctx, consumer.URL+"/hooks/adopted", &api.PetAdoptedWebhookRequest{Body: model.Adoption{Pet: adoption.Pet}}); !errors.As(err, &invalid) {
		t.Errorf("invalid webhook: %v", err)
	}
	if _, err := sender(SenderCredentials{}).PetAdoptedWebhook(ctx, consumer.URL+"/hooks/adopted", &api.PetAdoptedWebhookRequest{Body: adoption}); !errors.Is(err, client.ErrNoCredentials) {
		t.Errorf("webhook without a key: %v", err)
	}
	wrong := SenderCredentials{APIKey: func(context.Context, string) (string, error) { return "stolen", nil }}
	var unexpected *client.UnexpectedResponse
	if _, err := sender(wrong).PetAdoptedWebhook(ctx, consumer.URL+"/hooks/adopted", &api.PetAdoptedWebhookRequest{Body: adoption}); !errors.As(err, &unexpected) || unexpected.StatusCode != 401 {
		t.Errorf("wrong key: %v", err)
	}

	// The callback, to the URL the subscription gave, with a key or none.
	s, c := newClient(t, scannedclient.Credentials{})
	if _, err := c.Subscribe(ctx, &api.SubscribeRequest{Body: model.Subscription{URL: consumer.URL + "/hooks"}}); err != nil {
		t.Fatal(err)
	}
	target, err := api.NotifyPetCallbackURL(s.seen["Subscribe"].(*api.SubscribeRequest))
	if err != nil || target != consumer.URL+"/hooks/pets" {
		t.Fatalf("callback URL %q, %v", target, err)
	}
	for _, creds := range []SenderCredentials{key, {}} {
		if _, err := sender(creds).NotifyPetCallback(ctx, target, &api.NotifyPetCallbackRequest{Body: model.Pet{Name: "Tom", Kind: model.KindCat}}); err != nil {
			t.Fatalf("callback: %v", err)
		}
	}
	if len(h.events) != 2 || h.events[1].Name != "Tom" || h.callers[len(h.callers)-2] != "key" || h.lastCaller() != nil {
		t.Errorf("events %+v from %v", h.events, h.callers)
	}
}

func TestScannedMultipart(t *testing.T) {
	ctx := context.Background()
	s, c := newClient(t, scannedclient.Credentials{})
	photo := oapi.File{Name: "rex.png", ContentType: "image/png", Header: textproto.MIMEHeader{"X-Checksum": {"abc"}}, Data: []byte("PNG")}
	if _, err := c.UploadPhoto(ctx, &api.UploadPhotoRequest{ID: 1, Body: model.Upload{Photo: photo, Caption: oapi.Some("Rex")}}); err != nil {
		t.Fatal(err)
	}
	got := s.seen["UploadPhoto"].(*api.UploadPhotoRequest)
	if got.ID != 1 || string(got.Body.Photo.Data) != "PNG" || got.Body.Photo.Header.Get("X-Checksum") != "abc" || got.Body.Caption.Value != "Rex" {
		t.Errorf("upload = %+v", got)
	}
	// The encodings are checked: the photo's content type and header.
	var unexpected *client.UnexpectedResponse
	gif := photo
	gif.ContentType = "image/gif"
	if _, err := c.UploadPhoto(ctx, &api.UploadPhotoRequest{ID: 1, Body: model.Upload{Photo: gif}}); !errors.As(err, &unexpected) || unexpected.StatusCode != 400 {
		t.Errorf("a GIF: %v", err)
	}
	unsigned := photo
	unsigned.Header = nil
	if _, err := c.UploadPhoto(ctx, &api.UploadPhotoRequest{ID: 1, Body: model.Upload{Photo: unsigned}}); !errors.As(err, &unexpected) || unexpected.StatusCode != 400 {
		t.Errorf("no checksum: %v", err)
	}
}
