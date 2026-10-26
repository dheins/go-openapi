package ir

import (
	"fmt"
	"slices"
	"strings"
	"testing"
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
            badExpression: {operationId: getPet, parameters: {id: $url}}
            noHeader: {operationId: getPet, parameters: {id: $response.header.X-Nope}}
  /pets/{id}:
    get:
      operationId: getPet
      tags: [read]
      parameters:
        - {name: id, in: path, required: true, schema: {type: integer}}
        - {name: mode, in: query, schema: {type: string}}
        - {name: mode, in: header, schema: {type: string}}
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
				values = append(values, v.Param.In+"."+v.Param.Name+"="+v.Expression)
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
		"CreatePet201TextResponse byHeader ByHeaderLink GetPet [path.id=$response.header.x-id]",
		"CreatePet201TextResponse byRequest ByRequestLink GetPet [path.id=$request.body#/name]",
	}
	if !slices.Equal(got, want) {
		t.Errorf("links:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	var warnings []string
	for _, w := range m.Warnings {
		if strings.Contains(w.Message, "link") {
			warnings = append(warnings, w.Message)
		}
	}
	for _, w := range []string{
		`the link "ambiguous" of response 201 of CreatePet: mode is the name of several parameters`,
		`the link "server" of response 201 of CreatePet: it names a server`,
		`the link "nowhere" of response 201 of CreatePet: it leads to no operation`,
		`the link "unknownParam" of response 201 of CreatePet: GetPet has no parameter color`,
		`the link "badExpression" of response 201 of CreatePet: parameter id: $url is not a value of the response or request`,
		`the link "noHeader" of response 201 of CreatePet reads a body or header no variant of the response has`,
	} {
		if !slices.ContainsFunc(warnings, func(s string) bool { return strings.Contains(s, w) }) {
			t.Errorf("no warning %q in:\n%s", w, strings.Join(warnings, "\n"))
		}
	}

	// Selection leaves out the links to operations it leaves out.
	m = build(t, linksDoc, Options{Operations: true, Select: &Selection{ExcludeTags: []string{"read"}}})
	for _, r := range operationNamed(t, m, "CreatePet").Responses {
		if len(r.Links) > 0 {
			t.Errorf("%s keeps links to GetPet: %v", r.TypeName, r.Links)
		}
	}
}
