// Package loader reads OpenAPI 3.0, 3.1 and 3.2 documents into the oas
// model, upgrading 3.0 content and resolving every $ref.
package loader

import (
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/dheins/go-openapi/oas"
)

// ErrUnsupportedVersion is returned when the "openapi" (or "swagger")
// field names a version this loader cannot read.
var ErrUnsupportedVersion = errors.New("unsupported OpenAPI version")

// Spec is a loaded document together with what the loader learned about
// it: where every object came from, and the referenced fragments of other
// files.
type Spec struct {
	// Document is the 3.2 model of the document. All references in it and
	// in the objects they lead to have their Resolved fields set.
	Document *oas.Document
	// SourceVersion is the "openapi" value as written, before any upgrade,
	// or "2.0" for a Swagger 2.0 document.
	SourceVersion string
	// Notices report what the document says that the 3.2 model cannot,
	// such as Swagger 2.0 features without an OpenAPI 3 equivalent.
	Notices []Notice

	root      *Source
	sources   map[string]*Source
	locations map[any]Location
	fragments []Fragment
	// schemes are the security schemes that requirements name with a URI
	// reference.
	schemes map[schemeKey]*oas.SecurityScheme
	// written is the root document as JSON before the 3.0 upgrade, for
	// 3.0 documents only; root is still as written otherwise.
	written []byte
}

// Notice is something of a document that the loader could not carry
// over, at the position of the field that says it.
type Notice struct {
	Location Location
	Position Position
	Message  string
}

// schemeKey is a scheme name in the security requirements of owner, the
// *oas.Document or an *oas.Operation; a relative URI reference depends on
// the file the requirement is in.
type schemeKey struct {
	owner any
	name  string
}

// Fragment is an object decoded from a file other than the root document
// because a reference pointed to it.
type Fragment struct {
	Location Location
	// Object is a pointer to the model type the reference expected, such
	// as *oas.Schema or *oas.Parameter.
	Object any
	// JSON is the fragment as written in its file, before any upgrade.
	JSON []byte
	// SourceVersion is the OpenAPI version the fragment was read as.
	SourceVersion string

	src *Source
	// converted is the fragment as converted from Swagger 2.0, which a
	// bundle copies, or nil.
	converted []byte
}

// Position returns the source position of the JSON pointer ptr, taken
// relative to the fragment.
func (f Fragment) Position(ptr string) Position {
	return f.src.Position(f.Location.Pointer + ptr)
}

// Location returns where obj, a pointer into the loaded model, was defined.
func (s *Spec) Location(obj any) (Location, bool) {
	loc, ok := s.locations[obj]
	return loc, ok
}

// SecurityScheme returns the security scheme that name, a key of a
// security requirement of owner (the *oas.Document or an *oas.Operation),
// stands for: the component of that name or, as 3.2 allows, the scheme a
// URI reference points to. It returns nil if there is none.
func (s *Spec) SecurityScheme(owner any, name string) *oas.SecurityScheme {
	if def := s.componentScheme(name); def != nil {
		return def
	}
	return s.schemes[schemeKey{owner, name}].Deref()
}

// componentScheme returns the component scheme called name, or nil.
func (s *Spec) componentScheme(name string) *oas.SecurityScheme {
	if c := s.Document.Components; c != nil {
		def, _ := c.SecuritySchemes.Get(name)
		return def.Deref()
	}
	return nil
}

// Position returns the source position of loc, or the zero Position if the
// file is unknown.
func (s *Spec) Position(loc Location) Position {
	src, ok := s.sources[loc.URI]
	if !ok {
		return Position{}
	}
	return src.Position(loc.Pointer)
}

// Root returns the parsed root document. For 3.0 documents it holds the
// upgraded content.
func (s *Spec) Root() *Source {
	return s.root
}

// Sources returns the files the document was read from: the root, then
// the others by URI. Their content is that of the model: upgraded or
// converted, for documents that are not 3.1 or 3.2.
func (s *Spec) Sources() []*Source {
	out := []*Source{s.root}
	for _, uri := range slices.Sorted(maps.Keys(s.sources)) {
		if src := s.sources[uri]; src != s.root {
			out = append(out, src)
		}
	}
	return out
}

// Fragments returns the objects decoded from other files, in the order
// they were first referenced.
func (s *Spec) Fragments() []Fragment {
	return s.fragments
}

// Error is a loading problem tied to a place in a source file.
type Error struct {
	Location Location
	Position Position
	Err      error
}

func (e *Error) Error() string {
	where := displayURI(e.Location.URI)
	if e.Position.IsValid() {
		where = fmt.Sprintf("%s:%d:%d", where, e.Position.Line, e.Position.Column)
	}
	if e.Location.Pointer != "" {
		return fmt.Sprintf("%s: %s: %v", where, e.Location.Pointer, e.Err)
	}
	return fmt.Sprintf("%s: %v", where, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// Fetcher retrieves the content of an external document named by a
// reference.
type Fetcher func(u *url.URL) ([]byte, error)

// FileFetcher reads file: URLs from the local file system and refuses
// everything else. It is the default, so loading a document never makes
// network requests unless a Fetcher that does is supplied.
func FileFetcher(u *url.URL) ([]byte, error) {
	if u.Scheme != "file" {
		return nil, fmt.Errorf("cannot fetch %s: only local files are allowed (see loader.WithFetcher)", u)
	}
	return os.ReadFile(filePath(u))
}

type config struct {
	fetch   Fetcher
	baseURI string
	check   func(Fragment) error
}

// Option configures loading.
type Option func(*config)

// WithFetcher sets how external documents are retrieved.
func WithFetcher(f Fetcher) Option {
	return func(c *config) { c.fetch = f }
}

// WithFragmentCheck sets a function that inspects every fragment before it
// is decoded; Fragment.Object is then a pointer to the zero value of the
// expected type. If check returns an error, the fragment is not decoded
// and the reference fails with that error.
func WithFragmentCheck(check func(Fragment) error) Option {
	return func(c *config) { c.check = check }
}

// WithBaseURI sets the URI of a document loaded with LoadBytes, which is
// the base for its relative references. The default is a file named
// "openapi" in the working directory.
func WithBaseURI(uri string) Option {
	return func(c *config) { c.baseURI = uri }
}

func newConfig(opts []Option) *config {
	c := &config{fetch: FileFetcher}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Load reads the document at path and everything it references.
func Load(path string, opts ...Option) (*Spec, error) {
	src, err := Parse(path)
	if err != nil {
		return nil, err
	}
	return LoadSource(src, opts...)
}

// LoadBytes reads a JSON or YAML document from memory.
func LoadBytes(data []byte, opts ...Option) (*Spec, error) {
	c := newConfig(opts)
	uri := c.baseURI
	if uri == "" {
		u, err := fileURL("openapi")
		if err != nil {
			return nil, err
		}
		uri = u.String()
	}
	src, err := ParseBytes(data, uri)
	if err != nil {
		return nil, err
	}
	return LoadSource(src, opts...)
}

// LoadSource decodes an already parsed document. It upgrades 3.0 content
// in place, so src should not be reused afterwards.
func LoadSource(src *Source, opts ...Option) (*Spec, error) {
	version := src.Version()
	if err := src.CheckVersion(); err != nil {
		return nil, &Error{Location: Location{URI: src.URI()}, Err: err}
	}

	spec := &Spec{
		SourceVersion: version,
		root:          src,
		sources:       map[string]*Source{src.URI(): src},
		locations:     make(map[any]Location),
		schemes:       make(map[schemeKey]*oas.SecurityScheme),
	}
	r := newResolver(spec, newConfig(opts))
	r.addSource(src)

	switch {
	case is30(version):
		written, err := nodeJSON(src.root)
		if err != nil {
			return nil, &Error{Location: Location{URI: src.URI()}, Err: err}
		}
		spec.written = written
		upgrade30(src.root, (*oas.Document)(nil))
	case is20(version):
		// Bundle then returns the converted document: a 2.0 document with
		// components added would be neither 2.0 nor 3.x.
		r.converter().document(src.root, src.URI())
	}
	var doc oas.Document
	if err := decodeNode(src.root, &doc); err != nil {
		return nil, &Error{Location: Location{URI: src.URI()}, Err: err}
	}
	spec.Document = &doc

	if err := r.register(&doc, src, ""); err != nil {
		return nil, err
	}
	err := r.run()
	if r.conv != nil {
		spec.Notices = r.conv.notices
	}
	if err != nil {
		return nil, err
	}
	return spec, nil
}

// CheckVersion reports whether the document declares a version the loader
// can read: Swagger 2.0, or OpenAPI 3.0, 3.1 or 3.2. It returns
// ErrUnsupportedVersion otherwise.
func (s *Source) CheckVersion() error {
	version := s.Version()
	switch {
	case is20(version), is30(version), strings.HasPrefix(version, "3.1."), strings.HasPrefix(version, "3.2."):
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedVersion, version)
	}
}

func is30(version string) bool {
	return strings.HasPrefix(version, "3.0.")
}

// is20 reports whether version, as Source.Version returns it, is Swagger
// 2.0, which declares it as swagger: "2.0".
func is20(version string) bool {
	return version == "2.0"
}
