package ir

import (
	"fmt"
	"path"
	"reflect"
	"slices"
	"strings"
	"unsafe"

	"github.com/dheins/go-openapi/loader"
)

// Selection chooses the operations a model keeps (Options.Select). An
// operation is selected when its path matches one of Paths or it has one
// of Tags, or when neither is given; it is then left out if its path
// matches one of ExcludePaths or it has one of ExcludeTags. Callbacks go
// with the operation declaring them; webhooks, which have no path, are
// selected by their tags alone.
//
// Path patterns are matched against path templates segment by segment:
// "*" is one segment, "**" any number of them, and other segments are
// path.Match patterns, so "/pets/{id}" matches the template "/pets/{id}".
type Selection struct {
	Paths, Tags, ExcludePaths, ExcludeTags []string
}

// selects reports whether the selection keeps an operation of path (""
// for a webhook) with tags.
func (s *Selection) selects(p string, tags []string) bool {
	hasTag := func(want []string) bool {
		return slices.ContainsFunc(tags, func(t string) bool { return slices.Contains(want, t) })
	}
	matches := func(patterns []string) bool {
		return p != "" && slices.ContainsFunc(patterns, func(pattern string) bool { return matchPath(pattern, p) })
	}
	included := len(s.Paths) == 0 && len(s.Tags) == 0
	if !included {
		included = matches(s.Paths) || hasTag(s.Tags)
	}
	return included && !matches(s.ExcludePaths) && !hasTag(s.ExcludeTags)
}

// matchPath reports whether the path template p matches pattern.
func matchPath(pattern, p string) bool {
	return matchSegments(splitPath(pattern), splitPath(p))
}

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func matchSegments(pattern, segs []string) bool {
	for len(pattern) > 0 {
		if pattern[0] == "**" {
			// Any number of segments, the fewest that let the rest match.
			for i := 0; i <= len(segs); i++ {
				if matchSegments(pattern[1:], segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		if ok, err := path.Match(pattern[0], segs[0]); err != nil || !ok {
			return false
		}
		pattern, segs = pattern[1:], segs[1:]
	}
	return len(segs) == 0
}

// applySelection keeps the selected operations and webhooks, and the
// types and security schemes they use. Everything is built first, so that
// names do not depend on the selection: a client of some operations uses
// the names a server of all of them declares.
func (b *builder) applySelection(sel *Selection) {
	var dropped []loader.Location
	known := map[string]bool{}
	keep := func(ops []*Operation, path func(*Operation) string) []*Operation {
		var kept []*Operation
		for _, o := range ops {
			for _, t := range o.Tags {
				known[t] = true
			}
			if sel.selects(path(o), o.Tags) {
				kept = append(kept, o)
			} else {
				dropped = append(dropped, o.Location)
			}
		}
		return kept
	}
	b.model.Operations = keep(b.model.Operations, func(o *Operation) string { return o.Path })
	b.model.Webhooks = keep(b.model.Webhooks, func(*Operation) string { return "" })
	for _, tag := range append(slices.Clone(sel.Tags), sel.ExcludeTags...) {
		if !known[tag] {
			b.warnAt(b.spec.Document, []string{"tags"}, fmt.Sprintf("no operation has the tag %q", tag))
		}
	}

	used := reachable(append(slices.Clone(b.model.Operations), b.model.Webhooks...))
	var types []*Type
	for _, t := range b.model.Types {
		if used[t] {
			types = append(types, t)
		} else {
			dropped = append(dropped, t.Location)
		}
	}
	b.model.Types = types

	schemes, senderSchemes := map[*SecurityScheme]bool{}, map[*SecurityScheme]bool{}
	uses := func(ops []*Operation, into map[*SecurityScheme]bool) {
		for _, o := range ops {
			for _, req := range o.Security {
				for _, u := range req {
					into[u.Scheme] = true
				}
			}
		}
	}
	uses(b.model.Operations, schemes)
	uses(b.model.Webhooks, senderSchemes)
	for _, o := range b.model.Operations {
		uses(o.Callbacks, senderSchemes)
	}
	b.model.SecuritySchemes = slices.DeleteFunc(b.model.SecuritySchemes, func(s *SecurityScheme) bool { return !schemes[s] })
	b.model.SenderSchemes = slices.DeleteFunc(b.model.SenderSchemes, func(s *SecurityScheme) bool { return !senderSchemes[s] })

	// Warnings about what is not generated are noise.
	b.model.Warnings = slices.DeleteFunc(b.model.Warnings, func(w Warning) bool {
		return slices.ContainsFunc(dropped, func(loc loader.Location) bool { return within(w.Location, loc) })
	})
}

// within reports whether loc is at or below outer.
func within(loc, outer loader.Location) bool {
	return loc.URI == outer.URI && (loc.Pointer == outer.Pointer || strings.HasPrefix(loc.Pointer, outer.Pointer+"/"))
}

var (
	typeType     = reflect.TypeFor[*Type]()
	foreignPaths = []string{reflect.TypeFor[loader.Location]().PkgPath(), "github.com/dheins/go-openapi/oas"}
)

// reachable returns the types the operations use, through any field of
// the model's structures: parameters, bodies, responses and their
// headers and parts, and the fields, items, variants and checks of types
// in turn. It walks them by reflection, so a field added to them later is
// followed too; it does not follow a type's Root, the type it was derived
// from, nor into the oas and loader values the model keeps.
func reachable(ops []*Operation) map[*Type]bool {
	types := map[*Type]bool{}
	type visit struct {
		p unsafe.Pointer
		t reflect.Type
	}
	seen := map[visit]bool{}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() || slices.Contains(foreignPaths, v.Type().Elem().PkgPath()) {
				return
			}
			key := visit{v.UnsafePointer(), v.Type()}
			if seen[key] {
				return
			}
			seen[key] = true
			if v.Type() == typeType {
				// Not Interface, which values of unexported fields refuse.
				types[(*Type)(v.UnsafePointer())] = true
			}
			walk(v.Elem())
		case reflect.Struct:
			if slices.Contains(foreignPaths, v.Type().PkgPath()) {
				return
			}
			for i := range v.NumField() {
				if v.Type() == typeType.Elem() && v.Type().Field(i).Name == "Root" {
					continue
				}
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := range v.Len() {
				walk(v.Index(i))
			}
		case reflect.Map:
			for _, k := range v.MapKeys() {
				walk(v.MapIndex(k))
			}
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		}
	}
	for _, o := range ops {
		walk(reflect.ValueOf(o))
	}
	return types
}
