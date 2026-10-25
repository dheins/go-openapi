package scanned

// End-to-end tests of the server and client generated from the document
// goapi scan makes of ./service. They are not generated; codegen's
// TestGeneratedCode runs them.

import (
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/dheins/go-openapi/codegen/testdata/golden/scanned/api"
	scannedclient "github.com/dheins/go-openapi/codegen/testdata/golden/scanned/client"
	"github.com/dheins/go-openapi/codegen/testdata/golden/scanned/model"
	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/client"
	"github.com/dheins/go-openapi/runtime/server"
)

// store implements Handler, recording the requests it gets.
type store struct {
	pets   []model.Pet
	photos map[int64][]byte
	seen   map[string]any
}

func (s *store) ListPets(_ context.Context, req *api.ListPetsRequest) (api.ListPetsResponse, error) {
	s.seen["ListPets"] = req
	var pets []model.Pet
	for _, p := range s.pets {
		if !req.Kind.Set || slices.Contains(req.Kind.Value, p.Kind) {
			pets = append(pets, p)
		}
	}
	return api.ListPets200JSONResponse{Body: pets[:min(len(pets), int(req.Limit))], XTotal: int64(len(pets))}, nil
}

func (s *store) CreatePet(_ context.Context, req *api.CreatePetRequest) (api.CreatePetResponse, error) {
	for _, p := range s.pets {
		if p.Name == req.Body.Name {
			return api.CreatePet409JSONResponse{Body: model.Problem{Title: "Conflict", Detail: oapi.Some(p.Name + " exists")}}, nil
		}
	}
	pet := req.Body
	pet.ID = int64(len(s.pets) + 1)
	s.pets = append(s.pets, pet)
	return api.CreatePet201JSONResponse{Body: pet}, nil
}

func (s *store) GetPet(_ context.Context, req *api.GetPetRequest) (api.GetPetResponse, error) {
	for _, p := range s.pets {
		if p.ID == req.ID {
			return api.GetPet200JSONResponse{Body: p}, nil
		}
	}
	return api.GetPet404Response{}, nil
}

func (s *store) DeletePet(_ context.Context, req *api.DeletePetRequest) (api.DeletePetResponse, error) {
	s.seen["DeletePet"] = req
	return api.DeletePet204Response{}, nil
}

func (s *store) PutPhoto(_ context.Context, req *api.PutPhotoRequest) (api.PutPhotoResponse, error) {
	s.photos[req.ID] = req.Body
	return api.PutPhoto204Response{}, nil
}

type auth struct{}

func (auth) APIKey(ctx context.Context, key string, _ []string) (context.Context, error) {
	if key != "secret" {
		return nil, errors.New("unknown key")
	}
	return ctx, nil
}

func newClient(t *testing.T, creds scannedclient.Credentials) (*store, *scannedclient.Client) {
	t.Helper()
	s := &store{pets: []model.Pet{{ID: 1, Name: "Rex", Kind: model.KindDog}}, photos: map[int64][]byte{}, seen: map[string]any{}}
	srv := httptest.NewServer(NewServer(s, auth{}, server.WithLogger(slog.New(slog.DiscardHandler))))
	t.Cleanup(srv.Close)
	c, err := scannedclient.New(creds, client.WithBaseURL(srv.URL+BasePath))
	if err != nil {
		t.Fatal(err)
	}
	return s, c
}

func TestScannedRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, c := newClient(t, scannedclient.Credentials{APIKey: func(context.Context) (string, error) { return "secret", nil }})

	born := oapi.Date{Year: 2020, Month: time.May, Day: 4}
	resp, err := c.CreatePet(ctx, &api.CreatePetRequest{Body: model.Pet{Name: "Tom", Kind: model.KindCat, Tag: oapi.Some("grey"), Born: oapi.Some(born)}})
	created, ok := resp.(api.CreatePet201JSONResponse)
	if err != nil || !ok || created.Body.ID != 2 || created.Body.Born.Value != born || created.Body.Tag.Value != "grey" {
		t.Fatalf("create: %+v %v", resp, err)
	}
	if r, err := c.CreatePet(ctx, &api.CreatePetRequest{Body: model.Pet{Name: "Rex", Kind: model.KindDog}}); err != nil ||
		r != (api.CreatePet409JSONResponse{Body: model.Problem{Title: "Conflict", Detail: oapi.Some("Rex exists")}}) {
		t.Errorf("conflict: %+v %v", r, err)
	}
	// The constraints of the Go types are checked before sending.
	var invalid *client.InvalidRequest
	if _, err := c.CreatePet(ctx, &api.CreatePetRequest{Body: model.Pet{Kind: "bird"}}); !errors.As(err, &invalid) || len(invalid.Problem.Errors) != 2 {
		t.Errorf("invalid pet: %v", err)
	}

	// The variants of an interface: the discriminator value is the one
	// //goapi:discriminatorValue gives, set when encoding.
	fang := model.Pet{Name: "Fang", Kind: model.KindDog, Diet: oapi.Some(model.NewDiet(model.Carnivore{Prey: "rabbits"}))}
	resp, err = c.CreatePet(ctx, &api.CreatePetRequest{Body: fang})
	created, ok = resp.(api.CreatePet201JSONResponse)
	if diet, meat := created.Body.Diet.Value.Value().(model.Carnivore); err != nil || !ok || !meat || diet.Prey != "rabbits" || diet.Type != "meat" {
		t.Fatalf("carnivore: %+v %v", resp, err)
	}
	if diet, meat := s.pets[2].Diet.Value.Value().(model.Carnivore); !meat || diet.Type != "meat" {
		t.Errorf("stored diet = %+v", s.pets[2].Diet)
	}
	if _, err := c.CreatePet(ctx, &api.CreatePetRequest{Body: model.Pet{Name: "Bambi", Kind: model.KindDog, Diet: oapi.Some(model.NewDiet(model.Carnivore{}))}}); !errors.As(err, &invalid) ||
		invalid.Problem.Errors[0].Pointer != "/diet/prey" {
		t.Errorf("invalid diet: %v", err)
	}

	list, err := c.ListPets(ctx, &api.ListPetsRequest{Limit: 5, Kind: oapi.Some([]model.Kind{model.KindCat})})
	if page, ok := list.(api.ListPets200JSONResponse); err != nil || !ok || len(page.Body) != 1 || page.Body[0].Name != "Tom" || page.XTotal != 1 {
		t.Errorf("list: %+v %v", list, err)
	}
	// The default of the Go tag.
	if _, err := c.ListPets(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if got := s.seen["ListPets"].(*api.ListPetsRequest); got.Limit != 20 {
		t.Errorf("default limit = %d", got.Limit)
	}

	if r, err := c.GetPet(ctx, &api.GetPetRequest{ID: 2}); err != nil || r.(api.GetPet200JSONResponse).Body.Name != "Tom" {
		t.Errorf("get: %+v %v", r, err)
	}
	if r, err := c.GetPet(ctx, &api.GetPetRequest{ID: 9}); err != nil || r != (api.GetPet404Response{}) {
		t.Errorf("missing: %+v %v", r, err)
	}
	if _, err := c.GetPet(ctx, &api.GetPetRequest{ID: 0}); !errors.As(err, &invalid) {
		t.Errorf("id 0: %v", err)
	}

	if _, err := c.PutPhoto(ctx, &api.PutPhotoRequest{ID: 2, Body: []byte("PNG")}); err != nil || string(s.photos[2]) != "PNG" {
		t.Errorf("photo: %v %q", err, s.photos[2])
	}

	// Only DeletePet is secured.
	if r, err := c.DeletePet(ctx, &api.DeletePetRequest{ID: 2}); err != nil || r != (api.DeletePet204Response{}) {
		t.Errorf("delete: %+v %v", r, err)
	}
	_, anon := newClient(t, scannedclient.Credentials{})
	if _, err := anon.DeletePet(ctx, &api.DeletePetRequest{ID: 2}); !errors.Is(err, client.ErrNoCredentials) {
		t.Errorf("anonymous delete: %v", err)
	}
	if _, err := anon.GetPet(ctx, &api.GetPetRequest{ID: 1}); err != nil {
		t.Errorf("anonymous get: %v", err)
	}
}
