package codescan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// listedPackage is what go list -json says about a package.
type listedPackage struct {
	ImportPath string
	Name       string
	Dir        string
	GoFiles    []string
	Export     string
	DepOnly    bool
	ImportMap  map[string]string
	Error      *struct{ Err string }
}

// pkg is a package being scanned: parsed with its comments and
// type-checked from source.
type pkg struct {
	path  string
	name  string
	files []*ast.File
	types *types.Package
	info  *types.Info
}

// load lists the packages patterns match, run in dir, and parses and
// type-checks them; their dependencies are read from export data, which
// go list builds. The packages are in dependency order.
func load(fset *token.FileSet, dir string, patterns []string) ([]*pkg, error) {
	args := append([]string{"list", "-json", "-export", "-deps", "--"}, patterns...)
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go list: %v: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	var listed []*listedPackage
	exports := map[string]string{}
	dec := json.NewDecoder(&stdout)
	for {
		var lp listedPackage
		if err := dec.Decode(&lp); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("go list: %w", err)
		}
		if lp.Error != nil {
			return nil, fmt.Errorf("package %s: %s", lp.ImportPath, lp.Error.Err)
		}
		exports[lp.ImportPath] = lp.Export
		listed = append(listed, &lp)
	}
	gc := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		file, ok := exports[path]
		if !ok || file == "" {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(file)
	})
	// Scanned packages are imported as checked from source, so that the
	// types of one are those of the other; go list -deps lists a package
	// after its dependencies.
	checked := map[string]*types.Package{}
	var pkgs []*pkg
	for _, lp := range listed {
		if lp.DepOnly {
			continue
		}
		p := &pkg{path: lp.ImportPath, name: lp.Name}
		for _, f := range lp.GoFiles {
			file, err := parser.ParseFile(fset, filepath.Join(lp.Dir, f), nil, parser.ParseComments)
			if err != nil {
				return nil, err
			}
			p.files = append(p.files, file)
		}
		p.info = &types.Info{Defs: map[*ast.Ident]types.Object{}, Types: map[ast.Expr]types.TypeAndValue{}}
		conf := types.Config{Importer: importerFunc(func(path string) (*types.Package, error) {
			if mapped, ok := lp.ImportMap[path]; ok {
				path = mapped
			}
			if tp, ok := checked[path]; ok {
				return tp, nil
			}
			return gc.Import(path)
		})}
		tp, err := conf.Check(lp.ImportPath, fset, p.files, p.info)
		if err != nil {
			return nil, fmt.Errorf("type-checking %s: %w", lp.ImportPath, err)
		}
		p.types = tp
		checked[lp.ImportPath] = tp
		pkgs = append(pkgs, p)
	}
	return pkgs, nil
}

type importerFunc func(path string) (*types.Package, error)

func (f importerFunc) Import(path string) (*types.Package, error) { return f(path) }
