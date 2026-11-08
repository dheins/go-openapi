package petserver

// End-to-end tests of XML maps of arrays, unions of scalars and objects,
// and typed additional properties. Not generated; codegen's
// TestGeneratedCode runs them.

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/api"
	petclient "github.com/dheins/go-openapi/codegen/testdata/golden/petserver/client"
	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/model"
	"github.com/dheins/go-openapi/oapi"
)

// labelsDoc is what the server writes for sampleLabels; the TypeScript
// client's tests (xml.test.ts) write and read the same.
const labelsDoc = `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
	`<labels><name>a</name><tags><color>red</color><color>blue</color><size>L</size></tags><value>42</value><count>3</count></labels>`

func sampleLabels() model.Labels {
	return model.Labels{
		Name:                 "a",
		Tags:                 oapi.Some(map[string][]string{"color": {"red", "blue"}, "size": {"L"}}),
		Value:                oapi.Some(model.NewAmount(model.AmountInteger(42))),
		AdditionalProperties: map[string]int64{"count": 3},
	}
}

func TestXMLGaps(t *testing.T) {
	_, h := newTestServer()
	r := do(t, h, "PUT", "/labels", "application/xml", labelsDoc)
	if r.status != 200 || r.body != labelsDoc {
		t.Errorf("echo: %d\n got %s\nwant %s", r.status, r.body, labelsDoc)
	}
	// A union's object variant, from attributes and child elements, and
	// its string variant from text that is no integer.
	for doc, want := range map[string]string{
		`<labels><name>b</name><value unit="kg"><amount>1.5</amount></value></labels>`: `<labels><name>b</name><value unit="kg"><amount>1.5</amount></value></labels>`,
		`<labels><name>b</name><value>about 3</value></labels>`:                        `<labels><name>b</name><value>about 3</value></labels>`,
	} {
		if r := do(t, h, "PUT", "/labels", "application/xml", doc); r.status != 200 || !strings.HasSuffix(r.body, want) {
			t.Errorf("%s: %d %s", doc, r.status, r.body)
		}
	}
	// Additional properties are checked against their schema, where they
	// are; a value of the wrong type stops decoding before constraints are
	// checked.
	for doc, where := range map[string]string{
		`<labels><name>b</name><count>-1</count></labels>`: "body /count: ",
		`<labels><name>b</name><other>x</other></labels>`:  "body /other: ",
	} {
		r = do(t, h, "PUT", "/labels", "application/xml", doc)
		if got := problemErrors(t, r); r.status != 400 || len(got) != 1 || !strings.HasPrefix(got[0], where) {
			t.Errorf("%s: %d %q", doc, r.status, got)
		}
	}

	_, c := newTestClient(t, petclient.Credentials{})
	ctx := context.Background()
	for _, labels := range []model.Labels{
		sampleLabels(),
		{Name: "s", Value: oapi.Some(model.NewAmount(model.AmountString("x")))},
		{Name: "o", Value: oapi.Some(model.NewAmount(model.AmountVariant3{Amount: 2.5, Unit: oapi.Some("kg")}))},
	} {
		resp := as[api.PutLabels200XMLResponse](t)(c.PutLabels(ctx, &api.PutLabelsRequest{Body: labels}))
		if !reflect.DeepEqual(resp.Body, labels) {
			t.Errorf("round trip:\n got %+v\nwant %+v", resp.Body, labels)
		}
	}
}
