package loader

import (
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/dheins/go-openapi/oas"
)

// upgradedVersion is the "openapi" value given to upgraded 3.0 documents.
// The rewritten content is valid 3.1 (and therefore valid for the 3.2
// model); it uses no 3.2-only fields.
const upgradedVersion = "3.1.0"

// upgrade30 rewrites the OpenAPI 3.0 content of n, which is about to be
// decoded as the model type of as, into its 3.1 equivalent. The only
// incompatible changes between 3.0 and 3.1 are in Schema Objects, so this
// finds every schema reachable from n and rewrites:
//
//   - nullable: true          → "null" added to type
//   - exclusiveMinimum: true  → exclusiveMinimum: <minimum> (same for maximum)
//   - example: v              → examples: [v]
//
// It is idempotent, so a node reached through two references may be
// upgraded twice. Node positions are kept for diagnostics.
func upgrade30(n *yaml.Node, as any) {
	u := &upgrader{seen: make(map[*yaml.Node]bool)}
	switch as.(type) {
	case *oas.Document:
		u.upgradeDocument(n)
	case *oas.Components:
		u.upgradeComponents(n)
	case *oas.PathItem:
		u.upgradePathItem(n)
	case *oas.Operation:
		u.upgradeOperation(n)
	case *oas.Parameter, *oas.Header:
		u.upgradeParameter(n)
	case *oas.RequestBody:
		u.upgradeContent(mappingValue(n, "content"))
	case *oas.MediaType:
		u.upgradeMediaType(n)
	case *oas.Response:
		u.upgradeResponse(n)
	case *oas.Callback:
		u.upgradeCallback(n)
	case *oas.Schema:
		u.upgradeSchema(n)
	}
}

func (u *upgrader) upgradeDocument(n *yaml.Node) {
	if u.visited(n) {
		return
	}
	if v := mappingValue(n, "openapi"); v != nil && v.Kind == yaml.ScalarNode {
		v.Value = upgradedVersion
	}
	eachValue(mappingValue(n, "paths"), u.upgradePathItem)
	u.upgradeComponents(mappingValue(n, "components"))
}

func (u *upgrader) upgradeComponents(n *yaml.Node) {
	if u.visited(n) {
		return
	}
	eachValue(mappingValue(n, "schemas"), u.upgradeSchema)
	eachValue(mappingValue(n, "responses"), u.upgradeResponse)
	eachValue(mappingValue(n, "parameters"), u.upgradeParameter)
	eachValue(mappingValue(n, "requestBodies"), func(rb *yaml.Node) {
		if !isRef(rb) {
			u.upgradeContent(mappingValue(rb, "content"))
		}
	})
	eachValue(mappingValue(n, "headers"), u.upgradeParameter)
	eachValue(mappingValue(n, "callbacks"), u.upgradeCallback)
}

func (u *upgrader) upgradePathItem(n *yaml.Node) {
	if u.visited(n) {
		return
	}
	// A 3.0 Path Item may combine $ref with its own fields, so it is not
	// skipped like other references.
	for _, m := range []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"} {
		u.upgradeOperation(mappingValue(n, m))
	}
	eachItem(mappingValue(n, "parameters"), u.upgradeParameter)
}

func (u *upgrader) upgradeOperation(n *yaml.Node) {
	if u.visited(n) {
		return
	}
	if n == nil {
		return
	}
	eachItem(mappingValue(n, "parameters"), u.upgradeParameter)
	if rb := mappingValue(n, "requestBody"); !isRef(rb) {
		u.upgradeContent(mappingValue(rb, "content"))
	}
	eachValue(mappingValue(n, "responses"), u.upgradeResponse)
	eachValue(mappingValue(n, "callbacks"), u.upgradeCallback)
}

func (u *upgrader) upgradeCallback(n *yaml.Node) {
	if u.visited(n) {
		return
	}
	if isRef(n) {
		return
	}
	eachValue(n, u.upgradePathItem)
}

// upgradeParameter handles Parameter and Header Objects, which share the
// schema/content fields.
func (u *upgrader) upgradeParameter(n *yaml.Node) {
	if u.visited(n) {
		return
	}
	if isRef(n) {
		return
	}
	u.upgradeSchema(mappingValue(n, "schema"))
	u.upgradeContent(mappingValue(n, "content"))
}

func (u *upgrader) upgradeResponse(n *yaml.Node) {
	if u.visited(n) {
		return
	}
	if isRef(n) {
		return
	}
	eachValue(mappingValue(n, "headers"), u.upgradeParameter)
	u.upgradeContent(mappingValue(n, "content"))
}

func (u *upgrader) upgradeContent(n *yaml.Node) {
	if u.visited(n) {
		return
	}
	eachValue(n, u.upgradeMediaType)
}

func (u *upgrader) upgradeMediaType(n *yaml.Node) {
	if u.visited(n) {
		return
	}
	if n == nil {
		return
	}
	u.upgradeSchema(mappingValue(n, "schema"))
	eachValue(mappingValue(n, "encoding"), func(enc *yaml.Node) {
		eachValue(mappingValue(enc, "headers"), u.upgradeParameter)
	})
}

func (u *upgrader) upgradeSchema(n *yaml.Node) {
	if u.visited(n) {
		return
	}
	n = resolveAlias(n)
	// In 3.0 the siblings of $ref are ignored, so there is nothing to
	// upgrade next to one; the target is upgraded where it is defined.
	if n == nil || n.Kind != yaml.MappingNode || isRef(n) {
		return
	}

	if nullable := removeKey(n, "nullable"); isTrue(nullable) {
		// 3.0.3 clarifies that nullable only has an effect together with type.
		if t := mappingValue(n, "type"); t != nil && t.Kind == yaml.ScalarNode {
			null := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "null", Line: nullable.Line, Column: nullable.Column}
			typ := *t
			*t = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Line: t.Line, Column: t.Column, Content: []*yaml.Node{&typ, null}}
		}
	}
	upgradeExclusive(n, "exclusiveMinimum", "minimum")
	upgradeExclusive(n, "exclusiveMaximum", "maximum")
	if mappingValue(n, "examples") == nil {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == "example" {
				n.Content[i].Value = "examples"
				v := n.Content[i+1]
				n.Content[i+1] = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Line: v.Line, Column: v.Column, Content: []*yaml.Node{v}}
				break
			}
		}
	}

	eachValue(mappingValue(n, "properties"), u.upgradeSchema)
	u.upgradeSchema(mappingValue(n, "items"))
	u.upgradeSchema(mappingValue(n, "additionalProperties"))
	u.upgradeSchema(mappingValue(n, "not"))
	for _, k := range []string{"allOf", "anyOf", "oneOf"} {
		eachItem(mappingValue(n, k), u.upgradeSchema)
	}
}

// upgradeExclusive turns the 3.0 boolean form (exclusiveMinimum: true next
// to minimum: 5) into the 3.1 numeric form (exclusiveMinimum: 5).
func upgradeExclusive(n *yaml.Node, exclusiveKey, boundKey string) {
	ex := mappingValue(n, exclusiveKey)
	if ex == nil || ex.Kind != yaml.ScalarNode || ex.ShortTag() != "!!bool" {
		return
	}
	removeKey(n, exclusiveKey)
	if !isTrue(ex) {
		return
	}
	if bound := removeKey(n, boundKey); bound != nil {
		key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: exclusiveKey, Line: ex.Line, Column: ex.Column}
		n.Content = append(n.Content, key, bound)
	}
}

// upgrader rewrites 3.0 content, visiting each node once: an alias leads
// to a node that is upgraded where it is first reached, and following
// aliases again could expand exponentially ("billion laughs").
type upgrader struct {
	seen map[*yaml.Node]bool
}

// visited reports whether n (after aliases) is nil or was seen already,
// and marks it as seen.
func (u *upgrader) visited(n *yaml.Node) bool {
	n = resolveAlias(n)
	if n == nil || u.seen[n] {
		return true
	}
	u.seen[n] = true
	return false
}

func isRef(n *yaml.Node) bool {
	return mappingValue(n, "$ref") != nil
}

func isTrue(n *yaml.Node) bool {
	return n != nil && n.Kind == yaml.ScalarNode && n.ShortTag() == "!!bool" && n.Value == "true"
}

// removeKey deletes key from the mapping n and returns its value node.
func removeKey(n *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			v := n.Content[i+1]
			n.Content = append(n.Content[:i], n.Content[i+2:]...)
			return resolveAlias(v)
		}
	}
	return nil
}

// eachValue calls fn for every value of the mapping n, skipping "x-"
// extensions.
func eachValue(n *yaml.Node, fn func(*yaml.Node)) {
	n = resolveAlias(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if !strings.HasPrefix(n.Content[i].Value, "x-") {
			fn(resolveAlias(n.Content[i+1]))
		}
	}
}

func eachItem(n *yaml.Node, fn func(*yaml.Node)) {
	n = resolveAlias(n)
	if n == nil || n.Kind != yaml.SequenceNode {
		return
	}
	for _, item := range n.Content {
		fn(resolveAlias(item))
	}
}
