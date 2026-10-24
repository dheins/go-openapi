package client

import (
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/runtime/server"
)

// item, order and node are bodies and parameters with arrays of objects,
// maps of objects, and a recursive type.
type item struct {
	Name string   `json:"name"`
	Tags []string `json:"tags,omitempty"`
	Qty  int64    `json:"qty"`
}

type node struct {
	Name     string `json:"name"`
	Children []node `json:"children,omitempty"`
}

type order struct {
	ID    string          `json:"id"`
	Items []item          `json:"items"`
	Meta  map[string]item `json:"meta,omitempty"`
	Tree  *node           `json:"tree,omitempty"`
}

var (
	itemShape = server.Property{Type: "object", Properties: map[string]server.Property{
		"name": {Type: "string"}, "tags": {Type: "string", Array: true}, "qty": {Type: "integer"},
	}}
	// nodeShape refers to itself, as generated code builds it.
	nodeShape = func() *server.Property {
		p := &server.Property{}
		*p = server.Property{Type: "object", Properties: map[string]server.Property{
			"name": {Type: "string"}, "children": {Array: true, Ref: p},
		}}
		return p
	}()
	orderShape = server.Property{Type: "object", Properties: map[string]server.Property{
		"id":    {Type: "string"},
		"items": {Type: "object", Array: true, Properties: itemShape.Properties},
		"meta":  {Type: "object", Additional: &itemShape},
		"tree":  {Ref: nodeShape},
	}}
)

func sampleOrder() order {
	return order{
		ID:    "o-1",
		Items: []item{{Name: "apple", Tags: []string{"red", "sweet"}, Qty: 3}, {Name: "pear", Qty: 1}},
		Meta:  map[string]item{"gift": {Name: "bow", Qty: 1}},
		Tree: &node{Name: "root", Children: []node{
			{Name: "a", Children: []node{{Name: "a1"}, {Name: "a2"}}},
			{Name: "b"},
		}},
	}
}

func TestNestedFieldsRoundTrip(t *testing.T) {
	in := sampleOrder()

	// A form body, with indexed brackets for arrays of objects.
	r := NewRequest("POST")
	r.FormBody("application/x-www-form-urlencoded", in)
	form := string(r.body)
	for _, want := range []string{"items%5B0%5D%5Bname%5D=apple", "items%5B0%5D%5Btags%5D=red", "items%5B1%5D%5Bqty%5D=1",
		"meta%5Bgift%5D%5Bname%5D=bow", "tree%5Bchildren%5D%5B0%5D%5Bchildren%5D%5B1%5D%5Bname%5D=a2"} {
		if !strings.Contains(form, want) {
			t.Errorf("form %s lacks %s", form, want)
		}
	}
	fields, err := server.FormFields(r.body)
	if err != nil {
		t.Fatal(err)
	}
	out, err := server.DecodeFields[order](fields, orderShape)
	if err != nil || !reflect.DeepEqual(out, in) {
		t.Errorf("form:\n got %+v %v\nwant %+v", out, err, in)
	}

	// A deepObject parameter.
	p := server.Param{Name: "o", Style: "deepObject", Explode: true, Shape: server.Object, Properties: []string{"id", "items", "meta", "tree"}}
	r = NewRequest("GET")
	r.QueryParam(p, in)
	req := build(t, r)
	vals, ok, err := p.QueryValues(req.URL.Query())
	if err != nil || !ok {
		t.Fatalf("deepObject: %v %v", ok, err)
	}
	if out, err := server.DecodeFields[order](vals.Fields, orderShape); err != nil || !reflect.DeepEqual(out, in) {
		t.Errorf("deepObject:\n got %+v %v\nwant %+v", out, err, in)
	}

	// Multipart: an array of objects is a JSON part per item, an object one
	// JSON part.
	r = NewRequest("POST")
	r.MultipartBody(in, orderShape)
	body := string(r.body)
	if strings.Count(body, `name="items"`) != 2 || strings.Count(body, "Content-Type: application/json") != 4 {
		t.Errorf("multipart parts:\n%s", body)
	}
	got, err := server.DecodeMultipart[order](r.body, r.contentType, orderShape)
	if err != nil || !reflect.DeepEqual(got, in) {
		t.Errorf("multipart:\n got %+v %v\nwant %+v", got, err, in)
	}
}

func TestNestedFieldsErrors(t *testing.T) {
	tests := []struct {
		query, want string
	}{
		// An index next to a name, or a value next to an object.
		{"items[0][name]=a&items[x][name]=b", "/items/x: expected an array item with an index"},
		{"items[0]=a&items[0][name]=b", "/items/0: both a value and nested properties"},
		{"items[01][name]=a", "/items/01: expected an array item with an index"},
		// A value where the recursive type has an object.
		{"tree[children][0]=leaf", "expected object, got string"},
	}
	for _, tc := range tests {
		q, err := url.ParseQuery(tc.query)
		if err != nil {
			t.Fatal(err)
		}
		_, err = server.DecodeFields[order](q, orderShape)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %s", tc.query, err, tc.want)
		}
	}

	// Indices need not start at 0 or be contiguous; items keep their order.
	q, _ := url.ParseQuery("id=o&items[5][name]=b&items[5][qty]=2&items[2][name]=a&items[2][qty]=1")
	out, err := server.DecodeFields[order](q, orderShape)
	if err != nil || len(out.Items) != 2 || out.Items[0].Name != "a" || out.Items[1].Qty != 2 {
		t.Errorf("sparse indices: %+v %v", out, err)
	}
}
