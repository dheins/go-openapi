package codegen

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
	"text/template"

	"go.yaml.in/yaml/v3"

	"github.com/dheins/go-openapi/ir"
)

// TargetsFile is the file of a templates directory that declares the
// files its templates add.
const TargetsFile = "goapi.yaml"

// Target is a file of your own, written from a block of a templates
// directory, as its goapi.yaml declares it.
type Target struct {
	// Template is the {{define}} block the file is written from.
	Template string `yaml:"template"`
	// Scope is what one file is written for: "document" (once, given the
	// *ir.Model), "model" (each component's root type, given the
	// *ir.Type) or "operation" (each operation, given the *ir.Operation).
	Scope string `yaml:"scope"`
	// Path is the file's name, a template given the same data, such as
	// "{{snake .Name}}_repo.go". A .go file is Go source of the target's
	// package: it gets the package clause, the imports of imp and gofmt.
	// Other files are written as the block renders them, and may be in
	// subdirectories.
	Path string `yaml:"path"`
	// Package, if set, is the directory below the output the file goes
	// to, such as "repo" or "internal/repo": one of the generated
	// packages', or a package of its own, named after its last element.
	Package string `yaml:"package"`
	// When, if set, is a template given the same data: the file is
	// written when it renders "true".
	When string `yaml:"when"`
}

// LoadTargets reads the targets of the templates directory fsys, from its
// goapi.yaml; a directory without one has none.
func LoadTargets(fsys fs.FS) ([]Target, error) {
	if fsys == nil {
		return nil, nil
	}
	data, err := fs.ReadFile(fsys, TargetsFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var config struct {
		Targets []Target `yaml:"targets"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	// An empty file has no targets.
	if err := dec.Decode(&config); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("templates: %s: %w", TargetsFile, err)
	}
	for i, t := range config.Targets {
		where := fmt.Sprintf("templates: %s: target %d", TargetsFile, i+1)
		switch {
		case t.Template == "":
			return nil, fmt.Errorf("%s has no template", where)
		case t.Scope != "document" && t.Scope != "model" && t.Scope != "operation":
			return nil, fmt.Errorf("%s (%s): scope %q is not document, model or operation", where, t.Template, t.Scope)
		case t.Path == "":
			return nil, fmt.Errorf("%s (%s) has no path", where, t.Template)
		}
		if t.Package != "" {
			if err := CheckPackageDir(t.Package); err != nil {
				return nil, fmt.Errorf("%s (%s): %w", where, t.Template, err)
			}
		}
	}
	return config.Targets, nil
}

// targetNames returns the blocks the targets of fsys are written from,
// which its templates may define without a built-in block of the name.
func targetNames(fsys fs.FS) ([]string, error) {
	targets, err := LoadTargets(fsys)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, t := range targets {
		names = append(names, t.Template)
	}
	return names, nil
}

// namingFuncs are the functions that turn names into others, for the
// paths of targets and their blocks.
var namingFuncs = template.FuncMap{
	"snake":  func(s string) string { return strings.TrimSuffix(fileName(s), ".go") },
	"camel":  lowerFirst,
	"pascal": ir.GoName,
	"lower":  strings.ToLower,
	"upper":  strings.ToUpper,
}

// expand renders a template of a target's path or condition with data.
func expand(name, text string, data any, funcs template.FuncMap) (string, error) {
	t, err := template.New(name).Funcs(funcs).Parse(text)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

// items returns what target t writes a file for, from m.
func (t Target) items(m *ir.Model, haveOperations bool) ([]any, error) {
	switch t.Scope {
	case "document":
		return []any{m}, nil
	case "model":
		var out []any
		for _, typ := range m.Types {
			if typ.Root == typ {
				out = append(out, typ)
			}
		}
		return out, nil
	}
	if !haveOperations {
		return nil, fmt.Errorf("templates: target %s: models have no operations; scope operation needs goapi generate server or client", t.Template)
	}
	var out []any
	for _, o := range m.Operations {
		if o.Unsupported == "" {
			out = append(out, o)
		}
	}
	return out, nil
}

// addTargets writes the files of the targets of opts.Templates for m.
// pkg returns the generator of a package directory ("" for the root
// package), creating one for a package of a target's own.
func addTargets(m *ir.Model, opts Options, haveOperations bool, pkg func(dir string) (*generator, error)) error {
	targets, err := LoadTargets(opts.Templates)
	if err != nil {
		return err
	}
	for _, t := range targets {
		g, err := pkg(t.Package)
		if err != nil {
			return fmt.Errorf("templates: target %s: %w", t.Template, err)
		}
		items, err := t.items(m, haveOperations)
		if err != nil {
			return err
		}
		funcs := (&fileGen{gen: g, imports: map[string]string{}}).funcs()
		for _, item := range items {
			if t.When != "" {
				ok, err := expand(t.Template+" when", t.When, item, funcs)
				if err != nil {
					return fmt.Errorf("templates: target %s: when: %w", t.Template, err)
				}
				if strings.TrimSpace(ok) != "true" {
					continue
				}
			}
			name, err := expand(t.Template+" path", t.Path, item, funcs)
			if err != nil {
				return fmt.Errorf("templates: target %s: path: %w", t.Template, err)
			}
			clean := path.Clean(name)
			goFile := strings.HasSuffix(clean, ".go")
			switch {
			case name == "" || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(name, "\\"):
				return fmt.Errorf("templates: target %s: path %q is not below its package", t.Template, name)
			case goFile && strings.Contains(clean, "/"):
				return fmt.Errorf("templates: target %s: Go file %q must be in its package's directory; use package for another", t.Template, name)
			case g.fileNames[clean]:
				return fmt.Errorf("templates: target %s: %s is written twice", t.Template, path.Join(g.dir, clean))
			}
			var content []byte
			if goFile {
				content, err = g.render(t.Template, item, nil)
			} else {
				content, err = g.renderText(t.Template, item)
			}
			if err != nil {
				return fmt.Errorf("templates: target %s: %s: %w", t.Template, clean, err)
			}
			g.fileNames[clean] = true
			g.files = append(g.files, File{Name: clean, Content: content})
		}
	}
	return nil
}
