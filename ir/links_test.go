package ir

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/loader"
)

const linksDoc = `openapi: 3.1.0
info: {title: t, version: v}
paths:
  /pets:
    post:
      operationId: createPet
      requestBody:
        content: {application/json: {schema: {type: object, properties: {name: {type: string}}}}}
      responses:
        "201":
          description: ok
          headers:
            X-Id: {schema: {type: integer}}
          content:
            application/json: {schema: {type: object, properties: {id: {type: integer}}}}
            text/plain: {schema: {type: string}}
          links:
            byBody: {operationId: getPet, parameters: {path.id: $response.body#/id, header.mode: fast}}
            byHeader: {operationRef: "#/paths/~1pets~1{id}/get", parameters: {id: $response.header.x-id}}
            byRequest: {operationId: getPet, parameters: {id: $request.body#/name}}
            server: {operationId: getPet, server: {url: https://other.example.com}}
            nowhere: {operationId: missing}
            unknownParam: {operationId: getPet, parameters: {color: red}}
            ambiguous: {operationId: getPet, parameters: {mode: fast}}
            badExpression: {operationId: getPet, parameters: {id: $foo}}
            noHeader: {operationId: getPet, parameters: {id: $response.header.X-Nope}}
            byRequestFacts: {operationId: getPet, parameters: {id: $statusCode, query.mode: $method, header.mode: $url}}
            embedded: {operationId: getPet, parameters: {id: "{$response.body#/id}", query.mode: "{$method} {$url}?s={$statusCode}&h={$response.header.X-Id}"}}
            embeddedArray: {operationId: getPet, parameters: {tags: "a{$statusCode}"}}
            embeddedBody: {operationId: createPet, requestBody: "x{$statusCode}"}
            embeddedBad: {operationId: getPet, parameters: {id: "{$response.header.X-Nope}"}}
            nested: {operationId: createPet, requestBody: {name: "copy of {$response.body#/id}", tags: [$statusCode, "{$response.header.X-Id}", $5], id: "{$statusCode}", raw: '{"a": "{$method}"}'}}
            nestedBad: {operationId: createPet, requestBody: {name: $response.nope}}
  /pets/{id}:
    get:
      operationId: getPet
      tags: [read]
      parameters:
        - {name: id, in: path, required: true, schema: {type: integer}}
        - {name: mode, in: query, schema: {type: string}}
        - {name: mode, in: header, schema: {type: string}}
        - {name: tags, in: query, schema: {type: array, items: {type: string}}}
      responses: {"200": {description: ok}}
`

func TestLinks(t *testing.T) {
	m := build(t, linksDoc, Options{Operations: true})
	create := operationNamed(t, m, "CreatePet")
	var got []string
	for _, r := range create.Responses {
		for _, l := range r.Links {
			var values []string
			for _, v := range l.Values {
				key := "body"
				if v.Param != nil {
					key = v.Param.In + "." + v.Param.Name
				}
				values = append(values, key+"="+v.Expression)
			}
			got = append(got, fmt.Sprintf("%s %s %s %s [%s]", r.TypeName, l.Name, l.Method, l.Operation.Name, strings.Join(values, " ")))
		}
	}
	// A link reading the body is followed from the JSON variant only; the
	// others from both.
	want := []string{
		"CreatePet201JSONResponse byBody ByBodyLink GetPet [path.id=$response.body#/id header.mode=\"fast\"]",
		"CreatePet201JSONResponse byHeader ByHeaderLink GetPet [path.id=$response.header.x-id]",
		"CreatePet201JSONResponse byRequest ByRequestLink GetPet [path.id=$request.body#/name]",
		"CreatePet201JSONResponse server ServerLink GetPet []",
		"CreatePet201JSONResponse byRequestFacts ByRequestFactsLink GetPet [path.id=$statusCode query.mode=$method header.mode=$url]",
		"CreatePet201JSONResponse embedded EmbeddedLink GetPet [path.id={$response.body#/id} query.mode={$method} {$url}?s={$statusCode}&h={$response.header.X-Id}]",
		"CreatePet201JSONResponse embeddedArray EmbeddedArrayLink GetPet [query.tags=a{$statusCode}]",
		"CreatePet201JSONResponse embeddedBody EmbeddedBodyLink CreatePet [body=x{$statusCode}]",
		`CreatePet201JSONResponse nested NestedLink CreatePet [body={"name":"copy of {$response.body#/id}","tags":["$statusCode","{$response.header.X-Id}","$5"],"id":"{$statusCode}","raw":"{\"a\": \"{$method}\"}"}]`,
		"CreatePet201TextResponse byHeader ByHeaderLink GetPet [path.id=$response.header.x-id]",
		"CreatePet201TextResponse byRequest ByRequestLink GetPet [path.id=$request.body#/name]",
		"CreatePet201TextResponse server ServerLink GetPet []",
		"CreatePet201TextResponse byRequestFacts ByRequestFactsLink GetPet [path.id=$statusCode query.mode=$method header.mode=$url]",
		"CreatePet201TextResponse embeddedArray EmbeddedArrayLink GetPet [query.tags=a{$statusCode}]",
		"CreatePet201TextResponse embeddedBody EmbeddedBodyLink CreatePet [body=x{$statusCode}]",
	}
	if !slices.Equal(got, want) {
		t.Errorf("links:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, l := range create.Responses[0].Links {
		switch l.Name {
		case "byRequestFacts":
			if v := l.Values; string(v[1].Const) != `"POST"` || !v[0].StatusCode || !v[2].URL || !l.NeedsURL() {
				t.Errorf("byRequestFacts = %+v %+v %+v", v[0], v[1], v[2])
			}
		case "embedded":
			if p := l.Values[0].Parts; len(p) != 1 || !p[0].ResponseBody || p[0].Pointer != "/id" {
				t.Errorf("embedded id = %+v", p)
			}
			if p := l.Values[1].Parts; len(p) != 6 || p[0].Text != "POST " || !p[1].URL || p[2].Text != "?s=" || !p[3].StatusCode || p[4].Text != "&h=" || p[5].Header != "X-Id" {
				t.Errorf("embedded mode = %+v", p)
			}
		case "byBody":
			if l.NeedsURL() || l.ServerURL != "" {
				t.Errorf("byBody needs the URL or has a server: %+v", l)
			}
		case "server":
			if l.ServerURL != "https://other.example.com" {
				t.Errorf("server link's server = %q", l.ServerURL)
			}
		case "nested":
			// "$5" does not begin like an expression, so it is text.
			var fills []string
			for _, f := range l.Values[0].Fills {
				v := f.Value
				fills = append(fills, fmt.Sprintf("%s %t %t %s %d", f.Pointer, f.Item, v.StatusCode, v.Header, len(v.Parts)))
			}
			want := []string{"/id false true  0", "/name false false  2", "/raw false false  1", "/tags/0 true true  0", "/tags/1 true false X-Id 0"}
			if !slices.Equal(fills, want) {
				t.Errorf("nested fills = %q, want %q", fills, want)
			}
			// Braces that do not open an expression are text.
			if p := l.Values[0].Fills[2].Value.Parts; p[0].Text != `{"a": "POST"}` {
				t.Errorf("raw parts = %+v", p)
			}
		}
	}

	var warnings []string
	for _, w := range m.Warnings {
		if strings.Contains(w.Message, "link") {
			warnings = append(warnings, w.Message)
		}
	}
	for _, w := range []string{
		`the link "ambiguous" of response 201 of CreatePet: mode is the name of several parameters`,
		`the link "nowhere" of response 201 of CreatePet: it leads to no operation`,
		`the link "unknownParam" of response 201 of CreatePet: GetPet has no parameter color`,
		`the link "badExpression" of response 201 of CreatePet: parameter id: $foo is not a value of the response or request`,
		`the link "noHeader" of response 201 of CreatePet reads a body or header no variant of the response has`,
		`the link "nestedBad" of response 201 of CreatePet: requestBody: /name: $response.nope is not a value of the response or request`,
		`the link "embeddedBad" of response 201 of CreatePet: parameter id: $response.header.X-Nope refers to a header no response declares`,
	} {
		if !slices.ContainsFunc(warnings, func(s string) bool { return strings.Contains(s, w) }) {
			t.Errorf("no warning %q in:\n%s", w, strings.Join(warnings, "\n"))
		}
	}

	// Selection leaves out the links to operations it leaves out.
	m = build(t, linksDoc, Options{Operations: true, Select: &Selection{ExcludeTags: []string{"read"}}})
	for _, r := range operationNamed(t, m, "CreatePet").Responses {
		for _, l := range r.Links {
			if l.Operation.Name == "GetPet" {
				t.Errorf("%s keeps the link %s to GetPet", r.TypeName, l.Name)
			}
		}
	}
}

func TestLinkOperationRefToOtherFile(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"openapi.yaml": `openapi: 3.1.0
info: {title: t, version: v}
paths:
  /pets:
    post:
      operationId: createPet
      responses:
        "201":
          description: ok
          content: {application/json: {schema: {type: object, properties: {id: {type: integer}}}}}
          links:
            reached: {operationRef: "other.yaml#/paths/~1pets~1{id}/get", parameters: {id: $response.body#/id}}
            elsewhere: {operationRef: "other.yaml#/paths/~1owners/get"}
  /pets/{id}:
    $ref: "other.yaml#/paths/~1pets~1{id}"
`,
		"other.yaml": `openapi: 3.1.0
info: {title: other, version: v}
paths:
  /pets/{id}:
    get:
      operationId: getPet
      parameters:
        - {name: id, in: path, required: true, schema: {type: integer}}
      responses: {"200": {description: ok}}
  /owners:
    get:
      operationId: listOwners
      responses: {"200": {description: ok}}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	spec, err := loader.Load(filepath.Join(dir, "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	m := Build(spec, Options{Operations: true})
	links := operationNamed(t, m, "CreatePet").Responses[0].Links
	if len(links) != 1 || links[0].Name != "reached" || links[0].Operation.Name != "GetPet" {
		t.Errorf("links = %+v", links)
	}
	var warnings []string
	for _, w := range m.Warnings {
		warnings = append(warnings, w.Message)
	}
	want := `the link "elsewhere" of response 201 of CreatePet: it leads to an operation of another document, which this client has no method for; no method follows it`
	if !slices.Contains(warnings, want) {
		t.Errorf("no warning %q in:\n%s", want, strings.Join(warnings, "\n"))
	}
}
