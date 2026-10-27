package petserver

// End-to-end tests of the generated client against the generated server.
// They are not generated; codegen's TestGeneratedCode runs them.

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/api"
	petclient "github.com/dheins/go-openapi/codegen/testdata/golden/petserver/client"
	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/model"
	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/client"
	"github.com/dheins/go-openapi/runtime/server"
)

// token returns a credential function returning s.
func token(s string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return s, nil }
}

// newTestClient starts the generated server and returns its store and a
// client of it.
func newTestClient(t *testing.T, creds petclient.Credentials, opts ...client.Option) (*store, *petclient.Client) {
	t.Helper()
	s := newStore()
	srv := httptest.NewServer(NewServer(s, auth{}, server.WithLogger(slog.New(slog.DiscardHandler))))
	t.Cleanup(srv.Close)
	c, err := petclient.New(creds, append([]client.Option{client.WithBaseURL(srv.URL + "/v1")}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return s, c
}

// accept makes a client ask for mediaType only.
func accept(mediaType string) client.Option {
	return client.WithMiddleware(func(next client.Doer) client.Doer {
		return client.DoerFunc(func(r *http.Request) (*http.Response, error) {
			r.Header.Set("Accept", mediaType)
			return next.Do(r)
		})
	})
}

// respond makes a client answer every request with resp, without a server.
func respond(status int, header http.Header, body string) client.Option {
	return client.WithDoer(client.DoerFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}))
}

// as returns a function returning the response of a client method as a
// T, failing the test on an error or another variant.
func as[T any](t *testing.T) func(any, error) T {
	return func(resp any, err error) T {
		t.Helper()
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		v, ok := resp.(T)
		if !ok {
			var want T
			t.Fatalf("response %T %+v, want %T", resp, resp, want)
		}
		return v
	}
}

// unexpected returns err as an *UnexpectedResponse with the given status.
func unexpected(t *testing.T, err error, status int) *client.UnexpectedResponse {
	t.Helper()
	var u *client.UnexpectedResponse
	if !errors.As(err, &u) || u.StatusCode != status {
		t.Fatalf("error %v, want an unexpected %d response", err, status)
	}
	return u
}

func TestClientParameters(t *testing.T) {
	ctx := context.Background()
	s, c := newTestClient(t, petclient.Credentials{})

	r := as[api.ListPets200JSONResponse](t)(c.ListPets(ctx, &api.ListPetsRequest{
		Limit: oapi.Some(int32(1)), Tags: oapi.Some([]string{"a", "b,c"}),
		XRequestID: oapi.Some("123e4567-e89b-12d3-a456-426614174000"), Sort: model.ListPetsSortID,
	}))
	if len(r.Body) != 1 || r.Body[0].Name != "Rex" || r.XTotal != 2 || r.XNext.Value != "cursor-2" || r.XPage.Value.Size.Value != 2 {
		t.Errorf("response = %+v", r)
	}
	if got := s.seen["ListPets"].(*api.ListPetsRequest); got.Limit.Value != 1 || !slices.Equal(got.Tags.Value, []string{"a", "b", "c"}) ||
		got.XRequestID.Value != "123e4567-e89b-12d3-a456-426614174000" || got.Sort != model.ListPetsSortID {
		t.Errorf("request = %+v", got)
	}

	// A nil request sends nothing; the server applies the default.
	as[api.ListPets200JSONResponse](t)(c.ListPets(ctx, nil))
	if got := s.seen["ListPets"].(*api.ListPetsRequest); got.Limit.Set || got.Sort != model.ListPetsSortName {
		t.Errorf("request = %+v", got)
	}

	if p := as[api.GetPet200JSONResponse](t)(c.GetPet(ctx, &api.GetPetRequest{PetID: 2, Session: oapi.Some("s 1")})); p.Body.Name != "Tom" {
		t.Errorf("pet = %+v", p.Body)
	}
	if got := s.seen["GetPet"].(*api.GetPetRequest); got.Session.Value != "s 1" {
		t.Errorf("cookie = %+v", got)
	}
	as[api.GetPet404Response](t)(c.GetPet(ctx, &api.GetPetRequest{PetID: 9}))

	if p := as[api.CopyPet201JSONResponse](t)(c.CopyPet(ctx, &api.CopyPetRequest{PetID: 1, Destination: "Rex II"})); p.Body.Name != "Rex II" {
		t.Errorf("copy = %+v", p.Body)
	}
	if f := as[api.GetFile200JSONResponse](t)(c.GetFile(ctx, &api.GetFileRequest{Name: "read me", Ext: model.GetFileExtMd})); f.Body.Name != "read me" || f.Body.Ext != "md" {
		t.Errorf("file = %+v", f.Body)
	}

	filter := model.Filter{Role: "admin", Age: oapi.Some(int64(3)), Tags: oapi.Some([]string{"x", "y"}),
		Owner: oapi.Some(model.FilterOwner{Name: oapi.Some("Ann")}), Labels: oapi.Some(map[string]int64{"a": 1})}
	reports := &api.GetReportsRequest{
		IDs: []int64{1, 2}, Filter: oapi.Some(filter), Fields: oapi.Some([]string{"id", "name"}),
		Q: oapi.Some(""), Limit: oapi.Some(int64(5)), XMeta: oapi.Some(filter),
		Prefs: oapi.Some(model.Prefs{Theme: oapi.Some("dark"), Compact: oapi.Some(true)}),
	}
	as[api.GetReports204Response](t)(c.GetReports(ctx, reports))
	if got := s.seen["GetReports"].(*api.GetReportsRequest); !reflect.DeepEqual(got, reports) {
		t.Errorf("reports:\n got %+v\nwant %+v", got, reports)
	}

	lookup := &api.LookupRequest{Criteria: oapi.Some(filter)}
	as[api.Lookup204Response](t)(c.Lookup(ctx, lookup))
	if got := s.seen["Lookup"].(*api.LookupRequest); !reflect.DeepEqual(got, lookup) {
		t.Errorf("lookup:\n got %+v\nwant %+v", got, lookup)
	}
}

func TestClientBodies(t *testing.T) {
	ctx := context.Background()
	s, c := newTestClient(t, petclient.Credentials{})

	for _, ct := range []string{"", "application/json", "application/xml"} {
		r := as[api.CreatePet201JSONResponse](t)(c.CreatePet(ctx, &api.CreatePetRequest{Body: model.Pet{Name: "Ann", Secret: oapi.Some("s")}, ContentType: ct}))
		if r.Body.Name != "Ann" || r.Body.ID == 0 || r.Body.Secret.Set {
			t.Errorf("%q: created %+v", ct, r.Body)
		}
		if got := s.seen["CreatePet"].(*api.CreatePetRequest); got.Body.Secret.Value != "s" {
			t.Errorf("%q: request %+v", ct, got)
		}
	}
	if r := as[api.CreatePet4XXJSONResponse](t)(c.CreatePet(ctx, &api.CreatePetRequest{Body: model.Pet{Name: "Taken"}})); r.StatusCode != 409 || r.Body.Message != "name taken" {
		t.Errorf("conflict = %+v", r)
	}

	if r := as[api.SearchPets200JSONResponse](t)(c.SearchPets(ctx, &api.SearchPetsRequest{Body: oapi.Some(model.SearchPetsRequestBody{Name: oapi.Some("Tom")})})); len(r.Body) != 1 || r.Body[0].Name != "Tom" {
		t.Errorf("search = %+v", r.Body)
	}
	if r := as[api.SearchPets200JSONResponse](t)(c.SearchPets(ctx, nil)); len(r.Body) != len(s.pets) {
		t.Errorf("search without a body = %+v", r.Body)
	}

	photo := oapi.File{Name: "rex.png", ContentType: "image/png", Data: []byte("PNG")}
	as[api.UploadPhoto204Response](t)(c.UploadPhoto(ctx, &api.UploadPhotoRequest{PetID: 1, Body: model.UploadPhotoRequestBody{Caption: oapi.Some("Rex"), Photo: photo}}))
	if got := s.seen["UploadPhoto"].(*api.UploadPhotoRequest); got.PetID != 1 || got.Body.Caption.Value != "Rex" ||
		got.Body.Photo.Name != "rex.png" || string(got.Body.Photo.Data) != "PNG" {
		t.Errorf("photo = %+v", got)
	}

	records := model.UploadRecordsRequestBody{
		Report: oapi.File{Name: "r.pdf", ContentType: "application/pdf", Header: textproto.MIMEHeader{"X-Checksum": {"abc"}}, Data: []byte("%PDF")},
		Scans:  oapi.Some([]model.Scan{{Name: "1.png", ContentType: "image/png", Data: []byte("1")}, {Name: "2.jpg", ContentType: "image/jpeg", Data: []byte("2")}}),
	}
	as[api.UploadRecords204Response](t)(c.UploadRecords(ctx, &api.UploadRecordsRequest{PetID: 1, Body: records}))
	if got := s.seen["UploadRecords"].(*api.UploadRecordsRequest).Body; got.Report.Header.Get("X-Checksum") != "abc" || len(got.Scans.Value) != 2 ||
		got.Scans.Value[1].ContentType != "image/jpeg" || string(got.Scans.Value[1].Data) != "2" {
		t.Errorf("records = %+v", got)
	}

	// Each media type of a body with several, chosen by ContentType or by
	// the field that is set.
	imports := []*api.ImportPetRequest{
		{Body: oapi.Some(model.Pet{Name: "Ann"})},
		{JSONBody: oapi.Some(model.ImportPetJSONRequestBody{Name: oapi.Some("Bo")})},
		{JSONBody: oapi.Some(model.ImportPetJSONRequestBody{Name: oapi.Some("Cy")}), ContentType: "application/x-www-form-urlencoded"},
		{MultipartBody: oapi.Some(model.ImportPetMultipartRequestBody{Photo: photo})},
		{TextBody: oapi.Some("id,name\n")},
	}
	for _, req := range imports {
		as[api.ImportPet204Response](t)(c.ImportPet(ctx, req))
		got := s.seen["ImportPet"].(*api.ImportPetRequest)
		if got.Body != req.Body || got.JSONBody != req.JSONBody || got.TextBody != req.TextBody ||
			got.MultipartBody.Set != req.MultipartBody.Set || string(got.MultipartBody.Value.Photo.Data) != string(req.MultipartBody.Value.Photo.Data) {
			t.Errorf("import %+v: got %+v", req, got)
		}
	}

	card := model.Card{ID: 7, Name: "Rex", Nicknames: oapi.Some([]string{"T-Rex", "Rexy"}), Secret: oapi.Some("s"),
		Vets: oapi.Some([]model.Vet{{Name: oapi.Some("Dr. Who"), Phone: oapi.Some("555")}}),
		Note: oapi.Some(model.Note{Lang: oapi.Some("en"), Text: oapi.Some("likes <bones>")})}
	as[api.PutCard204Response](t)(c.PutCard(ctx, &api.PutCardRequest{PetID: 7, Body: card}))
	if got := s.seen["PutCard"].(*api.PutCardRequest); !reflect.DeepEqual(got.Body, card) {
		t.Errorf("card:\n got %+v\nwant %+v", got.Body, card)
	}

	form := model.Filter{Role: "vet", Tags: oapi.Some([]string{"a", "b"}), Owner: oapi.Some(model.FilterOwner{Age: oapi.Some(int64(4))})}
	for _, ct := range []string{"application/x-www-form-urlencoded", "application/json"} {
		as[api.SubmitForm204Response](t)(c.SubmitForm(ctx, &api.SubmitFormRequest{Body: form, ContentType: ct}))
		if got := s.seen["SubmitForm"].(*api.SubmitFormRequest); !reflect.DeepEqual(got.Body, form) || got.ContentType != ct {
			t.Errorf("%s form = %+v", ct, got)
		}
	}

	as[api.PutNotes204Response](t)(c.PutNotes(ctx, &api.PutNotesRequest{Body: []string{"hi", "<b>there</b>"}}))
	if got := s.seen["PutNotes"].(*api.PutNotesRequest).Body; !slices.Equal(got, []string{"hi", "<b>there</b>"}) {
		t.Errorf("notes = %q", got)
	}

	if r := as[api.Shout200TextResponse](t)(c.Shout(ctx, &api.ShoutRequest{Body: oapi.Some("hey")})); r.Body != "HEY!" {
		t.Errorf("shout = %q", r.Body)
	}

	as[api.Upload204Response](t)(c.Upload(ctx, &api.UploadRequest{Body: oapi.Some(map[string]any{"a": "b"})}))
	if got := s.seen["Upload"].(*api.UploadRequest).Body.Value; got["a"] != "b" {
		t.Errorf("upload = %v", got)
	}
}

func TestClientResponses(t *testing.T) {
	ctx := context.Background()
	_, c := newTestClient(t, petclient.Credentials{})

	if r := as[api.GetCard200JSONResponse](t)(c.GetCard(ctx, &api.GetCardRequest{PetID: 3})); r.Body.ID != 3 || r.Body.Secret.Set {
		t.Errorf("card = %+v", r.Body)
	}
	_, xc := newTestClient(t, petclient.Credentials{}, accept("application/xml"))
	r := as[api.GetCard200XMLResponse](t)(xc.GetCard(ctx, &api.GetCardRequest{PetID: 3}))
	if r.Body.ID != 3 || r.Body.Note.Value.Text.Value != "likes <bones>" || r.Body.Vets.Value[0].Phone.Value != "555" ||
		!slices.Equal(r.Body.Nicknames.Value, []string{"T-Rex"}) {
		t.Errorf("XML card = %+v", r.Body)
	}

	if r := as[api.GetPhoto200ImageResponse](t)(c.GetPhoto(ctx, &api.GetPhotoRequest{PetID: 1})); r.ContentType != "image/png" || string(r.Body) != "PNGDATA" {
		t.Errorf("photo = %q %q", r.ContentType, r.Body)
	}

	images := []oapi.File{{ContentType: "image/png", Data: []byte("1")}, {ContentType: "image/gif", Data: []byte("2")}}
	if r := as[api.UploadBundle200RelatedResponse](t)(c.UploadBundle(ctx, &api.UploadBundleRequest{Body: images})); len(r.Body) != 2 ||
		r.Body[1].ContentType != "image/gif" || string(r.Body[1].Data) != "2" {
		t.Errorf("bundle = %+v", r.Body)
	}

	lines := as[api.ExportPets200JSONLinesResponse](t)(c.ExportPets(ctx, nil))
	var names []string
	for p, err := range lines.Items {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, p.Name)
	}
	if !slices.Equal(names, []string{"Rex", "Tom"}) {
		t.Errorf("lines = %q", names)
	}
	_, csv := newTestClient(t, petclient.Credentials{}, accept("text/csv"))
	if r := as[api.ExportPets200TextResponse](t)(csv.ExportPets(ctx, nil)); r.Body != "1,Rex\n2,Tom\n" {
		t.Errorf("csv = %q", r.Body)
	}

	events := as[api.Watch200EventsResponse](t)(c.Watch(ctx, nil))
	var got []string
	for ev, err := range events.Events {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ev.Event+" "+ev.Data)
	}
	if !slices.Equal(got, []string{`pet {"id":1,"name":"Rex"}`, `pet {"id":2,"name":"Tom","tag":"cat"}`}) {
		t.Errorf("events = %q", got)
	}
}

func TestClientErrors(t *testing.T) {
	ctx := context.Background()
	s, c := newTestClient(t, petclient.Credentials{})

	// Problems the server reports, and statuses the document does not
	// declare for the operation.
	_, err := c.DeletePet(ctx, &api.DeletePetRequest{PetID: 99})
	if u := unexpected(t, err, 403); u.Problem == nil || u.Problem.Detail != "pet 99 is protected" {
		t.Errorf("problem = %+v", u.Problem)
	}
	_, err = c.CreatePet(ctx, &api.CreatePetRequest{Body: model.Pet{Name: "Bad status"}})
	unexpected(t, err, 500)
	_, err = c.GetPhoto(ctx, &api.GetPhotoRequest{PetID: 2})
	unexpected(t, err, 500)

	// Invalid requests are not sent, unless validation is off.
	delete(s.seen, "ListPets")
	_, err = c.ListPets(ctx, &api.ListPetsRequest{Limit: oapi.Some(int32(0))})
	var invalid *client.InvalidRequest
	if !errors.As(err, &invalid) || len(invalid.Problem.Errors) != 1 || invalid.Problem.Errors[0].Parameter != "limit" {
		t.Errorf("invalid request: %v", err)
	}
	if _, sent := s.seen["ListPets"]; sent {
		t.Error("an invalid request was sent")
	}
	_, nc := newTestClient(t, petclient.Credentials{}, client.WithoutRequestValidation())
	_, err = nc.ListPets(ctx, &api.ListPetsRequest{Limit: oapi.Some(int32(0))})
	if u := unexpected(t, err, 400); u.Problem == nil || len(u.Problem.Errors) != 1 || u.Problem.Errors[0].Parameter != "limit" {
		t.Errorf("server problem = %+v", u.Problem)
	}

	// Responses are validated on request.
	json := http.Header{"Content-Type": {"application/json"}}
	bad, err := petclient.New(petclient.Credentials{}, respond(200, json, `{"id":1,"name":""}`))
	if err != nil {
		t.Fatal(err)
	}
	as[api.GetPet200JSONResponse](t)(bad.GetPet(ctx, &api.GetPetRequest{PetID: 1}))
	strict, err := petclient.New(petclient.Credentials{}, respond(200, json, `{"id":1,"name":""}`), client.WithResponseValidation())
	if err != nil {
		t.Fatal(err)
	}
	var invalidResp *client.InvalidResponse
	if _, err := strict.GetPet(ctx, &api.GetPetRequest{PetID: 1}); !errors.As(err, &invalidResp) || invalidResp.StatusCode != 200 {
		t.Errorf("invalid response: %v", err)
	}
	noTotal, err := petclient.New(petclient.Credentials{}, respond(200, json, `[]`), client.WithResponseValidation())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := noTotal.ListPets(ctx, nil); !errors.As(err, &invalidResp) || !strings.Contains(err.Error(), "X-Total") {
		t.Errorf("missing required header: %v", err)
	}
	// A body that does not decode is invalid whether or not validation is
	// on.
	broken, err := petclient.New(petclient.Credentials{}, respond(200, json, `{"id":"one"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := broken.GetPet(ctx, &api.GetPetRequest{PetID: 1}); !errors.As(err, &invalidResp) {
		t.Errorf("undecodable response: %v", err)
	}
	// A declared status in an undeclared media type is unexpected too.
	teapot, err := petclient.New(petclient.Credentials{}, respond(200, http.Header{"Content-Type": {"text/html"}}, "<p>"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = teapot.GetPet(ctx, &api.GetPetRequest{PetID: 1})
	if u := unexpected(t, err, 200); string(u.Body) != "<p>" {
		t.Errorf("body = %q", u.Body)
	}
}

func TestClientSecurity(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		creds petclient.Credentials
		user  string
	}{
		{"oauth", petclient.Credentials{Oauth: token("admin-token")}, "admin-token"},
		{"basic", petclient.Credentials{BasicAuth: func(context.Context) (string, string, error) { return "ann", "pw", nil }}, "ann"},
		// The first requirement with credentials is used.
		{"both", petclient.Credentials{Oauth: token("admin-token"), BasicAuth: func(context.Context) (string, string, error) { return "x", "y", nil }}, "admin-token"},
	}
	for _, tc := range cases {
		_, c := newTestClient(t, tc.creds)
		if r := as[api.Admin200TextResponse](t)(c.Admin(ctx, nil)); r.Body != tc.user {
			t.Errorf("%s: user %q", tc.name, r.Body)
		}
	}

	_, c := newTestClient(t, petclient.Credentials{Oauth: token("user-token")})
	_, err := c.Admin(ctx, nil)
	if u := unexpected(t, err, 403); u.Problem == nil || !strings.Contains(u.Problem.Detail, "admin scope") {
		t.Errorf("problem = %+v", u.Problem)
	}

	_, c = newTestClient(t, petclient.Credentials{})
	if _, err := c.Admin(ctx, nil); !errors.Is(err, client.ErrNoCredentials) {
		t.Errorf("no credentials: %v", err)
	}
	// Operations without security send none.
	as[api.GetPet200JSONResponse](t)(c.GetPet(ctx, &api.GetPetRequest{PetID: 1}))

	_, c = newTestClient(t, petclient.Credentials{APIKey: token("good")})
	// The handler returns no response, which the server reports.
	_, err = c.Secure(ctx, nil)
	unexpected(t, err, 500)
	_, c = newTestClient(t, petclient.Credentials{APIKey: token("bad")})
	_, err = c.Secure(ctx, nil)
	unexpected(t, err, 401)

	if _, err := petclient.New(petclient.Credentials{}, client.WithBaseURL("relative")); err == nil {
		t.Error("a relative base URL")
	}
	if c, err := petclient.New(petclient.Credentials{}); err != nil || c == nil {
		t.Errorf("default server URL: %v", err)
	}
}

func TestClientPartHeaders(t *testing.T) {
	ctx := context.Background()
	_, c := newTestClient(t, petclient.Credentials{})
	report := func(sum string) *api.UploadRecordsRequest {
		return &api.UploadRecordsRequest{PetID: 1, Body: model.UploadRecordsRequestBody{
			Report: oapi.File{Name: "r.pdf", ContentType: "application/pdf", Header: textproto.MIMEHeader{"X-Checksum": {sum}}, Data: []byte("%PDF")},
		}}
	}
	if _, err := c.UploadRecords(ctx, report("abc")); err != nil {
		t.Fatal(err)
	}
	// The headers of file parts are checked before sending, against their
	// schemas, as the server checks them.
	var invalid *client.InvalidRequest
	if _, err := c.UploadRecords(ctx, report("AB!")); !errors.As(err, &invalid) || len(invalid.Problem.Errors) != 1 ||
		invalid.Problem.Errors[0].Pointer != "/report" || !strings.Contains(invalid.Problem.Errors[0].Detail, `part header "X-Checksum": must match the pattern`) {
		t.Errorf("bad checksum: %v", err)
	}
	missing := report("abc")
	missing.Body.Report.Header = nil
	if _, err := c.UploadRecords(ctx, missing); !errors.As(err, &invalid) || invalid.Problem.Errors[0].Detail != `part header "X-Checksum" is missing` {
		t.Errorf("no checksum: %v", err)
	}

	// Positional parts, by position.
	images := []oapi.File{
		{ContentType: "image/png", Header: textproto.MIMEHeader{"X-Order": {"1"}}, Data: []byte("1")},
		{ContentType: "image/png", Header: textproto.MIMEHeader{"X-Order": {"0"}}, Data: []byte("2")},
	}
	if _, err := c.UploadBundle(ctx, &api.UploadBundleRequest{Body: images}); !errors.As(err, &invalid) || len(invalid.Problem.Errors) != 1 ||
		invalid.Problem.Errors[0].Pointer != "/1" || invalid.Problem.Errors[0].Detail != `part header "X-Order": must be at least 1` {
		t.Errorf("bad order: %v", err)
	}
	// Without validation, the server finds the same problems.
	_, lax := newTestClient(t, petclient.Credentials{}, client.WithoutRequestValidation())
	_, err := lax.UploadBundle(ctx, &api.UploadBundleRequest{Body: images})
	if u := unexpected(t, err, 400); u.Problem == nil || len(u.Problem.Errors) != 1 || u.Problem.Errors[0].Detail != `part header "X-Order": must be at least 1` {
		t.Errorf("server-side bad order: %+v", u.Problem)
	}
	_, err = lax.UploadRecords(ctx, report("AB!"))
	if u := unexpected(t, err, 400); u.Problem == nil || len(u.Problem.Errors) != 1 || u.Problem.Errors[0].Pointer != "/report" {
		t.Errorf("server-side bad checksum: %+v", u.Problem)
	}

	// JSON parts carry their headers in oapi.Part, both ways.
	pets := func(seq string, names ...string) *api.BatchPetsRequest {
		req := &api.BatchPetsRequest{}
		for i, name := range names {
			p := oapi.Part[model.Pet]{Value: model.Pet{Name: name}}
			if i == 0 {
				p.Header = textproto.MIMEHeader{"X-Seq": {seq}}
			}
			req.Body = append(req.Body, p)
		}
		return req
	}
	resp, err := c.BatchPets(ctx, pets("1", "Rex", "Tom"))
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.(api.BatchPets200MixedResponse).Body; len(got) != 2 || got[1].Value.Name != "Tom" || got[1].Header.Get("X-Stored") != "true" {
		t.Errorf("stored = %+v", got)
	}
	if _, err := c.BatchPets(ctx, pets("0", "Rex")); !errors.As(err, &invalid) || len(invalid.Problem.Errors) != 1 ||
		invalid.Problem.Errors[0].Pointer != "/0" || invalid.Problem.Errors[0].Detail != `part header "X-Seq": must be at least 1` {
		t.Errorf("bad seq: %v", err)
	}
	if _, err := c.BatchPets(ctx, &api.BatchPetsRequest{Body: []oapi.Part[model.Pet]{{Value: model.Pet{Name: "Rex"}}}}); !errors.As(err, &invalid) ||
		invalid.Problem.Errors[0].Detail != `part header "X-Seq" is missing` {
		t.Errorf("no seq: %v", err)
	}
	_, err = lax.BatchPets(ctx, pets("0", "Rex"))
	if u := unexpected(t, err, 400); u.Problem == nil || len(u.Problem.Errors) != 1 || u.Problem.Errors[0].Detail != `part header "X-Seq": must be at least 1` {
		t.Errorf("server-side bad seq: %+v", u.Problem)
	}
	// A response part without its required header is invalid.
	var invalidResp *client.InvalidResponse
	if _, err := c.BatchPets(ctx, pets("1", "Unstored")); !errors.As(err, &invalidResp) {
		t.Errorf("response part without its header: %v", err)
	}
}
