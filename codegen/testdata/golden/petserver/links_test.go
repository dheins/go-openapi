package petserver

// Tests of the links of responses: the requests they lead to, built by
// the generated methods. They are not generated; codegen's
// TestGeneratedCode runs them.

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/api"
	petclient "github.com/dheins/go-openapi/codegen/testdata/golden/petserver/client"
	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/model"
	"github.com/dheins/go-openapi/oapi"
)

func TestLinks(t *testing.T) {
	ctx := context.Background()
	_, c := newTestClient(t, petclient.Credentials{})
	req := &api.CreatePetRequest{Body: model.Pet{Name: "Ann", Tag: oapi.Some("cat")}}
	created := as[api.CreatePet201JSONResponse](t)(c.CreatePet(ctx, req))

	// From the response's body, with a constant.
	get, err := created.GetPetLink(req)
	if err != nil || get.PetID != created.Body.ID || get.Session.Value != "s1" {
		t.Fatalf("GetPet link: %+v %v", get, err)
	}
	if p := as[api.GetPet200JSONResponse](t)(c.GetPet(ctx, get)); p.Body.Name != "Ann" {
		t.Errorf("linked pet = %+v", p.Body)
	}
	// From the body of the request the response answers.
	cp, err := created.CopyPetLink(req)
	if err != nil || cp.PetID != created.Body.ID || cp.Destination != "Ann" {
		t.Fatalf("CopyPet link: %+v %v", cp, err)
	}
	// The whole response body as the request body: read-only properties
	// are not sent.
	again, err := created.AgainLink(req)
	if err != nil || again.Body.Name != "Ann" || again.Body.Tag.Value != "cat" {
		t.Fatalf("Again link: %+v %v", again, err)
	}
	if r := as[api.CreatePet201JSONResponse](t)(c.CreatePet(ctx, again)); r.Body.ID == created.Body.ID {
		t.Errorf("the pet was not created again: %+v", r.Body)
	}

	// From the status code, the method and the URL, and expressions
	// embedded in text, which a number parameter parses.
	facts, err := created.FactsLink(req, "http://pets.test/v1/pets")
	if err != nil || facts.PetID != 201 || facts.Session.Value != "POST http://pets.test/v1/pets#Ann" {
		t.Fatalf("Facts link: %+v %v", facts, err)
	}
	if emb, err := created.EmbeddedLink(req); err != nil || emb.PetID != created.Body.ID {
		t.Errorf("Embedded link: %+v %v", emb, err)
	}
	if _, err := created.ByNameLink(req); err == nil || !strings.Contains(err.Error(), "link ByName: {$request.body#/name}") {
		t.Errorf("a name as a pet ID: %v", err)
	}

	// From a header; a request value that is not set leaves the linked
	// one unset.
	list := &api.ListPetsRequest{Tags: oapi.Some([]string{"cat"})}
	page := as[api.ListPets200JSONResponse](t)(c.ListPets(ctx, list))
	all, err := page.AllLink(list)
	if err != nil || all.Limit.Value != int32(page.XTotal) || !slices.Equal(all.Tags.Value, []string{"cat"}) {
		t.Fatalf("All link: %+v %v", all, err)
	}
	if all, err := page.AllLink(&api.ListPetsRequest{}); err != nil || all.Tags.Set {
		t.Errorf("All link without tags: %+v %v", all, err)
	}
	// A value the linked request cannot hold is an error naming the link.
	page.XTotal = 1 << 40
	if _, err := page.AllLink(list); err == nil || !strings.Contains(err.Error(), "link All: $response.header.X-Total") {
		t.Errorf("a limit over int32: %v", err)
	}
}

// TestLinkTextValues follows links giving text with expressions embedded
// to arrays, objects and bodies, and filling in expressions inside a
// body.
func TestLinkTextValues(t *testing.T) {
	ctx := context.Background()
	s, c := newTestClient(t, petclient.Credentials{})
	req := &api.CreatePetRequest{Body: model.Pet{Name: "Ann", Tag: oapi.Some("cat")}}
	created := as[api.CreatePet201JSONResponse](t)(c.CreatePet(ctx, req))

	// Arrays and objects from text as their styles write them, or JSON.
	rep, err := created.ReportsLink(req)
	if err != nil {
		t.Fatalf("Reports link: %v", err)
	}
	want := &api.GetReportsRequest{
		IDs: []int64{created.Body.ID, 7}, Fields: oapi.Some([]string{"Ann", "tag"}),
		Filter: oapi.Some(model.Filter{Role: "Ann"}), XMeta: oapi.Some(model.Filter{Role: "Ann", Age: oapi.Some(int64(201))}),
		Prefs: oapi.Some(model.Prefs{Theme: oapi.Some("Ann")}),
	}
	if !reflect.DeepEqual(rep, want) {
		t.Errorf("Reports link:\n got %+v\nwant %+v", rep, want)
	}
	as[api.GetReports204Response](t)(c.GetReports(ctx, rep))
	if got := s.seen["GetReports"].(*api.GetReportsRequest); !reflect.DeepEqual(got, want) {
		t.Errorf("reports sent:\n got %+v\nwant %+v", got, want)
	}

	// Expressions inside a body; a value that is not there leaves its
	// property out.
	renamed, err := created.RenameLink(req)
	if err != nil || renamed.Body.Name != "copy of Ann" || renamed.Body.Tag.Value != "cat" || renamed.Body.Secret.Value != "code 201" {
		t.Fatalf("Rename link: %+v %v", renamed, err)
	}
	as[api.CreatePet201JSONResponse](t)(c.CreatePet(ctx, renamed))
	untagged := as[api.CreatePet201JSONResponse](t)(c.CreatePet(ctx, &api.CreatePetRequest{Body: model.Pet{Name: "Bo"}}))
	if renamed, err := untagged.RenameLink(req); err != nil || renamed.Body.Name != "copy of Bo" || renamed.Body.Tag.Set {
		t.Errorf("Rename link of an untagged pet: %+v %v", renamed, err)
	}

	// Text given to a text body.
	shout, err := created.ShoutLink(req)
	if err != nil || shout.Body.Value != "hi Ann" {
		t.Fatalf("Shout link: %+v %v", shout, err)
	}
	if r := as[api.Shout200TextResponse](t)(c.Shout(ctx, shout)); r.Body != "HI ANN!" {
		t.Errorf("shouted = %q", r.Body)
	}
}

func TestFollowLinks(t *testing.T) {
	ctx := context.Background()
	s, c := newTestClient(t, petclient.Credentials{})
	req := &api.CreatePetRequest{Body: model.Pet{Name: "Bo"}}
	created, err := c.CreatePet(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	// Following a link builds its request and sends it.
	if p := as[api.GetPet200JSONResponse](t)(c.FollowCreatePetGetPet(ctx, created, req)); p.Body.Name != "Bo" {
		t.Errorf("followed to %+v", p.Body)
	}
	if got := s.seen["GetPet"].(*api.GetPetRequest); got.Session.Value != "s1" {
		t.Errorf("GetPet received %+v", got)
	}
	if p := as[api.CopyPet201JSONResponse](t)(c.FollowCreatePetCopyPet(ctx, created, req)); p.Body.Name != "Bo" || p.Body.ID == created.(api.CreatePet201JSONResponse).Body.ID {
		t.Errorf("copied to %+v", p.Body)
	}
	// $url is where the request was sent, built again.
	if p := as[api.GetPet200JSONResponse](t)(c.FollowCreatePetSent(ctx, created, req)); p.Body.Name != "Bo" {
		t.Errorf("followed Sent to %+v", p.Body)
	}
	if got := s.seen["GetPet"].(*api.GetPetRequest).Session.Value; !strings.HasPrefix(got, "http://127.0.0.1:") || !strings.HasSuffix(got, "/v1/pets") {
		t.Errorf("Sent session = %q", got)
	}
	list := &api.ListPetsRequest{Limit: oapi.Some(int32(1))}
	page, err := c.ListPets(ctx, list)
	if err != nil {
		t.Fatal(err)
	}
	all := as[api.ListPets200JSONResponse](t)(c.FollowListPetsAll(ctx, page, list))
	if len(all.Body) != int(page.(api.ListPets200JSONResponse).XTotal) || len(all.Body) < 2 {
		t.Errorf("all %d pets, of %d", len(all.Body), page.(api.ListPets200JSONResponse).XTotal)
	}

	// A response without the link cannot be followed.
	conflict, err := c.CreatePet(ctx, &api.CreatePetRequest{Body: model.Pet{Name: "Taken"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.FollowCreatePetGetPet(ctx, conflict, req); err == nil || !strings.Contains(err.Error(), "link GetPet: a api.CreatePet4XXJSONResponse has no such link") {
		t.Errorf("following a 409: %v", err)
	}
}
