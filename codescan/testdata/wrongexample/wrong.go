// Package wrongexample is a fixture of codescan: an example that does not
// match its schema, which validating the scanned document reports.
package wrongexample

// Req has a bounded limit.
type Req struct {
	Limit int32 `goapi:"in=query;name=limit;minimum=1"`
}

//goapi:operation GET /items
//goapi:response 204
//goapi:example query:limit 0
func items(req Req) {}
