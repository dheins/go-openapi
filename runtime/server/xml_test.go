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
