package validate

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/dheins/go-openapi/loader"
	"github.com/dheins/go-openapi/oas"
)

// checker runs the semantic rules over a loaded document and the fragments
// it references.
type checker struct {
	spec  *loader.Spec
	diags []Diagnostic

	pathItems      []*oas.PathItem
	operations     []*oas.Operation
	servers        []*oas.Server
	links          []*oas.Link
	discriminators []*oas.Schema
	allOfs         []*oas.Schema
	oneOfs         []*oas.Schema
}

func semantic(spec *loader.Spec) ([]Diagnostic, error) {
	c := &checker{spec: spec}
	if err := c.collect(); err != nil {
		return nil, err
	}
	c.checkOperationIDs()
	c.checkPaths()
	c.checkParameters()
	c.checkServers()
	c.checkSecurity()
	c.checkTags()
	c.checkLinks()
	c.checkDiscriminators()
	c.checkAllOfConflicts()
	c.checkOneOfAmbiguity()
	slices.SortStableFunc(c.diags, func(a, b Diagnostic) int {
		return cmp.Or(
			strings.Compare(a.Location.URI, b.Location.URI),
			cmp.Compare(a.Position.Line, b.Position.Line),
			cmp.Compare(a.Position.Column, b.Position.Column),
		)
	})
	return c.diags, nil
}

// collect gathers the objects the rules look at from the document and all
// fragments. An object is taken once per source location, even when two
// overlapping fragments both decoded it.
func (c *checker) collect() error {
	seen := make(map[loader.Location]bool)
	visit := func(obj any, _ string, _ *url.URL) error {
		loc, ok := c.spec.Location(obj)
		if !ok || seen[loc] {
			return nil
		}
		seen[loc] = true
		switch o := obj.(type) {
		case *oas.PathItem:
			c.pathItems = append(c.pathItems, o)
		case *oas.Operation:
			c.operations = append(c.operations, o)
		case *oas.Server:
			c.servers = append(c.servers, o)
		case *oas.Link:
			c.links = append(c.links, o)
		case *oas.Schema:
			if o.Discriminator != nil {
				c.discriminators = append(c.discriminators, o)
			}
			if len(o.AllOf) > 0 {
				c.allOfs = append(c.allOfs, o)
			}
			if len(o.OneOf) > 1 && o.Discriminator == nil {
				c.oneOfs = append(c.oneOfs, o)
			}
		}
		return nil
	}
	if err := oas.Walk(c.spec.Document, "", nil, visit); err != nil {
		return err
	}
	for _, f := range c.spec.Fragments() {
		if err := oas.Walk(f.Object, "", nil, visit); err != nil {
			return err
		}
	}
	return nil
}

// report adds a diagnostic at obj's location, extended by tokens.
func (c *checker) report(sev Severity, rule string, obj any, tokens []string, format string, args ...any) {
	loc, _ := c.spec.Location(obj)
	loc.Pointer = oas.JoinPointer(loc.Pointer, tokens...)
	c.diags = append(c.diags, Diagnostic{
		Severity: sev, Rule: rule, Message: fmt.Sprintf(format, args...),
		Location: loc, Position: c.spec.Position(loc),
	})
}

// where formats obj's position for use inside a message.
func (c *checker) where(obj any) string {
	loc, _ := c.spec.Location(obj)
	if pos := c.spec.Position(loc); pos.IsValid() {
		return fmt.Sprintf("%s:%d:%d", loc.File(), pos.Line, pos.Column)
	}
	return loc.String()
}

func (c *checker) checkOperationIDs() {
	first := make(map[string]*oas.Operation)
	for _, op := range c.operations {
		if op.OperationID == "" {
			continue
		}
		if prev, ok := first[op.OperationID]; ok {
			c.report(Error, RuleOperationID, op, []string{"operationId"},
				"operationId %q is already used at %s", op.OperationID, c.where(prev))
			continue
		}
		first[op.OperationID] = op
	}
}

var templateParam = regexp.MustCompile(`\{([^{}]+)\}`)

// checkPaths checks path templates against their path parameters, and
// that no two templates are equivalent.
func (c *checker) checkPaths() {
	paths := c.spec.Document.Paths
	if paths == nil {
		return
	}
	equivalent := make(map[string]string)
	for path, item := range paths.Items.All() {
		if item == nil {
			continue
		}
		normalized := templateParam.ReplaceAllString(path, "{}")
		if prev, ok := equivalent[normalized]; ok {
			c.report(Error, RulePathUnique, paths, []string{path},
				"path %q is equivalent to %q; templated paths must differ by more than parameter names", path, prev)
		} else {
			equivalent[normalized] = path
		}

		names := make(map[string]bool)
		for _, m := range templateParam.FindAllStringSubmatch(path, -1) {
			if names[m[1]] {
				c.report(Error, RulePathParameters, paths, []string{path}, "path template repeats parameter {%s}", m[1])
			}
			names[m[1]] = true
		}

		pi := item.Deref()
		for _, p := range pi.Parameters {
			if dp := p.Deref(); dp.In == "path" && !names[dp.Name] {
				c.report(Error, RulePathParameters, p, nil, "path parameter %q does not appear in path %q", dp.Name, path)
			}
		}
		for _, op := range pathOperations(pi) {
			declared := make(map[string]bool)
			for _, p := range effectiveParameters(pi, op) {
				if dp := p.Deref(); dp.In == "path" {
					declared[dp.Name] = true
				}
			}
			for _, p := range op.Parameters {
				if dp := p.Deref(); dp.In == "path" && !names[dp.Name] {
					c.report(Error, RulePathParameters, p, nil, "path parameter %q does not appear in path %q", dp.Name, path)
				}
			}
			for _, m := range templateParam.FindAllStringSubmatch(path, -1) {
				if !declared[m[1]] {
					c.report(Error, RulePathParameters, op, nil, "path parameter {%s} of %q is not declared", m[1], path)
				}
			}
		}
	}
}

func pathOperations(pi *oas.PathItem) []*oas.Operation {
	var ops []*oas.Operation
	for _, op := range pi.Operations() {
		ops = append(ops, op)
	}
	return ops
}

// paramKey identifies a parameter; header names are case-insensitive.
func paramKey(p *oas.Parameter) string {
	name := p.Name
	if p.In == "header" {
		name = strings.ToLower(name)
	}
	return p.In + ":" + name
}

// effectiveParameters merges path-level parameters with an operation's,
// which override them by name and location.
func effectiveParameters(pi *oas.PathItem, op *oas.Operation) []*oas.Parameter {
	overridden := make(map[string]bool)
	for _, p := range op.Parameters {
		overridden[paramKey(p.Deref())] = true
	}
	var params []*oas.Parameter
	for _, p := range pi.Parameters {
		if !overridden[paramKey(p.Deref())] {
			params = append(params, p)
		}
	}
	return append(params, op.Parameters...)
}

// checkParameters checks every parameter list for duplicates and every
// operation for querystring misuse.
func (c *checker) checkParameters() {
	unique := func(list []*oas.Parameter) {
		seen := make(map[string]bool)
		for _, p := range list {
			dp := p.Deref()
			key := paramKey(dp)
			if seen[key] {
				c.report(Error, RuleParameterUnique, p, nil, "duplicate parameter %q in %s", dp.Name, dp.In)
			}
			seen[key] = true
		}
	}
	for _, pi := range c.pathItems {
		unique(pi.Parameters)
		for _, op := range pathOperations(pi) {
			unique(op.Parameters)

			var querystring, query int
			for _, p := range effectiveParameters(pi, op) {
				switch p.Deref().In {
				case "querystring":
					querystring++
				case "query":
					query++
				}
			}
			if querystring > 1 {
				c.report(Error, RuleQuerystring, op, nil, "an operation may have at most one querystring parameter, found %d", querystring)
			}
			if querystring > 0 && query > 0 {
				c.report(Error, RuleQuerystring, op, nil, "querystring and query parameters cannot be used together")
			}
		}
	}
}

func (c *checker) checkServers() {
	for _, s := range c.servers {
		used := make(map[string]bool)
		for _, m := range templateParam.FindAllStringSubmatch(s.URL, -1) {
			used[m[1]] = true
			if _, ok := s.Variables.Get(m[1]); !ok {
				c.report(Error, RuleServerVariables, s, []string{"url"}, "server variable {%s} is not defined", m[1])
			}
		}
		for name, v := range s.Variables.All() {
			if v == nil {
				continue
			}
			if len(v.Enum) > 0 && !slices.Contains(v.Enum, v.Default) {
				c.report(Error, RuleServerVariables, s, []string{"variables", name, "default"},
					"default %q is not one of the enum values %q", v.Default, v.Enum)
			}
			if !used[name] {
				c.report(Warning, RuleServerVariables, s, []string{"variables", name}, "server variable %q is not used in the URL", name)
			}
		}
	}
}

func (c *checker) checkSecurity() {
	doc := c.spec.Document
	var schemes *oas.OrderedMap[*oas.SecurityScheme]
	if doc.Components != nil {
		schemes = doc.Components.SecuritySchemes
	}
	check := func(owner any, reqs []oas.SecurityRequirement) {
		for i, req := range reqs {
			for name := range req.All() {
				if _, ok := schemes.Get(name); ok || isURIReference(name) {
					continue
				}
				c.report(Error, RuleSecuritySchemes, owner, []string{"security", strconv.Itoa(i), name},
					"security scheme %q is not defined in components.securitySchemes", name)
			}
		}
	}
	check(doc, doc.Security)
	for _, op := range c.operations {
		check(op, op.Security)
	}
}

// isURIReference reports whether a security requirement name is written
// as a URI reference to a scheme rather than a component name, which 3.2
// allows. Component names cannot contain these characters.
func isURIReference(name string) bool {
	return strings.ContainsAny(name, "#/:")
}

func (c *checker) checkTags() {
	doc := c.spec.Document
	byName := make(map[string]*oas.Tag)
	for i, t := range doc.Tags {
		if t == nil {
			continue
		}
		if _, dup := byName[t.Name]; dup {
			c.report(Error, RuleTags, doc, []string{"tags", strconv.Itoa(i), "name"}, "duplicate tag %q", t.Name)
			continue
		}
		byName[t.Name] = t
	}
	for i, t := range doc.Tags {
		if t == nil || t.Parent == "" {
			continue
		}
		if _, ok := byName[t.Parent]; !ok {
			c.report(Error, RuleTags, doc, []string{"tags", strconv.Itoa(i), "parent"}, "parent tag %q is not defined", t.Parent)
			continue
		}
		visited := map[string]bool{t.Name: true}
		for p := byName[t.Parent]; p != nil; p = byName[p.Parent] {
			if visited[p.Name] {
				c.report(Error, RuleTags, doc, []string{"tags", strconv.Itoa(i), "parent"}, "tag %q is its own ancestor", t.Name)
				break
			}
			visited[p.Name] = true
		}
	}
}

func (c *checker) checkLinks() {
	ids := make(map[string]bool)
	for _, op := range c.operations {
		ids[op.OperationID] = true
	}
	for _, l := range c.links {
		if l.Ref != "" {
			continue
		}
		if l.OperationID != "" && l.OperationRef != "" {
			c.report(Error, RuleLinkOperation, l, nil, "a link must not have both operationId and operationRef")
		}
		if l.OperationID != "" && !ids[l.OperationID] {
			c.report(Error, RuleLinkOperation, l, []string{"operationId"}, "no operation has operationId %q", l.OperationID)
		}
	}
}

// checkDiscriminators warns about discriminator properties that are not
// required. Unresolvable mapping values are reported by the loader.
func (c *checker) checkDiscriminators() {
	for _, s := range c.discriminators {
		d := s.Discriminator
		if d.DefaultMapping == "" && !requires(s, d.PropertyName, map[*oas.Schema]bool{}) {
			c.report(Warning, RuleDiscriminatorProp, s, []string{"discriminator", "propertyName"},
				"discriminator property %q is not a required property", d.PropertyName)
		}
	}
}

// checkAllOfConflicts reports properties that allOf members define with
// types or values no value can satisfy at once: such an object is never
// valid. A conflict is reported once, at the innermost schema whose
// members define both sides.
func (c *checker) checkAllOfConflicts() {
	reported := make(map[[2]*oas.Schema]bool)
	for _, s := range c.allOfs {
		shape := shapeOf(s)
		for _, name := range slices.Sorted(maps.Keys(shape.props)) {
			defs := shape.props[name]
		pairs:
			for i, a := range defs {
				for _, b := range defs[i+1:] {
					if compatible(a, b) || reported[[2]*oas.Schema{a, b}] {
						continue
					}
					reported[[2]*oas.Schema{a, b}] = true
					c.report(Error, RuleAllOfConflict, s, []string{"allOf"},
						"property %q is defined as %s and as %s by allOf members; no value is both", name, allowed(a), allowed(b))
					break pairs
				}
			}
		}
	}
}

// checkOneOfAmbiguity warns about a oneOf without a discriminator whose
// members are objects one value can satisfy together. oneOf rejects such
// a value, which is rarely what the document means.
func (c *checker) checkOneOfAmbiguity() {
	for _, s := range c.oneOfs {
		var objects []int
		var shapes []objectShape
		for i, m := range s.OneOf {
			// Inline members without a type, such as {required: [email]},
			// are constraints of the object rather than variants of it,
			// and are meant to exclude each other.
			if (m.Ref != "" || len(m.Type) > 0) && isObjectSchema(deref(m)) {
				objects = append(objects, i)
				shapes = append(shapes, shapeOf(m))
			}
		}
	pairs:
		for i := range objects {
			for j := i + 1; j < len(objects); j++ {
				if overlap(shapes[i], shapes[j]) {
					c.report(Warning, RuleOneOfAmbiguous, s, []string{"oneOf"},
						"oneOf members %d and %d can both match the same object, which oneOf then rejects; add a discriminator, or required properties that tell them apart",
						objects[i], objects[j])
					break pairs
				}
			}
		}
	}
}

// objectShape is what an object schema, with its allOf members, says
// about properties.
type objectShape struct {
	props    map[string][]*oas.Schema
	required map[string]bool
	// closed means properties it does not define are not allowed.
	closed bool
}

func shapeOf(s *oas.Schema) objectShape {
	shape := objectShape{props: make(map[string][]*oas.Schema), required: make(map[string]bool)}
	if top := deref(s); top != nil {
		shape.closed = closes(top.AdditionalProperties) || closes(top.UnevaluatedProperties)
	}
	seen := make(map[*oas.Schema]bool)
	var walk func(*oas.Schema)
	walk = func(s *oas.Schema) {
		s = deref(s)
		if s == nil || seen[s] {
			return
		}
		seen[s] = true
		for name, p := range s.Properties.All() {
			shape.props[name] = append(shape.props[name], deref(p))
		}
		for _, name := range s.Required {
			shape.required[name] = true
		}
		for _, m := range s.AllOf {
			walk(m)
		}
	}
	walk(s)
	return shape
}

// overlap reports whether one object could satisfy both shapes: every
// property either requires is allowed by both, with compatible
// definitions.
func overlap(a, b objectShape) bool {
	names := slices.Collect(maps.Keys(a.required))
	for name := range b.required {
		names = append(names, name)
	}
	for _, name := range names {
		da, db := a.props[name], b.props[name]
		if (len(da) == 0 && a.closed) || (len(db) == 0 && b.closed) {
			return false
		}
		for _, x := range da {
			for _, y := range db {
				if !compatible(x, y) {
					return false
				}
			}
		}
	}
	return true
}

// compatible reports whether some value could satisfy both a and b, as far
// as their types and enum or const values tell.
func compatible(a, b *oas.Schema) bool {
	if a == nil || b == nil {
		return true
	}
	if len(a.Type) > 0 && len(b.Type) > 0 && !slices.ContainsFunc(a.Type, func(t string) bool {
		return slices.ContainsFunc(b.Type, func(u string) bool { return sameType(t, u) })
	}) {
		return false
	}
	va, vb := values(a), values(b)
	return va == nil || vb == nil || slices.ContainsFunc(va, func(v string) bool { return slices.Contains(vb, v) })
}

// sameType reports whether JSON Schema types t and u share values;
// integers are numbers.
func sameType(t, u string) bool {
	return t == u || (t == "integer" && u == "number") || (t == "number" && u == "integer")
}

// values returns the compacted JSON values s allows by const or enum, or
// nil if it does not restrict them.
func values(s *oas.Schema) []string {
	raw := s.Enum
	if s.Const != nil {
		raw = []json.RawMessage{s.Const}
	}
	if raw == nil {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		var buf bytes.Buffer
		if json.Compact(&buf, v) != nil {
			return nil
		}
		out = append(out, buf.String())
	}
	return out
}

// allowed names what s allows, for messages.
func allowed(s *oas.Schema) string {
	if v := values(s); v != nil {
		return strings.Join(v, " or ")
	}
	return strings.Join(s.Type, " or ")
}

func isObjectSchema(s *oas.Schema) bool {
	if s == nil {
		return false
	}
	if len(s.Type) > 0 {
		return len(s.Type) == 1 && s.Type[0] == "object"
	}
	return s.Properties.Len() > 0 || len(s.Required) > 0
}

func closes(s *oas.Schema) bool {
	return s != nil && s.Bool != nil && !*s.Bool
}

// deref follows $refs to a schema with content of its own.
func deref(s *oas.Schema) *oas.Schema {
	seen := make(map[*oas.Schema]bool)
	for s != nil && s.Ref != "" && s.Resolved != nil && !seen[s] {
		seen[s] = true
		s = s.Resolved
	}
	return s
}

// requires reports whether s, or one of its allOf members, lists prop as
// required. seen guards against allOf reference cycles.
func requires(s *oas.Schema, prop string, seen map[*oas.Schema]bool) bool {
	if s == nil || seen[s] {
		return false
	}
	seen[s] = true
	if slices.Contains(s.Required, prop) {
		return true
	}
	for _, sub := range s.AllOf {
		if requires(sub, prop, seen) || requires(sub.Resolved, prop, seen) {
			return true
		}
	}
	return false
}
