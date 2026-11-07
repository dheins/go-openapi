package generics

// End-to-end tests of types specialised by $dynamicAnchor. They are not
// generated; codegen's TestGeneratedCode runs them.

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/codegen/testdata/golden/generics/api"
	genclient "github.com/dheins/go-openapi/codegen/testdata/golden/generics/client"
	"github.com/dheins/go-openapi/codegen/testdata/golden/generics/model"
	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/client"
	"github.com/dheins/go-openapi/runtime/server"
)

type handler struct {
	stored model.PetPage
}

func (h *handler) ListPets(context.Context, *api.ListPetsRequest) (api.ListPetsResponse, error) {
	return api.ListPets200JSONResponse{Body: model.PetPage{Items: []model.Pet{{Name: "Rex"}}, Meta: oapi.Some(model.PetPageMeta{Sample: oapi.Some(model.Pet{Name: "Tom"})})}}, nil
}

func (h *handler) PutPets(_ context.Context, req *api.PutPetsRequest) (api.PutPetsResponse, error) {
	h.stored = req.Body
	return api.PutPets204Response{}, nil
}

func (h *handler) ListTags(context.Context, *api.ListTagsRequest) (api.ListTagsResponse, error) {
	return api.ListTags200JSONResponse{Body: model.TagPage{Items: []string{"cat"}}}, nil
}

func (h *handler) ListOwners(context.Context, *api.ListOwnersRequest) (api.ListOwnersResponse, error) {
	return api.ListOwners200JSONResponse{Body: model.ListOwners200ResponseBody{Items: []model.Owner{{Email: "a@example.com"}}}}, nil
}

func (h *handler) GetTree(context.Context, *api.GetTreeRequest) (api.GetTreeResponse, error) {
	return api.GetTree200JSONResponse{Body: model.Tree{Name: "root", Children: oapi.Some([]model.Tree{{Name: "leaf"}})}}, nil
}

func TestSpecialised(t *testing.T) {
	h := &handler{}
	srv := httptest.NewServer(NewServer(h, server.WithLogger(slog.New(slog.DiscardHandler))))
	t.Cleanup(srv.Close)
	c, err := genclient.New(client.WithBaseURL(srv.URL + "/v1"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	resp, err := c.ListPets(ctx, &api.ListPetsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if page := resp.(api.ListPets200JSONResponse).Body; page.Items[0].Name != "Rex" || page.Meta.Value.Sample.Value.Name != "Tom" {
		t.Errorf("pets = %+v", page)
	}
	if _, err := c.PutPets(ctx, &api.PutPetsRequest{Body: model.PetPage{Items: []model.Pet{{Name: "Kit"}}}}); err != nil || h.stored.Items[0].Name != "Kit" {
		t.Errorf("put: %v, stored %+v", err, h.stored)
	}
	if resp, err := c.ListOwners(ctx, &api.ListOwnersRequest{}); err != nil || resp.(api.ListOwners200JSONResponse).Body.Items[0].Email != "a@example.com" {
		t.Errorf("owners: %v %+v", err, resp)
	}
	if resp, err := c.GetTree(ctx, &api.GetTreeRequest{}); err != nil || resp.(api.GetTree200JSONResponse).Body.Children.Value[0].Name != "leaf" {
		t.Errorf("tree: %v %+v", err, resp)
	}

	// The item's schema is checked: a pet needs a name, a tag at most 20
	// characters.
	for body, want := range map[string]string{
		`{"items":[{"name":""}]}`: "/items/0/name",
		`{"items":[{}]}`:          "/items/0",
		`{"items":["x"]}`:         "/items/0",
	} {
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/pets", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil || r.StatusCode != 400 || !strings.Contains(string(data), want) {
			t.Errorf("%s: %d %s", body, r.StatusCode, data)
		}
	}
	var tags model.TagPage
	if err := json.Unmarshal([]byte(`{"items":["a-tag-longer-than-twenty"]}`), &tags); err != nil {
		t.Fatal(err)
	}
	if err := tags.Validate(); err == nil || !strings.Contains(err.Error(), "/items/0") {
		t.Errorf("tag validation: %v", err)
	}
	// The template's own item accepts nothing (not: true).
	var page model.Page
	if err := json.Unmarshal([]byte(`{"items":[1]}`), &page); err == nil {
		if err := page.Validate(); err == nil {
			t.Error("an item of the template is accepted")
		}
	}
}
