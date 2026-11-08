package petserver

// End-to-end tests of sending streamed request bodies again: a retrying
// middleware whose first sending fails partway, as a reset connection
// does. Not generated; codegen's TestGeneratedCode runs them.

import (
	"bufio"
	"context"
	"errors"
	"iter"
	"net/http"
	"net/textproto"
	"slices"
	"testing"

	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/api"
	petclient "github.com/dheins/go-openapi/codegen/testdata/golden/petserver/client"
	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/model"
	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/client"
	"github.com/dheins/go-openapi/runtime/server"
)

// resetAfter is retrying middleware whose first sending of a request
// reads lines lines of its body and fails, as a connection reset then
// would; it sends the request again with its GetBody.
func resetAfter(lines int, attempts *int) func(client.Doer) client.Doer {
	return func(next client.Doer) client.Doer {
		return client.DoerFunc(func(r *http.Request) (*http.Response, error) {
			*attempts++
			br := bufio.NewReader(r.Body)
			for range lines {
				if _, err := br.ReadString('\n'); err != nil {
					return nil, err
				}
			}
			r.Body.Close()
			*attempts++
			body, err := r.GetBody()
			if err != nil {
				return nil, err
			}
			again := r.Clone(r.Context())
			again.Body = body
			return next.Do(again)
		})
	}
}

// once is items that may be ranged over once only, as a stream read from
// a socket or a file is.
func once[T any](items iter.Seq2[T, error]) iter.Seq2[T, error] {
	used := false
	return func(yield func(T, error) bool) {
		if used {
			var zero T
			yield(zero, errors.New("ranged over twice"))
			return
		}
		used = true
		for v, err := range items {
			if !yield(v, err) {
				return
			}
		}
	}
}

func TestClientStreamRetry(t *testing.T) {
	ctx := context.Background()
	attempts := 0
	s, c := newTestClient(t, petclient.Credentials{}, client.WithMiddleware(resetAfter(2, &attempts)))

	before := len(s.pets)
	r := as[api.ImportLines200JSONResponse](t)(c.ImportLines(ctx, &api.ImportLinesRequest{Body: once(pets(nil, "Ann", "Bo", "Cy"))}))
	if r.Body != 3 || attempts != 2 || len(s.pets) != before+3 || s.pets[len(s.pets)-1].Name != "Cy" {
		t.Errorf("import: %+v in %d attempts, pets %+v", r, attempts, s.pets[before:])
	}

	attempts = 0
	events := func(yield func(server.TypedEvent[model.Pet], error) bool) {
		for _, name := range []string{"Ann", "Bo", "Cy"} {
			if !yield(server.TypedEvent[model.Pet]{Data: model.Pet{Name: name}}, nil) {
				return
			}
		}
	}
	// An event is two lines and the blank one ending it.
	_, c = newTestClient(t, petclient.Credentials{}, client.WithMiddleware(resetAfter(4, &attempts)))
	typed := as[api.PostTypedEvents200JSONResponse](t)(c.PostTypedEvents(ctx, &api.PostTypedEventsRequest{Body: once(iter.Seq2[server.TypedEvent[model.Pet], error](events))}))
	if !slices.Equal(typed.Body, []string{"Ann", "Bo", "Cy"}) || attempts != 2 {
		t.Errorf("typed events: %+v in %d attempts", typed.Body, attempts)
	}

	attempts = 0
	photos := func(yield func(oapi.File, error) bool) {
		for _, seq := range []string{"1", "2"} {
			if !yield(oapi.File{ContentType: "image/png", Header: textproto.MIMEHeader{"X-Seq": {seq}}, Data: []byte("PNG" + seq)}, nil) {
				return
			}
		}
	}
	_, c = newTestClient(t, petclient.Credentials{}, client.WithMiddleware(resetAfter(3, &attempts)))
	resp := as[api.StreamPhotos200MixedResponse](t)(c.StreamPhotos(ctx, &api.StreamPhotosRequest{PetID: 1, Body: once(iter.Seq2[oapi.File, error](photos))}))
	var got []string
	for ack, err := range resp.Items {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ack.Value)
	}
	if !slices.Equal(got, []string{"photo 1: 4 bytes", "photo 2: 4 bytes"}) || attempts != 2 {
		t.Errorf("photos: %q in %d attempts", got, attempts)
	}

	// Past the replay limit, the request fails rather than sending part
	// of the stream again.
	attempts = 0
	_, c = newTestClient(t, petclient.Credentials{}, client.WithMiddleware(resetAfter(2, &attempts)), client.WithReplayLimit(16))
	if _, err := c.ImportLines(ctx, &api.ImportLinesRequest{Body: pets(nil, "Ann", "Bo", "Cy")}); !errors.Is(err, client.ErrNotReplayable) {
		t.Errorf("past the limit: %v", err)
	}
}
