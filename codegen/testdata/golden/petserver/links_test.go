package petserver

// Tests of the links of responses: the requests they lead to, built by
// the generated methods. They are not generated; codegen's
// TestGeneratedCode runs them.

import (
	"context"
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
