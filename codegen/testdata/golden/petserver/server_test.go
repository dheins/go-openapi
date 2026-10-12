package petserver

// End-to-end tests of the generated server. They are not generated;
// codegen's TestGeneratedCode runs them.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/problem"
	"github.com/dheins/go-openapi/runtime/server"
)

// store implements Handler with an in-memory list of pets.
type store struct {
	pets   []Pet
	nextID int64
	// seen records the last request each method received.
	seen map[string]any
}

func newStore() *store {
	return &store{pets: []Pet{{ID: 1, Name: "Rex"}, {ID: 2, Name: "Tom", Tag: oapi.Some("cat")}}, nextID: 3, seen: map[string]any{}}
}

func (s *store) ListPets(ctx context.Context, req *ListPetsRequest) (ListPetsResponse, error) {
	s.seen["ListPets"] = req
	if _, ok := server.HTTPRequest(ctx); !ok {
		return nil, errors.New("no *http.Request in context")
	}
	pets := s.pets
	if n, ok := req.Limit.Get(); ok && int(n) < len(pets) {
		pets = pets[:n]
	}
	return ListPets200JSONResponse{Body: pets, XTotal: int64(len(s.pets)), XNext: oapi.Some("cursor-2")}, nil
}

func (s *store) CreatePet(_ context.Context, req *CreatePetRequest) (CreatePetResponse, error) {
	s.seen["CreatePet"] = req
	if req.Body.Name == "Taken" {
		return CreatePet4XXJSONResponse{StatusCode: http.StatusConflict, Body: Error{Message: "name taken"}}, nil
	}
	if req.Body.Name == "Bad status" {
		return CreatePet4XXJSONResponse{StatusCode: http.StatusInternalServerError}, nil
	}
	pet := req.Body
	pet.ID = s.nextID
	s.nextID++
	s.pets = append(s.pets, pet)
	return CreatePet201JSONResponse{Body: pet}, nil
}

func (s *store) GetPet(_ context.Context, req *GetPetRequest) (GetPetResponse, error) {
	s.seen["GetPet"] = req
	if req.PetID == 666 {
		panic("cursed pet")
	}
	for _, p := range s.pets {
		if p.ID == req.PetID {
			return GetPet200JSONResponse{Body: p}, nil
		}
	}
	return GetPet404Response{}, nil
}

func (s *store) DeletePet(_ context.Context, req *DeletePetRequest) (DeletePetResponse, error) {
	if req.PetID == 99 {
		return nil, problem.New(http.StatusForbidden, "pet 99 is protected")
	}
	if req.PetID == 500 {
		return nil, errors.New("database password is hunter2")
	}
	return DeletePet204Response{}, nil
}

func (s *store) GetFile(_ context.Context, req *GetFileRequest) (GetFileResponse, error) {
	return GetFile200JSONResponse{Body: GetFile200ResponseBody{Name: req.Name, Ext: string(req.Ext)}}, nil
}

func (s *store) SearchPets(_ context.Context, req *SearchPetsRequest) (SearchPetsResponse, error) {
	s.seen["SearchPets"] = req
	var found []Pet
	for _, p := range s.pets {
		if !req.Body.Set || p.Name == req.Body.Value.Name.Value {
			found = append(found, p)
		}
	}
	return SearchPets200JSONResponse{Body: found}, nil
}

func (s *store) Secure(context.Context, *SecureRequest) (SecureResponse, error) {
	return nil, nil
}

func (s *store) CopyPet(_ context.Context, req *CopyPetRequest) (CopyPetResponse, error) {
	for _, p := range s.pets {
		if p.ID == req.PetID {
			p.ID = s.nextID
			p.Name = req.Destination
			s.nextID++
			s.pets = append(s.pets, p)
			return CopyPet201JSONResponse{Body: p}, nil
		}
	}
	return nil, problem.New(http.StatusNotFound, "no such pet")
}

func (s *store) Admin(ctx context.Context, _ *AdminRequest) (AdminResponse, error) {
	user, _ := ctx.Value(userKey{}).(string)
	return Admin200TextResponse{Body: user}, nil
}

func (s *store) GetReports(_ context.Context, req *GetReportsRequest) (GetReportsResponse, error) {
	s.seen["GetReports"] = req
	return GetReports204Response{}, nil
}

func (s *store) Lookup(_ context.Context, req *LookupRequest) (LookupResponse, error) {
	s.seen["Lookup"] = req
	return Lookup204Response{}, nil
}

func (s *store) UploadPhoto(_ context.Context, req *UploadPhotoRequest) (UploadPhotoResponse, error) {
	s.seen["UploadPhoto"] = req
	return UploadPhoto204Response{}, nil
}

func (s *store) GetPhoto(_ context.Context, req *GetPhotoRequest) (GetPhotoResponse, error) {
	if req.PetID == 2 {
		return GetPhoto200ImageResponse{ContentType: "text/plain", Body: []byte("oops")}, nil
	}
	return GetPhoto200ImageResponse{ContentType: "image/png", Body: []byte("PNGDATA")}, nil
}

func (s *store) SubmitForm(_ context.Context, req *SubmitFormRequest) (SubmitFormResponse, error) {
	s.seen["SubmitForm"] = req
	return SubmitForm204Response{}, nil
}

func (s *store) Shout(_ context.Context, req *ShoutRequest) (ShoutResponse, error) {
	return Shout200TextResponse{Body: strings.ToUpper(req.Body.Value) + "!"}, nil
}

func (s *store) ExportPets(ctx context.Context, _ *ExportPetsRequest) (ExportPetsResponse, error) {
	r, _ := server.HTTPRequest(ctx)
	if server.Negotiate(r, "application/jsonl", "text/csv") == "text/csv" {
		var csv strings.Builder
		for _, p := range s.pets {
			fmt.Fprintf(&csv, "%d,%s\n", p.ID, p.Name)
		}
		return ExportPets200TextResponse{Body: csv.String()}, nil
	}
	return ExportPets200JSONLinesResponse{Items: func(yield func(Pet, error) bool) {
		for _, p := range s.pets {
			if !yield(p, nil) {
				return
			}
		}
	}}, nil
}

func (s *store) Watch(context.Context, *WatchRequest) (WatchResponse, error) {
	return Watch200EventsResponse{Events: func(yield func(server.Event, error) bool) {
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
	if r.status != 200 || r.header.Get("X-Total") != "2" || r.header.Get("X-Next") != "cursor-2" {
		t.Fatalf("%d %v %s", r.status, r.header, r.body)
	}
	if r.body != `[{"id":1,"name":"Rex"}]` {
		t.Errorf("body = %s", r.body)
	}
	req := s.seen["ListPets"].(*ListPetsRequest)
	if !slices.Equal(req.Tags.Value, []string{"a", "b"}) || req.Limit.Value != 1 || req.Sort != ListPetsSortName {
		t.Errorf("request = %+v", req)
	}
	do(t, h, "GET", "/pets?sort=id", "", "")
	if req := s.seen["ListPets"].(*ListPetsRequest); req.Sort != ListPetsSortID {
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
	if req := s.seen["CreatePet"].(*CreatePetRequest); req.Body.ID != 0 || req.Body.Secret.Value != "s3cret" {
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
	s.pets = append(s.pets, Pet{ID: 7, Name: ""})
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
	if req := s.seen["CreatePet"].(*CreatePetRequest); req.Body.Name != "Kit" {
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
	r = do(t, h, "POST", "/pets", "application/xml", "<pet/>")
	if r.status != 415 {
		t.Errorf("xml body: %d %s", r.status, r.body)
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
	if got := s.seen["GetPet"].(*GetPetRequest); got.Session.Value != "s1" || got.PetID != 2 {
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
	got := s.seen["GetReports"].(*GetReportsRequest)
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
	c := s.seen["Lookup"].(*LookupRequest).Criteria.Value
	if c.Role != "admin" || c.Age.Value != 4 || !slices.Equal(c.Tags.Value, []string{"a", "b"}) {
		t.Errorf("criteria = %+v", c)
	}
	r := do(t, h, "GET", "/lookup?role=admin&age=-1", "", "")
	if got := problemErrors(t, r); !slices.Equal(got, []string{"querystring criteria /age: must be at least 0"}) {
		t.Errorf("errors = %q", got)
	}
	if r := do(t, h, "GET", "/lookup", "", ""); r.status != 204 || s.seen["Lookup"].(*LookupRequest).Criteria.Set {
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
	got := s.seen["UploadPhoto"].(*UploadPhotoRequest)
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

func TestFormAndJSONBodies(t *testing.T) {
	s, h := newTestServer()
	if r := do(t, h, "POST", "/pets/form", "application/x-www-form-urlencoded", "role=admin&age=3&tags=a&tags=b"); r.status != 204 {
		t.Fatalf("form: %d %s", r.status, r.body)
	}
	got := s.seen["SubmitForm"].(*SubmitFormRequest)
	if got.ContentType != "application/x-www-form-urlencoded" || got.Body.Role != "admin" || got.Body.Age.Value != 3 || len(got.Body.Tags.Value) != 2 {
		t.Errorf("form request = %+v", got)
	}
	if r := do(t, h, "POST", "/pets/form", "application/json", `{"role": "user"}`); r.status != 204 {
		t.Fatalf("json: %d %s", r.status, r.body)
	}
	if got := s.seen["SubmitForm"].(*SubmitFormRequest); got.ContentType != "application/json" || got.Body.Role != "user" {
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
	if r.status != 200 || s.seen["SearchPets"].(*SearchPetsRequest).Body.Set {
		t.Errorf("absent optional body: %d %s", r.status, r.body)
	}
}

func TestUnsupportedAndMisbehavingOperations(t *testing.T) {
	_, h := newTestServer()
	r := do(t, h, "POST", "/upload", "multipart/form-data", "")
	if r.status != 501 || !strings.Contains(r.body, "multipart/form-data") {
		t.Errorf("upload: %d %s", r.status, r.body)
	}
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
			json.NewEncoder(w).Encode(Error{Message: be.Problem.Errors[0].Detail})
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
