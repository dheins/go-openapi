package swagger

// End-to-end tests of the server and client generated from a Swagger 2.0
// document. They are not generated; codegen's TestGeneratedCode runs
// them.

import (
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/dheins/go-openapi/codegen/testdata/golden/swagger/api"
	swaggerclient "github.com/dheins/go-openapi/codegen/testdata/golden/swagger/client"
	"github.com/dheins/go-openapi/codegen/testdata/golden/swagger/model"
	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/client"
	"github.com/dheins/go-openapi/runtime/server"
)

// store implements Handler, recording the requests it gets.
type store struct {
	pets []model.Pet
	seen map[string]any
}

func (s *store) ListPets(_ context.Context, req *api.ListPetsRequest) (api.ListPetsResponse, error) {
	s.seen["ListPets"] = req
	return api.ListPets200JSONResponse{Body: s.pets, XTotal: oapi.Some(int64(len(s.pets)))}, nil
}

func (s *store) CreatePet(_ context.Context, req *api.CreatePetRequest) (api.CreatePetResponse, error) {
	s.seen["CreatePet"] = req
	for _, p := range s.pets {
		if p.Name == req.Body.Name {
			return api.CreatePet409JSONResponse{Body: model.Error{Message: "taken"}}, nil
		}
	}
	pet := req.Body
	pet.ID = oapi.Some(int64(len(s.pets) + 1))
	s.pets = append(s.pets, pet)
	return api.CreatePet201JSONResponse{Body: pet}, nil
}

func (s *store) GetPet(_ context.Context, req *api.GetPetRequest) (api.GetPetResponse, error) {
	for _, p := range s.pets {
		if p.ID.Value == req.ID {
			return api.GetPet200JSONResponse{Body: p}, nil
		}
	}
	return api.GetPet404Response{}, nil
}

func (s *store) UploadPhoto(_ context.Context, req *api.UploadPhotoRequest) (api.UploadPhotoResponse, error) {
	s.seen["UploadPhoto"] = req
	return api.UploadPhoto204Response{}, nil
}

func (s *store) Login(_ context.Context, req *api.LoginRequest) (api.LoginResponse, error) {
	s.seen["Login"] = req
	return api.Login200JSONResponse{Body: "hello " + req.Body.User}, nil
}

// auth accepts the key "good" and the user ann with password pw.
type auth struct{}

func (auth) Key(ctx context.Context, key string, _ []string) (context.Context, error) {
	if key != "good" {
		return nil, errors.New("unknown key")
	}
	return ctx, nil
}

func (auth) Basic(ctx context.Context, user, password string, _ []string) (context.Context, error) {
	if user != "ann" || password != "pw" {
		return nil, errors.New("wrong password")
	}
	return ctx, nil
}

// newClient starts the server and returns its store and a client of it.
func newClient(t *testing.T, creds swaggerclient.Credentials, opts ...client.Option) (*store, *swaggerclient.Client) {
	t.Helper()
	s := &store{pets: []model.Pet{{ID: oapi.Some(int64(1)), Name: "Rex", Tag: oapi.Some(oapi.Nullable[string]{})}}, seen: map[string]any{}}
	srv := httptest.NewServer(NewServer(s, auth{}, server.WithLogger(slog.New(slog.DiscardHandler))))
	t.Cleanup(srv.Close)
	c, err := swaggerclient.New(creds, append([]client.Option{client.WithBaseURL(srv.URL + BasePath)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return s, c
}

func TestSwaggerRoundTrip(t *testing.T) {
	ctx := context.Background()
	key := swaggerclient.Credentials{Key: func(context.Context) (string, error) { return "good", nil }}
	s, c := newClient(t, key)

	// Query arrays in the 2.0 collection formats: csv, multi and pipes.
	req := &api.ListPetsRequest{Tags: oapi.Some([]string{"a", "b"}), IDs: oapi.Some([]int64{1, 2}),
		Fields: oapi.Some([]string{"id", "name"}), Limit: 5, XRequestID: oapi.Some("r1")}
	resp, err := c.ListPets(ctx, req)
	list, ok := resp.(api.ListPets200JSONResponse)
	if err != nil || !ok || len(list.Body) != 1 || list.XTotal.Value != 1 {
		t.Fatalf("list: %+v %v", resp, err)
	}
	if got := s.seen["ListPets"].(*api.ListPetsRequest); !slices.Equal(got.Tags.Value, req.Tags.Value) || !slices.Equal(got.IDs.Value, req.IDs.Value) ||
		!slices.Equal(got.Fields.Value, req.Fields.Value) || got.Limit != 5 || got.XRequestID.Value != "r1" {
		t.Errorf("list request = %+v", got)
	}
	// The 2.0 default, now a 3.x one.
	if _, err := c.ListPets(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if got := s.seen["ListPets"].(*api.ListPetsRequest); got.Limit != 20 {
		t.Errorf("default limit = %d", got.Limit)
	}
	// x-nullable: a null tag is kept apart from no tag.
	if tag := list.Body[0].Tag; !tag.Set || tag.Value.Valid {
		t.Errorf("tag = %+v", tag)
	}

	// The body parameter, as a request body.
	pet := model.Pet{Name: "Tom", Tag: oapi.Some(oapi.NewNullable("cat"))}
	created, err := c.CreatePet(ctx, &api.CreatePetRequest{Body: pet})
	if r, ok := created.(api.CreatePet201JSONResponse); err != nil || !ok || r.Body.ID.Value != 2 || r.Body.Tag.Value.Value != "cat" {
		t.Errorf("create: %+v %v", created, err)
	}
	if r, err := c.CreatePet(ctx, &api.CreatePetRequest{Body: pet}); err != nil || r != (api.CreatePet409JSONResponse{Body: model.Error{Message: "taken"}}) {
		t.Errorf("duplicate: %+v %v", r, err)
	}
	var invalid *client.InvalidRequest
	if _, err := c.CreatePet(ctx, &api.CreatePetRequest{}); !errors.As(err, &invalid) {
		t.Errorf("empty name: %v", err)
	}
	if r, err := c.GetPet(ctx, &api.GetPetRequest{ID: 2}); err != nil || r.(api.GetPet200JSONResponse).Body.Name != "Tom" {
		t.Errorf("get: %+v %v", r, err)
	}
	if r, err := c.GetPet(ctx, &api.GetPetRequest{ID: 9}); err != nil || r != (api.GetPet404Response{}) {
		t.Errorf("missing: %+v %v", r, err)
	}

	// formData parameters: a file as multipart, fields as a form.
	photo := oapi.File{Name: "tom.png", ContentType: "image/png", Data: []byte("PNG")}
	if _, err := c.UploadPhoto(ctx, &api.UploadPhotoRequest{ID: 2, Body: model.UploadPhotoRequestBody{Photo: photo, Caption: oapi.Some("Tom")}}); err != nil {
		t.Fatal(err)
	}
	if got := s.seen["UploadPhoto"].(*api.UploadPhotoRequest); got.ID != 2 || got.Body.Photo.Name != "tom.png" || string(got.Body.Photo.Data) != "PNG" || got.Body.Caption.Value != "Tom" {
		t.Errorf("upload = %+v", got)
	}
	r, err := c.Login(ctx, &api.LoginRequest{Body: model.LoginRequestBody{User: "ann", Remember: oapi.Some(true)}})
	if hi, ok := r.(api.Login200JSONResponse); err != nil || !ok || hi.Body != "hello ann" {
		t.Errorf("login: %+v %v", r, err)
	}
	if got := s.seen["Login"].(*api.LoginRequest); !got.Body.Remember.Value {
		t.Errorf("login request = %+v", got)
	}
}

func TestSwaggerSecurity(t *testing.T) {
	ctx := context.Background()
	pet := &api.CreatePetRequest{Body: model.Pet{Name: "Kit"}}
	basic := swaggerclient.Credentials{Basic: func(context.Context) (string, string, error) { return "ann", "pw", nil }}
	_, c := newClient(t, basic)
	if r, err := c.CreatePet(ctx, pet); err != nil || r.(api.CreatePet201JSONResponse).Body.Name != "Kit" {
		t.Errorf("basic: %+v %v", r, err)
	}
	_, c = newClient(t, swaggerclient.Credentials{})
	if _, err := c.CreatePet(ctx, pet); !errors.Is(err, client.ErrNoCredentials) {
		t.Errorf("no credentials: %v", err)
	}
	// security: [] on the operation overrides the document's.
	if _, err := c.ListPets(ctx, nil); err != nil {
		t.Errorf("anonymous list: %v", err)
	}
	_, c = newClient(t, swaggerclient.Credentials{Key: func(context.Context) (string, error) { return "bad", nil }})
	var unexpected *client.UnexpectedResponse
	if _, err := c.CreatePet(ctx, pet); !errors.As(err, &unexpected) || unexpected.StatusCode != 401 {
		t.Errorf("wrong key: %v", err)
	}
}
