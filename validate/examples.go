package validate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"

	"github.com/dheins/go-openapi/oas"
)

// direction is where an example is sent: in a request, where read-only
// properties need not be, in a response, where write-only ones need not
// be, or either, for an example in a schema.
type direction int

const (
	eitherWay direction = iota
	inRequest
	inResponse
)

// exampleChecker checks examples against the schemas they are examples
// of, compiled where they are in the loaded files, so that references
// resolve as the loader resolved them.
type exampleChecker struct {
	c        *checker
	compiler *jsonschema.Compiler
	compiled map[string]*jsonschema.Schema
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
	e := &exampleChecker{c: c, compiler: jsonschema.NewCompiler(), compiled: map[string]*jsonschema.Schema{}, seen: map[exampleKey]bool{}}
	e.compiler.AssertFormat()
	// Every file is added below; nothing else is fetched.
	e.compiler.UseLoader(noLoader{})
	for _, src := range c.spec.Sources() {
		data, err := src.JSON()
		if err != nil {
			return err
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			return err
		}
		if err := e.compiler.AddResource(src.URI(), doc); err != nil {
			return err
		}
	}
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
	compiled := e.schema(schema)
	if compiled == nil {
		return
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(value))
	if err != nil {
		// Not JSON: the document does not parse, which is reported first.
		return
	}
	var ve *jsonschema.ValidationError
	if err := compiled.Validate(inst); !errors.As(err, &ve) {
		return
	}
	var problems []string
	collectLeaves(ve, func(leaf *jsonschema.ValidationError) {
		if msg := e.problem(leaf, dir); msg != "" {
			problems = append(problems, msg)
		}
	})
	if len(problems) > 0 {
		e.c.report(Warning, RuleExampleSchema, obj, tokens, "the example does not match its schema: %s", strings.Join(problems, "; "))
	}
}

// problem describes a leaf error, or returns "" for one that does not
// apply in direction dir: a missing required property that is read-only
// in a request, or write-only in a response.
func (e *exampleChecker) problem(leaf *jsonschema.ValidationError, dir direction) string {
	where := oas.JoinPointer("", leaf.InstanceLocation...)
	if where == "" {
		where = "/"
	}
	if req, ok := leaf.ErrorKind.(*kind.Required); ok {
		var missing []string
		for _, name := range req.Missing {
			if !e.exempt(leaf.SchemaURL, name, dir) {
				missing = append(missing, name)
			}
		}
		if len(missing) == 0 {
			return ""
		}
		req = &kind.Required{Missing: missing}
		return where + ": " + req.LocalizedString(printer)
	}
	return where + ": " + describe(leaf)
}

// exempt reports whether the property name of the schema at schemaURL
// need not be in a value sent in direction dir.
func (e *exampleChecker) exempt(schemaURL, name string, dir direction) bool {
	s, err := e.compiler.Compile(strings.TrimSuffix(schemaURL, "/required"))
	if err != nil {
		return false
	}
	prop := s.Properties[name]
	for prop != nil {
		switch {
		case dir != inResponse && prop.ReadOnly, dir != inRequest && prop.WriteOnly:
			return true
		}
		prop = prop.Ref
	}
	return false
}

// schema returns schema, compiled where it is, or nil if it cannot be:
// an invalid schema is reported by the structural checks.
func (e *exampleChecker) schema(schema *oas.Schema) *jsonschema.Schema {
	loc, ok := e.c.spec.Location(schema)
	if !ok {
		return nil
	}
	u := loc.URI + "#" + (&url.URL{Fragment: loc.Pointer}).EscapedFragment()
	if s, ok := e.compiled[u]; ok {
		return s
	}
	s, err := e.compiler.Compile(u)
	if err != nil {
		s = nil
	}
	e.compiled[u] = s
	return s
}

// noLoader refuses to load anything: every file of the document is
// added to the compiler as a resource.
type noLoader struct{}

func (noLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("%s is not part of the document", url)
}
