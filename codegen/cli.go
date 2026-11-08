package codegen

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/dheins/go-openapi/ir"
)

const cliImport = "github.com/dheins/go-openapi/runtime/cli"

// CLI generates a command-line client of m: a main package with a
// command per operation, sent with the generated client of
// opts.ClientImport (package opts.ClientPackage), whose request types are
// in the operations' package (opts.OperationsPackage and
// OperationsImport). opts.Name names the program. It returns warnings
// about the operations left out, whose bodies a command line cannot
// give yet. m must be built with ir.Options.Operations.
func CLI(m *ir.Model, opts Options) ([]File, []string, error) {
	if opts.ClientImport == "" || opts.ClientPackage == "" || opts.OperationsImport == "" {
		return nil, nil, fmt.Errorf("codegen: a command-line client needs the client's and the operations' packages")
	}
	if opts.Name == "" {
		return nil, nil, fmt.Errorf("codegen: a command-line client needs a name")
	}
	opts.Package = "main"
	lay, err := layoutOf(opts)
	if err != nil {
		return nil, nil, err
	}
	g, err := newGenerator(opts)
	if err != nil {
		return nil, nil, err
	}
	g.lay = lay
	var warnings []string
	var commands []*cliCommand
	taken := map[string]bool{}
	for _, o := range m.Operations {
		if o.Unsupported != "" {
			continue
		}
		cmd, why := cliCommandOf(o)
		if why != "" {
			warnings = append(warnings, fmt.Sprintf("%s: %s; the command is left out", o.Name, why))
			continue
		}
		name := cmd.Name
		for i := 2; taken[cmd.Name]; i++ {
			cmd.Name = fmt.Sprintf("%s-%d", name, i)
		}
		taken[cmd.Name] = true
		commands = append(commands, cmd)
	}
	var schemes []cliScheme
	for _, s := range m.SecuritySchemes {
		if s.Type == "mutualTLS" {
			// The client certificate is the HTTP client's.
			continue
		}
		schemes = append(schemes, cliScheme{
			Scheme: s, Flag: kebab(s.Method), Env: strings.ToUpper(strings.ReplaceAll(kebab(s.Method), "-", "_")),
			Basic: s.Type == "http" && s.Scheme == "basic", Help: "the credentials of the scheme " + s.Name,
		})
	}
	data := cliData{Model: m, Name: opts.Name, Summary: opts.Title, Commands: commands, Schemes: schemes,
		Client: opts.ClientPackage, HasServerURL: m.ServerURL != ""}
	content, err := g.render("cli", data, func(f *fileGen) {
		f.imp("context", "context")
		f.imp("io", "io")
		f.imp("os", "os")
		f.imp("slices", "slices")
		f.imp(cliImport, "cli")
		f.imp(clientImport, "goapiclient")
		f.imp(opts.ClientImport, opts.ClientPackage)
	})
	if err != nil {
		return nil, nil, fmt.Errorf("generating main.go: %w", err)
	}
	g.add("main.go", content)
	gens := map[string]*generator{"": g}
	order := []string{""}
	pkg := func(dir string) (*generator, error) { return targetPackage(opts, lay, gens, &order, dir) }
	if err := addTargets(m, opts, true, pkg); err != nil {
		return nil, nil, err
	}
	return packageFiles(gens, order), warnings, nil
}

// cliData is what the cli template gets.
type cliData struct {
	*ir.Model
	// Name is the program's, Summary what it is a client of, and Client
	// the name of the generated client's package.
	Name, Summary, Client string
	HasServerURL          bool
	Commands              []*cliCommand
	Schemes               []cliScheme
}

// cliCommand is the command of an operation.
type cliCommand struct {
	Operation *ir.Operation
	// Name is the command's, in kebab case, Summary the first line of the
	// operation's description, and Tag its first tag.
	Name, Summary, Tag string
	Params             []cliParam
	Media              []cliMedia
	BodyRequired       bool
	// ContentType is the request's field naming the body's media type, if
	// the operation has several.
	ContentType string
	// BodyFields are the flags of the properties of the body, of the
	// request's field FieldsOf.
	BodyFields []cliBodyField
	FieldsOf   string
}

// cliBodyField is a flag of a property of the body, at Path.
type cliBodyField struct {
	Flag, Path, Type, Help string
	List                   bool
	Values                 []string
}

type cliParam struct {
	Flag, Field, In, Name, Help, Kind string
	Required                          bool
	Values                            []string
}

type cliMedia struct {
	IR                     *ir.BodyMedia
	MediaType, Kind, Field string
	Files                  []cliFile
}

type cliFile struct {
	Flag, Property, Field string
}

type cliScheme struct {
	Scheme          *ir.SecurityScheme
	Flag, Env, Help string
	Basic           bool
}

// cliFlags are the flags every command has, which parameters are named
// apart from.
var cliFlags = map[string]bool{"server": true, "header": true, "timeout": true, "v": true, "body": true, "content-type": true, "h": true, "help": true}

// cliCommandOf returns the command of o, or why it has none.
func cliCommandOf(o *ir.Operation) (*cliCommand, string) {
	cmd := &cliCommand{Operation: o, Name: kebab(o.Name), Summary: firstLine(o.Doc)}
	if len(o.Tags) > 0 {
		cmd.Tag = o.Tags[0]
	}
	flags := map[string]bool{}
	for _, p := range o.Params {
		kind := "cli.Text"
		switch {
		case p.Array && p.Content == "":
			kind = "cli.List"
		case p.Object || (p.Content != "" && p.ContentKind != "text"):
			kind = "cli.JSON"
		}
		flag := kebab(p.Name)
		if cliFlags[flag] || flags[flag] {
			flag = p.In + "-" + flag
		}
		flags[flag] = true
		var values []string
		if p.Content == "" && !p.Object {
			var typ string
			if typ, values = scalarJSON(unwrapRef(p.Elem)); typ == "boolean" {
				values = []string{"true", "false"}
			}
		}
		cmd.Params = append(cmd.Params, cliParam{Flag: flag, Field: p.Field, In: p.In, Name: p.Name, Help: firstLine(p.Doc), Kind: kind,
			Required: p.Required && p.Default == "", Values: values})
	}
	if o.Body == nil {
		return cmd, ""
	}
	var unsupported []string
	fields := map[string]bool{}
	for _, m := range o.Body.Media {
		switch m.Kind {
		case "json", "xml", "form", "codec", "multipart", "text", "binary", "parts", "partstream", "jsonl", "sse":
		default:
			unsupported = append(unsupported, m.MediaType)
			continue
		}
		md := cliMedia{IR: m, MediaType: m.MediaType, Kind: m.Kind, Field: m.Field.Name}
		if m.Kind == "multipart" {
			for _, prop := range m.Properties {
				if prop.Type != "file" {
					continue
				}
				field := structField(m.Field.Value, prop.Name)
				if field == "" {
					continue
				}
				md.Files = append(md.Files, cliFile{Flag: kebab(prop.Name), Property: prop.Name, Field: field})
			}
		}
		fields[m.Field.Name] = true
		cmd.Media = append(cmd.Media, md)
	}
	if len(cmd.Media) == 0 {
		if o.Body.Required {
			return nil, fmt.Sprintf("bodies of type %s cannot be given on a command line yet", strings.Join(unsupported, ", "))
		}
		return cmd, ""
	}
	cmd.BodyRequired = o.Body.Required
	if len(o.Body.Media) > 1 {
		cmd.ContentType = "ContentType"
	}
	if m := o.Body.Media[0]; structured(m.Kind) {
		cmd.BodyFields = bodyFields(m.Field.Value, nil, map[*ir.Type]bool{})
		if len(cmd.BodyFields) > 0 {
			cmd.FieldsOf = m.Field.Name
		}
	}
	return cmd, ""
}

// structured reports whether a body kind is given as the JSON of its
// value.
func structured(kind string) bool {
	switch kind {
	case "json", "xml", "form", "codec", "multipart":
		return true
	}
	return false
}

// maxBodyFieldDepth is how deep in nested objects body flags go.
const maxBodyFieldDepth = 3

// bodyFields returns the flags of the properties of r, a struct, below
// path: one per scalar or array of scalars, nested objects to a depth,
// leaving out read-only properties, files, maps, and the types on the way
// there (recursive types).
func bodyFields(r *ir.Ref, path []string, seen map[*ir.Type]bool) []cliBodyField {
	r = unwrapRef(r)
	if r.Kind != ir.Named || r.Type.Kind != ir.Struct || seen[r.Type] || len(path) >= maxBodyFieldDepth {
		return nil
	}
	seen[r.Type] = true
	defer delete(seen, r.Type)
	var out []cliBodyField
	for _, f := range r.Type.Fields {
		if f.ReadOnly || !flagName.MatchString(f.JSONName) {
			continue
		}
		at := append(append([]string{}, path...), f.JSONName)
		t := unwrapRef(f.Type)
		list := false
		if t.Kind == ir.Slice {
			list, t = true, unwrapRef(t.Elem)
		}
		if typ, values := scalarJSON(t); typ != "" {
			out = append(out, cliBodyField{Flag: "body." + strings.Join(at, "."), Path: strings.Join(at, "."), Type: typ, List: list,
				Values: values, Help: firstLine(f.Doc)})
			continue
		}
		if !list {
			out = append(out, bodyFields(t, at, seen)...)
		}
	}
	return out
}

// flagName matches property names a flag can have.
var flagName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// unwrapRef returns r without Optional, Nullable and Pointer.
func unwrapRef(r *ir.Ref) *ir.Ref {
	for r.Kind == ir.Optional || r.Kind == ir.Nullable || r.Kind == ir.Pointer {
		r = r.Elem
	}
	return r
}

// scalarJSON returns the JSON type of r, string, number or boolean, and
// its values if it is an enum; "" if r is not a scalar a flag gives.
func scalarJSON(r *ir.Ref) (string, []string) {
	switch r.Kind {
	case ir.Builtin:
		switch {
		case r.MappedJSON == "string" || r.Name == "string" || r.Name == "time.Time" || r.Name == "oapi.Date":
			return "string", nil
		case r.MappedJSON == "integer" || r.MappedJSON == "number" || (r.MappedJSON == "" && isNumber(r.Name)):
			return "number", nil
		case r.MappedJSON == "boolean" || r.Name == "bool":
			return "boolean", nil
		}
	case ir.Named:
		switch r.Type.Kind {
		case ir.Enum:
			typ, _ := scalarJSON(r.Type.Base)
			var values []string
			for _, v := range r.Type.Values {
				if s, err := strconv.Unquote(v.Literal); err == nil {
					values = append(values, s)
				} else {
					values = append(values, v.Literal)
				}
			}
			return typ, values
		case ir.Defined, ir.Alias:
			return scalarJSON(r.Type.Underlying)
		}
	}
	return "", nil
}

// cliStream returns the function reading a JSON Lines or event stream
// body of m from a command line's -body, or "" for other bodies.
func (f *fileGen) cliStream(m *ir.BodyMedia) string {
	switch {
	case m.Kind == "jsonl":
		return fmt.Sprintf("func(r io.Reader) any { return cli.JSONLines[%s](r) }", f.goType(m.Field.Value.Elem))
	case m.Kind == "sse" && m.Events == nil:
		return "func(r io.Reader) any { return cli.Events(r) }"
	case m.Kind == "sse":
		return fmt.Sprintf("func(r io.Reader) any { return cli.TypedEvents(r, %s) }", f.eventDecoder(m.Events, "Request"))
	}
	return ""
}

// structField returns the Go field of the property of JSON name prop of
// the struct type r is, or "".
func structField(r *ir.Ref, prop string) string {
	for r != nil && (r.Kind == ir.Optional || r.Kind == ir.Pointer || r.Kind == ir.Nullable) {
		r = r.Elem
	}
	if r == nil || r.Kind != ir.Named {
		return ""
	}
	for _, f := range r.Type.Fields {
		if f.JSONName == prop {
			return f.Name
		}
	}
	return ""
}

// kebab turns a name into kebab case: ListPets and listPets are
// list-pets, X-Request-ID is x-request-id.
func kebab(name string) string {
	return strings.ReplaceAll(strings.TrimSuffix(fileName(ir.GoName(name)), ".go"), "_", "-")
}

// firstLine returns the first line of a description.
func firstLine(doc string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(doc), "\n")
	return strings.TrimSpace(line)
}
