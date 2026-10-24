// Package codescan generates an OpenAPI 3.1 document from Go source: the
// schemas of Go types, and (from //goapi: directives) the operations
// that use them.
//
// Types become schemas as encoding/json encodes them: a field's json tag
// gives its name, omitempty or omitzero (or an oapi.Optional type) makes
// it optional, and a pointer without them may be null. A goapi struct tag
// adds JSON Schema keywords, such as `goapi:"minLength=1;format=email"`.
// A type is put in components.schemas when an operation uses it, or when
// its doc comment has a //goapi:schema directive; //goapi:enum makes the
// type's constants its enum.
package codescan

import (
	"cmp"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dheins/go-openapi/oas"
)

// Version is the "openapi" value of scanned documents, and Version32 that
// of documents using what only 3.2 has: the QUERY method, other methods
// (additionalOperations), and tag summaries and parents.
const (
	Version   = "3.1.0"
	Version32 = "3.2.0"
)

// Options configures a scan.
type Options struct {
	// Dir is the directory go list runs in, which patterns are relative
	// to; "" is the current directory.
	Dir string
	// Patterns are the packages to scan, as go list takes them; none
	// means "./...".
	Patterns []string
}

// Result is the document a scan produced, with what it reported.
type Result struct {
	Document    *oas.Document
	Diagnostics []Diagnostic
}

// HasErrors reports whether a diagnostic is an error.
func (r *Result) HasErrors() bool {
	return slices.ContainsFunc(r.Diagnostics, func(d Diagnostic) bool { return d.Severity == Error })
}

// Severity is how bad a diagnostic is.
type Severity int

const (
	// Warning is something left out of the document or approximated.
	Warning Severity = iota
	// Error is a directive or tag that cannot be applied.
	Error
)

func (s Severity) String() string {
	if s == Error {
		return "error"
	}
	return "warning"
}

// Diagnostic is a problem at a position in the scanned source.
type Diagnostic struct {
	Position token.Position
	Severity Severity
	Message  string
}

func (d Diagnostic) String() string {
	return fmt.Sprintf("%s: %s: %s", d.Position, d.Severity, d.Message)
}

// Scan reads the packages opts names and returns the document of their
// types and operations. The error is for packages that cannot be loaded;
// problems with the annotations are diagnostics.
func Scan(opts Options) (*Result, error) {
	patterns := opts.Patterns
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	dir := opts.Dir
	if dir == "" {
		dir = "."
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	s := &scanner{
		fset:    token.NewFileSet(),
		dir:     absDir,
		decls:   map[*types.TypeName]*typeDecl{},
		fields:  map[token.Pos]*ast.Field{},
		schemas: &oas.OrderedMap[*oas.Schema]{},
		names:   map[string]string{},
		taken:   map[string]bool{},
		schemes: map[string]bool{},
	}
	if s.pkgs, err = load(s.fset, absDir, patterns); err != nil {
		return nil, err
	}
	s.index()
	title := "API"
	if len(s.pkgs) > 0 {
		title = s.pkgs[len(s.pkgs)-1].path
	}
	doc := &oas.Document{OpenAPI: Version, Info: &oas.Info{Title: title, Version: "0.0.0"}}
	s.documentDirectives(doc)
	s.operations(doc)
	if s.uses32 {
		doc.OpenAPI = Version32
	}
	for _, td := range s.roots {
		s.schemaOf(td.obj.Type())
	}
	if s.schemas.Len() > 0 {
		if doc.Components == nil {
			doc.Components = &oas.Components{}
		}
		doc.Components.Schemas = s.schemas
	}
	slices.SortStableFunc(s.diags, func(a, b Diagnostic) int {
		return cmp.Or(strings.Compare(a.Position.Filename, b.Position.Filename), a.Position.Line-b.Position.Line, a.Position.Column-b.Position.Column)
	})
	return &Result{Document: doc, Diagnostics: s.diags}, nil
}

// scanner maps the scanned packages to a document.
type scanner struct {
	fset  *token.FileSet
	dir   string
	pkgs  []*pkg
	decls map[*types.TypeName]*typeDecl
	// fields are the struct fields of the scanned source, by the
	// position of their name, for their comments.
	fields map[token.Pos]*ast.Field
	// roots are the types //goapi:schema puts in the document.
	roots []*typeDecl
	// funcs are the functions with goapi directives, and packageDocs the
	// package comments with some.
	funcs       []*funcDecl
	packageDocs []*packageDoc
	// schemes are the declared security schemes, and schemeUses the
	// names requirements use.
	schemes    map[string]bool
	schemeUses []schemeUse
	// uses32 says that the document uses what only 3.2 has.
	uses32  bool
	schemas *oas.OrderedMap[*oas.Schema]
	// names are the component names of the named types mapped so far, by
	// type, and taken the names in use.
	names map[string]string
	taken map[string]bool
	diags []Diagnostic
	// pos is the declaration being mapped, where problems with its types
	// are reported.
	pos token.Pos
}

// at makes pos the declaration being mapped, until the returned function
// restores the previous one.
func (s *scanner) at(pos token.Pos) func() {
	saved := s.pos
	s.pos = pos
	return func() { s.pos = saved }
}

// typeDecl is a type declared in a scanned package.
type typeDecl struct {
	obj *types.TypeName
	doc *ast.CommentGroup
	// name is the component name //goapi:schema gives, and enum says the
	// type's constants are its values.
	name string
	enum bool
}

func (s *scanner) report(pos token.Pos, sev Severity, format string, args ...any) {
	s.diags = append(s.diags, Diagnostic{Position: s.position(pos), Severity: sev, Message: fmt.Sprintf(format, args...)})
}

// position returns the position of pos, relative to the scanned
// directory when it is in it.
func (s *scanner) position(pos token.Pos) token.Position {
	p := s.fset.Position(pos)
	if rel, err := filepath.Rel(s.dir, p.Filename); err == nil && !strings.HasPrefix(rel, "..") {
		p.Filename = filepath.ToSlash(rel)
	}
	return p
}

// where describes pos in a message.
func (s *scanner) where(pos token.Pos) string { return s.position(pos).String() }

// index records the declarations of the scanned packages: their types,
// with their directives, their struct fields, and the functions and
// package comments with directives.
func (s *scanner) index() {
	for _, p := range s.pkgs {
		for _, f := range p.files {
			if ds, err := directives(f.Doc); err != nil {
				s.posErr(err)
			} else if len(ds) > 0 {
				s.packageDocs = append(s.packageDocs, &packageDoc{pkg: p, ds: ds})
			}
			ast.Inspect(f, func(n ast.Node) bool {
				if st, ok := n.(*ast.StructType); ok {
					for _, fd := range st.Fields.List {
						for _, name := range fd.Names {
							s.fields[name.Pos()] = fd
						}
					}
				}
				return true
			})
			for _, decl := range f.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok {
					if ds, err := directives(fn.Doc); err != nil {
						s.posErr(err)
					} else if len(ds) > 0 {
						s.funcs = append(s.funcs, &funcDecl{pkg: p, decl: fn, ds: ds})
					}
					continue
				}
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.TYPE {
					continue
				}
				for _, spec := range gen.Specs {
					ts := spec.(*ast.TypeSpec)
					obj, ok := p.info.Defs[ts.Name].(*types.TypeName)
					if !ok {
						continue
					}
					td := &typeDecl{obj: obj, doc: ts.Doc}
					if td.doc == nil && len(gen.Specs) == 1 {
						td.doc = gen.Doc
					}
					s.decls[obj] = td
					s.typeDirectives(td)
				}
			}
		}
	}
}

// typeDirectives applies the directives of a type's doc comment.
func (s *scanner) typeDirectives(td *typeDecl) {
	ds, err := directives(td.doc)
	if err != nil {
		s.posErr(err)
		return
	}
	for _, d := range ds {
		switch d.name {
		case "schema":
			if len(d.args) > 1 || len(d.kv) > 0 {
				s.report(d.pos, Error, "goapi:schema takes at most a name")
				continue
			}
			if len(d.args) == 1 {
				td.name = d.args[0]
			}
			s.roots = append(s.roots, td)
		case "enum":
			if len(d.args) > 0 || len(d.kv) > 0 {
				s.report(d.pos, Error, "goapi:enum takes no arguments")
				continue
			}
			td.enum = true
		default:
			s.report(d.pos, Error, "unknown directive goapi:%s on a type", d.name)
		}
	}
}

func (s *scanner) posErr(err error) {
	if pe, ok := err.(*posError); ok {
		s.report(pe.pos, Error, "%v", pe.err)
		return
	}
	s.report(token.NoPos, Error, "%v", err)
}

// docText returns the text of a doc comment without its directives, and
// whether it has a "Deprecated:" paragraph, which it leaves out.
func docText(doc *ast.CommentGroup) (string, bool) {
	if doc == nil {
		return "", false
	}
	var paras []string
	deprecated := false
	for _, para := range strings.Split(strings.TrimSpace(doc.Text()), "\n\n") {
		if strings.HasPrefix(para, "Deprecated:") {
			deprecated = true
			continue
		}
		paras = append(paras, para)
	}
	return strings.TrimSpace(strings.Join(paras, "\n\n")), deprecated
}
