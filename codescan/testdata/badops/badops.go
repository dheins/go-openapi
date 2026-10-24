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
