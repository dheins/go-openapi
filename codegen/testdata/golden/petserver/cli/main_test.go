package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver"
	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/api"
	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/model"
	"github.com/dheins/go-openapi/runtime/client"
	"github.com/dheins/go-openapi/runtime/server"
)

// handler serves what the tests send; other operations are not called.
type handler struct {
	api.Handler
	seen map[string]any
}

func (h *handler) ListPets(_ context.Context, req *api.ListPetsRequest) (api.ListPetsResponse, error) {
	h.seen["ListPets"] = req
	return api.ListPets200JSONResponse{Body: []model.Pet{{ID: 1, Name: "Rex"}}, XTotal: 1}, nil
}

func (h *handler) CreatePet(_ context.Context, req *api.CreatePetRequest) (api.CreatePetResponse, error) {
	if req.Body.Name == "Taken" {
		return api.CreatePet4XXJSONResponse{StatusCode: 409, Body: model.Error{Message: "taken"}}, nil
	}
	return api.CreatePet201JSONResponse{Body: req.Body}, nil
}

func (h *handler) UploadPhoto(_ context.Context, req *api.UploadPhotoRequest) (api.UploadPhotoResponse, error) {
	h.seen["UploadPhoto"] = req
	return api.UploadPhoto204Response{}, nil
}

func (h *handler) ExportPets(_ context.Context, req *api.ExportPetsRequest) (api.ExportPetsResponse, error) {
	return api.ExportPets200JSONLinesResponse{Items: func(yield func(model.Pet, error) bool) {
		for _, name := range []string{"Rex", "Tom"} {
			if !yield(model.Pet{Name: name}, nil) {
				return
			}
		}
	}}, nil
}

func (h *handler) CreateOrder(_ context.Context, req *api.CreateOrderRequest) (api.CreateOrderResponse, error) {
	h.seen["CreateOrder"] = req
	return api.CreateOrder201JSONResponse{Body: req.Body}, nil
}

func (h *handler) Secure(context.Context, *api.SecureRequest) (api.SecureResponse, error) {
	return api.Secure204Response{}, nil
}

type security struct{ api.SecurityHandler }

func (security) APIKey(ctx context.Context, key string, _ []string) (context.Context, error) {
	if key != "k" {
		return nil, errors.New("wrong key")
	}
	return ctx, nil
}

// codec stands in for the fixture's codecs, which the server requires.
type codec struct{}

func (codec) Marshal(any) ([]byte, error) { return nil, errors.New("unused") }
func (codec) Unmarshal([]byte, any) error { return errors.New("unused") }

// The client needs the fixture's codecs, which a file of the program's
// gives it so.
func init() {
	clientOptions = append(clientOptions, client.WithCodec("application/x-tc", codec{}), client.WithCodec("application/x-tc2", codec{}))
}

func TestCommands(t *testing.T) {
	h := &handler{seen: map[string]any{}}
	srv := httptest.NewServer(petserver.NewServer(h, security{}, server.WithLogger(slog.New(slog.DiscardHandler)),
		server.WithCodec("application/x-tc", codec{}), server.WithCodec("application/x-tc2", codec{})))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	pet := filepath.Join(dir, "pet.json")
	photo := filepath.Join(dir, "rex.png")
	for name, data := range map[string]string{pet: `{"name":"Kit"}`, photo: "PNG"} {
		if err := os.WriteFile(name, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runCLI := func(args ...string) (int, string, string) {
		var stdout, stderr bytes.Buffer
		code := run(append(args, "-server", srv.URL+"/v1"), &stdout, &stderr)
		return code, stdout.String(), stderr.String()
	}

	code, out, errOut := runCLI("list-pets", "-limit", "2", "-tags", "a,b", "-sort", "id", "-v")
	if code != 0 || out != "[\n  {\n    \"id\": 1,\n    \"name\": \"Rex\"\n  }\n]\n" || !strings.Contains(errOut, "200 OK") || !strings.Contains(errOut, "X-Total: 1") {
		t.Errorf("list-pets: %d %q %q", code, out, errOut)
	}
	if req := h.seen["ListPets"].(*api.ListPetsRequest); req.Limit.Value != 2 || !slices.Equal(req.Tags.Value, []string{"a", "b"}) || req.Sort != model.ListPetsSortID {
		t.Errorf("list-pets request = %+v", req)
	}

	if code, out, errOut := runCLI("create-pet", "-body", "@"+pet); code != 0 || !strings.Contains(out, `"name": "Kit"`) {
		t.Errorf("create-pet: %d %q %q", code, out, errOut)
	}
	// Another declared response exits 1, its body printed.
	if code, out, _ := runCLI("create-pet", "-body", `{"name":"Taken"}`); code != 1 || !strings.Contains(out, `"message": "taken"`) {
		t.Errorf("create-pet taken: %d %q", code, out)
	}
	// An invalid request is not sent.
	if code, _, errOut := runCLI("create-pet", "-body", `{"name":""}`); code != 2 || !strings.Contains(errOut, "the request is invalid") || !strings.Contains(errOut, "/name") {
		t.Errorf("create-pet invalid: %d %q", code, errOut)
	}

	if code, _, errOut := runCLI("upload-photo", "-pet-id", "1", "-body", `{"caption":"me"}`, "-photo", "@"+photo); code != 0 {
		t.Errorf("upload-photo: %d %q", code, errOut)
	}
	if req := h.seen["UploadPhoto"].(*api.UploadPhotoRequest); req.PetID != 1 || req.Body.Caption.Value != "me" || req.Body.Photo.Name != "rex.png" || string(req.Body.Photo.Data) != "PNG" {
		t.Errorf("upload-photo request = %+v", req)
	}
	if code, _, errOut := runCLI("upload-photo", "-photo", "@"+photo); code != 2 || !strings.Contains(errOut, "-pet-id is required") {
		t.Errorf("upload-photo without -pet-id: %d %q", code, errOut)
	}

	// A form body of arrays of objects and of arrays, given as JSON, is
	// sent with brackets.
	order := `{"id":"o-1","lines":[{"sku":"a","qty":1},{"sku":"b","qty":2}],"boxes":[[0],[0,1]]}`
	if code, _, errOut := runCLI("create-order", "-body", order); code != 0 {
		t.Errorf("create-order: %d %q", code, errOut)
	}
	if req := h.seen["CreateOrder"].(*api.CreateOrderRequest); len(req.Body.Lines.Value) != 2 || req.Body.Lines.Value[1].Sku != "b" ||
		len(req.Body.Boxes.Value) != 2 || !slices.Equal(req.Body.Boxes.Value[1], []int64{0, 1}) {
		t.Errorf("create-order request = %+v", req.Body)
	}

	// A stream is printed item by item.
	if code, out, errOut := runCLI("export-pets", "-header", "Accept: application/jsonl"); code != 0 || out != "{\"id\":0,\"name\":\"Rex\"}\n{\"id\":0,\"name\":\"Tom\"}\n" {
		t.Errorf("export-pets: %d %q %q", code, out, errOut)
	}

	// Credentials by flag, or by environment variable.
	if code, _, _ := runCLI("secure"); code != 2 {
		t.Errorf("secure without a key: %d", code)
	}
	if code, _, errOut := runCLI("secure", "-api-key", "k"); code != 0 {
		t.Errorf("secure with -api-key: %d %q", code, errOut)
	}
	t.Setenv("PETSERVERCTL_API_KEY", "k")
	if code, _, errOut := runCLI("secure"); code != 0 {
		t.Errorf("secure with PETSERVERCTL_API_KEY: %d %q", code, errOut)
	}

	var stdout bytes.Buffer
	if code := run([]string{"help"}, &stdout, io.Discard); code != 0 || !strings.Contains(stdout.String(), "list-pets") || !strings.Contains(stdout.String(), "petserverctl") {
		t.Errorf("help: %d %q", code, stdout.String())
	}
	if code := run([]string{"nope"}, io.Discard, io.Discard); code != 2 {
		t.Errorf("unknown command: %d", code)
	}
}
