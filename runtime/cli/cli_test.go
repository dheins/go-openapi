package cli

import (
	"bytes"
	"errors"
	"iter"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/client"
)

type color string

type filter struct {
	Age  int    `json:"age"`
	Name string `json:"name"`
}

type testRequest struct {
	ID     int64
	Limit  oapi.Optional[int32]
	Tags   oapi.Optional[[]string]
	Color  color
	Since  oapi.Optional[oapi.Date]
	Filter oapi.Optional[filter]
	Ratio  *float64
	Body   oapi.Optional[upload]
	Text   string
	Raw    []byte
	Type   string
}

type upload struct {
	Name  string    `json:"name"`
	Photo oapi.File `json:"photo"`
}

func TestRequest(t *testing.T) {
	cmd := &Command[struct{}]{
		Name:    "test",
		Request: func() any { return &testRequest{} },
		Params: []Param{
			{Flag: "id", Field: "ID", Required: true},
			{Flag: "limit", Field: "Limit"},
			{Flag: "tags", Field: "Tags", Kind: List},
			{Flag: "color", Field: "Color"},
			{Flag: "since", Field: "Since"},
			{Flag: "filter", Field: "Filter", Kind: JSON},
			{Flag: "ratio", Field: "Ratio"},
		},
		Media: []Media{
			{MediaType: "multipart/form-data", Kind: "multipart", Field: "Body", Files: []File{{Flag: "photo", Property: "photo", Field: "Photo"}}},
			{MediaType: "text/plain", Kind: "text", Field: "Text"},
			{MediaType: "application/octet-stream", Kind: "binary", Field: "Raw"},
		},
		ContentType: "Type",
	}
	app := App[struct{}]{Name: "pets"}
	photo := filepath.Join(t.TempDir(), "rex.png")
	if err := os.WriteFile(photo, []byte("PNG"), 0o644); err != nil {
		t.Fatal(err)
	}
	parse := func(args ...string) (*testRequest, error) {
		fs, g := app.flags(cmd, &bytes.Buffer{})
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		req, err := request(g, cmd)
		if err != nil {
			return nil, err
		}
		return req.(*testRequest), nil
	}
	req, err := parse("-id", "7", "-limit", "3", "-tags", "a,b", "-tags", "c", "-color", "red", "-since", "2024-02-29",
		"-filter", `{"age":3,"name":"Rex"}`, "-ratio", "0.5", "-body", `{"name":"Rex"}`, "-photo", "@"+photo)
	if err != nil {
		t.Fatal(err)
	}
	want := testRequest{ID: 7, Limit: oapi.Some(int32(3)), Tags: oapi.Some([]string{"a", "b", "c"}), Color: "red",
		Filter: oapi.Some(filter{Age: 3, Name: "Rex"}), Type: "multipart/form-data"}
	if req.ID != want.ID || req.Limit != want.Limit || !reflect.DeepEqual(req.Tags, want.Tags) || req.Color != want.Color ||
		req.Filter != want.Filter || req.Ratio == nil || *req.Ratio != 0.5 || req.Since.Value.String() != "2024-02-29" || req.Type != want.Type {
		t.Errorf("request = %+v", req)
	}
	if b := req.Body.Value; !req.Body.Set || b.Name != "Rex" || b.Photo.Name != "rex.png" || string(b.Photo.Data) != "PNG" || b.Photo.ContentType != "image/png" {
		t.Errorf("body = %+v", req.Body)
	}
	if req, err := parse("-id", "1", "-content-type", "text/plain", "-body", "hi"); err != nil || req.Text != "hi" || req.Type != "text/plain" {
		t.Errorf("text: %+v %v", req, err)
	}
	if req, err := parse("-id", "1", "-content-type", "application/octet-stream", "-body", "@"+photo); err != nil || string(req.Raw) != "PNG" {
		t.Errorf("bytes: %+v %v", req, err)
	}
	for _, tt := range []struct {
		args []string
		want string
	}{
		{nil, "-id is required"},
		{[]string{"-id", "x"}, `-id: "x" is not an integer`},
		{[]string{"-id", "1", "-since", "yesterday"}, "-since:"},
		{[]string{"-id", "1", "-filter", "{"}, "-filter:"},
		{[]string{"-id", "1", "-content-type", "image/gif", "-body", "x"}, `-content-type "image/gif" is not one of the operation's`},
		{[]string{"-id", "1", "-photo", photo}, "give a file as @path"},
	} {
		if _, err := parse(tt.args...); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%q: %v, want %q", tt.args, err, tt.want)
		}
	}
}

type streamed struct {
	Items iter.Seq2[map[string]int, error]
}

func TestPrint(t *testing.T) {
	var out bytes.Buffer
	resp := streamed{Items: func(yield func(map[string]int, error) bool) {
		for i := range 3 {
			if !yield(map[string]int{"n": i}, nil) {
				return
			}
		}
		yield(nil, errors.New("cut off"))
	}}
	if err := print(resp, &captured{}, &out); err == nil || err.Error() != "cut off" || out.String() != "{\"n\":0}\n{\"n\":1}\n{\"n\":2}\n" {
		t.Errorf("stream: %q %v", out.String(), err)
	}
}

// TestServer checks that the document's server keeps operations' own
// servers in use, and that -server or the environment replaces both.
func TestServer(t *testing.T) {
	errStop := errors.New("stop")
	for _, tc := range []struct {
		name, env string
		args      []string
		base      string
		opServers bool
	}{
		{"default", "", nil, "https://api.example.com/v1", true},
		{"flag", "", []string{"-server", "http://localhost:8080/v1"}, "http://localhost:8080/v1", false},
		{"environment", "http://env.example.com/v1", nil, "http://env.example.com/v1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PETS_SERVER", tc.env)
			var cfg *client.Config
			app := App[struct{}]{
				Name:     "pets",
				Server:   "https://api.example.com/v1",
				Commands: []Command[struct{}]{{Name: "ping", Request: func() any { return &struct{}{} }}},
				New: func(_ map[string]Credential, opts ...client.Option) (struct{}, error) {
					var err error
					if cfg, err = client.NewConfig(opts...); err != nil {
						return struct{}{}, err
					}
					return struct{}{}, errStop
				},
			}
			var stderr bytes.Buffer
			if code := Run(app, append([]string{"ping"}, tc.args...), &bytes.Buffer{}, &stderr); code != ExitFailure || !strings.Contains(stderr.String(), "stop") {
				t.Fatalf("exit code %d: %s", code, &stderr)
			}
			if cfg.BaseURL.String() != tc.base || cfg.OperationServers != tc.opServers {
				t.Errorf("base URL %s, operation servers %v", cfg.BaseURL, cfg.OperationServers)
			}
		})
	}
}
