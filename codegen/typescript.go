package codegen

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"

	"github.com/dheins/go-openapi/ir"
)

// tsRuntime is runtime.ts, written as it is next to every TypeScript
// client.
//
//go:embed ts/runtime.ts
var tsRuntime []byte

// tsTemplateFiles are the templates of models.ts, client.ts, receiver.ts
// and index.ts.
//
//go:embed ts/*.tmpl
var tsTemplateFiles embed.FS

// TSOptions configures a TypeScript client.
type TSOptions struct {
	// Source names the input document in the generated file header.
	Source string
	// Templates, if set, holds *.tmpl files whose {{define}} blocks
	// replace the built-in blocks of models.ts, client.ts, receiver.ts and
	// index.ts, as
	// Options.Templates does for Go, and a goapi.yaml whose targets add
	// files of your own, written as their blocks render. This is
	// experimental.
	Templates fs.FS
}

// TypeScript generates a TypeScript client of the model: runtime.ts, the
// runtime every client shares; models.ts, with a type and a schema
// descriptor per type of the model, which the runtime validates and
// encodes values with; client.ts, with a Client class and the request and
// response types of the operations, if the model has some; receiver.ts,
// with the handlers of the webhooks and callbacks a consumer receives, if
// there are some; and index.ts.
// It uses only syntax that type stripping erases. It returns what it
// leaves out, such as media types the TypeScript runtime cannot send.
func TypeScript(m *ir.Model, opts TSOptions) ([]File, []string, error) {
	g := newTSGen(m, opts)
	if err := g.parseTemplates(); err != nil {
		return nil, nil, err
	}
	models, err := g.models()
	if err != nil {
		return nil, nil, err
	}
	files := []File{
		{Name: "runtime.ts", Content: tsRuntime},
		{Name: "models.ts", Content: []byte(models)},
	}
	hasClient := len(m.Operations) > 0
	if hasClient {
		client, err := g.client()
		if err != nil {
			return nil, nil, err
		}
		files = append(files, File{Name: "client.ts", Content: []byte(client)})
	}
	receiver, hasReceiver, err := g.receiver()
	if err != nil {
		return nil, nil, err
	}
	if hasReceiver {
		files = append(files, File{Name: "receiver.ts", Content: []byte(receiver)})
	}
	index, err := g.index(hasClient, hasReceiver)
	if err != nil {
		return nil, nil, err
	}
	files = append(files, File{Name: "index.ts", Content: []byte(index)})
	extra, err := tsTargets(m, opts.Templates, files, g.funcs(), g.render)
	if err != nil {
		return nil, nil, err
	}
	return append(files, extra...), g.warnings, nil
}

// parseTemplates parses the templates of the files, the built-in ones
// and, after them, those of opts.Templates, whose blocks replace them.
func (g *tsGen) parseTemplates() error {
	funcs := g.funcs()
	tmpl, err := template.New("ts").Funcs(funcs).ParseFS(tsTemplateFiles, "ts/*.tmpl")
	if err != nil {
		return err
	}
	if g.opts.Templates != nil {
		names, err := targetNames(g.opts.Templates)
		if err != nil {
			return err
		}
		if err := checkTemplates(g.opts.Templates, tmpl, funcs, names); err != nil {
			return err
		}
		if err := parseUserTemplates(tmpl, g.opts.Templates); err != nil {
			return err
		}
	}
	g.tmpl = tmpl
	return nil
}

// render executes the block name with data.
func (g *tsGen) render(name string, data any) (string, error) {
	var b strings.Builder
	if err := g.tmpl.ExecuteTemplate(&b, name, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

// funcs are the functions of the TypeScript templates.
func (g *tsGen) funcs() template.FuncMap {
	funcs := template.FuncMap{
		"tsDoc":         tsDoc,
		"tsType":        func(r *ir.Ref) string { return g.tsType(r) },
		"tsKey":         tsKey,
		"tsCamel":       tsCamel,
		"jsString":      jsString,
		"join":          strings.Join,
		"kind":          kindName,
		"name":          func(t *ir.Type) string { return g.names[t] },
		"schemaName":    func(t *ir.Type) string { return g.schemas[t] },
		"typeSchema":    g.typeSchema,
		"fieldDoc":      func(f *ir.Field) string { return tsDoc("  ", fieldText(f), f.Deprecated) },
		"fieldType":     g.fieldType,
		"fieldOptional": fieldOptional,
		"hasOther":      hasOther,
		"enumUnion":     enumUnion,
		"enumKey":       enumKey,
		"enumLiteral":   enumLiteral,
		"schemeDoc":     schemeDoc,
		"operationDoc":  operationDoc,
		"linkDoc":       linkDoc,
		"goapiVersion":  goapiVersion,
	}
	for name, fn := range namingFuncs {
		funcs[name] = fn
	}
	return funcs
}

// tsRuntimeExports are the names of runtime.ts that users of a client
// need, which index.ts re-exports unless a model has the name; the whole
// runtime is exported as runtime too. Those of receivers are exported
// with receiver.ts.
var tsRuntimeExports = []struct {
	name     string
	typ      bool
	receiver bool
}{
	{"ClientOptions", true, false}, {"SenderOptions", true, false}, {"CallOptions", true, false}, {"Credential", true, false}, {"BasicCredential", true, false},
	{"ServerEvent", true, false}, {"NamedEvent", true, false}, {"Part", true, false}, {"ByteRange", true, false}, {"Problem", true, false}, {"Issue", true, false}, {"Schema", true, false}, {"FormatCheck", true, false}, {"Codec", true, false},
	{"InvalidRequestError", false, false}, {"UnexpectedResponseError", false, false}, {"InvalidResponseError", false, false},
	{"NoCredentialsError", false, false}, {"NotReplayableError", false, false}, {"validate", false, false}, {"encode", false, false}, {"registerFormat", false, false},
	{"ReceiverOptions", true, true}, {"Received", true, true}, {"CredentialCheck", true, true}, {"BasicCheck", true, true},
	{"CertificateCheck", true, true}, {"MissingValueError", false, true}, {"nodeHandler", false, true},
	{"NodeHandlerOptions", true, true}, {"NodeRequest", true, true}, {"NodeResponse", true, true},
}

func (g *tsGen) index(hasClient, hasReceiver bool) (string, error) {
	taken := map[string]bool{"Client": hasClient, "Credentials": hasClient, "defaultServerUrl": hasClient,
		"CallbackHandler": hasReceiver, "SecurityHandler": hasReceiver}
	for _, t := range g.m.Types {
		taken[g.names[t]], taken[g.schemas[t]] = true, true
	}
	var names []string
	for _, e := range tsRuntimeExports {
		if taken[e.name] || e.receiver && !hasReceiver {
			continue
		}
		if e.typ {
			names = append(names, "type "+e.name)
		} else {
			names = append(names, e.name)
		}
	}
	body, err := g.render("index", struct {
		Names            []string
		Client, Receiver bool
	}{names, hasClient, hasReceiver})
	if err != nil {
		return "", err
	}
	return g.header() + body, nil
}

// tsGen generates the TypeScript of one model.
type tsGen struct {
	m    *ir.Model
	opts TSOptions
	// names are the TypeScript names of the model's types, and schemas
	// those of their descriptors.
	names, schemas map[*ir.Type]string
	// qual qualifies the names of models: "" in models.ts, "$m." in
	// client.ts, which imports them as $m.
	qual     string
	warnings []string
	// tmpl are the templates of the files.
	tmpl *template.Template
}

// tsReserved are global names the generated code refers to, which a type
// must not shadow.
var tsReserved = []string{"Blob"}

func newTSGen(m *ir.Model, opts TSOptions) *tsGen {
	g := &tsGen{m: m, opts: opts, names: map[*ir.Type]string{}, schemas: map[*ir.Type]string{}}
	taken := map[string]bool{}
	for _, t := range m.Types {
		name := t.Name
		if slices.Contains(tsReserved, name) {
			name += "_"
		}
		g.names[t] = name
		taken[name] = true
	}
	for _, t := range m.Types {
		name := g.names[t] + "Schema"
		for taken[name] {
			name += "_"
		}
		taken[name] = true
		g.schemas[t] = name
	}
	return g
}

func (g *tsGen) header() string {
	source := g.opts.Source
	if source == "" {
		source = "an OpenAPI document"
	}
	return fmt.Sprintf("// Code generated by goapi from %s. DO NOT EDIT.\n\n", source)
}

func (g *tsGen) models() (string, error) {
	body, err := g.render("models", g.m.Types)
	if err != nil {
		return "", err
	}
	// Only what is used, as noUnusedLocals requires.
	var imports []string
	if strings.Contains(body, "$ref(") {
		imports = append(imports, "ref as $ref")
	}
	if strings.Contains(body, "$Schema") {
		imports = append(imports, "type Schema as $Schema")
	}
	head := g.header()
	if len(imports) > 0 {
		head += fmt.Sprintf("import { %s } from \"./runtime.ts\";\n", strings.Join(imports, ", "))
	}
	if body == "" {
		// Still a module, which index.ts re-exports.
		body = "export {};\n"
	}
	return head + body, nil
}

// fieldType is the TypeScript type of a property.
func (g *tsGen) fieldType(f *ir.Field) string {
	typ := g.tsType(f.Type)
	if f.Nullable && !strings.HasSuffix(typ, " | null") {
		typ += " | null"
	}
	return typ
}

// fieldOptional reports whether a property may be left out. Read-only
// properties are absent from requests and write-only ones from responses,
// so neither is required by the type.
func fieldOptional(f *ir.Field) bool {
	return !f.Required || f.ReadOnly || f.WriteOnly
}

// hasOther reports whether an object type allows properties it does not
// declare.
func hasOther(t *ir.Type) bool {
	a := t.Additional
	return (a != nil && !a.Forbidden && a.Value != nil) || t.Patterns != nil
}

// enumUnion is the union of the literals of an enum, never for none.
func enumUnion(t *ir.Type) string {
	var literals []string
	for _, v := range t.Values {
		literals = append(literals, enumLiteral(v.Literal))
	}
	if len(literals) == 0 {
		return "never"
	}
	return strings.Join(literals, " | ")
}

// enumKey is the key of an enum's value in the constant of its values:
// its name without the enum's.
func enumKey(t *ir.Type, v *ir.EnumValue) string {
	key := strings.TrimPrefix(v.Name, t.Name)
	if key == "" {
		key = v.Name
	}
	return tsKey(key)
}

func fieldText(f *ir.Field) string {
	if f.ReadOnly {
		return strings.TrimSpace(f.Doc + "\n\nSet by the server: it is not sent in requests.")
	}
	return f.Doc
}

// tsType renders the TypeScript type of values of r.
func (g *tsGen) tsType(r *ir.Ref) string {
	switch r.Kind {
	case ir.Named:
		return g.qual + g.names[r.Type]
	case ir.Slice:
		elem := g.tsType(r.Elem)
		if strings.ContainsAny(elem, "|&") {
			elem = "(" + elem + ")"
		}
		return elem + "[]"
	case ir.Map:
		return "{ [key: string]: " + g.tsType(r.Elem) + " }"
	case ir.Optional, ir.Pointer, ir.Part:
		return g.tsType(r.Elem)
	case ir.Nullable:
		return g.tsType(r.Elem) + " | null"
	case ir.Builtin:
		switch r.MappedJSON {
		case "string":
			return "string"
		case "integer", "number":
			return "number"
		case "boolean":
			return "boolean"
		}
		switch r.Name {
		case "string", "time.Time", "oapi.Date", "[]byte":
			return "string"
		case "bool":
			return "boolean"
		case "oapi.File":
			return "Blob"
		}
		if isNumber(r.Name) {
			return "number"
		}
	}
	return "unknown"
}

func isNumber(name string) bool {
	return strings.HasPrefix(name, "int") || strings.HasPrefix(name, "uint") || strings.HasPrefix(name, "float")
}

// typeSchema renders the descriptor of t.
func (g *tsGen) typeSchema(t *ir.Type) string {
	switch t.Kind {
	case ir.Struct:
		obj := g.objectSchema(t)
		if len(t.InlineUnions) == 0 {
			return obj
		}
		of := []string{obj}
		for _, u := range t.InlineUnions {
			of = append(of, g.schema(u.Type))
		}
		return fmt.Sprintf(`{ kind: "all", of: [%s] }`, strings.Join(of, ", "))
	case ir.Enum:
		base := g.schema(t.Base)
		var values []string
		for _, v := range t.Values {
			values = append(values, enumLiteral(v.Literal))
		}
		return strings.TrimSuffix(base, " }") + fmt.Sprintf(", enum: [%s] }", strings.Join(values, ", "))
	case ir.Union:
		var variants, mapping []string
		for _, v := range t.Variants {
			s := g.schema(v.Type)
			variants = append(variants, s)
			for _, value := range v.Values {
				mapping = append(mapping, fmt.Sprintf("%s: %s", tsKey(value), s))
			}
		}
		s := fmt.Sprintf(`{ kind: "union", oneOf: %t, variants: [%s]`, t.OneOf, strings.Join(variants, ", "))
		if t.Discriminator != "" && len(mapping) > 0 {
			s += fmt.Sprintf(", discriminator: %s, mapping: { %s }", strconv.Quote(t.Discriminator), strings.Join(mapping, ", "))
		}
		return s + " }"
	default:
		return g.withExtra(g.schema(t.Underlying), t.Extra)
	}
}

// withExtra combines a schema with further checks on the same value.
func (g *tsGen) withExtra(s string, extra []*ir.Ref) string {
	if len(extra) == 0 {
		return s
	}
	of := []string{s}
	for _, e := range extra {
		of = append(of, g.schema(e))
	}
	return fmt.Sprintf(`{ kind: "all", of: [%s] }`, strings.Join(of, ", "))
}

func (g *tsGen) objectSchema(t *ir.Type) string {
	var props []string
	for _, f := range t.Fields {
		s := g.withExtra(g.schema(f.Type), f.Extra)
		if f.Nullable && !strings.HasPrefix(s, `{ kind: "nullable"`) {
			s = fmt.Sprintf(`{ kind: "nullable", of: %s }`, s)
		}
		p := "schema: " + s
		if f.Required {
			p += ", required: true"
		}
		if f.ReadOnly {
			p += ", readOnly: true"
		}
		if f.WriteOnly {
			p += ", writeOnly: true"
		}
		if f.AlwaysWrite {
			p += ", zeroIsAbsent: true"
		}
		if f.JSONString {
			p += ", jsonString: true"
		}
		if f.Default != "" {
			var lit bytes.Buffer
			if json.Compact(&lit, []byte(f.Default)) == nil {
				p += ", default: " + lit.String()
			}
		}
		props = append(props, fmt.Sprintf("%s: { %s }", tsKey(f.JSONName), p))
	}
	s := fmt.Sprintf(`{ kind: "object", properties: { %s }`, strings.Join(props, ", "))
	if a := t.Additional; a != nil {
		switch {
		case a.Forbidden:
			s += ", additional: false"
		case a.Value != nil:
			s += ", additional: " + g.schema(a.Value)
		}
	}
	s += counts("Properties", t.MinProperties, t.MaxProperties)
	return s + " }"
}

// schema renders the descriptor of values of r.
func (g *tsGen) schema(r *ir.Ref) string {
	c := r.Checks
	if c == nil {
		c = &ir.Checks{}
	}
	switch r.Kind {
	case ir.Named:
		return fmt.Sprintf("$ref(() => %s%s)", g.qual, g.schemas[r.Type])
	case ir.Optional, ir.Pointer, ir.Part:
		return g.schema(r.Elem)
	case ir.Nullable:
		return fmt.Sprintf(`{ kind: "nullable", of: %s }`, g.schema(r.Elem))
	case ir.Slice:
		s := fmt.Sprintf(`{ kind: "array", items: %s`, g.schema(r.Elem)) + counts("Items", c.MinItems, c.MaxItems)
		if c.UniqueItems {
			s += ", uniqueItems: true"
		}
		return s + " }"
	case ir.Map:
		return fmt.Sprintf(`{ kind: "map", values: %s`, g.schema(r.Elem)) + counts("Properties", c.MinProperties, c.MaxProperties) + " }"
	case ir.Builtin:
		if r.MappedFormat != "" {
			// A Go type of the format: its values are checked by it.
			switch r.MappedJSON {
			case "string":
				return `{ kind: "string", format: ` + jsString(r.MappedFormat) + ` }`
			case "integer", "number", "boolean":
				return fmt.Sprintf(`{ kind: %q }`, r.MappedJSON)
			}
		}
		switch r.Name {
		case "string", "time.Time", "oapi.Date", "[]byte":
			format := c.Format
			if format == "" {
				format = map[string]string{"time.Time": "date-time", "oapi.Date": "date", "[]byte": "byte"}[r.Name]
			}
			s := `{ kind: "string"` + counts("Length", c.MinLength, c.MaxLength)
			if c.Pattern != "" {
				s += ", pattern: " + jsString(c.Pattern)
			}
			if format != "" {
				s += ", format: " + jsString(format)
			}
			return s + " }"
		case "bool":
			return `{ kind: "boolean" }`
		case "oapi.File":
			return `{ kind: "file" }`
		}
		if isNumber(r.Name) {
			kind := "number"
			if !strings.HasPrefix(r.Name, "float") {
				kind = "integer"
			}
			s := fmt.Sprintf(`{ kind: %q`, kind)
			for _, b := range []struct{ key, value string }{
				{"minimum", c.Minimum}, {"maximum", c.Maximum}, {"exclusiveMinimum", c.ExclusiveMinimum},
				{"exclusiveMaximum", c.ExclusiveMaximum}, {"multipleOf", c.MultipleOf},
			} {
				if b.value != "" && jsNumber.MatchString(b.value) {
					s += fmt.Sprintf(", %s: %s", b.key, b.value)
				}
			}
			return s + " }"
		}
	}
	return `{ kind: "any" }`
}

// jsNumber matches the JSON numbers that are also JavaScript literals.
var jsNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][-+]?[0-9]+)?$`)

// counts renders minX and maxX bounds.
func counts(what string, lo, hi *int) string {
	var s string
	if lo != nil {
		s += fmt.Sprintf(", min%s: %d", what, *lo)
	}
	if hi != nil {
		s += fmt.Sprintf(", max%s: %d", what, *hi)
	}
	return s
}

// enumLiteral turns the Go literal of an enum value into TypeScript.
func enumLiteral(lit string) string {
	if s, err := strconv.Unquote(lit); err == nil {
		return jsString(s)
	}
	return lit
}

// jsString renders s as a JavaScript string literal.
func jsString(s string) string {
	data, _ := json.Marshal(s)
	// JSON allows U+2028 and U+2029 in strings; older JavaScript does not.
	return strings.NewReplacer(" ", ` `, " ", ` `).Replace(string(data))
}

var tsIdent = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// tsKey renders a property name, quoted unless it is an identifier.
func tsKey(name string) string {
	if tsIdent.MatchString(name) {
		return name
	}
	return jsString(name)
}

// tsDoc renders a JSDoc comment of text, indented, or "" for none.
func tsDoc(indent, text string, deprecated bool) string {
	text = strings.TrimSpace(text)
	if deprecated {
		text = strings.TrimSpace(text + "\n\n@deprecated")
	}
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "*/", "*\\/")
	lines := strings.Split(text, "\n")
	if len(lines) == 1 {
		return fmt.Sprintf("%s/** %s */\n", indent, lines[0])
	}
	var b strings.Builder
	b.WriteString(indent + "/**\n")
	for _, l := range lines {
		if l = strings.TrimRight(l, " \t"); l == "" {
			b.WriteString(indent + " *\n")
		} else {
			b.WriteString(indent + " * " + l + "\n")
		}
	}
	b.WriteString(indent + " */\n")
	return b.String()
}
