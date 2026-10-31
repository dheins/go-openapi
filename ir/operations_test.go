package ir

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestRoutePattern(t *testing.T) {
	tests := []struct {
		method, path string
		pattern      string
		segments     string
	}{
		{"GET", "/pets", "GET /pets", ""},
		{"GET", "/", "GET /{$}", ""},
		{"GET", "/pets/", "GET /pets/{$}", ""},
		{"DELETE", "/pets/{pet-id}/toys/{toyId}", "DELETE /pets/{p0}/toys/{p1}", "p0=[pet-id] p1=[toyId]"},
		{"GET", "/files/{name}.{ext}", "GET /files/{p0}", `p0=[name ext]^(.+)\.(.+)$`},
		{"QUERY", "/v{version}/search", "QUERY /{p0}/search", "p0=[version]^v(.+)$"},
	}
	for _, tt := range tests {
		pattern, segs, err := routePattern(tt.method, tt.path)
		if err != nil {
			t.Errorf("%s: %v", tt.path, err)
			continue
		}
		var got []string
		for _, s := range segs {
			got = append(got, s.Wildcard+"="+strings.Join([]string{"[" + strings.Join(s.Params, " ") + "]"}, "")+s.Pattern)
		}
		if pattern != tt.pattern || strings.Join(got, " ") != tt.segments {
			t.Errorf("%s: pattern %q segments %q, want %q %q", tt.path, pattern, got, tt.pattern, tt.segments)
		}
	}
	if _, _, err := routePattern("GET", "/a/{b"); err == nil {
		t.Error("unbalanced braces accepted")
	}
}

func operationNamed(t *testing.T, m *Model, name string) *Operation {
	t.Helper()
	for _, o := range m.Operations {
		if o.Name == name {
			return o
		}
	}
	var names []string
	for _, o := range m.Operations {
		names = append(names, o.Name)
	}
	t.Fatalf("no operation %q; have %v", name, names)
	return nil
}

func TestOperations(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
security:
  - key: []
paths:
  /items/{id}:
    parameters:
      - {name: id, in: path, required: true, schema: {type: string}}
      - {name: verbose, in: query, schema: {type: boolean}}
    get:
      parameters:
        - {name: verbose, in: query, required: true, schema: {type: boolean}}
        - {name: Accept, in: header, schema: {type: string}}
        - {name: ids, in: query, schema: {type: array, items: {type: integer}}}
      responses:
        "200": {description: ok}
    put:
      operationId: replaceItem
      security: [{}]
      parameters:
        - {name: filter, in: query, schema: {type: array, items: {type: object}}}
      responses:
        "204": {description: ok}
  /a/{x}/b:
    get:
      operationId: first
      parameters: [{name: x, in: path, required: true, schema: {type: string}}]
      responses: {"204": {description: ok}}
  /a/x/{y}:
    get:
      operationId: second
      parameters: [{name: y, in: path, required: true, schema: {type: string}}]
      responses: {"204": {description: ok}}
  /blob:
    get:
      operationId: blob
      responses:
        "200":
          description: ok
          content:
            image/png: {}
components:
  securitySchemes:
    key: {type: apiKey, in: header, name: X-Key}
`, Options{Operations: true})

	get := operationNamed(t, m, "GetItemsID")
	if get.Unsupported != "" {
		t.Fatalf("GetItemsID unsupported: %s", get.Unsupported)
	}
	var params []string
	for _, p := range get.Params {
		params = append(params, p.In+":"+p.Name+":"+p.Field)
	}
	// The operation's verbose overrides the path-level one; Accept is
	// ignored as the specification requires.
	if strings.Join(params, " ") != "path:id:ID query:verbose:Verbose query:ids:IDs" {
		t.Errorf("params = %v", params)
	}
	if verbose := get.Params[1]; !verbose.Required || !verbose.Explode || verbose.Type.Kind != Builtin {
		t.Errorf("verbose = %+v", verbose)
	}
	if ids := get.Params[2]; !ids.Array || ids.Elem.Name != "int64" || ids.Type.Kind != Optional {
		t.Errorf("ids = %+v", ids)
	}

	if put := operationNamed(t, m, "ReplaceItem"); !strings.Contains(put.Unsupported, "not a primitive value, an array of them, or an object of them") {
		t.Errorf("array of objects: %q", put.Unsupported)
	}
	if second := operationNamed(t, m, "Second"); !strings.Contains(second.Unsupported, "conflicts") || second.Pattern != "" {
		t.Errorf("conflicting route: %+v", second)
	}
	if blob := operationNamed(t, m, "Blob"); blob.Unsupported != "" || blob.Responses[0].TypeName != "Blob200PNGResponse" || blob.Responses[0].Kind != "binary" {
		t.Errorf("binary response: %q %+v", blob.Unsupported, blob.Responses)
	}

	var warnings []string
	for _, w := range m.Warnings {
		warnings = append(warnings, w.Message)
	}
	all := strings.Join(warnings, "\n")
	// Every operation inherits the document's security except replaceItem,
	// whose empty requirement allows anonymous access.
	for _, o := range m.Operations {
		if len(o.Security) != 1 {
			t.Errorf("%s security = %v", o.Name, o.Security)
		}
	}
	if put := operationNamed(t, m, "ReplaceItem"); len(put.Security[0]) != 0 {
		t.Errorf("replaceItem should allow anonymous access, got %v", put.Security)
	}
	if get := operationNamed(t, m, "GetItemsID"); get.Security[0][0].Scheme.Name != "key" || get.Security[0][0].Scheme.In != "header" {
		t.Errorf("GetItemsID security = %+v", get.Security[0][0].Scheme)
	}
	if strings.Contains(all, "security requirements are not enforced") {
		t.Errorf("security is enforced now:\n%s", all)
	}
	for _, want := range []string{"an object of them", "conflicts with pattern"} {
		if !strings.Contains(all, want) {
			t.Errorf("no warning mentioning %q in:\n%s", want, all)
		}
	}
}

func TestOperationTypesAvoidReservedNames(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
paths: {}
components:
  schemas:
    Handler: {type: string}
`, Options{Operations: true})
	if ty := typeNamed(t, m, "Handler2"); ty.Kind != Defined {
		t.Errorf("Handler2 = %+v", ty)
	}
}

func TestParameterStyles(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
paths:
  /items/{id}:
    get:
      operationId: styles
      parameters:
        - {name: id, in: path, required: true, style: matrix, explode: true, schema: {type: array, items: {type: integer}}}
        - {name: filter, in: query, style: deepObject, explode: true, schema: {$ref: "#/components/schemas/Filter"}}
        - {name: pipes, in: query, style: pipeDelimited, schema: {type: array, items: {type: string}}}
        - {name: q, in: query, allowEmptyValue: true, schema: {type: string}}
        - {name: meta, in: header, content: {application/json: {schema: {$ref: "#/components/schemas/Filter"}}}}
      responses: {"204": {description: ok}}
  /bad:
    get:
      operationId: badStyle
      parameters:
        - {name: x, in: header, style: form, schema: {type: string}}
      responses: {"204": {description: ok}}
  /search:
    get:
      operationId: search
      parameters:
        - name: qs
          in: querystring
          content:
            application/x-www-form-urlencoded:
              schema: {$ref: "#/components/schemas/Filter"}
      responses: {"204": {description: ok}}
components:
  schemas:
    Filter:
      type: object
      properties:
        role: {type: string}
        age: {type: integer}
        tags: {type: array, items: {type: string}}
`, Options{Operations: true})
	styles := operationNamed(t, m, "Styles")
	if styles.Unsupported != "" {
		t.Fatalf("unsupported: %s", styles.Unsupported)
	}
	var got []string
	for _, p := range styles.Params {
		shape := "scalar"
		if p.Array {
			shape = "array"
		} else if p.Object {
			shape = "object"
		}
		got = append(got, fmt.Sprintf("%s:%s:%s:%v:%s", p.Name, p.Style, shape, p.Explode, p.Content))
	}
	want := "id:matrix:array:true: filter:deepObject:object:true: pipes:pipeDelimited:array:false: q:form:scalar:true: meta::scalar:false:application/json"
	if strings.Join(got, " ") != want {
		t.Errorf("params:\n%s\nwant\n%s", strings.Join(got, " "), want)
	}
	filter := styles.Params[1]
	if fmt.Sprint(filter.Properties) != "[{role string false [] <nil> [] []  } {age integer false [] <nil> [] []  } {tags string true [] <nil> [] []  }]" {
		t.Errorf("properties = %v", filter.Properties)
	}
	if !styles.Params[3].AllowEmptyValue {
		t.Error("allowEmptyValue lost")
	}
	if bad := operationNamed(t, m, "BadStyle"); !strings.Contains(bad.Unsupported, "the form style cannot be used in header") {
		t.Errorf("bad style: %q", bad.Unsupported)
	}
	search := operationNamed(t, m, "Search")
	if qs := search.Params[0]; search.Unsupported != "" || qs.In != "querystring" || !qs.Object || qs.Content != "application/x-www-form-urlencoded" {
		t.Errorf("querystring: %q %+v", search.Unsupported, search.Params)
	}
}

func TestBodiesAndResponseMedia(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
paths:
  /pets:
    post:
      operationId: createPet
      requestBody:
        required: true
        content:
          application/json: {schema: {$ref: "#/components/schemas/Pet"}}
          application/x-www-form-urlencoded: {schema: {$ref: "#/components/schemas/Pet"}}
          multipart/form-data: {schema: {$ref: "#/components/schemas/Pet"}}
          text/plain: {schema: {type: string}}
          application/jsonl: {itemSchema: {$ref: "#/components/schemas/Pet"}}
      responses:
        "200":
          description: ok
          content:
            application/json: {schema: {$ref: "#/components/schemas/Pet"}}
            text/csv: {schema: {type: string}}
            image/*: {}
            application/jsonl: {itemSchema: {$ref: "#/components/schemas/Pet"}}
            text/event-stream: {}
components:
  schemas:
    Pet:
      type: object
      properties:
        name: {type: string}
        photo: {type: string, format: binary}
        tags: {type: array, items: {type: string}}
`, Options{Operations: true})
	o := operationNamed(t, m, "CreatePet")
	var media []string
	for _, bm := range o.Body.Media {
		media = append(media, bm.MediaType+"="+bm.Kind)
	}
	// A form cannot carry the binary photo: it is answered with 415.
	// text/plain is a different kind than the first (structured) media
	// type and gets its own field, as does the stream of Pets.
	if strings.Join(media, " ") != "application/json=json multipart/form-data=multipart text/plain=text application/jsonl=jsonl" {
		t.Fatalf("body media = %v", media)
	}
	var fields []string
	for _, f := range o.Body.Fields {
		fields = append(fields, fmt.Sprintf("%s:%d", f.Name, len(f.Media)))
	}
	if strings.Join(fields, " ") != "Body:2 TextBody:1 JSONLinesBody:1" || o.Body.Fields[1].Type.Kind != Optional {
		t.Errorf("body fields = %v", fields)
	}
	// A stream is not wrapped: nil is no body.
	if lines := o.Body.Fields[2].Type; lines.Kind != Stream || lines.Elem.Kind != Named || lines.Elem.Type.Name != "Pet" {
		t.Errorf("stream field = %+v", lines)
	}
	if props := fmt.Sprint(o.Body.Media[1].Properties); props != "[{name string false [] <nil> [] []  } {photo file false [] <nil> [] []  } {tags string true [] <nil> [] []  }]" {
		t.Errorf("multipart properties = %s", props)
	}
	var warnings []string
	for _, w := range m.Warnings {
		warnings = append(warnings, w.Message)
	}
	if all := strings.Join(warnings, "\n"); !strings.Contains(all, "form bodies must be objects") || strings.Contains(all, "jsonl") {
		t.Errorf("warnings:\n%s", all)
	}
	var variants []string
	for _, r := range o.Responses {
		variants = append(variants, fmt.Sprintf("%s:%s:%v", r.TypeName, r.Kind, r.ContentTypeField))
	}
	want := "CreatePet200JSONResponse:json:false CreatePet200TextResponse:text:false CreatePet200ImageResponse:binary:true " +
		"CreatePet200JSONLinesResponse:jsonl:false CreatePet200EventsResponse:sse:false"
	if strings.Join(variants, " ") != want {
		t.Errorf("variants:\n%s\nwant\n%s", strings.Join(variants, " "), want)
	}
	if item := o.Responses[3].Item; item.Kind != Named || item.Type.Name != "Pet" {
		t.Errorf("JSON Lines item = %+v", item)
	}
}

func TestOperationWithoutResponses(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
paths:
  /ping:
    get:
      operationId: ping
`, Options{Operations: true})
	ping := operationNamed(t, m, "Ping")
	if ping.Unsupported != "" || len(ping.Responses) != 1 {
		t.Fatalf("ping = %+v", ping)
	}
	if r := ping.Responses[0]; r.Status != "default" || r.Code != 0 || r.MediaType != "" || r.TypeName != "PingDefaultResponse" {
		t.Errorf("response = %+v", r)
	}
}

func TestNamedArrayParameterKeepsChecks(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
paths:
  /search:
    get:
      operationId: search
      parameters:
        - {name: tags, in: query, schema: {$ref: "#/components/schemas/Tags"}}
      responses: {"204": {description: ok}}
components:
  schemas:
    Tags:
      type: array
      maxItems: 3
      items: {type: string, minLength: 2}
`, Options{Operations: true})
	tags := operationNamed(t, m, "Search").Params[0]
	arr := tags.Type.Elem
	if !tags.Array || arr.Kind != Slice || arr.Checks == nil || *arr.Checks.MaxItems != 3 || *tags.Elem.Checks.MinLength != 2 {
		t.Errorf("tags = %+v, array = %+v", tags, arr)
	}
}

func TestMultipartFileParts(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
paths:
  /docs:
    post:
      operationId: upload
      requestBody:
        content:
          multipart/form-data:
            schema:
              type: object
              properties:
                pdf: {type: string, contentMediaType: application/pdf}
                untyped: {}
                anything: true
                scans: {type: array, items: {contentMediaType: image/png}}
                caption: {type: string}
            encoding:
              pdf: {contentType: "application/pdf; version=1.7", headers: {X-Sum: {required: true}, Content-Type: {required: true}}}
      responses: {"204": {description: ok}}
    put:
      operationId: replace
      requestBody:
        content:
          application/json: {schema: {$ref: "#/components/schemas/Doc"}}
      responses: {"204": {description: ok}}
components:
  schemas:
    Doc:
      type: object
      properties:
        pdf: {type: string, contentMediaType: application/pdf}
`, Options{Operations: true})
	props := operationNamed(t, m, "Upload").Body.Media[0].Properties
	if got := fmt.Sprint(props); got != "[{pdf file false [] <nil> [application/pdf] [X-Sum]  } {untyped file false [] <nil> [] []  } {anything file false [] <nil> [] []  } {scans file true [] <nil> [] []  } {caption string false [] <nil> [] []  }]" {
		t.Errorf("multipart properties = %s", got)
	}
	// Outside multipart, a contentMediaType string holds text.
	pdf := typeNamed(t, m, "Doc").Fields[0].Type
	for pdf.Kind == Optional {
		pdf = pdf.Elem
	}
	if pdf.Kind != Builtin || pdf.Name != "string" {
		t.Errorf("Doc.pdf = %+v", pdf)
	}
}

func TestBodyFieldsPerSchema(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
paths:
  /pets:
    post:
      operationId: create
      parameters:
        - {name: jsonBody, in: query, schema: {type: string}}
      requestBody:
        required: true
        content:
          application/json: {schema: {type: object, properties: {a: {type: string}}}}
          application/merge-patch+json: {schema: {type: object, properties: {b: {type: string}}}}
          application/vnd.pet+json: {schema: {type: object, properties: {c: {type: string}}}}
          application/xml: {schema: {type: object, properties: {a: {type: string}}}}
          application/octet-stream: {schema: {type: string, format: binary}}
      responses: {"204": {description: ok}}
`, Options{Operations: true})
	o := operationNamed(t, m, "Create")
	var fields []string
	for _, f := range o.Body.Fields {
		fields = append(fields, f.Name+"="+f.Media[0].MediaType)
	}
	// The parameter claimed JSONBody, so the media type names the field; XML with an object schema is
	// answered with 415, but bytes described as a string are passed on.
	want := "Body=application/json ApplicationMergePatchJSONBody=application/merge-patch+json ApplicationVndPetJSONBody=application/vnd.pet+json BinaryBody=application/octet-stream"
	if got := strings.Join(fields, " "); got != want {
		t.Errorf("fields:\n%s\nwant\n%s", got, want)
	}
	if o.Body.Fields[0].Type.Kind == Optional || o.Body.Fields[1].Type.Kind != Optional {
		t.Errorf("Body should be required and the others optional: %+v", o.Body.Fields)
	}
}

func TestNestedObjectParameters(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
paths:
  /deep:
    get:
      operationId: deep
      parameters:
        - {name: f, in: query, style: deepObject, explode: true, schema: {$ref: "#/components/schemas/Filter"}}
        - {name: m, in: query, style: deepObject, explode: true, schema: {type: object, additionalProperties: {type: integer}}}
      responses: {"204": {description: ok}}
  /exploded:
    get:
      operationId: exploded
      parameters:
        - {name: m, in: query, schema: {type: object, additionalProperties: {type: string}}}
      responses: {"204": {description: ok}}
  /recursive:
    get:
      operationId: recursive
      parameters:
        - {name: n, in: query, style: deepObject, explode: true, schema: {$ref: "#/components/schemas/Node"}}
        - {name: l, in: query, style: deepObject, explode: true, schema: {$ref: "#/components/schemas/List"}}
      responses: {"204": {description: ok}}
  /flat:
    get:
      operationId: flat
      parameters:
        - {name: l, in: query, schema: {$ref: "#/components/schemas/List"}}
      responses: {"204": {description: ok}}
components:
  schemas:
    Filter:
      type: object
      properties:
        owner: {type: object, properties: {age: {type: integer}}}
        any: {}
    Node:
      type: object
      properties:
        child: {$ref: "#/components/schemas/Node"}
    List:
      type: object
      properties:
        items: {type: array, items: {$ref: "#/components/schemas/Filter"}}
`, Options{Operations: true})
	deep := operationNamed(t, m, "Deep")
	if deep.Unsupported != "" {
		t.Fatalf("deep: %s", deep.Unsupported)
	}
	if got := fmt.Sprint(deep.Params[0].Properties); got != "[{owner object false [{age integer false [] <nil> [] []  }] <nil> [] []  } {any any false [] <nil> [] []  }]" {
		t.Errorf("nested properties = %s", got)
	}
	if a := deep.Params[1].Additional; a == nil || a.Type != "integer" {
		t.Errorf("map additional = %+v", a)
	}
	if o := operationNamed(t, m, "Exploded"); !strings.Contains(o.Unsupported, "cannot use the exploded form style") {
		t.Errorf("exploded map: %q", o.Unsupported)
	}
	// A recursive type refers to its own shape; arrays of objects hold
	// their item's properties.
	rec := operationNamed(t, m, "Recursive")
	if rec.Unsupported != "" {
		t.Fatalf("recursive: %s", rec.Unsupported)
	}
	if n := rec.Params[0]; n.Shape != "Node" || len(n.Properties) != 1 || n.Properties[0].Ref != "Node" || n.Properties[0].Type != "" {
		t.Errorf("recursive = %+v", n)
	}
	if items := rec.Params[1].Properties[0]; !items.Array || items.Type != "object" || len(items.Properties) != 2 || items.Properties[0].Name != "owner" {
		t.Errorf("array of objects = %+v", items)
	}
	// Only brackets write them.
	if o := operationNamed(t, m, "Flat"); !strings.Contains(o.Unsupported, "arrays of objects and recursive types need the deepObject style") {
		t.Errorf("flat: %q", o.Unsupported)
	}
}

func TestSecuritySchemeURIs(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
security:
  - "#/components/securitySchemes/apiKey": []
paths:
  /a:
    get:
      operationId: a
      responses: {"204": {description: ok}}
  /b:
    get:
      operationId: b
      security: [{apiKey: []}, {"#/$defs/token": []}]
      responses: {"204": {description: ok}}
components:
  securitySchemes:
    apiKey: {type: apiKey, in: header, name: X-Key}
$defs:
  token: {type: http, scheme: bearer}
`, Options{Operations: true})
	var schemes []string
	for _, s := range m.SecuritySchemes {
		schemes = append(schemes, s.Name+"="+s.Method)
	}
	// The URI of a component and its name are one scheme, named after the
	// component; other URIs are named after their last segment.
	if got := strings.Join(schemes, " "); got != "apiKey=APIKey #/$defs/token=Token" {
		t.Errorf("schemes = %s", got)
	}
	a, b := operationNamed(t, m, "A"), operationNamed(t, m, "B")
	if a.Unsupported != "" || b.Unsupported != "" || a.Security[0][0].Scheme != b.Security[0][0].Scheme {
		t.Errorf("a = %q %+v, b = %q %+v", a.Unsupported, a.Security, b.Unsupported, b.Security)
	}
}

func TestXMLBodies(t *testing.T) {
	m := build(t, `openapi: 3.0.3
info: {title: t, version: v}
paths:
  /ok:
    post:
      operationId: ok
      requestBody:
        content:
          application/xml: {schema: {$ref: "#/components/schemas/Book"}}
          text/plain: {schema: {type: string}}
      responses:
        "200":
          description: ok
          content:
            application/atom+xml:
              schema: {type: array, items: {$ref: "#/components/schemas/Book"}}
            text/xml: {}
  /bad:
    post:
      operationId: bad
      requestBody:
        content:
          application/xml: {schema: {type: object, additionalProperties: {type: integer}}}
          application/json: {schema: {type: object, additionalProperties: {type: integer}}}
      responses:
        "200":
          description: ok
          content:
            application/xml: {schema: {$ref: "#/components/schemas/Node"}}
            application/json: {schema: {$ref: "#/components/schemas/Node"}}
components:
  schemas:
    Book:
      type: object
      properties:
        isbn: {type: string, xml: {attribute: true}}
        authors: {type: array, items: {type: string, xml: {name: author}}, xml: {wrapped: true}}
    Node:
      type: object
      properties:
        next: {$ref: "#/components/schemas/Node"}
`, Options{Operations: true})
	ok := operationNamed(t, m, "Ok")
	book := ok.Body.Media[0].XML
	if book == nil || book.Name != "Book" || book.Properties[0].Node.Kind != "attribute" ||
		!book.Properties[1].Node.Wrapped || book.Properties[1].Node.Items.Name != "author" {
		t.Errorf("Book shape = %+v", book)
	}
	var kinds []string
	for _, r := range ok.Responses {
		kinds = append(kinds, r.MediaType+"="+r.Kind)
	}
	// A schema-less text/xml body stays text; a root array is wrapped.
	if got := strings.Join(kinds, " "); got != "application/atom+xml=xml text/xml=text" {
		t.Errorf("responses = %s", got)
	}
	if list := ok.Responses[0].XML; !list.Wrapped || list.Name != "items" || list.Items.Name != "Book" {
		t.Errorf("list shape = %+v", list)
	}

	bad := operationNamed(t, m, "Bad")
	if len(bad.Body.Media) != 1 || bad.Body.Media[0].MediaType != "application/json" || len(bad.Responses) != 1 {
		t.Errorf("bad: media %+v, responses %+v", bad.Body.Media, bad.Responses)
	}
	all := ""
	for _, w := range m.Warnings {
		all += w.Message + "\n"
	}
	for _, want := range []string{"a map cannot be written as XML yet; application/xml is answered with 415",
		"Node refers to itself, which cannot be written as XML yet; response 200 of type application/xml cannot be returned"} {
		if !strings.Contains(all, want) {
			t.Errorf("no warning %q in:\n%s", want, all)
		}
	}
}

func TestPositionalParts(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
paths:
  /batch:
    post:
      operationId: batch
      requestBody:
        content:
          application/json: {schema: {type: object, properties: {a: {type: string}}}}
          multipart/mixed:
            schema: {type: array, items: {type: string, contentMediaType: image/png}}
            prefixEncoding: [{contentType: "image/png; q=1", headers: {Content-ID: {required: true}}}]
            itemEncoding: {contentType: "image/*"}
          multipart/related:
            schema: {type: array, prefixItems: [{type: string}]}
            encoding: {a: {contentType: text/plain}}
      responses:
        "200":
          description: ok
          content:
            multipart/mixed: {schema: {type: array, items: {type: integer}}}
`, Options{Operations: true})
	o := operationNamed(t, m, "Batch")
	var fields []string
	for _, f := range o.Body.Fields {
		fields = append(fields, f.Name+"="+f.Media[0].MediaType)
	}
	if got := strings.Join(fields, " "); got != "Body=application/json MixedBody=multipart/mixed RelatedBody=multipart/related" {
		t.Errorf("fields = %s", got)
	}
	mixed, related := o.Body.Media[1].Parts, o.Body.Media[2].Parts
	if mixed.Item != "file" || fmt.Sprint(mixed.Prefix) != "[{  false [] <nil> [image/png] [Content-Id]  }]" || fmt.Sprint(mixed.Rest.ContentTypes) != "[image/*]" {
		t.Errorf("mixed = %+v", mixed)
	}
	// Items by position are the fields of a tuple, here a text part; any
	// parts after it are files, as items does not say.
	tuple := o.Body.Fields[2].Value
	if tuple.Kind != Tuple || tuple.Tuple.Name != "BatchRelatedRequestParts" || len(tuple.Tuple.Fields) != 1 || tuple.Tuple.Fields[0].Name != "Part1" ||
		!slices.Equal(related.Kinds, []string{"text"}) || related.Item != "file" || tuple.Tuple.Rest.Elem.Name != "oapi.File" {
		t.Errorf("related = %+v, %+v", related, tuple.Tuple)
	}
	if r := o.Responses[0]; r.Kind != "parts" || r.TypeName != "Batch200MixedResponse" || r.Parts.Item != "json" {
		t.Errorf("response = %+v", r)
	}
	all := ""
	for _, w := range m.Warnings {
		all += w.Message + "\n"
	}
	if !strings.Contains(all, "encoding by name applies to multipart/form-data only") {
		t.Errorf("no warning about encoding by name in:\n%s", all)
	}
}

func TestPartValues(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
paths:
  /batch:
    post:
      operationId: batch
      requestBody:
        content:
          multipart/mixed:
            schema: {$ref: "#/components/schemas/Pets"}
          multipart/related:
            schema: {$ref: "#/components/schemas/Pets"}
            itemEncoding: {headers: {X-Seq: {schema: {type: integer}}}}
      responses:
        "200":
          description: ok
          content:
            multipart/mixed:
              schema: {type: array, items: {type: string}}
              itemEncoding: {headers: {X-Seq: {required: true, schema: {type: integer}}}}
        "201":
          description: ok
          content:
            multipart/mixed: {schema: {type: array, items: {type: string}}}
components:
  schemas:
    Pets: {type: array, minItems: 1, items: {type: object, properties: {name: {type: string}}}}
`, Options{Operations: true})
	o := operationNamed(t, m, "Batch")
	// The media types share the body; one declares headers, so its items
	// are Parts, and the checks of the named array are kept.
	body := o.Body.Fields[0].Value
	if len(o.Body.Fields) != 1 || body.Kind != Slice || body.Elem.Kind != Part || body.Elem.Elem.Type.Name != "PetsItem" || body.Checks.MinItems == nil {
		t.Errorf("body = %+v", body)
	}
	// Text items too; without headers, items stay as they are.
	if r := o.Responses[0]; r.Body.Elem.Kind != Part || r.Body.Elem.Elem.Name != "string" {
		t.Errorf("200 = %+v", r.Body)
	}
	if r := o.Responses[1]; r.Body.Elem.Kind != Builtin {
		t.Errorf("201 = %+v", r.Body)
	}
}

func TestContentKinds(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
paths:
  /ok:
    get:
      operationId: ok
      parameters:
        - {name: n, in: query, content: {text/plain: {schema: {type: integer}}}}
        - {name: f, in: header, content: {application/x-www-form-urlencoded: {schema: {type: object, properties: {a: {type: string}}}}}}
        - {name: x, in: cookie, content: {application/xml: {schema: {type: object, properties: {a: {type: string}}}}}}
      responses:
        "204":
          description: ok
          headers:
            X-T: {content: {text/csv: {schema: {type: string}}}}
  /text:
    get:
      operationId: text
      parameters:
        - {name: t, in: query, content: {text/plain: {schema: {type: object}}}}
      responses: {"204": {description: ok}}
  /form:
    get:
      operationId: form
      parameters:
        - {name: f, in: query, content: {application/x-www-form-urlencoded: {schema: {type: string}}}}
      responses: {"204": {description: ok}}
  /qs:
    get:
      operationId: qs
      parameters:
        - {name: q, in: querystring, content: {text/plain: {schema: {type: string}}}}
      responses: {"204": {description: ok}}
`, Options{Operations: true})
	o := operationNamed(t, m, "Ok")
	var kinds []string
	for _, p := range o.Params {
		kinds = append(kinds, p.Name+"="+p.ContentKind)
	}
	if got := strings.Join(kinds, " "); got != "n=text f=form x=xml" || o.Params[1].Properties == nil || o.Params[2].XML == nil {
		t.Errorf("params = %s", got)
	}
	if h := o.Responses[0].Headers[0]; h.ContentKind != "text" {
		t.Errorf("header = %+v", h)
	}
	for name, want := range map[string]string{
		"Text": `parameter "t" has content of type "text/plain", which holds a primitive value only`,
		"Form": `parameter "f" has content of type "application/x-www-form-urlencoded", which must be an object`,
		"Qs":   `parameter "q" has content of type "text/plain", which is not supported yet`,
	} {
		if o := operationNamed(t, m, name); !strings.Contains(o.Unsupported, want) {
			t.Errorf("%s: unsupported = %q", name, o.Unsupported)
		}
	}
}

func TestTupleBody(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
paths:
  /t:
    post:
      operationId: send
      requestBody:
        content:
          multipart/mixed:
            schema:
              type: array
              minItems: 2
              prefixItems:
                - $ref: "#/components/schemas/Meta"
                - {title: meta, type: string}
                - {type: string, contentMediaType: image/png}
              items: false
      responses:
        "200":
          description: ok
          content:
            multipart/mixed:
              schema: {type: array, prefixItems: [{type: integer}]}
components:
  schemas:
    Meta: {type: object, properties: {a: {type: string}}}
`, Options{Operations: true})
	o := operationNamed(t, m, "Send")
	tuple := o.Body.Fields[0].Value.Tuple
	var fields []string
	for _, f := range tuple.Fields {
		fields = append(fields, fmt.Sprintf("%s:%v", f.Name, f.Type.Kind == Optional))
	}
	// Names come from the component, the title or the position, numbered
	// when they clash; positions from minItems on are optional, and
	// items: false allows no more parts.
	if got := strings.Join(fields, " "); got != "Meta:false Meta2:false Part3:true" || tuple.Rest != nil ||
		!slices.Equal(o.Body.Media[0].Parts.Kinds, []string{"json", "text", "file"}) {
		t.Errorf("fields = %s, rest %+v, kinds %v", got, tuple.Rest, o.Body.Media[0].Parts.Kinds)
	}
	// Without items, any parts may follow, as files.
	resp := o.Responses[0]
	if resp.Body.Tuple.Name != "Send200ResponseParts" || resp.Body.Tuple.Rest == nil || resp.Body.Tuple.Rest.Elem.Name != "oapi.File" || resp.Parts.Item != "file" {
		t.Errorf("response = %+v", resp.Body.Tuple)
	}
}
