package loader

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/dheins/go-openapi/oas"
)

// convertedVersion is the "openapi" value given to converted Swagger 2.0
// documents, as to upgraded 3.0 ones.
const convertedVersion = upgradedVersion

// converter rewrites Swagger 2.0 content into its OpenAPI 3.1 equivalent,
// in place: the root document (document), then the objects of other files
// that references lead to (fragment). Nodes that are moved keep their
// positions, and nodes it creates take the position of the 2.0 field they
// come from, so diagnostics point into the 2.0 files. Only the root is
// restructured, so only its references are rewritten; other files are
// read as written.
type converter struct {
	// notices are what could not be converted.
	notices []Notice
	seen    map[*yaml.Node]bool
	// root is the URI of the root document, and uri that of the file
	// being converted.
	root, uri string
	// lookup returns the node ref leads to from the file at base, or nil;
	// it is how references to other files are followed.
	lookup func(base, ref string) *yaml.Node
	// consumes and produces are the root's default media types.
	consumes, produces []string
	// forms are the formData parameters of the root's #/parameters,
	// inlined into the bodies that refer to them, and bodies its body
	// parameters, which become request bodies (or are inlined for other
	// media types).
	forms, bodies map[string]*yaml.Node
	// renamed are the new references of renamed components, by their
	// unescaped old ones.
	renamed map[string]string
}

func newConverter(lookup func(base, ref string) *yaml.Node) *converter {
	return &converter{seen: map[*yaml.Node]bool{}, lookup: lookup, forms: map[string]*yaml.Node{}, bodies: map[string]*yaml.Node{}}
}

func (c *converter) warn(at *yaml.Node, format string, args ...any) {
	var pos Position
	if at != nil {
		pos = Position{Line: at.Line, Column: at.Column}
	}
	c.notices = append(c.notices, Notice{Location: Location{URI: c.uri}, Position: pos, Message: fmt.Sprintf(format, args...)})
}

// inRoot reports whether the root document is being converted.
func (c *converter) inRoot() bool { return c.uri == c.root }

// fragment converts n, an object of the file at uri that a reference
// leads to, as the model type of as.
func (c *converter) fragment(n *yaml.Node, as any, uri string) {
	saved := c.uri
	c.uri = uri
	defer func() { c.uri = saved }()
	switch as.(type) {
	case *oas.Schema:
		c.schema(n)
	case *oas.Parameter:
		c.parameter(n)
	case *oas.RequestBody:
		// A body parameter of another file, which the root refers to.
		if in := mappingValue(n, "in"); in != nil && in.Value == "body" && !c.visited(n) {
			*n = *c.requestBody(copyNode(n), c.consumes)
		}
	case *oas.Response:
		c.response(n, c.produces)
	case *oas.Header:
		c.header(n)
	case *oas.PathItem:
		c.pathItem(n)
	case *oas.SecurityScheme:
		c.securityScheme(n)
	}
}

// visited reports whether n is nil or was converted already, and marks it.
func (c *converter) visited(n *yaml.Node) bool {
	n = resolveAlias(n)
	if n == nil || c.seen[n] {
		return true
	}
	c.seen[n] = true
	return false
}

// str returns a string scalar at the position of at.
func str(s string, at *yaml.Node) *yaml.Node {
	n := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
	if at != nil {
		n.Line, n.Column = at.Line, at.Column
	}
	return n
}

// mapping returns a mapping of the key and value pairs kv at the position
// of at.
func mapping(at *yaml.Node, kv ...*yaml.Node) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: kv}
	if at != nil {
		n.Line, n.Column = at.Line, at.Column
	}
	return n
}

// set sets key to v in the mapping n, replacing its value if present.
func set(n *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			n.Content[i+1] = v
			return
		}
	}
	n.Content = append(n.Content, str(key, v), v)
}

// keyIndex returns the index in n.Content of key, or of the end of the
// mapping n if it has no key.
func keyIndex(n *yaml.Node, key string) int {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return i
		}
	}
	return len(n.Content)
}

// rename renames the key from to to in the mapping n, keeping its place.
func rename(n *yaml.Node, from, to string) {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == from {
			n.Content[i].Value = to
			return
		}
	}
}

// stringsOf returns the strings of the sequence n.
func stringsOf(n *yaml.Node) []string {
	var ss []string
	eachItem(n, func(item *yaml.Node) {
		if item.Kind == yaml.ScalarNode {
			ss = append(ss, item.Value)
		}
	})
	return ss
}

func copyNode(n *yaml.Node) *yaml.Node {
	n = resolveAlias(n)
	if n == nil {
		return nil
	}
	cp := *n
	cp.Content = make([]*yaml.Node, len(n.Content))
	for i, child := range n.Content {
		cp.Content[i] = copyNode(child)
	}
	return &cp
}

// document converts n, the root document at uri.
func (c *converter) document(n *yaml.Node, uri string) {
	c.root, c.uri = uri, uri
	n = resolveAlias(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return
	}
	c.consumes = stringsOf(removeKey(n, "consumes"))
	c.produces = stringsOf(removeKey(n, "produces"))

	// Components first: operations need to know which parameters of
	// #/parameters are bodies or form fields.
	components := mapping(mappingValue(n, "definitions"))
	if defs := removeKey(n, "definitions"); defs != nil {
		eachEntry(defs, c.schema)
		components.Content = append(components.Content, str("schemas", defs), defs)
	}
	if params := removeKey(n, "parameters"); params != nil {
		c.componentParameters(params, components)
	}
	if resps := removeKey(n, "responses"); resps != nil {
		eachValue(resps, func(r *yaml.Node) { c.response(r, c.produces) })
		components.Content = append(components.Content, str("responses", resps), resps)
	}
	if secs := removeKey(n, "securityDefinitions"); secs != nil {
		eachEntry(secs, c.securityScheme)
		components.Content = append(components.Content, str("securitySchemes", secs), secs)
	}

	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == "swagger" {
			n.Content[i].Value = "openapi"
			n.Content[i+1] = str(convertedVersion, n.Content[i+1])
		}
	}
	c.servers(n)
	if paths := mappingValue(n, "paths"); paths != nil {
		eachValue(paths, c.pathItem)
	}
	if len(components.Content) > 0 {
		// After the paths, as 3.x documents are usually ordered.
		place := len(n.Content)
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == "paths" {
				place = i + 2
			}
		}
		n.Content = slices.Insert(n.Content, place, str("components", components), components)
	}
	c.renameComponents(n, components)
	c.refs(n, false)
}

var (
	componentName    = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
	notComponentName = regexp.MustCompile(`[^a-zA-Z0-9._-]`)
)

// renameComponents gives the components whose names 3.x does not allow
// (2.0 names have no pattern) names it does, recording their references
// for refs and renaming security schemes in the requirements of doc.
func (c *converter) renameComponents(doc, components *yaml.Node) {
	schemes := map[string]string{}
	for i := 0; i+1 < len(components.Content); i += 2 {
		kind, m := components.Content[i].Value, resolveAlias(components.Content[i+1])
		taken := map[string]bool{}
		for j := 0; j < len(m.Content); j += 2 {
			taken[m.Content[j].Value] = true
		}
		for j := 0; j < len(m.Content); j += 2 {
			key := m.Content[j]
			if componentName.MatchString(key.Value) {
				continue
			}
			base := notComponentName.ReplaceAllString(key.Value, "_")
			name := base
			for k := 2; taken[name]; k++ {
				name = fmt.Sprintf("%s_%d", base, k)
			}
			taken[name] = true
			c.warn(key, "%s %q is renamed %q: 3.x names have only letters, digits, '.', '_' and '-'", kind, key.Value, name)
			if c.renamed == nil {
				c.renamed = map[string]string{}
			}
			c.renamed[oas.JoinPointer("#/components", kind, key.Value)] = oas.JoinPointer("#/components", kind, name)
			if kind == "securitySchemes" {
				schemes[key.Value] = name
			}
			m.Content[j] = str(name, key)
		}
	}
	if len(schemes) == 0 {
		return
	}
	requirements := []*yaml.Node{mappingValue(doc, "security")}
	eachValue(mappingValue(doc, "paths"), func(item *yaml.Node) {
		for _, m := range operationMethods {
			requirements = append(requirements, mappingValue(mappingValue(item, m), "security"))
		}
	})
	for _, list := range requirements {
		eachItem(list, func(req *yaml.Node) {
			if req.Kind != yaml.MappingNode {
				return
			}
			for i := 0; i+1 < len(req.Content); i += 2 {
				if name, ok := schemes[req.Content[i].Value]; ok {
					req.Content[i] = str(name, req.Content[i])
				}
			}
		})
	}
}

// servers replaces host, basePath and schemes by a servers list: a URL
// per scheme, or the base path alone without a host.
func (c *converter) servers(n *yaml.Node) {
	place := -1
	for i := 0; i+1 < len(n.Content); i += 2 {
		if k := n.Content[i].Value; k == "host" || k == "basePath" || k == "schemes" {
			place = i
			break
		}
	}
	if place < 0 {
		return
	}
	host, basePath, schemes := removeKey(n, "host"), removeKey(n, "basePath"), removeKey(n, "schemes")
	at := host
	if at == nil {
		at = basePath
	}
	path := ""
	if basePath != nil {
		path = strings.TrimSuffix(basePath.Value, "/")
	}
	var urls []string
	switch {
	case host != nil:
		names := stringsOf(schemes)
		if len(names) == 0 {
			// 2.0 defaults to the scheme the document was read with,
			// which a server URL cannot say.
			names = []string{"https"}
		}
		for _, s := range names {
			urls = append(urls, s+"://"+host.Value+path)
		}
	case path != "":
		urls = []string{path}
	default:
		return
	}
	list := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	if at != nil {
		list.Line, list.Column = at.Line, at.Column
	}
	for _, u := range urls {
		list.Content = append(list.Content, mapping(at, str("url", at), str(u, at)))
	}
	n.Content = slices.Insert(n.Content, min(place, len(n.Content)), str("servers", at), list)
}

// componentParameters sorts the parameters of #/parameters: bodies become
// requestBodies, form fields are kept to be inlined, others parameters.
func (c *converter) componentParameters(params, components *yaml.Node) {
	params = resolveAlias(params)
	others := mapping(params)
	bodies := mapping(params)
	for i := 0; i+1 < len(params.Content); i += 2 {
		key, p := params.Content[i], resolveAlias(params.Content[i+1])
		switch in := mappingValue(p, "in"); {
		case in != nil && in.Value == "body":
			c.bodies[key.Value] = p
			bodies.Content = append(bodies.Content, key, c.requestBody(p, c.consumes))
		case in != nil && in.Value == "formData":
			c.forms[key.Value] = p
		default:
			c.parameter(p)
			others.Content = append(others.Content, key, p)
		}
	}
	if len(others.Content) > 0 {
		components.Content = append(components.Content, str("parameters", params), others)
	}
	if len(bodies.Content) > 0 {
		components.Content = append(components.Content, str("requestBodies", params), bodies)
	}
}

var operationMethods = []string{"get", "put", "post", "delete", "options", "head", "patch"}

func (c *converter) pathItem(n *yaml.Node) {
	if c.visited(n) || isRef(n) {
		return
	}
	// Bodies and form fields cannot be shared at the path level in 3.x:
	// they go to each operation that does not have its own.
	place := keyIndex(n, "parameters")
	shared := removeKey(n, "parameters")
	var kept, moved []*yaml.Node
	eachItem(shared, func(p *yaml.Node) {
		if kind, _ := c.paramKind(p); kind == "body" || kind == "formData" {
			moved = append(moved, p)
		} else {
			kept = append(kept, p)
		}
	})
	if len(kept) > 0 {
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Line: shared.Line, Column: shared.Column}
		for _, p := range kept {
			seq.Content = append(seq.Content, c.parameterOrRef(p))
		}
		n.Content = slices.Insert(n.Content, place, str("parameters", shared), seq)
	}
	for _, m := range operationMethods {
		if op := resolveAlias(mappingValue(n, m)); op != nil {
			c.operation(op, moved)
		}
	}
}

// paramKind returns where the parameter (or the one its $ref names) is:
// body, formData, or another location.
//
// A reference to a body or form field also returns the parameter it leads
// to; one into another file is followed with lookup.
func (c *converter) paramKind(p *yaml.Node) (string, *yaml.Node) {
	ref := mappingValue(p, "$ref")
	if ref == nil {
		if in := mappingValue(p, "in"); in != nil {
			return in.Value, nil
		}
		return "", nil
	}
	if name, ok := strings.CutPrefix(ref.Value, "#/parameters/"); ok && c.inRoot() {
		switch {
		case c.bodies[name] != nil:
			return "body", c.bodies[name]
		case c.forms[name] != nil:
			return "formData", c.forms[name]
		}
		return "ref", nil
	}
	if c.lookup == nil {
		return "ref", nil
	}
	target := resolveAlias(c.lookup(c.uri, ref.Value))
	if in := mappingValue(target, "in"); in != nil && (in.Value == "body" || in.Value == "formData") {
		return in.Value, target
	}
	return "ref", nil
}

// parameterOrRef converts a parameter, or rewrites its reference into the
// root's parameters.
func (c *converter) parameterOrRef(p *yaml.Node) *yaml.Node {
	if ref := mappingValue(p, "$ref"); ref != nil {
		if name, ok := strings.CutPrefix(ref.Value, "#/parameters/"); ok && c.inRoot() {
			ref.Value = "#/components/parameters/" + name
		}
		return p
	}
	c.parameter(p)
	return p
}

func (c *converter) operation(op *yaml.Node, inherited []*yaml.Node) {
	if c.visited(op) {
		return
	}
	consumes, produces := c.consumes, c.produces
	if v := removeKey(op, "consumes"); v != nil {
		consumes = stringsOf(v)
	}
	if v := removeKey(op, "produces"); v != nil {
		produces = stringsOf(v)
	}
	if s := removeKey(op, "schemes"); s != nil {
		c.warn(s, "the schemes of an operation are not converted; it uses the document's servers")
	}

	place := keyIndex(op, "parameters")
	params := removeKey(op, "parameters")
	// body is the body parameter, and bodyTarget the one it refers to.
	var body, bodyTarget *yaml.Node
	var forms []*yaml.Node
	var others []*yaml.Node
	own := map[string]bool{}
	collect := func(p *yaml.Node, inheritedParam bool) {
		kind, target := c.paramKind(p)
		switch kind {
		case "body":
			if inheritedParam && body != nil {
				return
			}
			body, bodyTarget = p, target
		case "formData":
			form := p
			if target != nil {
				form = copyNode(target)
			}
			name := ""
			if v := mappingValue(form, "name"); v != nil {
				name = v.Value
			}
			if inheritedParam && own[name] {
				return
			}
			own[name] = true
			forms = append(forms, form)
		default:
			others = append(others, c.parameterOrRef(p))
		}
	}
	eachItem(params, func(p *yaml.Node) { collect(p, false) })
	for _, p := range inherited {
		collect(p, true)
	}
	// The parameters and the body go where the 2.0 parameters were.
	var fields []*yaml.Node
	if len(others) > 0 {
		// Only the operation's own parameters are others; params is set.
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Line: params.Line, Column: params.Column, Content: others}
		fields = append(fields, str("parameters", params), seq)
	}
	var rb *yaml.Node
	switch {
	case body != nil && len(forms) > 0:
		c.warn(body, "an operation with both a body and form parameters is invalid; the form parameters are dropped")
		fallthrough
	case body != nil:
		ref := mappingValue(body, "$ref")
		name, local := "", false
		if ref != nil {
			name, local = strings.CutPrefix(ref.Value, "#/parameters/")
			local = local && c.inRoot()
		}
		same := slices.Equal(mediaTypes(consumes), mediaTypes(c.consumes))
		switch {
		case ref == nil:
			rb = c.requestBody(body, consumes)
		case local && same:
			rb = mapping(body, str("$ref", ref), str("#/components/requestBodies/"+name, ref))
		case local:
			// The shared body has the document's media types, not these.
			rb = c.requestBody(bodyTarget, consumes)
		default:
			// A body of another file is converted where it is, with the
			// document's media types, since other operations may share it.
			if !same {
				c.warn(ref, "the body in another file is sent as the document's media types, not the operation's")
			}
			rb = mapping(body, str("$ref", ref), str(ref.Value, ref))
		}
	case len(forms) > 0:
		rb = c.formBody(forms, consumes)
	}
	if rb != nil {
		fields = append(fields, str("requestBody", rb), rb)
	}
	op.Content = slices.Insert(op.Content, place, fields...)

	if resps := mappingValue(op, "responses"); resps != nil {
		eachValue(resps, func(r *yaml.Node) { c.response(r, produces) })
	}
}

// mediaTypes returns the media types a body or response is sent as: the
// given ones, or JSON.
func mediaTypes(given []string) []string {
	if len(given) == 0 {
		return []string{"application/json"}
	}
	return given
}

// requestBody converts the body parameter p into a request body with a
// media type per consumes.
func (c *converter) requestBody(p *yaml.Node, consumes []string) *yaml.Node {
	rb := mapping(p)
	if d := mappingValue(p, "description"); d != nil {
		rb.Content = append(rb.Content, str("description", d), d)
	}
	schema := resolveAlias(mappingValue(p, "schema"))
	c.schema(schema)
	content := mapping(p)
	for _, mt := range mediaTypes(consumes) {
		media := mapping(p)
		// The 2.0 schema requires one, but the loader reads invalid
		// documents too.
		if schema != nil {
			media = mapping(schema, str("schema", schema), schema)
		}
		content.Content = append(content.Content, str(mt, p), media)
	}
	rb.Content = append(rb.Content, str("content", p), content)
	if r := mappingValue(p, "required"); r != nil {
		rb.Content = append(rb.Content, str("required", r), r)
	}
	for i := 0; i+1 < len(p.Content); i += 2 {
		if strings.HasPrefix(p.Content[i].Value, "x-") {
			rb.Content = append(rb.Content, p.Content[i], p.Content[i+1])
		}
	}
	return rb
}

// formBody converts formData parameters into a request body whose schema
// is an object with a property per parameter, as a form, as multipart
// form data, or both, as consumes says; a file needs multipart.
func (c *converter) formBody(forms []*yaml.Node, consumes []string) *yaml.Node {
	at := forms[0]
	props := mapping(at)
	var required []*yaml.Node
	encoding := mapping(at)
	file := false
	for _, f := range forms {
		name := mappingValue(f, "name")
		if name == nil {
			c.warn(f, "a form parameter without a name is dropped")
			continue
		}
		if t := mappingValue(f, "type"); t != nil && t.Value == "file" {
			file = true
		}
		schema, style := c.paramSchema(f, "formData")
		if d := mappingValue(f, "description"); d != nil {
			schema.Content = slices.Insert(schema.Content, 0, str("description", d), d)
		}
		props.Content = append(props.Content, str(name.Value, name), schema)
		if isTrue(mappingValue(f, "required")) {
			required = append(required, str(name.Value, name))
		}
		if style != nil {
			encoding.Content = append(encoding.Content, str(name.Value, name), style)
		}
	}
	schema := mapping(at, str("type", at), str("object", at), str("properties", at), props)
	if len(required) > 0 {
		schema.Content = append(schema.Content, str("required", at), &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Line: at.Line, Column: at.Column, Content: required})
	}
	var types []string
	for _, mt := range consumes {
		if mt == "application/x-www-form-urlencoded" || mt == "multipart/form-data" {
			types = append(types, mt)
		}
	}
	switch {
	case len(types) == 0 && file:
		types = []string{"multipart/form-data"}
	case len(types) == 0:
		types = []string{"application/x-www-form-urlencoded"}
	case file && !slices.Contains(types, "multipart/form-data"):
		c.warn(at, "a file can only be sent as multipart/form-data; the body is sent as that")
		types = []string{"multipart/form-data"}
	}
	content := mapping(at)
	for i, mt := range types {
		media := mapping(at, str("schema", at), schema)
		if i > 0 {
			media = mapping(at, str("schema", at), copyNode(schema))
		}
		if len(encoding.Content) > 0 {
			media.Content = append(media.Content, str("encoding", at), copyNode(encoding))
		}
		content.Content = append(content.Content, str(mt, at), media)
	}
	return mapping(at, str("content", at), content, str("required", at), boolNode(len(required) > 0, at))
}

// parameter converts a parameter other than a body or form field in
// place: its type fields become a schema, its collectionFormat a style.
func (c *converter) parameter(p *yaml.Node) {
	if c.visited(p) || isRef(p) {
		return
	}
	in := ""
	if v := mappingValue(p, "in"); v != nil {
		in = v.Value
	}
	schema, style := c.paramSchema(p, in)
	for _, k := range schemaKeys {
		removeKey(p, k)
	}
	if in != "query" {
		// Only query parameters may allow empty values in 3.x.
		removeKey(p, "allowEmptyValue")
	}
	p.Content = append(p.Content, str("schema", p), schema)
	if style != nil {
		p.Content = append(p.Content, style.Content...)
	}
}

// schemaKeys are the fields of 2.0 parameters, headers and items that
// describe their value, which 3.x puts in a schema.
var schemaKeys = []string{"type", "format", "items", "collectionFormat", "default", "maximum", "exclusiveMaximum",
	"minimum", "exclusiveMinimum", "maxLength", "minLength", "pattern", "maxItems", "minItems", "uniqueItems",
	"enum", "multipleOf"}

// paramSchema returns the schema of a parameter, header or items object
// located in in, and the style and explode fields its collectionFormat
// becomes (nil for the default).
func (c *converter) paramSchema(p *yaml.Node, in string) (*yaml.Node, *yaml.Node) {
	schema := mapping(p)
	for _, k := range schemaKeys {
		v := mappingValue(p, k)
		if v == nil || k == "collectionFormat" {
			continue
		}
		switch k {
		case "items":
			items, _ := c.paramSchema(v, "items")
			if cf := mappingValue(v, "collectionFormat"); cf != nil && cf.Value != "csv" {
				c.warn(cf, "collectionFormat %s of nested items has no OpenAPI 3 equivalent; the items are read as csv", cf.Value)
			}
			schema.Content = append(schema.Content, str(k, v), items)
		case "type":
			if v.Value == "file" {
				schema.Content = append(schema.Content, str("type", v), str("string", v), str("format", v), str("binary", v))
				continue
			}
			schema.Content = append(schema.Content, str(k, v), v)
		default:
			schema.Content = append(schema.Content, str(k, v), v)
		}
	}
	upgradeSchemaFields(schema)
	if t := mappingValue(p, "type"); t == nil || t.Value != "array" || in == "items" {
		return schema, nil
	}
	format := "csv"
	cf := mappingValue(p, "collectionFormat")
	if cf != nil {
		format = cf.Value
	}
	at := cf
	if at == nil {
		at = p
	}
	style := func(s string, explode bool) *yaml.Node {
		return mapping(at, str("style", at), str(s, at), str("explode", at), boolNode(explode, at))
	}
	switch {
	case format == "multi" && (in == "query" || in == "formData"):
		// The 3.x default for query parameters and form fields.
		return schema, nil
	case format == "csv" && (in == "query" || in == "formData"):
		return schema, style("form", false)
	case format == "csv":
		// The 3.x default for paths and headers: simple, not exploded.
		return schema, nil
	case format == "ssv" && in == "query":
		return schema, style("spaceDelimited", false)
	case format == "pipes" && in == "query":
		return schema, style("pipeDelimited", false)
	}
	c.warn(at, "collectionFormat %s has no OpenAPI 3 equivalent in %s; the values are read as csv", format, in)
	if in == "query" || in == "formData" {
		return schema, style("form", false)
	}
	return schema, nil
}

// boolNode returns a boolean scalar at the position of at.
func boolNode(b bool, at *yaml.Node) *yaml.Node {
	n := str(fmt.Sprint(b), at)
	n.Tag = "!!bool"
	return n
}

// response converts a response in place: its schema becomes content, a
// media type per produces, with the examples given for them; its headers
// get schemas.
func (c *converter) response(r *yaml.Node, produces []string) {
	if c.visited(r) {
		return
	}
	if ref := mappingValue(r, "$ref"); ref != nil {
		if name, ok := strings.CutPrefix(ref.Value, "#/responses/"); ok && c.inRoot() {
			ref.Value = "#/components/responses/" + name
		}
		return
	}
	eachEntry(mappingValue(r, "headers"), c.header)
	schema := removeKey(r, "schema")
	examples := removeKey(r, "examples")
	if schema == nil && examples == nil {
		return
	}
	at := schema
	if at == nil {
		at = examples
	}
	c.schema(schema)
	content := mapping(at)
	media := map[string]*yaml.Node{}
	add := func(mt string) *yaml.Node {
		if m, ok := media[mt]; ok {
			return m
		}
		m := mapping(at)
		if schema != nil {
			m.Content = append(m.Content, str("schema", schema), schema)
		}
		media[mt] = m
		content.Content = append(content.Content, str(mt, at), m)
		return m
	}
	if schema != nil {
		for _, mt := range mediaTypes(produces) {
			add(mt)
		}
	}
	examples = resolveAlias(examples)
	if examples != nil && examples.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(examples.Content); i += 2 {
			mt, ex := examples.Content[i], examples.Content[i+1]
			m := add(mt.Value)
			m.Content = append(m.Content, str("example", mt), ex)
		}
	}
	r.Content = append(r.Content, str("content", at), content)
}

// header converts a response header in place.
func (c *converter) header(h *yaml.Node) {
	if c.visited(h) || isRef(h) {
		return
	}
	schema, style := c.paramSchema(h, "header")
	for _, k := range schemaKeys {
		removeKey(h, k)
	}
	h.Content = append(h.Content, str("schema", h), schema)
	if style != nil {
		h.Content = append(h.Content, style.Content...)
	}
}

// schema converts a 2.0 schema in place: x-nullable, discriminator names,
// file types, and the fields 3.0 schemas share with it.
func (c *converter) schema(n *yaml.Node) {
	n = resolveAlias(n)
	if c.visited(n) || n.Kind != yaml.MappingNode || isRef(n) {
		return
	}
	rename(n, "x-nullable", "nullable")
	if d := mappingValue(n, "discriminator"); d != nil && d.Kind == yaml.ScalarNode {
		set(n, "discriminator", mapping(d, str("propertyName", d), d))
	}
	if t := mappingValue(n, "type"); t != nil && t.Kind == yaml.ScalarNode && t.Value == "file" {
		t.Value = "string"
		if mappingValue(n, "format") == nil {
			n.Content = append(n.Content, str("format", t), str("binary", t))
		}
	}
	upgradeSchemaFields(n)
	eachEntry(mappingValue(n, "properties"), c.schema)
	c.schema(mappingValue(n, "additionalProperties"))
	c.schema(mappingValue(n, "not"))
	if items := resolveAlias(mappingValue(n, "items")); items != nil && items.Kind == yaml.SequenceNode {
		// A tuple; 3.1 writes it as prefixItems.
		rename(n, "items", "prefixItems")
		eachItem(items, c.schema)
	} else {
		c.schema(items)
	}
	for _, k := range []string{"allOf", "anyOf", "oneOf"} {
		eachItem(mappingValue(n, k), c.schema)
	}
}

// securityScheme converts a security definition in place.
func (c *converter) securityScheme(s *yaml.Node) {
	if c.visited(s) {
		return
	}
	t := mappingValue(s, "type")
	if t == nil {
		return
	}
	switch t.Value {
	case "basic":
		t.Value = "http"
		s.Content = append(s.Content, str("scheme", t), str("basic", t))
	case "oauth2":
		flow := removeKey(s, "flow")
		authURL, tokenURL, scopes := removeKey(s, "authorizationUrl"), removeKey(s, "tokenUrl"), removeKey(s, "scopes")
		if flow == nil {
			c.warn(s, "an oauth2 scheme without a flow is not converted")
			return
		}
		name := map[string]string{"implicit": "implicit", "password": "password", "application": "clientCredentials", "accessCode": "authorizationCode"}[flow.Value]
		if name == "" {
			c.warn(flow, "the oauth2 flow %q is not known", flow.Value)
			return
		}
		f := mapping(flow)
		if authURL != nil && (name == "implicit" || name == "authorizationCode") {
			f.Content = append(f.Content, str("authorizationUrl", authURL), authURL)
		}
		if tokenURL != nil && name != "implicit" {
			f.Content = append(f.Content, str("tokenUrl", tokenURL), tokenURL)
		}
		if scopes == nil {
			scopes = mapping(flow)
		}
		f.Content = append(f.Content, str("scopes", scopes), scopes)
		s.Content = append(s.Content, str("flows", flow), mapping(flow, str(name, flow), f))
	}
}

// namedMaps are the fields whose keys are names (of properties, paths,
// components, media types or status codes) rather than fields, so a key
// such as "example" there is not an example.
var namedMaps = []string{"properties", "patternProperties", "dependentSchemas", "$defs", "paths", "schemas",
	"responses", "parameters", "requestBodies", "headers", "securitySchemes", "content", "encoding", "callbacks", "links"}

// refs rewrites the references to definitions and responses of the
// document anywhere in n, except in examples and extensions. names says
// that the keys of n are names.
func (c *converter) refs(n *yaml.Node, names bool) {
	n = resolveAlias(n)
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.SequenceNode:
		for _, item := range n.Content {
			c.refs(item, false)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, v := n.Content[i].Value, resolveAlias(n.Content[i+1])
			switch {
			case names:
				c.refs(v, false)
			case key == "example" || key == "examples" || strings.HasPrefix(key, "x-"):
			case key == "$ref" && v.Kind == yaml.ScalarNode:
				for from, to := range map[string]string{"#/definitions/": "#/components/schemas/", "#/responses/": "#/components/responses/"} {
					if rest, ok := strings.CutPrefix(v.Value, from); ok {
						v.Value = to + rest
					}
				}
				// Unescaped, as a reference may percent-encode the name.
				if old, err := url.PathUnescape(v.Value); err == nil && c.renamed[old] != "" {
					v.Value = c.renamed[old]
				}
			default:
				c.refs(v, slices.Contains(namedMaps, key))
			}
		}
	}
}
