package petserver

// Tests of go-swagger's model extensions x-order and x-go-json-string.
// They are not generated; codegen's TestGeneratedCode runs them.

import (
	"encoding/json"
	"testing"

	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/model"
	"github.com/dheins/go-openapi/oapi"
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
