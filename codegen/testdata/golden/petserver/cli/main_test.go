package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver"
	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/api"
	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/model"
	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/client"
	"github.com/dheins/go-openapi/runtime/server"
)

// handler serves what the tests send; other operations are not called.
type handler struct {
	api.Handler
	seen map[string]any
	// arrived, if set, is told the name of each pet ImportLines reads.
	arrived chan string
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

func (h *handler) UploadReport(_ context.Context, req *api.UploadReportRequest) (api.UploadReportResponse, error) {
	h.seen["UploadReport"] = req
	return api.UploadReport200MixedResponse{Body: api.UploadReport200ResponseParts{Pet: req.Body.Pet, Summary: "stored"}}, nil
}

func (h *handler) BatchPets(_ context.Context, req *api.BatchPetsRequest) (api.BatchPetsResponse, error) {
	h.seen["BatchPets"] = req
	stored := make([]oapi.Part[model.Pet], len(req.Body))
	for i, p := range req.Body {
		stored[i] = oapi.Part[model.Pet]{Header: textproto.MIMEHeader{"X-Stored": {"true"}}, Value: p.Value}
	}
	return api.BatchPets200MixedResponse{Body: stored}, nil
}

func (h *handler) PutNotes(_ context.Context, req *api.PutNotesRequest) (api.PutNotesResponse, error) {
	h.seen["PutNotes"] = req
	return api.PutNotes204Response{}, nil
}

func (h *handler) UploadBundle(_ context.Context, req *api.UploadBundleRequest) (api.UploadBundleResponse, error) {
	h.seen["UploadBundle"] = req
	return api.UploadBundle200RelatedResponse{Body: req.Body}, nil
}

// ImportLines counts the pets as they arrive, telling arrived of each.
func (h *handler) ImportLines(_ context.Context, req *api.ImportLinesRequest) (api.ImportLinesResponse, error) {
	if req.JSONBody.Set {
		return api.ImportLines200JSONResponse{Body: int64(len(req.JSONBody.Value))}, nil
	}
	var n int64
	for p, err := range req.Body {
		if err != nil {
			return nil, err
		}
		if h.arrived != nil {
			h.arrived <- p.Name
		}
		n++
	}
	return api.ImportLines200JSONResponse{Body: n}, nil
}

func (h *handler) PostEvents(_ context.Context, req *api.PostEventsRequest) (api.PostEventsResponse, error) {
	var n int64
	for ev, err := range req.Body {
		if err != nil {
			return nil, err
		}
		h.seen["PostEvents"] = ev
		n++
	}
	return api.PostEvents200JSONResponse{Body: n}, nil
}

func (h *handler) PostTypedEvents(_ context.Context, req *api.PostTypedEventsRequest) (api.PostTypedEventsResponse, error) {
	names := []string{}
	for ev, err := range req.Body {
		if err != nil {
			return nil, err
		}
		names = append(names, ev.ID+":"+ev.Data.Name)
	}
	return api.PostTypedEvents200JSONResponse{Body: names}, nil
}

func (h *handler) StreamPhotos(_ context.Context, req *api.StreamPhotosRequest) (api.StreamPhotosResponse, error) {
	acks := func(yield func(oapi.Part[string], error) bool) {
		for photo, err := range req.Body {
			if err != nil {
				yield(oapi.Part[string]{}, err)
				return
			}
			seq := photo.Header.Get("X-Seq")
			if !yield(oapi.Part[string]{Header: textproto.MIMEHeader{"X-Seq": {seq}}, Value: "photo " + seq + ": " + string(photo.Data)}, nil) {
				return
			}
		}
	}
	return api.StreamPhotos200MixedResponse{Items: acks}, nil
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

	// Positional parts, by -part flags in order, with headers.
	code, out, errOut = runCLI("upload-report", "-pet-id", "1", "-part", "@"+pet, "-part", "@"+photo, "-part-header", "X-Seq: 2",
		"-part", "short", "-part-header", "X-Lang: en", "-part", "a,b")
	// A multipart response is printed as it is.
	if code != 0 || !strings.Contains(out, "\r\n\r\nstored\r\n") {
		t.Errorf("upload-report: %d %q %q", code, out, errOut)
	}
	if req := h.seen["UploadReport"].(*api.UploadReportRequest); req.Body.Pet.Name != "Kit" || string(req.Body.Part2.Data) != "PNG" ||
		req.Body.Part2.Header.Get("X-Seq") != "2" || req.Body.Note.Value.Value != "short" || req.Body.Note.Value.Header.Get("X-Lang") != "en" ||
		len(req.Body.Rest) != 1 || string(req.Body.Rest[0].Data) != "a,b" {
		t.Errorf("upload-report request = %+v", req.Body)
	}
	// A part the document requires a header of is checked before sending.
	if code, _, errOut := runCLI("upload-report", "-pet-id", "1", "-part", "@"+pet, "-part", "@"+photo); code != 2 || !strings.Contains(errOut, "X-Seq") {
		t.Errorf("upload-report without X-Seq: %d %q", code, errOut)
	}
	if code, _, errOut := runCLI("upload-report", "-pet-id", "1", "-part-header", "X-Seq: 1"); code != 2 || !strings.Contains(errOut, "give -part first") {
		t.Errorf("-part-header first: %d %q", code, errOut)
	}
	if code, _, errOut := runCLI("batch-pets", "-part", `{"name":"Rex"}`, "-part-header", "X-Seq: 1", "-part", `{"name":"Tom"}`, "-part-header", "X-Seq: 2"); code != 0 {
		t.Errorf("batch-pets: %d %q", code, errOut)
	}
	if req := h.seen["BatchPets"].(*api.BatchPetsRequest); len(req.Body) != 2 || req.Body[1].Value.Name != "Tom" {
		t.Errorf("batch-pets request = %+v", req.Body)
	}
	// Or the body is a multipart document, its boundary on its first line.
	doc := "--b\r\nContent-Type: text/plain\r\n\r\none\r\n--b\r\nContent-Type: text/plain\r\n\r\ntwo\r\n--b--\r\n"
	if code, _, errOut := runCLI("put-notes", "-body", doc); code != 0 {
		t.Errorf("put-notes: %d %q", code, errOut)
	}
	if req := h.seen["PutNotes"].(*api.PutNotesRequest); !slices.Equal(req.Body, []string{"one", "two"}) {
		t.Errorf("put-notes request = %+v", req.Body)
	}
	if code, _, errOut := runCLI("put-notes", "-body", "not multipart"); code != 2 || !strings.Contains(errOut, "boundary") {
		t.Errorf("put-notes not multipart: %d %q", code, errOut)
	}
	if code, _, errOut := runCLI("upload-bundle", "-part", "@"+photo, "-part", "@"+photo, "-part-header", "X-Order: 2"); code != 0 {
		t.Errorf("upload-bundle: %d %q", code, errOut)
	}
	if req := h.seen["UploadBundle"].(*api.UploadBundleRequest); len(req.Body) != 2 || string(req.Body[0].Data) != "PNG" || req.Body[1].Header.Get("X-Order") != "2" {
		t.Errorf("upload-bundle request = %+v", req.Body)
	}

	// Streams are sent as -body is read: JSON Lines, events, typed events,
	// and parts.
	lines := filepath.Join(dir, "pets.jsonl")
	if err := os.WriteFile(lines, []byte("{\"name\":\"Rex\"}\n\n{\"name\":\"Tom\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := runCLI("import-lines", "-body", "@"+lines); code != 0 || out != "2\n" {
		t.Errorf("import-lines: %d %q %q", code, out, errOut)
	}
	if code, out, errOut := runCLI("import-lines", "-body", "[{\"name\":\"Rex\"}]", "-content-type", "application/json"); code != 0 || out != "1\n" {
		t.Errorf("import-lines as JSON: %d %q %q", code, out, errOut)
	}
	if code, _, errOut := runCLI("import-lines", "-body", "{\"name\":\"Rex\"}\n{\"name\":\n"); code != 2 || !strings.Contains(errOut, "item 1") {
		t.Errorf("import-lines invalid: %d %q", code, errOut)
	}
	if code, out, errOut := runCLI("post-events", "-body", "id: 1\ndata: a\n\n: comment\n\nid: 2\ndata: b\ndata: c\n\n"); code != 0 || out != "2\n" {
		t.Errorf("post-events: %d %q %q", code, out, errOut)
	}
	if ev := h.seen["PostEvents"].(server.Event); ev.ID != "2" || ev.Data != "b\nc" {
		t.Errorf("post-events event = %+v", ev)
	}
	code, out, errOut = runCLI("post-typed-events", "-body", "id: a\ndata: {\"name\":\"Rex\"}\n\nid: b\ndata: {\"name\":\"Tom\"}\n\n")
	if code != 0 || !strings.Contains(out, `"a:Rex"`) || !strings.Contains(out, `"b:Tom"`) {
		t.Errorf("post-typed-events: %d %q %q", code, out, errOut)
	}
	// The client checks each event before sending it.
	if code, _, errOut := runCLI("post-typed-events", "-body", "data: {\"name\":\"\"}\n\n"); code != 2 || !strings.Contains(errOut, "/0/data/name") {
		t.Errorf("post-typed-events invalid: %d %q", code, errOut)
	}
	if code, _, errOut := runCLI("post-typed-events", "-body", "data: {\n\n"); code != 2 || !strings.Contains(errOut, "event 0") {
		t.Errorf("post-typed-events not JSON: %d %q", code, errOut)
	}
	code, out, errOut = runCLI("stream-photos", "-pet-id", "1", "-part", "@"+photo, "-part-header", "X-Seq: 1", "-part", "@"+photo, "-part-header", "X-Seq: 2")
	if code != 0 || !strings.Contains(out, "photo 1: PNG") || !strings.Contains(out, "photo 2: PNG") {
		t.Errorf("stream-photos: %d %q %q", code, out, errOut)
	}
	doc = "--p\r\nContent-Type: image/png\r\nX-Seq: 7\r\n\r\nGIF\r\n--p--\r\n"
	if code, out, errOut := runCLI("stream-photos", "-pet-id", "1", "-body", doc); code != 0 || !strings.Contains(out, "photo 7: GIF") {
		t.Errorf("stream-photos document: %d %q %q", code, out, errOut)
	}

	// From stdin, each line is sent as it is read: the server has the first
	// pet before the second is written.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = stdin })
	h.arrived = make(chan string, 2)
	done := make(chan int)
	go func() {
		code, _, _ := runCLI("import-lines", "-body", "-")
		done <- code
	}()
	if _, err := w.Write([]byte("{\"name\":\"Rex\"}\n")); err != nil {
		t.Fatal(err)
	}
	if name := <-h.arrived; name != "Rex" {
		t.Errorf("first pet %q", name)
	}
	if _, err := w.Write([]byte("{\"name\":\"Tom\"}\n")); err != nil {
		t.Fatal(err)
	}
	w.Close()
	if code := <-done; code != 0 || <-h.arrived != "Tom" {
		t.Errorf("import-lines from stdin: %d", code)
	}
	os.Stdin = stdin
	h.arrived = nil

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
