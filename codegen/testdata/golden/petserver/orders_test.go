package petserver

// End-to-end tests of arrays of objects and recursive types in forms,
// deepObject parameters and multipart bodies. They are not generated;
// codegen's TestGeneratedCode runs them.

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/api"
	petclient "github.com/dheins/go-openapi/codegen/testdata/golden/petserver/client"
	"github.com/dheins/go-openapi/codegen/testdata/golden/petserver/model"
	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/client"
)

func sampleOrder() model.Order {
	return model.Order{
		ID: "o-1",
		Lines: oapi.Some([]model.OrderLine{
			{Sku: "apple", Qty: 3, Tags: oapi.Some([]string{"red", "sweet"})},
			{Sku: "pear", Qty: 1},
		}),
		Notes: oapi.Some(map[string]string{"gift": "yes"}),
	}
}

func sampleTree() model.Tree {
	return model.Tree{Name: "root", Children: oapi.Some([]model.Tree{
		{Name: "a", Children: oapi.Some([]model.Tree{{Name: "a1"}, {Name: "a2"}})},
		{Name: "b"},
	})}
}

func TestNestedFormsRaw(t *testing.T) {
	s, h := newTestServer()

	// A form with indexed brackets, as qs and Stripe write it.
	form := "id=o-1&lines[0][sku]=apple&lines[0][qty]=3&lines[0][tags][0]=red&lines[0][tags][1]=sweet" +
		"&lines[1][sku]=pear&lines[1][qty]=1&notes[gift]=yes"
	if r := do(t, h, "POST", "/orders", "application/x-www-form-urlencoded", form); r.status != 201 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if got := s.seen["CreateOrder"].(*api.CreateOrderRequest).Body; !reflect.DeepEqual(got, sampleOrder()) {
		t.Errorf("order:\n got %+v\nwant %+v", got, sampleOrder())
	}
	// Each item is decoded and validated, at its JSON pointer.
	r := do(t, h, "POST", "/orders", "application/x-www-form-urlencoded", "id=o&lines[0][sku]=a&lines[0][qty]=1&lines[1][qty]=2")
	if got := problemErrors(t, r); strings.Join(got, "; ") != "body /lines/1/sku: required property is missing" {
		t.Errorf("missing sku: %d %q", r.status, got)
	}
	r = do(t, h, "POST", "/orders", "application/x-www-form-urlencoded", "id=o&lines[0][sku]=a&lines[0][qty]=1&lines[1][sku]=b&lines[1][qty]=0")
	if got := problemErrors(t, r); len(got) != 1 || !strings.HasPrefix(got[0], "body /lines/1/qty: ") {
		t.Errorf("qty 0: %d %q", r.status, got)
	}
	r = do(t, h, "POST", "/orders", "application/x-www-form-urlencoded", "id=o&lines[0]=a&lines[0][sku]=b")
	if got := problemErrors(t, r); len(got) != 1 || !strings.Contains(got[0], "both a value and nested properties") {
		t.Errorf("mixed lines: %d %q", r.status, got)
	}

	// The same as a deepObject parameter.
	q := "filter[id]=o-1&filter[lines][0][sku]=apple&filter[lines][0][qty]=3&filter[lines][0][tags][0]=red" +
		"&filter[lines][0][tags][1]=sweet&filter[lines][1][sku]=pear&filter[lines][1][qty]=1&filter[notes][gift]=yes"
	if r := do(t, h, "GET", "/orders?"+q, "", ""); r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if got := s.seen["FindOrders"].(*api.FindOrdersRequest).Filter; !got.Set || !reflect.DeepEqual(got.Value, sampleOrder()) {
		t.Errorf("filter:\n got %+v\nwant %+v", got.Value, sampleOrder())
	}

	// A recursive type, to any depth.
	tree := "name=root&children[0][name]=a&children[0][children][0][name]=a1&children[0][children][1][name]=a2&children[1][name]=b"
	if r := do(t, h, "POST", "/trees", "application/x-www-form-urlencoded", tree); r.status != 200 ||
		r.body != `{"name":"root","children":[{"name":"a","children":[{"name":"a1"},{"name":"a2"}]},{"name":"b"}]}` {
		t.Errorf("tree: %d %s", r.status, r.body)
	}
	r = do(t, h, "POST", "/trees", "application/x-www-form-urlencoded", "name=root&children[0][children][0][name]=x")
	if got := problemErrors(t, r); strings.Join(got, "; ") != "body /children/0/name: required property is missing" {
		t.Errorf("missing name: %d %q", r.status, got)
	}
	r = do(t, h, "POST", "/trees", "application/x-www-form-urlencoded", "name=root&children[0][name]=a&children[0][children][0][name]=")
	if got := problemErrors(t, r); len(got) != 1 || !strings.HasPrefix(got[0], "body /children/0/children/0/name: ") {
		t.Errorf("empty name: %d %q", r.status, got)
	}

	// Multipart: a JSON part per line, next to a file.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, line := range []string{`{"sku":"apple","qty":3,"tags":["red","sweet"]}`, `{"sku":"pear","qty":1}`} {
		pw, _ := mw.CreatePart(map[string][]string{"Content-Disposition": {`form-data; name="lines"`}, "Content-Type": {"application/json"}})
		pw.Write([]byte(line))
	}
	fw, _ := mw.CreateFormFile("receipt", "r.txt")
	fw.Write([]byte("paid"))
	mw.Close()
	if r := do(t, h, "PUT", "/orders/o-1/lines", mw.FormDataContentType(), buf.String()); r.status != 204 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	got := s.seen["PutLines"].(*api.PutLinesRequest)
	if !reflect.DeepEqual(got.Body.Lines, sampleOrder().Lines.Value) || string(got.Body.Receipt.Value.Data) != "paid" {
		t.Errorf("lines = %+v", got.Body)
	}
}

func TestNestedFormsClient(t *testing.T) {
	ctx := context.Background()
	s, c := newTestClient(t, petclient.Credentials{})
	order := sampleOrder()

	r := as[api.CreateOrder201JSONResponse](t)(c.CreateOrder(ctx, &api.CreateOrderRequest{Body: order}))
	if !reflect.DeepEqual(r.Body, order) {
		t.Errorf("created:\n got %+v\nwant %+v", r.Body, order)
	}
	found := as[api.FindOrders200JSONResponse](t)(c.FindOrders(ctx, &api.FindOrdersRequest{Filter: oapi.Some(order)}))
	if !reflect.DeepEqual(found.Body, order) {
		t.Errorf("found:\n got %+v\nwant %+v", found.Body, order)
	}
	tree := as[api.PlantTree200JSONResponse](t)(c.PlantTree(ctx, &api.PlantTreeRequest{Body: sampleTree()}))
	if !reflect.DeepEqual(tree.Body, sampleTree()) {
		t.Errorf("tree:\n got %+v\nwant %+v", tree.Body, sampleTree())
	}
	body := model.PutLinesRequestBody{Lines: order.Lines.Value, Receipt: oapi.Some(oapi.File{Name: "r.txt", Data: []byte("paid")})}
	as[api.PutLines204Response](t)(c.PutLines(ctx, &api.PutLinesRequest{ID: "o-1", Body: body}))
	if got := s.seen["PutLines"].(*api.PutLinesRequest); got.ID != "o-1" || !reflect.DeepEqual(got.Body.Lines, body.Lines) || got.Body.Receipt.Value.Name != "r.txt" {
		t.Errorf("lines = %+v", got)
	}

	// Each item is checked before sending.
	var invalid *client.InvalidRequest
	bad := model.Order{ID: "o", Lines: oapi.Some([]model.OrderLine{{Sku: "a", Qty: 0}})}
	if _, err := c.CreateOrder(ctx, &api.CreateOrderRequest{Body: bad}); !errors.As(err, &invalid) || invalid.Problem.Errors[0].Pointer != "/lines/0/qty" {
		t.Errorf("invalid order: %v", err)
	}
	// The client writes indexed brackets.
	hreq, err := api.NewFindOrdersRequest(ctx, mustConfig(t), &api.FindOrdersRequest{Filter: oapi.Some(order)})
	if err != nil {
		t.Fatal(err)
	}
	q, _ := url.QueryUnescape(hreq.URL.RawQuery)
	if !strings.Contains(q, "filter[lines][1][sku]=pear") || !strings.Contains(q, "filter[lines][0][tags]=red") {
		t.Errorf("query = %s", q)
	}
}

func mustConfig(t *testing.T) *client.Config {
	t.Helper()
	cfg, err := client.NewConfig(client.WithBaseURL("https://api.example.com/v1"))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
