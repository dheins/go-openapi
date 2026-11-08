package petserver

// Tests of the callbacks of webhooks and callbacks: the API sends the
// webhook petAdopted, its consumer sends the callback confirm back, and
// the API sends that callback's callback, receipt, to the consumer. They
// are not generated; codegen's TestGeneratedCode runs them.

import (
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/api"
	petclient "github.com/dheins/go-openapi/codegen/testdata/golden/petserver/client"
	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/model"
	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/client"
	"github.com/dheins/go-openapi/runtime/server"
)

func (r *receiver) SendReceiptCallback(ctx context.Context, req *api.SendReceiptCallbackRequest) (api.SendReceiptCallbackResponse, error) {
	r.record(ctx, req)
	return api.SendReceiptCallback204Response{}, nil
}

// confirmations implements InboundCallbackHandler: the API receives
// confirmations and answers each with a receipt, sent by sender.
type confirmations struct {
	sender *Sender
	got    []*api.ConfirmAdoptionCallbackRequest
}

func (c *confirmations) ConfirmAdoptionCallback(ctx context.Context, req *api.ConfirmAdoptionCallbackRequest) (api.ConfirmAdoptionCallbackResponse, error) {
	c.got = append(c.got, req)
	target, err := api.SendReceiptCallbackURL(req)
	if err != nil {
		return nil, err
	}
	if _, err := c.sender.SendReceiptCallback(ctx, target, &api.SendReceiptCallbackRequest{Body: "receipt: " + req.Body.Note.Value}); err != nil {
		return nil, err
	}
	return api.ConfirmAdoptionCallback204Response{}, nil
}

// apiKeys implements InboundSecurityHandler: the API issued consumers the
// key "api-key".
type apiKeys struct{}

func (apiKeys) APIKey(ctx context.Context, key string, _ []string) (context.Context, error) {
	if key != "api-key" {
		return nil, errors.New("unknown key")
	}
	return ctx, nil
}

func TestNestedCallbacks(t *testing.T) {
	ctx := context.Background()
	logger := server.WithLogger(slog.New(slog.DiscardHandler))

	// The consumer receives the webhook and the receipt.
	r := &receiver{}
	ccfg := server.NewConfig(logger)
	ccfg.Handle(api.PetAdoptedWebhookOperation, "/hooks/adopted", api.HandlePetAdoptedWebhook(ccfg, r, hookAuth{}))
	ccfg.Handle(api.SendReceiptCallbackOperation, "/receipts/1", api.HandleSendReceiptCallback(ccfg, r, hookAuth{}))
	consumer := httptest.NewServer(ccfg.Handler())
	defer consumer.Close()

	// The API receives confirmations, at the URL it puts in the webhook.
	sender := newSender(t, SenderCredentials{HookKey: consumerKey, HookToken: func(context.Context, string) (string, error) { return "token", nil }})
	confirms := &confirmations{sender: sender}
	acfg := server.NewConfig(logger)
	acfg.Handle(api.ConfirmAdoptionCallbackOperation, "/confirm/1", api.HandleConfirmAdoptionCallback(acfg, confirms, apiKeys{}))
	apiSrv := httptest.NewServer(acfg.Handler())
	defer apiSrv.Close()

	hook := &api.PetAdoptedWebhookRequest{Body: model.PetAdoptedWebhookRequestBody{
		Pet: model.Pet{ID: 1, Name: "Rex"}, By: "Ann", ConfirmURL: oapi.Some(apiSrv.URL + "/confirm/1"),
	}}
	if _, err := sender.PetAdoptedWebhook(ctx, consumer.URL+"/hooks/adopted", hook); err != nil {
		t.Fatal(err)
	}

	// The consumer confirms to the URL the webhook gave, with its key.
	got := r.last().(*api.PetAdoptedWebhookRequest)
	target, err := api.ConfirmAdoptionCallbackURL(got)
	if err != nil || target != apiSrv.URL+"/confirm/1" {
		t.Fatalf("confirm URL = %q, %v", target, err)
	}
	cs, err := petclient.NewSender(petclient.SenderCredentials{APIKey: func(context.Context, string) (string, error) { return "api-key", nil }})
	if err != nil {
		t.Fatal(err)
	}
	confirm := &api.ConfirmAdoptionCallbackRequest{Body: model.ConfirmAdoptionCallbackRequestBody{ReceiptURL: consumer.URL + "/receipts/1", Note: oapi.Some("Rex")}}
	if resp, err := cs.ConfirmAdoptionCallback(ctx, target, confirm); err != nil || resp != (api.ConfirmAdoptionCallback204Response{}) {
		t.Fatalf("confirm: %+v %v", resp, err)
	}
	if len(confirms.got) != 1 || confirms.got[0].Body.Note.Value != "Rex" {
		t.Errorf("confirmations = %+v", confirms.got)
	}
	// The API sent the receipt back, with the consumer's token.
	if receipt, ok := r.last().(*api.SendReceiptCallbackRequest); !ok || receipt.Body != "receipt: Rex" || r.lastCaller() != "token" {
		t.Errorf("receipt = %+v from %v", r.last(), r.lastCaller())
	}

	// A confirmation without the API's key is refused before it is read.
	bad, err := petclient.NewSender(petclient.SenderCredentials{APIKey: func(context.Context, string) (string, error) { return "wrong", nil }})
	if err != nil {
		t.Fatal(err)
	}
	var unexpected *client.UnexpectedResponse
	if _, err := bad.ConfirmAdoptionCallback(ctx, target, confirm); !errors.As(err, &unexpected) || unexpected.StatusCode != 401 {
		t.Errorf("a wrong key: %v", err)
	}
	if len(confirms.got) != 1 {
		t.Errorf("a refused confirmation was handled: %d", len(confirms.got))
	}
}
