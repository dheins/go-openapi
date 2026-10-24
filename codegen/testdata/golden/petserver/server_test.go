package petserver

// End-to-end tests of the generated server. They are not generated;
// codegen's TestGeneratedCode runs them.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/api"
	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/model"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"slices"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/problem"
	"github.com/dheins/go-openapi/runtime/server"
)

// store implements Handler with an in-memory list of pets.
type store struct {
	pets   []model.Pet
	nextID int64
	// seen records the last request each method received.
	seen map[string]any
}

func newStore() *store {
	return &store{pets: []model.Pet{{ID: 1, Name: "Rex"}, {ID: 2, Name: "Tom", Tag: oapi.Some("cat")}}, nextID: 3, seen: map[string]any{}}
}

func (s *store) ListPets(ctx context.Context, req *api.ListPetsRequest) (api.ListPetsResponse, error) {
	s.seen["ListPets"] = req
	if _, ok := server.HTTPRequest(ctx); !ok {
		return nil, errors.New("no *http.Request in context")
	}
	pets := s.pets
	if n, ok := req.Limit.Get(); ok && int(n) < len(pets) {
		pets = pets[:n]
	}
	return api.ListPets200JSONResponse{Body: pets, XTotal: int64(len(s.pets)), XNext: oapi.Some("cursor-2"),
		XPage: oapi.Some(model.ListPets200XPage{Size: oapi.Some(int64(len(s.pets))), After: oapi.Some("cursor-2")})}, nil
}

func (s *store) CreatePet(_ context.Context, req *api.CreatePetRequest) (api.CreatePetResponse, error) {
	s.seen["CreatePet"] = req
	if req.Body.Name == "Taken" {
		return api.CreatePet4XXJSONResponse{StatusCode: http.StatusConflict, Body: model.Error{Message: "name taken"}}, nil
	}
	if req.Body.Name == "Bad status" {
		return api.CreatePet4XXJSONResponse{StatusCode: http.StatusInternalServerError}, nil
	}
	pet := req.Body
	pet.ID = s.nextID
	s.nextID++
	s.pets = append(s.pets, pet)
	return api.CreatePet201JSONResponse{Body: pet}, nil
}

func (s *store) GetPet(_ context.Context, req *api.GetPetRequest) (api.GetPetResponse, error) {
	s.seen["GetPet"] = req
	if req.PetID == 666 {
		panic("cursed pet")
	}
	for _, p := range s.pets {
		if p.ID == req.PetID {
			return api.GetPet200JSONResponse{Body: p}, nil
		}
	}
	return api.GetPet404Response{}, nil
}

func (s *store) DeletePet(_ context.Context, req *api.DeletePetRequest) (api.DeletePetResponse, error) {
	if req.PetID == 99 {
		return nil, problem.New(http.StatusForbidden, "pet 99 is protected")
	}
	if req.PetID == 500 {
		return nil, errors.New("database password is hunter2")
	}
	return api.DeletePet204Response{}, nil
}

func (s *store) Subscribe(_ context.Context, req *api.SubscribeRequest) (api.SubscribeResponse, error) {
	s.seen["Subscribe"] = req
	return api.Subscribe201Response{}, nil
}

func (s *store) GetFile(_ context.Context, req *api.GetFileRequest) (api.GetFileResponse, error) {
	return api.GetFile200JSONResponse{Body: model.GetFile200ResponseBody{Name: req.Name, Ext: string(req.Ext)}}, nil
}

func (s *store) SearchPets(_ context.Context, req *api.SearchPetsRequest) (api.SearchPetsResponse, error) {
	s.seen["SearchPets"] = req
	var found []model.Pet
	for _, p := range s.pets {
		if !req.Body.Set || p.Name == req.Body.Value.Name.Value {
			found = append(found, p)
		}
	}
	return api.SearchPets200JSONResponse{Body: found}, nil
}

func (s *store) Secure(context.Context, *api.SecureRequest) (api.SecureResponse, error) {
	return nil, nil
}

func (s *store) CopyPet(_ context.Context, req *api.CopyPetRequest) (api.CopyPetResponse, error) {
	for _, p := range s.pets {
		if p.ID == req.PetID {
			p.ID = s.nextID
			p.Name = req.Destination
			s.nextID++
			s.pets = append(s.pets, p)
			return api.CopyPet201JSONResponse{Body: p}, nil
		}
	}
	return nil, problem.New(http.StatusNotFound, "no such pet")
}

func (s *store) Admin(ctx context.Context, _ *api.AdminRequest) (api.AdminResponse, error) {
	user, _ := ctx.Value(userKey{}).(string)
	return api.Admin200TextResponse{Body: user}, nil
}

func (s *store) GetReports(_ context.Context, req *api.GetReportsRequest) (api.GetReportsResponse, error) {
	s.seen["GetReports"] = req
	return api.GetReports204Response{}, nil
}

func (s *store) Lookup(_ context.Context, req *api.LookupRequest) (api.LookupResponse, error) {
	s.seen["Lookup"] = req
	return api.Lookup204Response{}, nil
}

func (s *store) UploadPhoto(_ context.Context, req *api.UploadPhotoRequest) (api.UploadPhotoResponse, error) {
	s.seen["UploadPhoto"] = req
	return api.UploadPhoto204Response{}, nil
}

func (s *store) UploadRecords(_ context.Context, req *api.UploadRecordsRequest) (api.UploadRecordsResponse, error) {
	s.seen["UploadRecords"] = req
	return api.UploadRecords204Response{}, nil
}

func (s *store) ImportPet(_ context.Context, req *api.ImportPetRequest) (api.ImportPetResponse, error) {
	s.seen["ImportPet"] = req
	return api.ImportPet204Response{}, nil
}

func (s *store) Upload(_ context.Context, req *api.UploadRequest) (api.UploadResponse, error) {
	s.seen["Upload"] = req
	return api.Upload204Response{}, nil
}

func (s *store) GetCard(ctx context.Context, req *api.GetCardRequest) (api.GetCardResponse, error) {
	card := model.Card{ID: req.PetID, Name: "Rex", Nicknames: oapi.Some([]string{"T-Rex"}), Secret: oapi.Some("hidden"),
		Vets: oapi.Some([]model.Vet{{Name: oapi.Some("Dr. Who"), Phone: oapi.Some("555")}}),
		Note: oapi.Some(model.Note{Lang: oapi.Some("en"), Text: oapi.Some("likes <bones>")})}
	r, _ := server.HTTPRequest(ctx)
	if server.Negotiate(r, "application/json", "application/xml") == "application/xml" {
		return api.GetCard200XMLResponse{Body: card}, nil
	}
	return api.GetCard200JSONResponse{Body: card}, nil
}

func (s *store) PutCard(_ context.Context, req *api.PutCardRequest) (api.PutCardResponse, error) {
	s.seen["PutCard"] = req
	return api.PutCard204Response{}, nil
}

func (s *store) BatchPets(_ context.Context, req *api.BatchPetsRequest) (api.BatchPetsResponse, error) {
	s.seen["BatchPets"] = req
	return api.BatchPets200MixedResponse{Body: req.Body}, nil
}

func (s *store) UploadBundle(_ context.Context, req *api.UploadBundleRequest) (api.UploadBundleResponse, error) {
	s.seen["UploadBundle"] = req
	return api.UploadBundle200RelatedResponse{Body: req.Body}, nil
}

func (s *store) PutNotes(_ context.Context, req *api.PutNotesRequest) (api.PutNotesResponse, error) {
	s.seen["PutNotes"] = req
	return api.PutNotes204Response{}, nil
}

func (s *store) GetPhoto(_ context.Context, req *api.GetPhotoRequest) (api.GetPhotoResponse, error) {
	if req.PetID == 2 {
		return api.GetPhoto200ImageResponse{ContentType: "text/plain", Body: []byte("oops")}, nil
	}
	return api.GetPhoto200ImageResponse{ContentType: "image/png", Body: []byte("PNGDATA")}, nil
}

func (s *store) SubmitForm(_ context.Context, req *api.SubmitFormRequest) (api.SubmitFormResponse, error) {
	s.seen["SubmitForm"] = req
	return api.SubmitForm204Response{}, nil
}

func (s *store) Shout(_ context.Context, req *api.ShoutRequest) (api.ShoutResponse, error) {
	return api.Shout200TextResponse{Body: strings.ToUpper(req.Body.Value) + "!"}, nil
}

func (s *store) ExportPets(ctx context.Context, _ *api.ExportPetsRequest) (api.ExportPetsResponse, error) {
	r, _ := server.HTTPRequest(ctx)
	if server.Negotiate(r, "application/jsonl", "text/csv") == "text/csv" {
		var csv strings.Builder
		for _, p := range s.pets {
			fmt.Fprintf(&csv, "%d,%s\n", p.ID, p.Name)
		}
		return api.ExportPets200TextResponse{Body: csv.String()}, nil
	}
	return api.ExportPets200JSONLinesResponse{Items: func(yield func(model.Pet, error) bool) {
		for _, p := range s.pets {
			if !yield(p, nil) {
				return
			}
		}
	}}, nil
}

func (s *store) Watch(context.Context, *api.WatchRequest) (api.WatchResponse, error) {
	return api.Watch200EventsResponse{Events: func(yield func(server.Event, error) bool) {
		for _, p := range s.pets {
			ev, err := server.JSONEvent("pet", p)
			if !yield(ev, err) {
				return
			}
		}
	}}, nil
}

type result struct {
	status int
	header http.Header
	body   string
}

// do sends a request below the document's base path, /v1.
func do(t *testing.T, h http.Handler, method, target, contentType, body string, headers ...string) result {
	t.Helper()
	req := httptest.NewRequest(method, "/v1"+target, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	data, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	return result{rec.Code, rec.Header(), string(data)}
}

// problemErrors decodes a problem response and returns its errors as
// "in parameter pointer: detail" strings, sorted.
func problemErrors(t *testing.T, r result) []string {
	t.Helper()
	if ct := r.header.Get("Content-Type"); ct != problem.ContentType {
		t.Fatalf("content type %q, body %s", ct, r.body)
	}
	var p problem.Problem
	if err := json.Unmarshal([]byte(r.body), &p); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range p.Errors {
		out = append(out, strings.Join(strings.Fields(e.In+" "+e.Parameter+" "+e.Pointer), " ")+": "+e.Detail)
	}
	slices.Sort(out)
	return out
}

// auth implements SecurityHandler with fixed credentials.
type auth struct{}

type userKey struct{}

func (auth) APIKey(ctx context.Context, key string, _ []string) (context.Context, error) {
	if key != "good" {
		return nil, errors.New("unknown key")
	}
	return context.WithValue(ctx, userKey{}, "key-holder"), nil
}

func (auth) Oauth(ctx context.Context, token string, scopes []string) (context.Context, error) {
	granted := map[string][]string{"admin-token": {"admin"}, "user-token": {}}
	have, ok := granted[token]
	if !ok {
		return nil, errors.New("unknown token")
	}
	for _, s := range scopes {
		if !slices.Contains(have, s) {
			return nil, problem.Newf(http.StatusForbidden, "the %s scope is required", s)
		}
	}
	return context.WithValue(ctx, userKey{}, token), nil
}

func (auth) BasicAuth(ctx context.Context, username, password string, _ []string) (context.Context, error) {
	if username != "ann" || password != "pw" {
		return nil, errors.New("wrong password")
	}
	return context.WithValue(ctx, userKey{}, username), nil
}

func newTestServer() (*store, http.Handler) {
	s := newStore()
	return s, NewServer(s, auth{}, server.WithLogger(slog.New(slog.DiscardHandler)))
}

func TestListPets(t *testing.T) {
	s, h := newTestServer()
	r := do(t, h, "GET", "/pets?limit=1&tags=a,b", "", "", "X-Request-ID", "123e4567-e89b-12d3-a456-426614174000")
	if r.status != 200 || r.header.Get("X-Total") != "2" || r.header.Get("X-Next") != "cursor-2" || r.header.Get("X-Page") != `{"size":2,"after":"cursor-2"}` {
		t.Fatalf("%d %v %s", r.status, r.header, r.body)
	}
	if r.body != `[{"id":1,"name":"Rex"}]` {
		t.Errorf("body = %s", r.body)
	}
	req := s.seen["ListPets"].(*api.ListPetsRequest)
	if !slices.Equal(req.Tags.Value, []string{"a", "b"}) || req.Limit.Value != 1 || req.Sort != model.ListPetsSortName {
		t.Errorf("request = %+v", req)
	}
	do(t, h, "GET", "/pets?sort=id", "", "")
	if req := s.seen["ListPets"].(*api.ListPetsRequest); req.Sort != model.ListPetsSortID {
		t.Errorf("sort = %q", req.Sort)
	}
}

func TestMiddlewareAndDocument(t *testing.T) {
	s := newStore()
	var seen []string
	logOps := func(op server.Operation, next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = append(seen, op.ID+" "+op.Method+" "+op.Path)
			next.ServeHTTP(w, r)
		})
	}
	h := NewServer(s, auth{}, server.WithMiddleware(logOps), server.WithDocument("/openapi.json"),
		server.WithLogger(slog.New(slog.DiscardHandler)))
	do(t, h, "GET", "/pets/1", "", "")
	do(t, h, "GET", "/secure", "", "")
	if !slices.Equal(seen, []string{"getPet GET /pets/{petId}", "secure GET /secure"}) {
		t.Errorf("middleware saw %q", seen)
	}
	r := do(t, h, "GET", "/openapi.json", "", "")
	if r.status != 200 || !strings.Contains(r.body, `"title":"Pet server"`) {
		t.Errorf("document: %d %.80s", r.status, r.body)
	}
	// Without the option the document is not served.
	_, plain := newTestServer()
	if r := do(t, plain, "GET", "/openapi.json", "", ""); r.status != 404 {
		t.Errorf("document served without WithDocument: %d", r.status)
	}
}

func TestBasePathAndUnknownRoutes(t *testing.T) {
	_, h := newTestServer()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/pets", nil))
	if rec.Code != 404 || rec.Header().Get("Content-Type") != problem.ContentType {
		t.Errorf("route outside the base path: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	r := do(t, h, "PUT", "/pets/1", "", "")
	if r.status != 405 || r.header.Get("Allow") == "" || r.header.Get("Content-Type") != problem.ContentType {
		t.Errorf("405: %d %v", r.status, r.header)
	}
	// The base path can be changed.
	s := newStore()
	h = NewServer(s, auth{}, server.WithBasePath(""), server.WithLogger(slog.New(slog.DiscardHandler)))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/pets", nil))
	if rec.Code != 200 {
		t.Errorf("WithBasePath(\"\"): %d", rec.Code)
	}
}

func TestReadOnlyAndWriteOnlyOverHTTP(t *testing.T) {
	s, h := newTestServer()
	// The client cannot set the read-only id; the handler sees 0.
	r := do(t, h, "POST", "/pets", "application/json", `{"id": 99, "name": "Kit", "secret": "s3cret"}`)
	if r.status != 201 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if req := s.seen["CreatePet"].(*api.CreatePetRequest); req.Body.ID != 0 || req.Body.Secret.Value != "s3cret" {
		t.Errorf("request body = %+v", req.Body)
	}
	// The write-only secret never leaves the server.
	if strings.Contains(r.body, "s3cret") {
		t.Errorf("write-only property in response: %s", r.body)
	}
	if r := do(t, h, "GET", "/pets/3", "", ""); r.status != 200 || strings.Contains(r.body, "s3cret") {
		t.Errorf("write-only property in response: %d %s", r.status, r.body)
	}
}

func TestResponseValidation(t *testing.T) {
	s := newStore()
	s.pets = append(s.pets, model.Pet{ID: 7, Name: ""})
	h := NewServer(s, auth{}, server.WithResponseValidation(), server.WithLogger(slog.New(slog.DiscardHandler)))
	if r := do(t, h, "GET", "/pets/7", "", ""); r.status != 500 {
		t.Errorf("invalid response sent: %d %s", r.status, r.body)
	}
	if r := do(t, h, "GET", "/pets/1", "", ""); r.status != 200 {
		t.Errorf("valid response: %d %s", r.status, r.body)
	}
	// Without the option the handler's response goes out as it is.
	_, plain := newTestServer()
	if r := do(t, plain, "GET", "/pets/1", "", ""); r.status != 200 {
		t.Errorf("%d", r.status)
	}
}

func TestParameterErrors(t *testing.T) {
	_, h := newTestServer()
	r := do(t, h, "GET", "/pets?limit=500", "", "", "X-Request-ID", "nope")
	if r.status != 400 {
		t.Fatalf("status %d", r.status)
	}
	got := problemErrors(t, r)
	want := []string{"header X-Request-ID: must be a valid uuid", "query limit: must be at most 100"}
	if !slices.Equal(got, want) {
		t.Errorf("errors = %q\nwant %q", got, want)
	}

	r = do(t, h, "GET", "/pets?limit=many", "", "")
	if got := problemErrors(t, r); !slices.Equal(got, []string{`query limit: expected integer, got "many"`}) {
		t.Errorf("errors = %q", got)
	}

	r = do(t, h, "GET", "/pets/abc", "", "")
	if got := problemErrors(t, r); r.status != 400 || !slices.Equal(got, []string{`path petId: expected integer, got "abc"`}) {
		t.Errorf("%d %q", r.status, got)
	}
}

func TestCreatePet(t *testing.T) {
	s, h := newTestServer()
	// id is required but read-only, so a request does not send it.
	r := do(t, h, "POST", "/pets", "application/json", `{"name":"Kit","tag":"cat"}`)
	if r.status != 201 || r.body != `{"id":3,"name":"Kit","tag":"cat"}` {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if req := s.seen["CreatePet"].(*api.CreatePetRequest); req.Body.Name != "Kit" {
		t.Errorf("request = %+v", req)
	}

	// A body that does not decode is not validated: only the type error
	// is reported, not the empty name.
	r = do(t, h, "POST", "/pets", "application/json", `{"name":"","tag":5}`)
	if got := problemErrors(t, r); r.status != 400 || !slices.Equal(got, []string{"body /tag: expected string, got number"}) {
		t.Errorf("%d %q", r.status, got)
	}
	r = do(t, h, "POST", "/pets", "application/json", `{"name":""}`)
	if got := problemErrors(t, r); !slices.Equal(got, []string{"body /name: length must be at least 1"}) {
		t.Errorf("validation errors = %q", got)
	}

	r = do(t, h, "POST", "/pets", "", "")
	if got := problemErrors(t, r); !slices.Equal(got, []string{"body: request body is required"}) {
		t.Errorf("missing body = %q", got)
	}
	// Without an xml name, the root element is named after the component.
	if r := do(t, h, "POST", "/pets", "application/xml", "<Pet><name>Rex</name></Pet>"); r.status != 201 {
		t.Errorf("xml body: %d %s", r.status, r.body)
	}
	r = do(t, h, "POST", "/pets", "application/xml", "<pet/>")
	if got := problemErrors(t, r); !slices.Equal(got, []string{"body: expected element <Pet>, got <pet>"}) {
		t.Errorf("xml root = %q", got)
	}
}

func TestRangeAndDefaultResponses(t *testing.T) {
	_, h := newTestServer()
	r := do(t, h, "POST", "/pets", "application/json", `{"name":"Taken"}`)
	if r.status != 409 || r.body != `{"message":"name taken"}` {
		t.Errorf("4XX response: %d %s", r.status, r.body)
	}
	// A status outside the declared range is a server error, not sent.
	r = do(t, h, "POST", "/pets", "application/json", `{"name":"Bad status"}`)
	if r.status != 500 {
		t.Errorf("out-of-range status: %d %s", r.status, r.body)
	}
}

func TestGetAndDeletePet(t *testing.T) {
	s, h := newTestServer()
	req := httptest.NewRequest("GET", "/v1/pets/2", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "s1"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.String() != `{"id":2,"name":"Tom","tag":"cat"}` {
		t.Errorf("get: %d %s", rec.Code, rec.Body)
	}
	if got := s.seen["GetPet"].(*api.GetPetRequest); got.Session.Value != "s1" || got.PetID != 2 {
		t.Errorf("request = %+v", got)
	}

	if r := do(t, h, "GET", "/pets/9", "", ""); r.status != 404 || r.body != "" {
		t.Errorf("declared 404: %d %q", r.status, r.body)
	}
	if r := do(t, h, "DELETE", "/pets/1", "", ""); r.status != 204 {
		t.Errorf("delete: %d", r.status)
	}
	if r := do(t, h, "DELETE", "/pets/99", "", ""); r.status != 403 || !strings.Contains(r.body, "protected") {
		t.Errorf("problem error: %d %s", r.status, r.body)
	}
	r := do(t, h, "DELETE", "/pets/500", "", "")
	if r.status != 500 || strings.Contains(r.body, "hunter2") {
		t.Errorf("internal error: %d %s", r.status, r.body)
	}
	if r := do(t, h, "PUT", "/pets/1", "", ""); r.status != 405 {
		t.Errorf("undeclared method: %d", r.status)
	}
}

func TestPartialPathSegment(t *testing.T) {
	_, h := newTestServer()
	r := do(t, h, "GET", "/files/archive.tar.md", "", "")
	if r.status != 200 || r.body != `{"name":"archive.tar","ext":"md"}` {
		t.Errorf("%d %s", r.status, r.body)
	}
	if r := do(t, h, "GET", "/files/readme", "", ""); r.status != 404 {
		t.Errorf("segment without extension: %d", r.status)
	}
	r = do(t, h, "GET", "/files/a.pdf", "", "")
	if got := problemErrors(t, r); !slices.Equal(got, []string{`path ext: must be one of "txt", "md"`}) {
		t.Errorf("enum error = %q", got)
	}
}

func TestParameterStyles(t *testing.T) {
	s, h := newTestServer()
	req := httptest.NewRequest("GET", "/v1/reports/;ids=1;ids=2?filter[role]=admin&filter[age]=3&filter[tags]=a&filter[tags]=b&fields=x|y&q=&limit=", nil)
	req.Header.Set("X-Meta", `{"role": "meta", "tags": ["t"]}`)
	req.Header.Set("Cookie", "theme=dark; compact=true")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 204 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	got := s.seen["GetReports"].(*api.GetReportsRequest)
	if !slices.Equal(got.IDs, []int64{1, 2}) {
		t.Errorf("matrix ids = %v", got.IDs)
	}
	if f := got.Filter.Value; f.Role != "admin" || f.Age.Value != 3 || !slices.Equal(f.Tags.Value, []string{"a", "b"}) {
		t.Errorf("deepObject filter = %+v", f)
	}
	if !slices.Equal(got.Fields.Value, []string{"x", "y"}) || !got.Q.Set || got.Q.Value != "" {
		t.Errorf("fields = %v, q = %+v (an allowed empty value is the empty string)", got.Fields, got.Q)
	}
	if got.Limit.Set {
		t.Errorf("limit = %+v (an allowed empty integer counts as absent)", got.Limit)
	}
	if got.XMeta.Value.Role != "meta" || got.XMeta.Value.Tags.Value[0] != "t" {
		t.Errorf("JSON header = %+v", got.XMeta)
	}
	if p := got.Prefs.Value; p.Theme.Value != "dark" || !p.Compact.Value {
		t.Errorf("cookie object = %+v", p)
	}
}

func TestParameterStyleErrors(t *testing.T) {
	_, h := newTestServer()
	r := do(t, h, "GET", "/reports/;ids=1;id=2?filter[age]=old&fields=", "", "", "X-Meta", "{not json")
	want := []string{
		`header X-Meta: invalid JSON: invalid character 'n' looking for beginning of object key string`,
		`path ids: expected ";ids=", got ";id"`,
		"query fields: must not be empty",
		"query filter /age: expected integer, got string",
		"query filter /role: required property is missing",
	}
	if got := problemErrors(t, r); r.status != 400 || !slices.Equal(got, want) {
		t.Errorf("%d\n%s\nwant\n%s", r.status, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestQuerystringParameter(t *testing.T) {
	s, h := newTestServer()
	if r := do(t, h, "GET", "/lookup?role=admin&age=4&tags=a&tags=b", "", ""); r.status != 204 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	c := s.seen["Lookup"].(*api.LookupRequest).Criteria.Value
	if c.Role != "admin" || c.Age.Value != 4 || !slices.Equal(c.Tags.Value, []string{"a", "b"}) {
		t.Errorf("criteria = %+v", c)
	}
	r := do(t, h, "GET", "/lookup?role=admin&age=-1", "", "")
	if got := problemErrors(t, r); !slices.Equal(got, []string{"querystring criteria /age: must be at least 0"}) {
		t.Errorf("errors = %q", got)
	}
	if r := do(t, h, "GET", "/lookup", "", ""); r.status != 204 || s.seen["Lookup"].(*api.LookupRequest).Criteria.Set {
		t.Errorf("absent querystring: %d", r.status)
	}
}

func TestMultipartUpload(t *testing.T) {
	s, h := newTestServer()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("caption", "Rex at the beach")
	fw, err := mw.CreateFormFile("photo", "rex.png")
	if err != nil {
		t.Fatal(err)
	}
	fw.Write([]byte("PNGDATA"))
	mw.Close()
	r := do(t, h, "PUT", "/pets/1/photo", mw.FormDataContentType(), body.String())
	if r.status != 204 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	got := s.seen["UploadPhoto"].(*api.UploadPhotoRequest)
	if got.PetID != 1 || got.Body.Caption.Value != "Rex at the beach" || string(got.Body.Photo.Data) != "PNGDATA" || got.Body.Photo.Name != "rex.png" {
		t.Errorf("upload = %+v", got.Body)
	}

	body.Reset()
	mw = multipart.NewWriter(&body)
	mw.WriteField("caption", strings.Repeat("x", 30))
	mw.Close()
	r = do(t, h, "PUT", "/pets/1/photo", mw.FormDataContentType(), body.String())
	if got := problemErrors(t, r); !slices.Equal(got, []string{"body /photo: required property is missing"}) {
		t.Errorf("errors = %q", got)
	}
}

// recordsBody builds a multipart body with one part per header and data.
func recordsBody(t *testing.T, parts ...any) (string, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for i := 0; i < len(parts); i += 2 {
		pw, err := mw.CreatePart(parts[i].(textproto.MIMEHeader))
		if err != nil {
			t.Fatal(err)
		}
		pw.Write(parts[i+1].([]byte))
	}
	mw.Close()
	return mw.FormDataContentType(), body.String()
}

func part(name, filename, contentType string, extra ...string) textproto.MIMEHeader {
	h := textproto.MIMEHeader{}
	disposition := fmt.Sprintf("form-data; name=%q", name)
	if filename != "" {
		disposition += fmt.Sprintf("; filename=%q", filename)
	}
	h.Set("Content-Disposition", disposition)
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	for i := 0; i < len(extra); i += 2 {
		h.Set(extra[i], extra[i+1])
	}
	return h
}

func TestMultipartFileParts(t *testing.T) {
	s, h := newTestServer()
	binary := []byte{0x89, 'P', 'N', 'G', 0xff, 0x00}
	ct, body := recordsBody(t,
		// contentMediaType parts are files with or without a file name.
		part("report", "", "application/pdf", "X-Checksum", "abc"), []byte("%PDF"),
		part("scans", "a.png", "image/png"), binary,
		part("scans", "b.jpg", "image/jpeg; q=1"), []byte("JPEG"),
		// An untyped part is a file and may have any content type.
		part("attachment", "notes.txt", ""), []byte("hello"),
	)
	r := do(t, h, "POST", "/pets/1/records", ct, body)
	if r.status != 204 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	got := s.seen["UploadRecords"].(*api.UploadRecordsRequest).Body
	if string(got.Report.Data) != "%PDF" || got.Report.Name != "" || got.Report.Header.Get("X-Checksum") != "abc" {
		t.Errorf("report = %+v", got.Report)
	}
	if len(got.Scans.Value) != 2 || !bytes.Equal(got.Scans.Value[0].Data, binary) || got.Scans.Value[1].ContentType != "image/jpeg; q=1" {
		t.Errorf("scans = %+v", got.Scans)
	}
	if !got.Attachment.Set || got.Attachment.Value.Name != "notes.txt" || string(got.Attachment.Value.Data) != "hello" {
		t.Errorf("attachment = %+v", got.Attachment)
	}

	// Parts that break their encoding are reported together.
	ct, body = recordsBody(t,
		part("report", "r.txt", "text/plain"), []byte("text"),
		part("scans", "a.gif", "image/gif"), []byte("GIF"),
	)
	r = do(t, h, "POST", "/pets/1/records", ct, body)
	want := []string{
		`body /report: content type "text/plain" is not allowed; expected application/pdf`,
		`body /scans: content type "image/gif" is not allowed; expected image/png, image/jpeg`,
	}
	if got := problemErrors(t, r); !slices.Equal(got, want) {
		t.Errorf("errors = %q", got)
	}

	ct, body = recordsBody(t, part("report", "r.pdf", "application/pdf"), []byte("%PDF"))
	r = do(t, h, "POST", "/pets/1/records", ct, body)
	if got := problemErrors(t, r); !slices.Equal(got, []string{`body /report: part header "X-Checksum" is missing`}) {
		t.Errorf("errors = %q", got)
	}
}

func TestBodiesWithDifferentSchemas(t *testing.T) {
	s, h := newTestServer()
	importPet := func(contentType, body string) *api.ImportPetRequest {
		t.Helper()
		r := do(t, h, "POST", "/pets/import", contentType, body)
		if r.status != 204 {
			t.Fatalf("%s: %d %s", contentType, r.status, r.body)
		}
		return s.seen["ImportPet"].(*api.ImportPetRequest)
	}
	if got := importPet("application/json", `{"name": "Rex", "photoUrls": []}`); !got.Body.Set || got.Body.Value.Name != "Rex" || got.JSONBody.Set {
		t.Errorf("json = %+v", got)
	}
	// Equal schemas share a field.
	if got := importPet("application/merge-patch+json", `{"name": "Rex"}`); got.Body.Set || got.JSONBody.Value.Name.Value != "Rex" || got.ContentType != "application/merge-patch+json" {
		t.Errorf("merge patch = %+v", got)
	}
	if got := importPet("application/x-www-form-urlencoded", "name=Rex"); got.JSONBody.Value.Name.Value != "Rex" || got.ContentType != "application/x-www-form-urlencoded" {
		t.Errorf("form = %+v", got)
	}
	ct, body := recordsBody(t, part("photo", "rex.png", "image/png"), []byte("PNG"))
	if got := importPet(ct, body); string(got.MultipartBody.Value.Photo.Data) != "PNG" {
		t.Errorf("multipart = %+v", got)
	}
	if got := importPet("text/csv", "name\nRex\n"); got.TextBody.Value != "name\nRex\n" || got.Body.Set {
		t.Errorf("csv = %+v", got)
	}
	if got := importPet("", ""); got.Body.Set || got.JSONBody.Set || got.MultipartBody.Set || got.TextBody.Set {
		t.Errorf("no body = %+v", got)
	}

	// Each field validates against its own schema.
	r := do(t, h, "POST", "/pets/import", "application/merge-patch+json", `{"name": ""}`)
	if got := problemErrors(t, r); !slices.Equal(got, []string{"body /name: length must be at least 1"}) {
		t.Errorf("errors = %q", got)
	}
}

func TestNestedFields(t *testing.T) {
	s, h := newTestServer()
	// A form body, a deepObject parameter and a form querystring write
	// nested objects and maps with brackets.
	if r := do(t, h, "POST", "/pets/form", "application/x-www-form-urlencoded", "role=admin&owner[name]=Ann&owner[age]=40&labels[a]=1&labels[b]=2"); r.status != 204 {
		t.Fatalf("form: %d %s", r.status, r.body)
	}
	got := s.seen["SubmitForm"].(*api.SubmitFormRequest).Body
	if got.Owner.Value.Name.Value != "Ann" || got.Owner.Value.Age.Value != 40 || got.Labels.Value["b"] != 2 {
		t.Errorf("form = %+v", got)
	}
	if r := do(t, h, "GET", "/reports/;ids=1?filter[role]=admin&filter[owner][age]=7&filter[labels][x]=3", "", ""); r.status != 204 {
		t.Fatalf("deepObject: %d %s", r.status, r.body)
	}
	f := s.seen["GetReports"].(*api.GetReportsRequest).Filter.Value
	if f.Owner.Value.Age.Value != 7 || f.Labels.Value["x"] != 3 {
		t.Errorf("deepObject filter = %+v", f)
	}
	if r := do(t, h, "GET", "/lookup?role=user&owner[name]=Bo", "", ""); r.status != 204 {
		t.Fatalf("querystring: %d %s", r.status, r.body)
	}
	if c := s.seen["Lookup"].(*api.LookupRequest).Criteria.Value; c.Owner.Value.Name.Value != "Bo" {
		t.Errorf("querystring = %+v", c)
	}

	// Errors point into the nested value.
	r := do(t, h, "POST", "/pets/form", "application/x-www-form-urlencoded", "role=admin&owner[age]=old&labels[a]=x")
	want := []string{"body /labels/a: expected integer, got string", "body /owner/age: expected integer, got string"}
	if got := problemErrors(t, r); !slices.Equal(got, want) {
		t.Errorf("errors = %q", got)
	}

	// A free-form multipart body keeps every field.
	ct, body := recordsBody(t, part("a", "", ""), []byte("1"), part("b", "", ""), []byte("x"))
	if r := do(t, h, "POST", "/upload", ct, body); r.status != 204 {
		t.Fatalf("upload: %d %s", r.status, r.body)
	}
	if got := s.seen["Upload"].(*api.UploadRequest).Body.Value; got["a"] != "1" || got["b"] != "x" {
		t.Errorf("upload = %v", got)
	}
}

func TestXMLBodies(t *testing.T) {
	s, h := newTestServer()
	doc := `<?xml version="1.0"?>
<p:card xmlns:p="urn:example:pets" id="7">
  <p:name>Rex</p:name>
  <nicknames><nickname>T-Rex</nickname><nickname>Rexy</nickname></nicknames>
  <vet phone="555">Dr. Who</vet>
  <vet>Dr. No</vet>
  <remark lang="en"><![CDATA[likes <bones>]]></remark>
  <secret>s3</secret>
</p:card>`
	if r := do(t, h, "PUT", "/pets/7/card", "application/xml", doc); r.status != 204 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	got := s.seen["PutCard"].(*api.PutCardRequest).Body
	if got.ID != 7 || got.Name != "Rex" || len(got.Nicknames.Value) != 2 || len(got.Vets.Value) != 2 ||
		got.Vets.Value[0].Name.Value != "Dr. Who" || got.Vets.Value[0].Phone.Value != "555" ||
		got.Note.Value.Text.Value != "likes <bones>" || got.Note.Value.Lang.Value != "en" || got.Secret.Value != "s3" {
		t.Errorf("card = %+v", got)
	}

	// Problems point into the value, as for JSON.
	r := do(t, h, "PUT", "/pets/7/card", "application/xml", `<p:card xmlns:p="urn:example:pets" id="x"><p:name></p:name><nicknames><x/></nicknames></p:card>`)
	if got := problemErrors(t, r); !slices.Equal(got, []string{"body /nicknames/0: expected element <nickname>, got <x>"}) {
		t.Errorf("errors = %q", got)
	}
	r = do(t, h, "PUT", "/pets/7/card", "application/xml", `<p:card xmlns:p="urn:example:pets" id="x"><p:name></p:name></p:card>`)
	if got := problemErrors(t, r); !slices.Equal(got, []string{"body /id: expected integer, got string"}) {
		t.Errorf("errors = %q", got)
	}
	r = do(t, h, "PUT", "/pets/7/card", "application/xml", `<p:card xmlns:p="urn:example:pets" id="7"><p:name></p:name></p:card>`)
	if got := problemErrors(t, r); !slices.Equal(got, []string{"body /name: length must be at least 1"}) {
		t.Errorf("errors = %q", got)
	}
	r = do(t, h, "PUT", "/pets/7/card", "application/xml", `<card`)
	if got := problemErrors(t, r); len(got) != 1 || !strings.HasPrefix(got[0], "body: invalid XML:") {
		t.Errorf("errors = %q", got)
	}

	// Responses are negotiated; write-only properties are left out.
	r = do(t, h, "GET", "/pets/7/card", "", "", "Accept", "application/xml")
	want := `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<p:card xmlns:p="urn:example:pets" id="7"><p:name>Rex</p:name><nicknames><nickname>T-Rex</nickname></nicknames>` +
		`<vet phone="555">Dr. Who</vet><remark lang="en"><![CDATA[likes <bones>]]></remark></p:card>`
	if r.status != 200 || r.header.Get("Content-Type") != "application/xml" || r.body != want {
		t.Errorf("xml response: %d %s\n%s\nwant\n%s", r.status, r.header.Get("Content-Type"), r.body, want)
	}
	r = do(t, h, "GET", "/pets/7/card", "", "", "Accept", "application/json")
	if r.status != 200 || !strings.Contains(r.body, `"nicknames":["T-Rex"]`) || strings.Contains(r.body, "secret") {
		t.Errorf("json response: %d %s", r.status, r.body)
	}
}

// partsBody builds a multipart body of the given subtype from pairs of
// part headers and data.
func partsBody(t *testing.T, subtype string, parts ...any) (string, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for i := 0; i < len(parts); i += 2 {
		pw, err := mw.CreatePart(parts[i].(textproto.MIMEHeader))
		if err != nil {
			t.Fatal(err)
		}
		pw.Write([]byte(parts[i+1].(string)))
	}
	mw.Close()
	return "multipart/" + subtype + "; boundary=" + mw.Boundary(), body.String()
}

func header(pairs ...string) textproto.MIMEHeader {
	h := textproto.MIMEHeader{}
	for i := 0; i < len(pairs); i += 2 {
		h.Set(pairs[i], pairs[i+1])
	}
	return h
}

// readParts splits a multipart response into the parts' content types
// and bodies.
func readParts(t *testing.T, r result) []string {
	t.Helper()
	mt, params, err := mime.ParseMediaType(r.header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("content type %q: %v", r.header.Get("Content-Type"), err)
	}
	out := []string{mt}
	mr := multipart.NewReader(strings.NewReader(r.body), params["boundary"])
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(p)
		out = append(out, p.Header.Get("Content-Type")+" "+string(data))
	}
}

func TestPositionalParts(t *testing.T) {
	s, h := newTestServer()
	ct, body := partsBody(t, "mixed",
		header("Content-Type", "application/json", "X-Seq", "1"), `{"name": "Rex", "secret": "s"}`,
		header("Content-Type", "application/json"), `{"name": "Tom"}`,
	)
	r := do(t, h, "POST", "/pets/batch", ct, body)
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if got := s.seen["BatchPets"].(*api.BatchPetsRequest).Body; len(got) != 2 || got[0].Name != "Rex" || got[1].Name != "Tom" {
		t.Errorf("pets = %+v", got)
	}
	// Written back as JSON parts, as responses: the write-only secret is
	// left out, the required read-only id is written.
	want := []string{"multipart/mixed", `application/json {"id":0,"name":"Rex"}`, `application/json {"id":0,"name":"Tom"}`}
	if got := readParts(t, r); !slices.Equal(got, want) {
		t.Errorf("parts = %q", got)
	}

	// Problems are reported at the part's index.
	ct, body = partsBody(t, "mixed",
		header("Content-Type", "application/json"), `{"name": "Rex"}`,
		header("Content-Type", "text/plain"), `Tom`,
		header("Content-Type", "application/json"), `{"name": 3}`,
	)
	r = do(t, h, "POST", "/pets/batch", ct, body)
	want = []string{`body /0: part header "X-Seq" is missing`, `body /1: content type "text/plain" is not allowed; expected application/json`,
		"body /2/name: expected string, got number"}
	if got := problemErrors(t, r); !slices.Equal(got, want) {
		t.Errorf("errors = %q", got)
	}

	// Without a schema, the parts are the items: files.
	ct, body = partsBody(t, "related",
		header("Content-Type", "image/png", "Content-ID", "<a>"), "PNG",
		header("Content-Type", "image/gif"), "GIF",
	)
	r = do(t, h, "POST", "/bundle", ct, body)
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	files := s.seen["UploadBundle"].(*api.UploadBundleRequest).Body
	if len(files) != 2 || string(files[0].Data) != "PNG" || files[0].Header.Get("Content-ID") != "<a>" || files[1].ContentType != "image/gif" {
		t.Errorf("files = %+v", files)
	}
	if got := readParts(t, r); !slices.Equal(got, []string{"multipart/related", "image/png PNG", "image/gif GIF"}) {
		t.Errorf("parts = %q", got)
	}
	ct, body = partsBody(t, "related", header("Content-Type", "text/plain"), "x")
	if got := problemErrors(t, do(t, h, "POST", "/bundle", ct, body)); !slices.Equal(got, []string{`body /0: content type "text/plain" is not allowed; expected image/*`}) {
		t.Errorf("errors = %q", got)
	}

	// Text items are checked like strings.
	ct, body = partsBody(t, "alternative",
		header("Content-Type", "text/plain"), "hi",
		header("Content-Type", "text/html"), "",
	)
	r = do(t, h, "PUT", "/memos", ct, body)
	if got := problemErrors(t, r); !slices.Equal(got, []string{"body /1: length must be at least 1"}) {
		t.Errorf("errors = %q", got)
	}
	ct, body = partsBody(t, "alternative", header("Content-Type", "text/plain"), "hi", header("Content-Type", "text/html"), "<b>hi</b>")
	if r := do(t, h, "PUT", "/memos", ct, body); r.status != 204 || !slices.Equal(s.seen["PutNotes"].(*api.PutNotesRequest).Body, []string{"hi", "<b>hi</b>"}) {
		t.Errorf("notes: %d %s", r.status, r.body)
	}
}

func TestFormAndJSONBodies(t *testing.T) {
	s, h := newTestServer()
	if r := do(t, h, "POST", "/pets/form", "application/x-www-form-urlencoded", "role=admin&age=3&tags=a&tags=b"); r.status != 204 {
		t.Fatalf("form: %d %s", r.status, r.body)
	}
	got := s.seen["SubmitForm"].(*api.SubmitFormRequest)
	if got.ContentType != "application/x-www-form-urlencoded" || got.Body.Role != "admin" || got.Body.Age.Value != 3 || len(got.Body.Tags.Value) != 2 {
		t.Errorf("form request = %+v", got)
	}
	if r := do(t, h, "POST", "/pets/form", "application/json", `{"role": "user"}`); r.status != 204 {
		t.Fatalf("json: %d %s", r.status, r.body)
	}
	if got := s.seen["SubmitForm"].(*api.SubmitFormRequest); got.ContentType != "application/json" || got.Body.Role != "user" {
		t.Errorf("json request = %+v", got)
	}
	// The missing role stops decoding, so the negative age is not
	// validated.
	r := do(t, h, "POST", "/pets/form", "application/x-www-form-urlencoded", "age=-1")
	if got := problemErrors(t, r); !slices.Equal(got, []string{"body /role: required property is missing"}) {
		t.Errorf("errors = %q", got)
	}
	r = do(t, h, "POST", "/pets/form", "application/x-www-form-urlencoded", "role=x&age=-1")
	if got := problemErrors(t, r); !slices.Equal(got, []string{"body /age: must be at least 0"}) {
		t.Errorf("errors = %q", got)
	}
}

func TestTextBodies(t *testing.T) {
	_, h := newTestServer()
	r := do(t, h, "POST", "/notes", "text/plain", "hello")
	if r.status != 200 || r.body != "HELLO!" || r.header.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Errorf("%d %q %q", r.status, r.body, r.header.Get("Content-Type"))
	}
	r = do(t, h, "POST", "/notes", "text/plain", "far too long")
	if got := problemErrors(t, r); !slices.Equal(got, []string{"body: length must be at most 10"}) {
		t.Errorf("errors = %q", got)
	}
}

func TestBinaryResponse(t *testing.T) {
	_, h := newTestServer()
	r := do(t, h, "GET", "/pets/1/photo", "", "")
	if r.status != 200 || r.body != "PNGDATA" || r.header.Get("Content-Type") != "image/png" {
		t.Errorf("%d %q %q", r.status, r.body, r.header.Get("Content-Type"))
	}
	// A content type outside image/* is a server error.
	if r := do(t, h, "GET", "/pets/2/photo", "", ""); r.status != 500 {
		t.Errorf("wrong content type: %d %s", r.status, r.body)
	}
}

func TestStreamingAndNegotiation(t *testing.T) {
	_, h := newTestServer()
	r := do(t, h, "GET", "/export", "", "", "Accept", "application/jsonl")
	if r.status != 200 || r.body != "{\"id\":1,\"name\":\"Rex\"}\n{\"id\":2,\"name\":\"Tom\",\"tag\":\"cat\"}\n" {
		t.Errorf("jsonl: %d %q", r.status, r.body)
	}
	r = do(t, h, "GET", "/export", "", "", "Accept", "text/csv;q=0.9, application/jsonl;q=0.1")
	if r.status != 200 || r.body != "1,Rex\n2,Tom\n" {
		t.Errorf("csv: %d %q", r.status, r.body)
	}
	r = do(t, h, "GET", "/events", "", "")
	want := "event: pet\ndata: {\"id\":1,\"name\":\"Rex\"}\n\nevent: pet\ndata: {\"id\":2,\"name\":\"Tom\",\"tag\":\"cat\"}\n\n"
	if r.status != 200 || r.body != want || r.header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("events: %d %q", r.status, r.body)
	}
}

func TestQueryMethodWithOptionalBody(t *testing.T) {
	s, h := newTestServer()
	r := do(t, h, "QUERY", "/search", "application/json", `{"name":"Tom"}`)
	if r.status != 200 || r.body != `[{"id":2,"name":"Tom","tag":"cat"}]` {
		t.Errorf("%d %s", r.status, r.body)
	}
	r = do(t, h, "QUERY", "/search", "", "")
	if r.status != 200 || s.seen["SearchPets"].(*api.SearchPetsRequest).Body.Set {
		t.Errorf("absent optional body: %d %s", r.status, r.body)
	}
}

func TestUnsupportedAndMisbehavingOperations(t *testing.T) {
	_, h := newTestServer()
	// Secure returns neither a response nor an error.
	if r := do(t, h, "GET", "/secure", "", "", "X-API-Key", "good"); r.status != 500 {
		t.Errorf("nil response: %d %s", r.status, r.body)
	}
}

func TestHandlerPanic(t *testing.T) {
	_, h := newTestServer()
	r := do(t, h, "GET", "/pets/666", "", "")
	if r.status != 500 || strings.Contains(r.body, "cursed") {
		t.Errorf("panic: %d %s", r.status, r.body)
	}
	// The server keeps serving.
	if r := do(t, h, "GET", "/pets/1", "", ""); r.status != 200 {
		t.Errorf("after a panic: %d", r.status)
	}
}

func TestCustomBadRequestFormat(t *testing.T) {
	// An API whose documented 400 body is its Error schema, not problem
	// details, can answer invalid requests in that format.
	errorHandler := func(w http.ResponseWriter, r *http.Request, err error) {
		var be *server.BindError
		if errors.As(err, &be) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(be.Problem.Status)
			json.NewEncoder(w).Encode(model.Error{Message: be.Problem.Errors[0].Detail})
			return
		}
		var p *problem.Problem
		if !errors.As(err, &p) {
			p = problem.New(http.StatusInternalServerError, "")
		}
		problem.Write(w, p)
	}
	h := NewServer(newStore(), auth{}, server.WithErrorHandler(errorHandler), server.WithLogger(slog.New(slog.DiscardHandler)))
	r := do(t, h, "GET", "/pets?limit=500", "", "")
	if r.status != 400 || r.header.Get("Content-Type") != "application/json" || !strings.Contains(r.body, `"message":"must be at most 100"`) {
		t.Errorf("bind error: %d %s %s", r.status, r.header.Get("Content-Type"), r.body)
	}
	// Other errors keep their problem details.
	if r := do(t, h, "DELETE", "/pets/99", "", ""); r.status != 403 || r.header.Get("Content-Type") != problem.ContentType {
		t.Errorf("handler problem: %d %s", r.status, r.body)
	}
}

func TestAdditionalOperation(t *testing.T) {
	_, h := newTestServer()
	r := do(t, h, "COPY", "/pets/2", "", "", "Destination", "Tom II")
	if r.status != 201 || r.body != `{"id":3,"name":"Tom II","tag":"cat"}` {
		t.Errorf("%d %s", r.status, r.body)
	}
	if got := problemErrors(t, do(t, h, "COPY", "/pets/2", "", "")); !slices.Equal(got, []string{"header Destination: required parameter is missing"}) {
		t.Errorf("errors = %q", got)
	}
}

func TestSecurity(t *testing.T) {
	_, h := newTestServer()
	tests := []struct {
		name       string
		path       string
		headers    []string
		status     int
		body       string
		challenges []string
	}{
		{"no key", "/secure", nil, 401, "", nil},
		{"wrong key", "/secure", []string{"X-API-Key", "bad"}, 401, "", nil},
		{"no credentials", "/admin", nil, 401, "", []string{"Bearer", `Basic charset="UTF-8"`}},
		{"token with scope", "/admin", []string{"Authorization", "Bearer admin-token"}, 200, "admin-token", nil},
		{"token without scope", "/admin", []string{"Authorization", "Bearer user-token"}, 403, "", nil},
		{"basic", "/admin", []string{"Authorization", "Basic YW5uOnB3"}, 200, "ann", nil},
		{"wrong password", "/admin", []string{"Authorization", "Basic YW5uOm5v"}, 401, "", []string{"Bearer", `Basic charset="UTF-8"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := do(t, h, "GET", tt.path, "", "", tt.headers...)
			if r.status != tt.status || (tt.status == 200 && r.body != tt.body) {
				t.Errorf("%d %q", r.status, r.body)
			}
			if tt.status >= 400 && strings.Contains(r.body, "password") {
				t.Errorf("security handler error leaked: %s", r.body)
			}
			if got := r.header.Values("WWW-Authenticate"); !slices.Equal(got, tt.challenges) {
				t.Errorf("challenges = %q, want %q", got, tt.challenges)
			}
		})
	}
}

func TestHandlerLogs(t *testing.T) {
	// Generated handlers log each request at Debug, and the runtime logs
	// how it ended, all tagged with the operation.
	var out strings.Builder
	h := NewServer(newStore(), auth{}, server.WithLogger(slog.New(slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug}))))
	do(t, h, "GET", "/pets/1", "", "")
	do(t, h, "GET", "/pets/abc", "", "")

	type entry struct{ Level, Msg, Operation, Response string }
	var got []entry
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var e entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("%v: %s", err, line)
		}
		got = append(got, e)
	}
	want := []entry{
		{"DEBUG", "handling request", "getPet", ""},
		{"DEBUG", "sending response", "getPet", "GetPet200JSONResponse"},
		{"DEBUG", "handling request", "getPet", ""},
		{"INFO", "invalid request", "getPet", ""},
	}
	if !slices.Equal(got, want) {
		t.Errorf("log entries:\n%+v\nwant\n%+v", got, want)
	}
}
