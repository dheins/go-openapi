package server

import (
	"strings"
	"testing"
)

// petXML is the classic petstore's Pet: a wrapped array of strings, a
// wrapped array of objects, a nested object, plus an attribute, a
// namespace and an unwrapped array.
var petXML = &XMLNode{Name: "pet", Namespace: "urn:pets", Prefix: "p", Type: "object", Properties: []XMLProperty{
	{"id", &XMLNode{Name: "id", Kind: "attribute", Type: "integer"}},
	{"name", &XMLNode{Name: "name", Namespace: "urn:pets", Prefix: "p", Type: "string"}},
	{"category", &XMLNode{Name: "category", Type: "object", Properties: []XMLProperty{
		{"id", &XMLNode{Name: "id", Type: "integer"}},
		{"label", &XMLNode{Kind: "text", Type: "string"}},
	}}},
	{"photoUrls", &XMLNode{Name: "photoUrls", Type: "array", Wrapped: true, Items: &XMLNode{Name: "photoUrl", Type: "string"}}},
	{"tags", &XMLNode{Name: "tags", Type: "array", Items: &XMLNode{Name: "tag", Type: "object", Properties: []XMLProperty{
		{"name", &XMLNode{Name: "name", Type: "string"}},
	}}}},
	{"vip", &XMLNode{Name: "vip", Type: "boolean"}},
	{"note", &XMLNode{Name: "note", Type: "string", Kind: ""}},
	{"weight", &XMLNode{Name: "weight", Type: "number"}},
}}

func TestXMLToJSON(t *testing.T) {
	doc := `<?xml version="1.0"?>
<!-- a pet -->
<p:pet xmlns:p="urn:pets" id="7" color="brown">
  <p:name>Rex &amp; co</p:name>
  <category><id> 3 </id>Dogs</category>
  <photoUrls><photoUrl>a.png</photoUrl><photoUrl>b.png</photoUrl></photoUrls>
  <tag><name>good</name></tag>
  <tag><name>boy</name></tag>
  <vip>true</vip>
  <note xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:nil="true"/>
  <weight>x</weight>
  <extra a="1"><b>2</b><b>3</b></extra>
</p:pet>`
	got, err := XMLToJSON([]byte(doc), petXML)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"category":{"id":3,"label":"Dogs"},"color":"brown","extra":{"a":"1","b":["2","3"]},"id":7,"name":"Rex \u0026 co",` +
		`"note":null,"photoUrls":["a.png","b.png"],"tags":[{"name":"good"},{"name":"boy"}],"vip":true,"weight":"x"}`
	if string(got) != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestXMLToJSONErrors(t *testing.T) {
	tests := []struct{ doc, want string }{
		{``, "expected an XML document, got nothing"},
		{`<pet/>`, "expected element <pet> in namespace urn:pets, got <pet>"},
		{`<pet xmlns="urn:dogs"/>`, "expected element <pet> in namespace urn:pets, got <pet> in namespace urn:dogs"},
		{`<p:dog xmlns:p="urn:pets"/>`, "expected element <pet> in namespace urn:pets, got <dog> in namespace urn:pets"},
		{`<p:pet xmlns:p="urn:pets"><p:name>a</p:name>`, "invalid XML: XML syntax error on line 1: unexpected EOF"},
		{`<p:pet xmlns:p="urn:pets"/><more/>`, "invalid XML: data after the root element"},
		{`<p:pet xmlns:p="urn:pets"><p:name><b/></p:name></p:pet>`, "/name: expected text, got element <b>"},
		{`<p:pet xmlns:p="urn:pets"><photoUrls><x/></photoUrls></p:pet>`, "/photoUrls/0: expected element <photoUrl>, got <x>"},
		// The default namespace applies to unprefixed elements.
		{`<pet xmlns="urn:pets"><name><b/></name></pet>`, "/name: expected text, got element <b>"},
		{`<p:pet xmlns:p="urn:pets">loose</p:pet>`, "unexpected text in element <pet>"},
		{`<!DOCTYPE pet [<!ENTITY x "y">]><p:pet xmlns:p="urn:pets"><p:name>&x;</p:name></p:pet>`, "invalid XML: XML syntax error on line 1: invalid character entity &x;"},
	}
	for _, tt := range tests {
		if _, err := XMLToJSON([]byte(tt.doc), petXML); err == nil || err.Error() != tt.want {
			t.Errorf("%s:\ngot  %v\nwant %s", tt.doc, err, tt.want)
		}
	}
	deep := strings.Repeat("<x>", maxXMLDepth+2) + strings.Repeat("</x>", maxXMLDepth+2)
	if _, err := XMLToJSON([]byte(`<p:pet xmlns:p="urn:pets">`+deep+`</p:pet>`), petXML); err == nil || !strings.Contains(err.Error(), "nested too deeply") {
		t.Errorf("deep nesting: %v", err)
	}
}

func TestJSONToXML(t *testing.T) {
	data := `{"id":7,"name":"Rex <3","category":{"id":3,"label":"Dogs"},"photoUrls":["a.png"],"tags":[{"name":"good"},{"name":"boy"}],"vip":false,"note":null,"color":"brown"}`
	got, err := JSONToXML([]byte(data), petXML)
	if err != nil {
		t.Fatal(err)
	}
	want := `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<p:pet xmlns:p="urn:pets" id="7"><p:name>Rex &lt;3</p:name><category><id>3</id>Dogs</category>` +
		`<photoUrls><photoUrl>a.png</photoUrl></photoUrls><tag><name>good</name></tag><tag><name>boy</name></tag>` +
		`<vip>false</vip><note xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:nil="true"/><color>brown</color></p:pet>`
	if string(got) != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	// What is written reads back as the same value.
	back, err := XMLToJSON(got, petXML)
	if err != nil {
		t.Fatal(err)
	}
	wantBack := `{"category":{"id":3,"label":"Dogs"},"color":"brown","id":7,"name":"Rex \u003c3","note":null,"photoUrls":["a.png"],"tags":[{"name":"good"},{"name":"boy"}],"vip":false}`
	if string(back) != wantBack {
		t.Errorf("read back %s\nwant      %s", back, wantBack)
	}

	cdata := &XMLNode{Name: "doc", Type: "object", Properties: []XMLProperty{{"body", &XMLNode{Kind: "cdata", Type: "string"}}}}
	if got, err := JSONToXML([]byte(`{"body":"a]]>b<"}`), cdata); err != nil || !strings.HasSuffix(string(got), `<doc><![CDATA[a]]]]><![CDATA[>b<]]></doc>`) {
		t.Errorf("cdata: %s %v", got, err)
	}
	list := &XMLNode{Name: "pets", Type: "array", Wrapped: true, Items: &XMLNode{Name: "pet", Type: "string"}}
	if got, err := JSONToXML([]byte(`["a","b"]`), list); err != nil || !strings.HasSuffix(string(got), `<pets><pet>a</pet><pet>b</pet></pets>`) {
		t.Errorf("root array: %s %v", got, err)
	}
	if back, err := XMLToJSON([]byte(`<pets><pet>a</pet><pet>b</pet></pets>`), list); err != nil || string(back) != `["a","b"]` {
		t.Errorf("root array read back: %s %v", back, err)
	}
}

// treeXML is a recursive tree, through a shape reference: a map of
// integers by key, and an inline object whose attribute and element are
// the tree's own.
var treeXML = &XMLNode{Name: "tree", Ref: "Tree", Shapes: map[string]*XMLNode{
	"Tree": {Type: "object", Properties: []XMLProperty{
		{"label", &XMLNode{Name: "label", Kind: "attribute", Type: "string"}},
		{"children", &XMLNode{Name: "children", Type: "array", Items: &XMLNode{Name: "child", Ref: "Tree"}}},
		{"counts", &XMLNode{Name: "counts", Type: "map", Values: &XMLNode{Type: "integer"}}},
		{"info", &XMLNode{Type: "object", Inline: true, Properties: []XMLProperty{
			{"owner", &XMLNode{Name: "owner", Kind: "attribute", Type: "string"}},
			{"size", &XMLNode{Name: "size", Type: "integer"}},
		}}},
	}},
}}

func TestXMLShapes(t *testing.T) {
	doc := `<tree label="root" owner="ann"><size>2</size>` +
		`<child label="a"><counts><x>1</x><y>2</y></counts></child>` +
		`<child label="b"><child label="c"/></child></tree>`
	got, err := XMLToJSON([]byte(doc), treeXML)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"children":[{"counts":{"x":1,"y":2},"label":"a"},{"children":[{"label":"c"}],"label":"b"}],"info":{"owner":"ann","size":2},"label":"root"}`
	if string(got) != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	back, err := JSONToXML(got, treeXML)
	if err != nil {
		t.Fatal(err)
	}
	wantXML := `<tree label="root" owner="ann"><child label="a"><counts><x>1</x><y>2</y></counts></child>` +
		`<child label="b"><child label="c"></child></child><size>2</size></tree>`
	if !strings.HasSuffix(string(back), wantXML) {
		t.Errorf("written %s\nwant     %s", back, wantXML)
	}
	if _, err := JSONToXML([]byte(`{"counts":{"a b":1}}`), treeXML); err == nil || err.Error() != `key "a b" cannot be written as an XML element` {
		t.Errorf("invalid key: %v", err)
	}
	if _, err := XMLToJSON([]byte(`<tree><counts><x>one</x>t</counts></tree>`), treeXML); err == nil {
		t.Error("text in a map: no error")
	}
	// Undeclared attributes and elements named as a property are ignored:
	// they could not be written back.
	if got, err := XMLToJSON([]byte(`<tree info="x" children="y"><info>z</info><size>1</size></tree>`), treeXML); err != nil || string(got) != `{"info":{"size":1}}` {
		t.Errorf("collisions: %s %v", got, err)
	}
	// A map of objects, and the root itself a map.
	m := &XMLNode{Name: "m", Type: "map", Values: &XMLNode{Type: "object", Properties: []XMLProperty{{"n", &XMLNode{Name: "n", Kind: "attribute", Type: "number"}}}}}
	if got, err := XMLToJSON([]byte(`<m><a n="1.5"/><b/></m>`), m); err != nil || string(got) != `{"a":{"n":1.5},"b":{}}` {
		t.Errorf("map of objects: %s %v", got, err)
	}
}

// labelsXML has a map of arrays, a union of a string, an integer and an
// object, and typed additional properties of its own.
var labelsXML = &XMLNode{Name: "labels", Type: "object", Properties: []XMLProperty{
	{"name", &XMLNode{Name: "name", Type: "string"}},
	{"tags", &XMLNode{Name: "tags", Type: "map", Values: &XMLNode{Type: "array", Items: &XMLNode{Type: "string"}}}},
	{"value", &XMLNode{Name: "value", Type: "union", Scalars: []string{"integer", "string"}, Properties: []XMLProperty{
		{"unit", &XMLNode{Name: "unit", Kind: "attribute", Type: "string"}},
		{"amount", &XMLNode{Name: "amount", Type: "number"}},
	}}},
}, Additional: &XMLNode{Type: "integer"}}

func TestXMLGaps(t *testing.T) {
	for _, tt := range []struct{ doc, json string }{
		// An element per item of a map's arrays, named by the key; an
		// undeclared element is a typed additional property.
		{`<labels><name>a</name><tags><color>red</color><size>L</size><color>blue</color></tags><count>3</count><more>4</more></labels>`,
			`{"count":3,"more":4,"name":"a","tags":{"color":["red","blue"],"size":["L"]}}`},
		// A union's text is its scalar, typed by the first type it is.
		{`<labels><value>42</value></labels>`, `{"value":42}`},
		{`<labels><value> 4x </value></labels>`, `{"value":" 4x "}`},
		// Child elements or attributes make it an object, as does nothing.
		{`<labels><value unit="kg"><amount>1.5</amount></value></labels>`, `{"value":{"amount":1.5,"unit":"kg"}}`},
		{`<labels><value>  <amount>2</amount></value></labels>`, `{"value":{"amount":2}}`},
		{`<labels><value/></labels>`, `{"value":{}}`},
		// Attributes are not additional properties.
		{`<labels other="x"><n>1</n></labels>`, `{"n":1}`},
	} {
		got, err := XMLToJSON([]byte(tt.doc), labelsXML)
		if err != nil || string(got) != tt.json {
			t.Errorf("%s:\n got %s %v\nwant %s", tt.doc, got, err, tt.json)
			continue
		}
		doc, err := JSONToXML(got, labelsXML)
		if err != nil {
			t.Errorf("writing %s: %v", got, err)
			continue
		}
		if back, err := XMLToJSON(doc, labelsXML); err != nil || string(back) != tt.json {
			t.Errorf("%s written as %s reads %s %v", got, doc, back, err)
		}
	}
	doc, err := JSONToXML([]byte(`{"name":"a","tags":{"color":["red","blue"]},"value":"x","count":3}`), labelsXML)
	if want := `<labels><name>a</name><tags><color>red</color><color>blue</color></tags><value>x</value><count>3</count></labels>`; err != nil || !strings.HasSuffix(string(doc), want) {
		t.Errorf("written %s %v\nwant %s", doc, err, want)
	}
	// An additional property of the wrong type is read for validation to
	// report; text where a scalar-only union is, is an error at an element.
	if got, err := XMLToJSON([]byte(`<labels><count>three</count></labels>`), labelsXML); err != nil || string(got) != `{"count":"three"}` {
		t.Errorf("wrong type: %s %v", got, err)
	}
	scalars := &XMLNode{Name: "v", Type: "union", Scalars: []string{"boolean", "number"}}
	if got, err := XMLToJSON([]byte(`<v>true</v>`), scalars); err != nil || string(got) != `true` {
		t.Errorf("boolean: %s %v", got, err)
	}
	if _, err := XMLToJSON([]byte(`<v><x/></v>`), scalars); err == nil || !strings.Contains(err.Error(), "expected text") {
		t.Errorf("element in a scalar union: %v", err)
	}
	if _, err := JSONToXML([]byte(`{"tags":{"a b":["x"]}}`), labelsXML); err == nil {
		t.Error("invalid key of a map of arrays: no error")
	}
}
