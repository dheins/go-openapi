package petserver

// Tests of go-swagger's model extensions x-order and x-go-json-string.
// They are not generated; codegen's TestGeneratedCode runs them.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/api"
	petclient "github.com/dheins/go-openapi/codegen/testdata/golden/petserver/client"
	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/model"
	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/server"
)

func TestOrder(t *testing.T) {
	v := model.Ranked{Note: oapi.Some("n"), ID: oapi.Some[int64](1), Name: oapi.Some("a"), Extra: oapi.Some("e"), Rank: oapi.Some("r")}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	// Those with x-order first, by it; then the others as declared.
	if want := `{"name":"a","id":1,"note":"n","extra":"e","rank":"r"}`; string(data) != want {
		t.Errorf("Marshal = %s, want %s", data, want)
	}
}

// PutCounter echoes the counter, as XML when it was sent as XML.
func (s *store) PutCounter(_ context.Context, req *api.PutCounterRequest) (api.PutCounterResponse, error) {
	if req.ContentType == "application/xml" {
		return api.PutCounter200XMLResponse{Body: req.Body}, nil
	}
	return api.PutCounter200JSONResponse{Body: req.Body}, nil
}

func TestJSONString(t *testing.T) {
	c := model.Counter{Count: 42, Ratio: oapi.Some(0.5), Enabled: oapi.Some(false), Limit: oapi.Some(oapi.Nullable[int64]{}), Label: oapi.Some("x")}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"count":"42","ratio":"0.5","enabled":"false","limit":null,"label":"x"}`; string(data) != want {
		t.Errorf("Marshal = %s, want %s", data, want)
	}
	var back model.Counter
	if err := json.Unmarshal(data, &back); err != nil || back != c {
		t.Errorf("Unmarshal = %+v, %v", back, err)
	}
	if err := json.Unmarshal([]byte(`{"count":"7","limit":"3"}`), &back); err != nil || back.Count != 7 || back.Limit.Value.Value != 3 {
		t.Errorf("Unmarshal = %+v, %v", back, err)
	}
	// A response gets the default as a string too.
	c.Enabled = oapi.Optional[bool]{}
	if data, err = c.EncodeJSON(oapi.Response); err != nil || !strings.Contains(string(data), `"enabled":"true"`) {
		t.Errorf("EncodeJSON = %s, %v", data, err)
	}

	for body, want := range map[string]string{
		`{"count":42}`:                "/count: expected a string holding the value, got number",
		`{"count":"x"}`:               `/count: expected a string holding a number or boolean, got "x"`,
		`{"count":" 4"}`:              `/count: expected a string holding a number or boolean, got " 4"`,
		`{"count":"1","enabled":"1"}`: "/enabled: expected boolean",
		`{"count":"1.5"}`:             "/count",
		`{"count":null}`:              "/count: must not be null",
	} {
		var v model.Counter
		err := json.Unmarshal([]byte(body), &v)
		var verrs oapi.ValidationErrors
		if !errors.As(err, &verrs) || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", body, err, want)
		}
	}
}

func TestJSONStringOverHTTP(t *testing.T) {
	_, c := newTestClient(t, petclient.Credentials{})
	want := model.Counter{Count: 3, Ratio: oapi.Some(1.25), Enabled: oapi.Some(true), Limit: oapi.Some(oapi.NewNullable[int64](9)), Label: oapi.Some("a")}
	for _, mediaType := range []string{"application/json", "application/x-www-form-urlencoded", "application/xml"} {
		resp, err := c.PutCounter(context.Background(), &api.PutCounterRequest{Body: want, ContentType: mediaType})
		if err != nil {
			t.Fatalf("%s: %v", mediaType, err)
		}
		var got model.Counter
		switch r := resp.(type) {
		case api.PutCounter200JSONResponse:
			got = r.Body
		case api.PutCounter200XMLResponse:
			got = r.Body
		}
		if got != want {
			t.Errorf("%s: %+v, want %+v", mediaType, got, want)
		}
	}

	// On the wire, the values are strings, and bare ones are refused.
	srv := httptest.NewServer(newServer(newStore(), server.WithLogger(slog.New(slog.DiscardHandler))))
	t.Cleanup(srv.Close)
	for body, status := range map[string]int{`{"count":"5","ratio":"2"}`: 200, `{"count":5}`: 400} {
		req, err := http.NewRequest(http.MethodPut, srv.URL+"/v1/counters", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != status || status == 200 && string(data) != `{"count":"5","ratio":"2","enabled":"true"}` {
			t.Errorf("%s: %d %s", body, resp.StatusCode, data)
		}
	}
	// A form carries the text of the values.
	req, err := http.NewRequest(http.MethodPut, srv.URL+"/v1/counters", strings.NewReader("count=6&enabled=false"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != 200 || string(data) != `{"count":"6","enabled":"false"}` {
		t.Errorf("form: %d %s %v", resp.StatusCode, data, err)
	}
}
