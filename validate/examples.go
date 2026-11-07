package validate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dheins/go-openapi/internal/schemas"
	"github.com/dheins/go-openapi/oas"
)

// direction is where an example is sent: in a request, in a response, or
// either, for an example in a schema.
type direction = schemas.Direction

const (
	eitherWay  = schemas.Either
	inRequest  = schemas.Request
	inResponse = schemas.Response
)

// exampleChecker checks examples against the schemas they are examples
// of, compiled where they are in the loaded files, so that references
// resolve as the loader resolved them.
type exampleChecker struct {
	c       *checker
	schemas *schemas.Set
	// seen are the examples checked, so that an example reached twice,
	// such as a component referenced twice, is reported once.
	seen map[exampleKey]bool
}

type exampleKey struct {
	owner  any
	tokens string
	schema *oas.Schema
}

// checkExamples warns about examples that do not match their schemas:
// those of media types, parameters, headers and schemas, and of the
// components they come from.
func (c *checker) checkExamples() error {
	set, err := schemas.New(c.spec)
	if err != nil {
		return err
	}
	e := &exampleChecker{c: c, schemas: set, seen: map[exampleKey]bool{}}
	for _, item := range c.pathItems {
		for _, p := range item.Parameters {
			e.parameter(p.Deref())
		}
	}
	for _, op := range c.operations {
		for _, p := range op.Parameters {
			e.parameter(p.Deref())
		}
		if rb := op.RequestBody.Deref(); rb != nil {
			e.content(rb.Content, inRequest)
		}
		if op.Responses != nil {
			for _, r := range op.Responses.Items.All() {
				e.response(r.Deref())
			}
		}
	}
	if comp := c.spec.Document.Components; comp != nil {
		for _, p := range comp.Parameters.All() {
			e.parameter(p.Deref())
		}
		for _, rb := range comp.RequestBodies.All() {
			if rb = rb.Deref(); rb != nil {
				e.content(rb.Content, inRequest)
			}
		}
		for _, r := range comp.Responses.All() {
			e.response(r.Deref())
		}
		for _, h := range comp.Headers.All() {
			e.header(h.Deref())
		}
	}
	for _, s := range c.exampleSchemas {
		if s.Example != nil {
			e.check(s, []string{"example"}, s.Example, s, eitherWay)
		}
		for i, v := range s.Examples {
			e.check(s, []string{"examples", fmt.Sprint(i)}, v, s, eitherWay)
		}
	}
	return nil
}

func (e *exampleChecker) parameter(p *oas.Parameter) {
	if p == nil {
		return
	}
	e.values(p, p.Example, p.Examples, p.Schema, inRequest)
	e.content(p.Content, inRequest)
}

func (e *exampleChecker) header(h *oas.Header) {
	if h == nil {
		return
	}
	e.values(h, h.Example, h.Examples, h.Schema, inResponse)
	e.content(h.Content, inResponse)
}

func (e *exampleChecker) response(r *oas.Response) {
	if r == nil {
		return
	}
	e.content(r.Content, inResponse)
	for _, h := range r.Headers.All() {
		e.header(h.Deref())
	}
}

// content checks the examples of the media types of a body, parameter or
// header.
func (e *exampleChecker) content(content *oas.OrderedMap[*oas.MediaType], dir direction) {
	for mt, m := range content.All() {
		m = m.Deref()
		if m == nil {
			continue
		}
		schema, items := m.Schema, false
		if m.ItemSchema != nil {
			// JSON Lines and the like: an example is an item, or an array
			// of them.
			schema, items = m.ItemSchema, true
		}
		if schema == nil {
			continue
		}
		data := dataModel(mt)
		check := func(obj any, tokens []string, v json.RawMessage) {
			var arr []json.RawMessage
			if items && json.Unmarshal(v, &arr) == nil {
				for i, item := range arr {
					e.check(obj, append(tokens, fmt.Sprint(i)), item, schema, dir)
				}
				return
			}
			e.check(obj, tokens, v, schema, dir)
		}
		if data && m.Example != nil {
			check(m, []string{"example"}, m.Example)
		}
		for _, ex := range m.Examples.All() {
			ex = ex.Deref()
			if ex == nil {
				continue
			}
			if data && ex.Value != nil {
				check(ex, []string{"value"}, ex.Value)
			}
			if ex.DataValue != nil {
				check(ex, []string{"dataValue"}, ex.DataValue)
			}
		}
	}
}

// values checks the example and examples of a parameter or header.
func (e *exampleChecker) values(owner any, example json.RawMessage, examples *oas.OrderedMap[*oas.Example], schema *oas.Schema, dir direction) {
	if schema == nil {
		return
	}
	if example != nil {
		e.check(owner, []string{"example"}, example, schema, dir)
	}
	for _, ex := range examples.All() {
		if ex = ex.Deref(); ex == nil {
			continue
		}
		if ex.Value != nil {
			e.check(ex, []string{"value"}, ex.Value, schema, dir)
		}
		if ex.DataValue != nil {
			e.check(ex, []string{"dataValue"}, ex.DataValue, schema, dir)
		}
	}
}

// dataModel reports whether the example and value of a media type are its
// data, as JSON, form and multipart bodies and text have them, rather
// than the serialized document, as XML and binary bodies usually have
// them (3.2's dataValue says so either way).
func dataModel(mediaType string) bool {
	mt, _, _ := strings.Cut(strings.ToLower(mediaType), ";")
	mt = strings.TrimSpace(mt)
	switch {
	case mt == "application/json" || strings.HasSuffix(mt, "+json"):
		return true
	case mt == "application/x-www-form-urlencoded" || strings.HasPrefix(mt, "multipart/"):
		return true
	case mt == "application/jsonl" || mt == "application/x-ndjson":
		return true
	case mt == "text/event-stream":
		return false
	case strings.HasSuffix(mt, "/xml") || strings.HasSuffix(mt, "+xml"):
		return false
	}
	return strings.HasPrefix(mt, "text/")
}

// check reports how value, an example at tokens of obj, does not match
// schema, as it is sent in direction dir.
func (e *exampleChecker) check(obj any, tokens []string, value json.RawMessage, schema *oas.Schema, dir direction) {
	key := exampleKey{owner: obj, tokens: strings.Join(tokens, "/"), schema: schema}
	if e.seen[key] {
		return
	}
	e.seen[key] = true
	compiled := e.schemas.Compile(schema)
	if compiled == nil {
		return
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(value))
	if err != nil {
		// Not JSON: the document does not parse, which is reported first.
		return
	}
	var problems []string
	for _, p := range e.schemas.Check(compiled, inst, dir) {
		where := p.Pointer
		if where == "" {
			where = "/"
		}
		problems = append(problems, where+": "+p.Message)
	}
	if len(problems) > 0 {
		e.c.report(Warning, RuleExampleSchema, obj, tokens, "the example does not match its schema: %s", strings.Join(problems, "; "))
	}
}
