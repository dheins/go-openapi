package petserver

// End-to-end tests of codecs beyond whole bodies: parameters and headers
// of a codec's media type, positional parts and streams of them, and
// bodies of a codec's range, read with the JSON decoder's rules. Not
// generated; codegen's TestGeneratedCode runs them.

import (
	"bytes"
	"context"
	"iter"
	"mime/multipart"
	"net/textproto"
	"slices"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/api"
	petclient "github.com/dheins/go-openapi/codegen/testdata/golden/petserver/client"
	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/model"
	"github.com/dheins/go-openapi/oapi"
)

// tcParts returns a multipart body of subtype sub whose parts are the
// given TC documents.
func tcParts(t *testing.T, sub string, docs ...string) (string, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, d := range docs {
		w, err := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/x-tc2"}})
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(d))
	}
	mw.Close()
	return "multipart/" + sub + "; boundary=" + mw.Boundary(), buf.String()
}

func TestCodecEverywhere(t *testing.T) {
	s, h := newTestServer()

	// A parameter and a header read with the codec, and a header written.
	r := do(t, h, "GET", `/coded?where=TC{"w":2}`, "", "", "X-Meta", `TC{"h":3}`)
	if r.status != 204 || r.header.Get("X-Page") != `TC{"w":2}` {
		t.Errorf("parameters: %d %v", r.status, r.header)
	}
	if got := s.seen["GetCoded"].(*api.GetCodedRequest); got.Where.W.Value != 2 || got.XMeta.Value.H.Value != 3 {
		t.Errorf("parameters = %+v", got)
	}
	r = do(t, h, "GET", `/coded?where=TC{"w":0}`, "", "")
	if got := problemErrors(t, r); r.status != 400 || len(got) != 1 || !strings.HasPrefix(got[0], "query where /w: ") {
		t.Errorf("invalid parameter: %d %q", r.status, got)
	}

	// A body of the range, read as the media type sent and with the JSON
	// decoder's rules: the read-only id need not be there, the name must.
	r = do(t, h, "PUT", "/coded", "application/x-tc2", `TC{"name":"Rex"}`)
	if r.status != 200 || r.header.Get("Content-Type") != "application/x-tc" || r.body != `TC{"id":7,"name":"Rex"}` {
		t.Errorf("range: %d %s %s", r.status, r.header.Get("Content-Type"), r.body)
	}
	if got := s.seen["PutCoded"].(*api.PutCodedRequest); got.ContentType != "application/x-tc2" {
		t.Errorf("ContentType = %q", got.ContentType)
	}
	r = do(t, h, "PUT", "/coded", "application/x-tc2", `TC{"tag":"x"}`)
	if got := problemErrors(t, r); r.status != 400 || !slices.Equal(got, []string{"body /name: required property is missing"}) {
		t.Errorf("missing name: %d %q", r.status, got)
	}
	if r := do(t, h, "PUT", "/coded", "application/x-gob", "x"); r.status != 415 || !strings.Contains(r.body, "application/x-tc or application/x-tc2") {
		t.Errorf("no codec: %d %s", r.status, r.body)
	}

	// Parts of the codec's media type, and a stream of them.
	ct, body := tcParts(t, "mixed", `TC{"name":"Rex"}`, `TC{"name":"Tom"}`)
	if r := do(t, h, "POST", "/coded", ct, body); r.status != 200 || r.body != `["Rex","Tom"]` {
		t.Errorf("parts: %d %s", r.status, r.body)
	}
	ct, body = tcParts(t, "related", `TC{"name":"Kit"}`, `TC{"name":""}`)
	if r := do(t, h, "POST", "/coded", ct, body); r.status != 400 || !strings.Contains(r.body, `"/1/name"`) {
		t.Errorf("invalid streamed part: %d %s", r.status, r.body)
	}

	// The Go client sends and reads the same.
	_, c := newTestClient(t, petclient.Credentials{})
	ctx := context.Background()
	size := model.Size{W: oapi.Some(int64(4))}
	got := as[api.GetCoded204Response](t)(c.GetCoded(ctx, &api.GetCodedRequest{Where: size, XMeta: oapi.Some(model.Size{H: oapi.Some(int64(5))})}))
	if got.XPage.Value != size {
		t.Errorf("X-Page = %+v", got.XPage)
	}
	put := as[api.PutCoded200ApplicationResponse](t)(c.PutCoded(ctx, &api.PutCodedRequest{ContentType: "application/x-tc2", Body: model.Pet{Name: "Rex"}}))
	if put.ContentType != "application/x-tc" || put.Body.ID != 7 || put.Body.Name != "Rex" {
		t.Errorf("range = %+v", put)
	}
	if _, err := c.PutCoded(ctx, &api.PutCodedRequest{Body: model.Pet{Name: "Rex"}}); err == nil || !strings.Contains(err.Error(), "ContentType") {
		t.Errorf("no ContentType: %v", err)
	}
	names := as[api.PostCodedParts200JSONResponse](t)(c.PostCodedParts(ctx, &api.PostCodedPartsRequest{Body: []model.Pet{{Name: "Rex"}, {Name: "Tom"}}}))
	if !slices.Equal(names.Body, []string{"Rex", "Tom"}) {
		t.Errorf("parts = %q", names.Body)
	}
	stream := func(yield func(model.Pet, error) bool) {
		for _, n := range []string{"Ann", "Bo"} {
			if !yield(model.Pet{Name: n}, nil) {
				return
			}
		}
	}
	names = as[api.PostCodedParts200JSONResponse](t)(c.PostCodedParts(ctx, &api.PostCodedPartsRequest{ContentType: "multipart/related", RelatedBody: iter.Seq2[model.Pet, error](stream)}))
	if !slices.Equal(names.Body, []string{"Ann", "Bo"}) {
		t.Errorf("stream = %q", names.Body)
	}
}
