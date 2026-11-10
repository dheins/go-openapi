package petserver

// End-to-end tests of typed events beyond one kind or named kinds of JSON
// or text: kinds told apart by their data, data of XML, form fields, a
// codec and a number as text, and id and retry fields. Not generated;
// codegen's TestGeneratedCode runs them.

import (
	"context"
	"iter"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/api"
	petclient "github.com/dheins/go-openapi/codegen/testdata/golden/petserver/client"
	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/model"
	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/server"
)

// petEventsDoc is a stream of every kind, as a client writes it; the
// TypeScript client's tests (events_more.test.ts) send the same.
const petEventsDoc = "event: added\ndata: {\"name\":\"Rex\"}\n\n" +
	"data: {\"kind\":\"toy\",\"name\":\"ball\"}\n\n" +
	"id: 7\nretry: 100\ndata: {\"kind\":\"treat\",\"calories\":5}\n\n" +
	"event: card\ndata: <p:card xmlns:p=\"urn:example:pets\" id=\"3\"><p:name>Kit</p:name></p:card>\n\n" +
	"event: size\ndata: w=2&h=3\n\n" +
	"event: count\ndata: 4\n\n" +
	"event: coded\ndata: TC{\"w\":5}\n\n"

func TestEventKindsEndToEnd(t *testing.T) {
	_, h := newTestServer()
	r := do(t, h, "POST", "/pets/events", "text/event-stream", petEventsDoc)
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	// Each kind is written back as it came, a pet with its id.
	for _, want := range []string{
		"event: added\ndata: {\"id\":1,\"name\":\"Rex\"}\n\n",
		"data: {\"kind\":\"toy\",\"name\":\"ball\"}\n\n",
		"id: 7\nretry: 100\ndata: {\"kind\":\"treat\",\"calories\":5}\n\n",
		"event: card\ndata: <p:card xmlns:p=\"urn:example:pets\" id=\"3\"><p:name>Kit</p:name></p:card>\n\n",
		"event: size\ndata: h=3&w=2\n\n",
		"event: count\ndata: 4\n\n",
		"event: coded\ndata: TC{\"w\":5}\n\n",
	} {
		if !strings.Contains(r.body, want) {
			t.Errorf("no %q in:\n%s", want, r.body)
		}
	}
	// An event of no kind, or with an invalid id or retry, ends the
	// stream: the status is sent, so the error is logged and the stream
	// stops before it.
	for _, bad := range []string{
		"data: {\"kind\":\"rock\"}\n\n",
		"id: x\ndata: {\"kind\":\"toy\",\"name\":\"ball\"}\n\n",
		"retry: 90000\ndata: {\"kind\":\"toy\",\"name\":\"ball\"}\n\n",
		"event: count\ndata: -1\n\n",
	} {
		if r := do(t, h, "POST", "/pets/events", "text/event-stream", bad); strings.Contains(r.body, "data:") {
			t.Errorf("%q echoed: %d %s", bad, r.status, r.body)
		}
	}

	// The Go client sends and reads every kind.
	_, c := newTestClient(t, petclient.Credentials{})
	sent := []server.TypedEvent[model.PetEventsRequestEventData]{
		{Data: model.NewPetEventsRequestEventData(model.Pet{Name: "Rex"})},
		{Data: model.NewPetEventsRequestEventData(model.Toy{Kind: "toy", Name: "ball"})},
		{ID: "7", Retry: 100, Data: model.NewPetEventsRequestEventData(model.Treat{Kind: "treat", Calories: 5})},
		{Data: model.NewPetEventsRequestEventData(model.Card{ID: 3, Name: "Kit"})},
		{Data: model.NewPetEventsRequestEventData(model.Size{W: oapi.Some(int64(2)), H: oapi.Some(int64(3))})},
		{Data: model.NewPetEventsRequestEventData(model.PetEventsRequestEventDataInteger(4))},
		{Data: model.NewPetEventsRequestEventData(model.PetEventsRequestEventDataSize2{W: oapi.Some(int64(5))})},
	}
	var body iter.Seq2[server.TypedEvent[model.PetEventsRequestEventData], error] = func(yield func(server.TypedEvent[model.PetEventsRequestEventData], error) bool) {
		for _, ev := range sent {
			if !yield(ev, nil) {
				return
			}
		}
	}
	resp := as[api.PetEvents200EventsResponse](t)(c.PetEvents(context.Background(), &api.PetEventsRequest{Body: body}))
	var got []string
	for ev, err := range resp.Events {
		if err != nil {
			t.Fatal(err)
		}
		switch v := ev.Data.Value().(type) {
		case model.Pet:
			got = append(got, ev.Event+" pet "+v.Name)
		case model.Toy:
			got = append(got, "toy "+v.Name)
		case model.Treat:
			got = append(got, ev.ID+" treat")
		case model.Card:
			got = append(got, ev.Event+" card "+v.Name)
		case model.Size:
			got = append(got, ev.Event+" size")
		case model.PetEvents200EventDataInteger:
			got = append(got, ev.Event+" count")
		case model.PetEvents200EventDataSize2:
			got = append(got, ev.Event+" coded")
		}
	}
	want := "added pet Rex|toy ball|7 treat|card card Kit|size size|count count|coded coded"
	if strings.Join(got, "|") != want {
		t.Errorf("events %q, want %q", strings.Join(got, "|"), want)
	}
}
