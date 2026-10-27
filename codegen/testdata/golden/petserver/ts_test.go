package petserver

// TestTypeScriptClient runs the TypeScript client's end-to-end tests,
// ts/client.e2e.test.ts, against this server, then checks what the server
// received. It is not generated; codegen's TestGeneratedCode runs it.

import (
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"slices"
	"testing"

	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/api"
	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/model"
	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/server"
)

func TestTypeScriptClient(t *testing.T) {
	if testing.Short() {
		t.Skip("runs node")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	s := newStore()
	srv := httptest.NewServer(NewServer(s, auth{}, server.WithLogger(slog.New(slog.DiscardHandler))))
	defer srv.Close()

	cmd := exec.Command(node, "--test", "ts/client.e2e.test.ts")
	cmd.Env = append(os.Environ(), "PETSERVER_URL="+srv.URL+"/v1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node --test: %v\n%s", err, out)
	}

	// What the TypeScript client sent, as the server read it. Handlers of
	// streams the client stopped sending may still be running.
	s.mu.Lock()
	defer s.mu.Unlock()
	filter := model.Filter{Role: "admin", Age: oapi.Some(int64(3)), Tags: oapi.Some([]string{"x", "y"}),
		Owner: oapi.Some(model.FilterOwner{Name: oapi.Some("Ann")}), Labels: oapi.Some(map[string]int64{"a": 1})}
	wantReports := &api.GetReportsRequest{
		IDs: []int64{1, 2}, Filter: oapi.Some(filter), Fields: oapi.Some([]string{"id", "name"}),
		Q: oapi.Some(""), Limit: oapi.Some(int64(5)), XMeta: oapi.Some(filter),
		Prefs: oapi.Some(model.Prefs{Theme: oapi.Some("dark"), Compact: oapi.Some(true)}),
	}
	if got := s.seen["GetReports"].(*api.GetReportsRequest); !reflect.DeepEqual(got, wantReports) {
		t.Errorf("reports:\n got %+v\nwant %+v", got, wantReports)
	}
	if got := s.seen["Lookup"].(*api.LookupRequest); !reflect.DeepEqual(got.Criteria.Value, filter) {
		t.Errorf("lookup = %+v", got.Criteria.Value)
	}
	wantContent := &api.GetContentRequest{Code: 12,
		Where: oapi.Some(model.Filter{Role: "a&b=c", Tags: oapi.Some([]string{"x y", "z"}), Owner: oapi.Some(model.FilterOwner{Name: oapi.Some("Ann"), Age: oapi.Some(int64(4))})}),
		XNote: oapi.Some("a, b"), Session: oapi.Some(model.Prefs{Theme: oapi.Some(`"dark"; x`), Compact: oapi.Some(true)}), Label: oapi.Some("hi, there; you")}
	if got := s.seen["GetContent"].(*api.GetContentRequest); !reflect.DeepEqual(got, wantContent) {
		t.Errorf("content:\n got %+v\nwant %+v", got, wantContent)
	}
	// The last getPet is the link the TypeScript client followed, with
	// the link's constant.
	if got := s.seen["GetPet"].(*api.GetPetRequest); got.Session.Value != "s1" {
		t.Errorf("last pet = %+v", got)
	}
	up := s.seen["UploadPhoto"].(*api.UploadPhotoRequest)
	if up.PetID != 1 || up.Body.Caption.Value != "Rex" || up.Body.Photo.Name != "rex.png" || up.Body.Photo.ContentType != "image/png" || string(up.Body.Photo.Data) != "PNG" {
		t.Errorf("photo = %+v", up)
	}
	if got := s.seen["ImportPet"].(*api.ImportPetRequest); got.TextBody.Value != "id,name\n" || got.ContentType != "text/csv" {
		t.Errorf("import = %+v", got)
	}
	form := s.seen["SubmitForm"].(*api.SubmitFormRequest)
	if form.ContentType != "application/json" || form.Body.Role != "vet" || !slices.Equal(form.Body.Tags.Value, []string{"a", "b"}) || form.Body.Owner.Value.Age.Value != 4 {
		t.Errorf("form = %+v", form)
	}
	if got := s.seen["Upload"].(*api.UploadRequest).Body.Value; got["a"] != "b" {
		t.Errorf("upload = %v", got)
	}
	lines := s.seen["PutLines"].(*api.PutLinesRequest)
	if lines.ID != "o-1" || len(lines.Body.Lines) != 2 || lines.Body.Lines[0].Tags.Value[1] != "sweet" || string(lines.Body.Receipt.Value.Data) != "paid" {
		t.Errorf("lines = %+v", lines)
	}
	if got := s.seen["Subscribe"].(*api.SubscribeRequest); got.XCallback.Value != "https://hooks.example.com/ping" {
		t.Errorf("subscribe = %+v", got)
	}
	// The pets of the array, then those of the async iterable. (Items sent
	// before an invalid one or an error may have been read too.)
	var names []string
	for _, p := range s.pets {
		names = append(names, p.Name)
	}
	if i := slices.Index(names, "Bo"); i < 1 || len(names) < i+3 || !slices.Equal(names[i-1:i+3], []string{"Ann", "Bo", "Cy", "Di"}) {
		t.Errorf("imported pets = %q", names)
	}
	if got := s.seen["PostTypedEvents"].([]server.TypedEvent[model.Pet]); len(got) != 2 || got[0].Data.Name != "Cy" || got[1].Data.Name != "Di" {
		t.Errorf("typed events = %+v", got)
	}
	if got := s.seen["PostEvents"].([]server.Event); !slices.Equal(got, []server.Event{{Event: "a", Data: "1\n2"}, {ID: "7", Data: "x"}}) {
		t.Errorf("events = %+v", got)
	}
}
