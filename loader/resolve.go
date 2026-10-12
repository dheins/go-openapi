package loader

import (
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/dheins/go-openapi/oas"
)

// resolver resolves every $ref in a document eagerly.
//
// Each object is identified by its canonical location (file URI plus JSON
// pointer). Objects of the root document are registered before any
// reference is followed, so a local reference such as
// "#/components/schemas/Pet" yields the very object found under
// Document.Components. Targets elsewhere are decoded once, as the type the
// reference expects, and then registered and walked in turn, so reference
// cycles terminate.
type resolver struct {
	spec *Spec
	cfg  *config

	// resources maps a document or $id URI (without fragment, localized)
	// to its location; anchors maps "resourceURI#name" likewise.
	resources map[string]Location
	anchors   map[string]Location
	// aliases map a document's $self to the URI it was retrieved from.
	aliases []alias

	objects map[Location]any
	pending map[any]pendingRef
	queue   []any
	// extras are objects with references other than $ref: discriminator
	// mappings, link operationRefs and $dynamicRefs.
	extras []extra
	done   map[any]bool
	errs   []error
}

type alias struct {
	from, to string
}

type extra struct {
	obj  any
	base *url.URL
	loc  Location
}

type pendingRef struct {
	ref  string
	base *url.URL
	loc  Location
}

// errReported marks a failure that has already been added to resolver.errs.
var errReported = errors.New("reported")

func newResolver(spec *Spec, cfg *config) *resolver {
	return &resolver{
		spec:      spec,
		cfg:       cfg,
		resources: make(map[string]Location),
		anchors:   make(map[string]Location),
		objects:   make(map[Location]any),
		pending:   make(map[any]pendingRef),
		done:      make(map[any]bool),
	}
}

// addSource makes src known as a resource and indexes its $id and $anchor
// keywords.
func (r *resolver) addSource(src *Source) {
	if b := src.base.String(); b != src.URI() {
		r.aliases = append(r.aliases, alias{from: b, to: src.URI()})
	}
	r.resources[src.URI()] = Location{URI: src.URI()}
	r.index(src, src.root, "", src.base, false)
}

// dataKeywords hold instance data rather than schemas, so an "$id" inside
// them is just data.
var dataKeywords = map[string]bool{
	"example": true, "examples": true, "default": true, "const": true,
	"enum": true, "value": true, "dataValue": true,
}

// nameMapKeywords map arbitrary names to schemas, so their keys are never
// keywords.
var nameMapKeywords = map[string]bool{
	"properties": true, "patternProperties": true, "$defs": true,
	"dependentSchemas": true, "schemas": true,
}

func (r *resolver) index(src *Source, n *yaml.Node, ptr string, base *url.URL, namesOnly bool) {
	if n.Kind == yaml.AliasNode {
		// The aliased node is indexed where its anchor is; following
		// aliases here could expand exponentially ("billion laughs").
		return
	}
	switch n.Kind {
	case yaml.SequenceNode:
		for i, item := range n.Content {
			r.index(src, item, oas.JoinPointer(ptr, fmt.Sprint(i)), base, false)
		}
	case yaml.MappingNode:
		loc := Location{URI: src.URI(), Pointer: ptr}
		if !namesOnly {
			if id := mappingValue(n, "$id"); id != nil && id.Kind == yaml.ScalarNode {
				u, err := url.Parse(id.Value)
				if err != nil {
					r.fail(Location{src.URI(), oas.JoinPointer(ptr, "$id")}, fmt.Errorf("invalid $id: %w", err))
					return
				}
				base = base.ResolveReference(u)
				key := r.localize(withoutFragment(base).String())
				if prev, ok := r.resources[key]; ok && prev != loc {
					r.fail(Location{src.URI(), oas.JoinPointer(ptr, "$id")}, fmt.Errorf("$id %q is already used at %s", id.Value, prev))
				} else {
					r.resources[key] = loc
				}
			}
			resource := r.localize(withoutFragment(base).String())
			for _, kw := range []string{"$anchor", "$dynamicAnchor"} {
				if a := mappingValue(n, kw); a != nil && a.Kind == yaml.ScalarNode {
					r.anchors[resource+"#"+a.Value] = loc
				}
			}
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i].Value
			if !namesOnly && dataKeywords[key] {
				continue
			}
			r.index(src, n.Content[i+1], oas.JoinPointer(ptr, key), base, !namesOnly && nameMapKeywords[key])
		}
	}
}

// localize maps a URI under a document's $self to the document's actual
// location, so that references written against $self can be loaded.
func (r *resolver) localize(uri string) string {
	for _, a := range r.aliases {
		if uri == a.from {
			return a.to
		}
	}
	for _, a := range r.aliases {
		if dir := dirURI(a.from); strings.HasPrefix(uri, dir) {
			return dirURI(a.to) + uri[len(dir):]
		}
	}
	return uri
}

// register records obj (decoded from src at ptr) and everything below it,
// and queues their references.
func (r *resolver) register(obj any, src *Source, ptr string) error {
	base, err := src.baseAt(ptr)
	if err != nil {
		return r.errorAt(Location{src.URI(), ptr}, err)
	}
	err = oas.Walk(obj, ptr, base, func(o any, p string, b *url.URL) error {
		loc := Location{URI: src.URI(), Pointer: p}
		if _, exists := r.objects[loc]; !exists {
			r.objects[loc] = o
		}
		if _, exists := r.spec.locations[o]; !exists {
			r.spec.locations[o] = loc
		}
		if ref := refOf(o); ref != "" {
			r.pending[o] = pendingRef{ref: ref, base: b, loc: loc}
			r.queue = append(r.queue, o)
		}
		switch v := o.(type) {
		case *oas.Discriminator:
			if v.Mapping.Len() > 0 || v.DefaultMapping != "" {
				r.extras = append(r.extras, extra{o, b, loc})
			}
		case *oas.Link:
			if v.OperationRef != "" {
				r.extras = append(r.extras, extra{o, b, loc})
			}
		case *oas.Schema:
			if v.DynamicRef != "" {
				r.extras = append(r.extras, extra{o, b, loc})
			}
		}
		return nil
	})
	if err != nil {
		return r.errorAt(Location{src.URI(), ptr}, err)
	}
	return nil
}

// run resolves queued references until none are left. Decoding a target
// may queue more.
func (r *resolver) run() error {
	for i, j := 0, 0; i < len(r.queue) || j < len(r.extras); {
		for ; i < len(r.queue); i++ {
			r.resolve(r.queue[i], map[any]bool{})
		}
		for ; j < len(r.extras); j++ {
			r.resolveExtra(r.extras[j])
		}
	}
	return errors.Join(r.errs...)
}

var (
	schemaType    = reflect.TypeFor[*oas.Schema]()
	operationType = reflect.TypeFor[*oas.Operation]()
)

// resolveExtra resolves the references of e that are not $ref.
func (r *resolver) resolveExtra(e extra) {
	switch v := e.obj.(type) {
	case *oas.Discriminator:
		for key, value := range v.Mapping.All() {
			target, err := r.mappingTarget(value, e.base)
			if err != nil {
				r.fail(Location{e.loc.URI, oas.JoinPointer(e.loc.Pointer, "mapping", key)}, err)
				continue
			}
			if v.Targets == nil {
				v.Targets = &oas.OrderedMap[*oas.Schema]{}
			}
			v.Targets.Set(key, target)
		}
		if v.DefaultMapping != "" {
			target, err := r.mappingTarget(v.DefaultMapping, e.base)
			if err != nil {
				r.fail(Location{e.loc.URI, oas.JoinPointer(e.loc.Pointer, "defaultMapping")}, err)
				return
			}
			v.DefaultTarget = target
		}
	case *oas.Link:
		target, err := r.targetOf(v.OperationRef, e.base, operationType)
		if err != nil {
			r.fail(Location{e.loc.URI, oas.JoinPointer(e.loc.Pointer, "operationRef")}, err)
			return
		}
		v.Operation = target.(*oas.Operation)
	case *oas.Schema:
		target, err := r.targetOf(v.DynamicRef, e.base, schemaType)
		if err != nil {
			r.fail(Location{e.loc.URI, oas.JoinPointer(e.loc.Pointer, "$dynamicRef")}, err)
			return
		}
		v.DynamicResolved = target.(*oas.Schema)
	}
}

// mappingTarget resolves a discriminator mapping value: the name of a
// component schema of the root document, or else a URI reference.
func (r *resolver) mappingTarget(value string, base *url.URL) (*oas.Schema, error) {
	if c := r.spec.Document.Components; c != nil {
		if s, ok := c.Schemas.Get(value); ok && s != nil {
			return s, nil
		}
	}
	target, err := r.targetOf(value, base, schemaType)
	if err != nil {
		return nil, fmt.Errorf("mapping value %q is neither a component schema nor a resolvable reference: %w", value, err)
	}
	return target.(*oas.Schema), nil
}

// resolve sets obj.Resolved and returns it. For model types other than
// Schema, chains of references are followed to the final object. Failures
// are recorded once, at the object whose reference failed.
func (r *resolver) resolve(obj any, stack map[any]bool) (any, error) {
	if t := resolvedOf(obj); t != nil {
		return t, nil
	}
	pr := r.pending[obj]
	if stack[obj] {
		r.fail(refLocation(pr.loc), errors.New("circular reference"))
		return nil, errReported
	}
	if r.done[obj] {
		// Attempted before and failed; the failure is already recorded.
		return nil, errReported
	}
	r.done[obj] = true
	stack[obj] = true

	t, err := r.targetOf(pr.ref, pr.base, reflect.TypeOf(obj))
	if err != nil {
		r.fail(refLocation(pr.loc), err)
		return nil, errReported
	}
	if _, isSchema := obj.(*oas.Schema); !isSchema && refOf(t) != "" {
		if t, err = r.resolve(t, stack); err != nil {
			return nil, err
		}
	}
	setResolved(obj, t)
	return t, nil
}

// targetOf returns the object ref (relative to base) points to, decoding
// it as want, a pointer to a model type, if needed.
func (r *resolver) targetOf(ref string, base *url.URL, want reflect.Type) (any, error) {
	u, err := url.Parse(ref)
	if err != nil {
		return nil, fmt.Errorf("invalid reference %q: %w", ref, err)
	}
	abs := u
	if base != nil {
		abs = base.ResolveReference(u)
	}
	if !abs.IsAbs() {
		return nil, fmt.Errorf("cannot resolve relative reference %q without a base URI", ref)
	}
	loc, err := r.locate(abs)
	if err != nil {
		return nil, fmt.Errorf("reference %q: %w", ref, err)
	}
	if existing, ok := r.objects[loc]; ok {
		if got := reflect.TypeOf(existing); got != want {
			return nil, fmt.Errorf("reference %q points to a %s, expected a %s", ref, typeName(got), typeName(want))
		}
		return existing, nil
	}
	return r.decode(loc, want)
}

// locate turns an absolute reference into a canonical location, loading
// the referenced document if necessary.
func (r *resolver) locate(abs *url.URL) (Location, error) {
	doc := r.localize(withoutFragment(abs).String())
	start, ok := r.resources[doc]
	if !ok {
		src, err := r.fetch(doc)
		if err != nil {
			return Location{}, err
		}
		start = Location{URI: src.URI()}
	}
	frag := abs.Fragment
	switch {
	case frag == "":
		return start, nil
	case strings.HasPrefix(frag, "/"):
		loc := Location{URI: start.URI, Pointer: start.Pointer + frag}
		if _, err := r.spec.sources[loc.URI].node(loc.Pointer); err != nil {
			return Location{}, err
		}
		return loc, nil
	default:
		loc, ok := r.anchors[doc+"#"+frag]
		if !ok {
			return Location{}, fmt.Errorf("anchor %q not found in %s", frag, displayURI(doc))
		}
		return loc, nil
	}
}

func (r *resolver) fetch(uri string) (*Source, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	data, err := r.cfg.fetch(u)
	if err != nil {
		return nil, err
	}
	src, err := ParseBytes(data, uri)
	if err != nil {
		return nil, err
	}
	r.spec.sources[uri] = src
	r.addSource(src)
	return src, nil
}

// decode reads the value at loc as a new object of type typ (a pointer to
// a model type) and registers it.
func (r *resolver) decode(loc Location, typ reflect.Type) (any, error) {
	src := r.spec.sources[loc.URI]
	n, err := src.node(loc.Pointer)
	if err != nil {
		return nil, err
	}
	raw, err := nodeJSON(n)
	if err != nil {
		return nil, err
	}
	version := src.Version()
	if version == "" {
		version = r.spec.SourceVersion
	}
	obj := reflect.New(typ.Elem()).Interface()
	frag := Fragment{Location: loc, Object: obj, JSON: raw, SourceVersion: version, src: src}
	if r.cfg.check != nil {
		if err := r.cfg.check(frag); err != nil {
			return nil, err
		}
	}
	if is30(version) {
		upgrade30(n, obj)
	}
	if err := decodeNode(n, obj); err != nil {
		return nil, fmt.Errorf("decoding %s as %s: %w", loc, typeName(typ), err)
	}
	r.spec.fragments = append(r.spec.fragments, frag)
	if err := r.register(obj, src, loc.Pointer); err != nil {
		return nil, err
	}
	return obj, nil
}

func (r *resolver) fail(loc Location, err error) {
	r.errs = append(r.errs, r.errorAt(loc, err))
}

func (r *resolver) errorAt(loc Location, err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Location: loc, Position: r.spec.Position(loc), Err: err}
}

func refLocation(loc Location) Location {
	return Location{URI: loc.URI, Pointer: oas.JoinPointer(loc.Pointer, "$ref")}
}

func typeName(t reflect.Type) string {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Name()
}

func refOf(obj any) string {
	switch o := obj.(type) {
	case *oas.PathItem:
		return o.Ref
	case *oas.Parameter:
		return o.Ref
	case *oas.RequestBody:
		return o.Ref
	case *oas.MediaType:
		return o.Ref
	case *oas.Response:
		return o.Ref
	case *oas.Callback:
		return o.Ref
	case *oas.Example:
		return o.Ref
	case *oas.Link:
		return o.Ref
	case *oas.Header:
		return o.Ref
	case *oas.SecurityScheme:
		return o.Ref
	case *oas.Schema:
		return o.Ref
	}
	return ""
}

// resolvedOf returns obj.Resolved, or nil when unset.
func resolvedOf(obj any) any {
	switch o := obj.(type) {
	case *oas.PathItem:
		return nilIfNil(o.Resolved)
	case *oas.Parameter:
		return nilIfNil(o.Resolved)
	case *oas.RequestBody:
		return nilIfNil(o.Resolved)
	case *oas.MediaType:
		return nilIfNil(o.Resolved)
	case *oas.Response:
		return nilIfNil(o.Resolved)
	case *oas.Callback:
		return nilIfNil(o.Resolved)
	case *oas.Example:
		return nilIfNil(o.Resolved)
	case *oas.Link:
		return nilIfNil(o.Resolved)
	case *oas.Header:
		return nilIfNil(o.Resolved)
	case *oas.SecurityScheme:
		return nilIfNil(o.Resolved)
	case *oas.Schema:
		return nilIfNil(o.Resolved)
	}
	return nil
}

func nilIfNil[T any](p *T) any {
	if p == nil {
		return nil
	}
	return p
}

// setResolved sets obj.Resolved to t; target guarantees matching types.
func setResolved(obj, t any) {
	switch o := obj.(type) {
	case *oas.PathItem:
		o.Resolved = t.(*oas.PathItem)
	case *oas.Parameter:
		o.Resolved = t.(*oas.Parameter)
	case *oas.RequestBody:
		o.Resolved = t.(*oas.RequestBody)
	case *oas.MediaType:
		o.Resolved = t.(*oas.MediaType)
	case *oas.Response:
		o.Resolved = t.(*oas.Response)
	case *oas.Callback:
		o.Resolved = t.(*oas.Callback)
	case *oas.Example:
		o.Resolved = t.(*oas.Example)
	case *oas.Link:
		o.Resolved = t.(*oas.Link)
	case *oas.Header:
		o.Resolved = t.(*oas.Header)
	case *oas.SecurityScheme:
		o.Resolved = t.(*oas.SecurityScheme)
	case *oas.Schema:
		o.Resolved = t.(*oas.Schema)
	}
}
