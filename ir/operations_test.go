package ir

import (
	"fmt"
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
        - {name: filter, in: query, style: deepObject, schema: {type: object}}
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
		t.Errorf("deepObject with a free-form object: %q", put.Unsupported)
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
	if fmt.Sprint(filter.Properties) != "[{role string false} {age integer false} {tags string true}]" {
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
	// A form cannot carry the binary photo; text/plain is a different kind
	// than the first (structured) media type; request streams are not
	// supported. All three are answered with 415.
	if strings.Join(media, " ") != "application/json=json multipart/form-data=multipart" {
		t.Fatalf("body media = %v", media)
	}
	if props := fmt.Sprint(o.Body.Media[1].Properties); props != "[{name string false} {photo file false} {tags string true}]" {
		t.Errorf("multipart properties = %s", props)
	}
	var warnings []string
	for _, w := range m.Warnings {
		warnings = append(warnings, w.Message)
	}
	all := strings.Join(warnings, "\n")
	for _, want := range []string{"form bodies must be objects", "text/plain cannot share a Go type", "application/jsonl are not supported yet"} {
		if !strings.Contains(all, want) {
			t.Errorf("no warning mentioning %q in:\n%s", want, all)
		}
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
