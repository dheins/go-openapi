package main

import (
	"errors"
	"flag"
	"fmt"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/dheins/go-openapi/codegen"
	"github.com/dheins/go-openapi/ir"
	"github.com/dheins/go-openapi/validate"
)

// runGenerate implements "generate model" and "generate server".
func runGenerate(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "model" && args[0] != "server") {
		fmt.Fprintln(stderr, `goapi generate: expected "model" or "server"`)
		return exitFailure
	}
	target := args[0]
	cmd := "goapi generate " + target
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, cmd+": "+format+"\n", a...)
		return exitFailure
	}

	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	defaultOut := "models"
	if target == "server" {
		defaultOut = "gen"
	}
	out := fs.String("out", defaultOut, "directory to write the generated files to")
	pkg := fs.String("package", "", "package name (default: the base name of -out)")
	optional := fs.String("optional", "wrapper", "optional properties as oapi.Optional (wrapper) or pointers (pointer)")
	var operations, models, importPath *string
	if target == "server" {
		operations = fs.String("operations", "api", `subdirectory and package of the operations' request and response types ("" puts them in the server package)`)
		models = fs.String("models", "model", `subdirectory and package of the schema types ("" puts them with the operations)`)
		importPath = fs.String("import", "", "import path of -out, for the subpackages (default: from the go.mod above -out)")
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitFailure
	}
	if fs.NArg() != 1 {
		return fail("expected one document")
	}
	if *optional != "wrapper" && *optional != "pointer" {
		return fail("unknown -optional %q", *optional)
	}
	if *pkg == "" {
		*pkg = filepath.Base(filepath.Clean(*out))
	}
	if !token.IsIdentifier(*pkg) || token.IsKeyword(*pkg) {
		return fail("%q is not a valid package name; use -package", *pkg)
	}
	// Each subpackage is imported by the server, so its path is needed.
	var opsImport, modelsImport string
	if target == "server" && (*operations != "" || *models != "") {
		for _, f := range []struct{ flag, name string }{{"operations", *operations}, {"models", *models}} {
			if f.name != "" && (!token.IsIdentifier(f.name) || token.IsKeyword(f.name)) {
				return fail("%q is not a valid package name for -%s", f.name, f.flag)
			}
		}
		if *operations != "" && *operations == *models {
			return fail("-operations and -models must differ")
		}
		base := *importPath
		if base == "" {
			var err error
			if base, err = dirImportPath(*out); err != nil {
				return fail("%v; use -import", err)
			}
		}
		if *operations != "" {
			opsImport = path.Join(base, *operations)
		}
		if *models != "" {
			modelsImport = path.Join(base, *models)
		}
	}
	file := fs.Arg(0)

	res, err := validate.File(file)
	if err != nil {
		return fail("%v", err)
	}
	if res.HasErrors() {
		for _, d := range res.Diagnostics {
			fmt.Fprintln(stderr, d)
		}
		fmt.Fprintf(stderr, "%s: the document is invalid\n", cmd)
		return exitInvalid
	}

	model := ir.Build(res.Spec, ir.Options{
		OptionalPointers: *optional == "pointer",
		Operations:       target == "server",
	})
	for _, w := range model.Warnings {
		where := w.Location.File()
		if w.Position.IsValid() {
			where = fmt.Sprintf("%s:%d:%d", where, w.Position.Line, w.Position.Column)
		}
		fmt.Fprintf(stderr, "%s: warning: %s\n", where, w.Message)
	}

	opts := codegen.Options{
		Package: *pkg,
		Source:  filepath.ToSlash(filepath.Base(file)),
		Title:   strings.TrimSpace(res.Spec.Document.Info.Title),
	}
	generate := codegen.Models
	if target == "server" {
		generate = codegen.Server
		opts.OperationsPackage, opts.OperationsImport = *operations, opsImport
		opts.ModelsPackage, opts.ModelsImport = *models, modelsImport
		// Embed the document as written, not as upgraded for the model,
		// with the objects it refers to in other files copied in, so that
		// clients of server.WithDocument can follow every reference.
		if opts.Document, err = res.Spec.Bundle(); err != nil {
			return fail("%v", err)
		}
	}
	files, err := generate(model, opts)
	if err != nil {
		return fail("%v", err)
	}
	for _, f := range files {
		name := filepath.Join(*out, filepath.FromSlash(f.Name))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return fail("%v", err)
		}
		if err := os.WriteFile(name, f.Content, 0o644); err != nil {
			return fail("%v", err)
		}
	}
	fmt.Fprintf(stdout, "wrote %d files to %s\n", len(files), *out)
	return exitOK
}

// dirImportPath returns the Go import path of dir, from the module path in
// the nearest go.mod at or above it.
func dirImportPath(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for root := abs; ; {
		data, err := os.ReadFile(filepath.Join(root, "go.mod"))
		switch {
		case err == nil:
			module := modulePath(data)
			if module == "" {
				return "", fmt.Errorf("%s has no module line", filepath.Join(root, "go.mod"))
			}
			rel, err := filepath.Rel(root, abs)
			if err != nil {
				return "", err
			}
			return path.Join(module, filepath.ToSlash(rel)), nil
		case !errors.Is(err, fs.ErrNotExist):
			return "", err
		}
		parent := filepath.Dir(root)
		if parent == root {
			return "", fmt.Errorf("no go.mod found at or above %s", abs)
		}
		root = parent
	}
}

// modulePath returns the path of the module line of a go.mod file.
func modulePath(gomod []byte) string {
	for _, line := range strings.Split(string(gomod), "\n") {
		line, _, _ = strings.Cut(line, "//")
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module"); ok && (rest == "" || rest[0] == ' ' || rest[0] == '\t') {
			return strings.Trim(strings.TrimSpace(rest), `"`+"`")
		}
	}
	return ""
}
