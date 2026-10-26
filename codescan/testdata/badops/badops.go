// Package badops is a fixture of codescan: operation directives it
// reports.
//
//goapi:info title=Bad version=1
//goapi:info title=Again
//goapi:securityScheme odd type=magic
//goapi:securityScheme key type=apiKey in=body name=k
//goapi:frobnicate
package badops

// Req has fields codescan rejects.
type Req struct {
	ID    int64  `goapi:"in=path;name=id;optional"`
	Other string `goapi:"in=path;name=other"`
	Where string `goapi:"in=bogus"`
	None  string `goapi:"name=none"`
	A     []byte `goapi:"in=body"`
	B     []byte `goapi:"in=body"`
	M     string `goapi:"in=query;mediaType=text/plain"`
	E     bool   `goapi:"in=query;explode=maybe"`
}

// Resp has a field in no response location.
type Resp struct {
	Q string `goapi:"in=query"`
}

//goapi:response 200
func orphan() {}

//goapi:operation GET /a
//goapi:operation GET /b
//goapi:response 200
func twice() {}

//goapi:operation FETCH! /a
//goapi:response 200
func badMethod() {}

//goapi:operation GET nopath
//goapi:response 200
func noSlash() {}

//goapi:operation GET /x/{id}/{gone} color=red
//goapi:request Req
//goapi:response 200 Resp
//goapi:response 600
//goapi:response 200
//goapi:response 201 nosuchtype
//goapi:security nobody
//goapi:wiggle
func many() {}

//goapi:operation GET /quiet
func noResponses() {}

//goapi:operation GET /dup id=same
//goapi:response 200
func first() {}

//goapi:operation POST /dup id=same
//goapi:response 200
func second() {}

//goapi:operation GET /dup
//goapi:response 200
func third() {}

// HookReq has a path field, which webhooks and callbacks do not have.
type HookReq struct {
	ID int64 `goapi:"in=path;name=id"`
}

//goapi:webhook hook POST
//goapi:response 204
func hookWithPath(req HookReq) {}

//goapi:webhook hook
//goapi:response 204
func hookWithoutMethod() {}

//goapi:webhook again POST
//goapi:response 204
func hookOnce() {}

//goapi:webhook again POST
//goapi:response 204
func hookTwice() {}

//goapi:webhook quiet POST
func hookWithoutResponse() {}

//goapi:operation GET /both
//goapi:webhook both GET
//goapi:response 200
func both() {}

//goapi:callback nosuchop event POST {$request.body#/url}
//goapi:response 204
func orphanCallback() {}

//goapi:callback same event POST {$request.body#/url}
//goapi:response 204
func callbackOnce() {}

//goapi:callback same event POST {$request.body#/url} id=callbackAgain
//goapi:response 204
func callbackTwice() {}

//goapi:callback same event POST
//goapi:response 204
func callbackWithoutURL() {}

// BodyReq has a body.
type BodyReq struct {
	Body string `goapi:"in=body;mediaType=application/json|text/plain"`
}

//goapi:operation POST /examples
//goapi:response 200 string
//goapi:response 204
//goapi:example 200 {"unterminated":
//goapi:example 300 1
//goapi:example 201 1
//goapi:example 204 1
//goapi:example request 1
//goapi:example request text/csv 1
//goapi:example request application/json color=red 1
//goapi:example request application/json summary=lonely 1
//goapi:example 200
//goapi:example 200 2
//goapi:example 200 3
//goapi:example 200 name=a 4
//goapi:example 200 name=a 5
func examples(req BodyReq) {}

//goapi:operation GET /links
//goapi:response 200
//goapi:link 201 next operationId=examples
//goapi:link 200 nowhere
//goapi:link 200 both operationId=examples operationRef=#/paths/~1links/get
//goapi:link 200 missing operationId=nosuchop
//goapi:link 200 next operationId=examples
//goapi:link 200 next operationId=examples
//goapi:link 200
func links() {}

// Parts has encodings codescan rejects.
type Parts struct {
	A string `json:"a" goapi:"explode=maybe"`
	B string `json:"b" goapi:"contentType"`
}

// PartsReq sends Parts as a form.
type PartsReq struct {
	Body Parts `goapi:"in=body;mediaType=application/x-www-form-urlencoded"`
}

//goapi:operation POST /parts
//goapi:response 204
func parts(req PartsReq) {}
