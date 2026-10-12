// Package loader reads OpenAPI 3.0, 3.1 and 3.2 documents into the oas
// model, upgrading 3.0 content and resolving every $ref.
package loader

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/dheins/go-openapi/oas"
)

var (
	// ErrSwagger2 is returned for Swagger/OpenAPI 2.0 documents, which are
	// not supported.
	ErrSwagger2 = errors.New("swagger 2.0 documents are not supported")
	// ErrUnsupportedVersion is returned when the "openapi" field names a
	// version this loader cannot read.
	ErrUnsupportedVersion = errors.New("unsupported OpenAPI version")
)

// Spec is a loaded document together with what the loader learned about
// it: where every object came from, and the referenced fragments of other
// files.
type Spec struct {
	// Document is the 3.2 model of the document. All references in it and
	// in the objects they lead to have their Resolved fields set.
	Document *oas.Document
	// SourceVersion is the "openapi" value as written, before any upgrade.
	SourceVersion string

	root      *Source
	sources   map[string]*Source
	locations map[any]Location
	fragments []Fragment
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
	}
	r := newResolver(spec, newConfig(opts))
	r.addSource(src)

	if is30(version) {
		upgrade30(src.root, (*oas.Document)(nil))
	}
	var doc oas.Document
	if err := decodeNode(src.root, &doc); err != nil {
		return nil, &Error{Location: Location{URI: src.URI()}, Err: err}
	}
	spec.Document = &doc

	if err := r.register(&doc, src, ""); err != nil {
		return nil, err
	}
	if err := r.run(); err != nil {
		return nil, err
	}
	return spec, nil
}

// CheckVersion reports whether the document declares a version the loader
// can read. It returns ErrSwagger2 or ErrUnsupportedVersion otherwise.
func (s *Source) CheckVersion() error {
	version := s.Version()
	switch {
	case s.stringField("swagger") != "":
		return ErrSwagger2
	case is30(version), strings.HasPrefix(version, "3.1."), strings.HasPrefix(version, "3.2."):
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedVersion, version)
	}
}

func is30(version string) bool {
	return strings.HasPrefix(version, "3.0.")
}
