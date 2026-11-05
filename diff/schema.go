package diff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"slices"
	"strings"

	"github.com/dheins/go-openapi/oas"
)

// deref follows the references of s to the schema they lead to.
func deref(s *oas.Schema) *oas.Schema {
	seen := map[*oas.Schema]bool{}
	for s != nil && s.Ref != "" && s.Resolved != nil && !seen[s] {
		seen[s] = true
		s = s.Resolved
	}
	return s
}

// schema compares the schemas of a value at where, which goes in dir: a
// client sending it breaks when the new schema accepts less, one
// receiving it when the new schema allows more.
func (d *differ) schema(o, n *oas.Schema, where string, dir direction) {
	d.compare(o, n, where, dir, false)
}

// textSchema compares the schemas of a parameter or header, whose value is
// text: a number or a boolean is a string too.
func (d *differ) textSchema(o, n *oas.Schema, where string, dir direction) {
	d.compare(o, n, where, dir, true)
}

func (d *differ) compare(o, n *oas.Schema, where string, dir direction, text bool) {
	o, n = deref(o), deref(n)
	if o == nil || n == nil || o.Bool != nil || n.Bool != nil {
		return
	}
	pair := schemaPair{o, n, dir}
	if d.seen[pair] {
		return
	}
	d.seen[pair] = true

	d.types(o, n, where, dir, text)
	d.enums(o, n, where, dir)
	d.constraints(o, n, where, dir)
	d.properties(o, n, where, dir)
	d.schema(o.Items, n.Items, where+"/items", dir)
	for i := range min(len(o.PrefixItems), len(n.PrefixItems)) {
		d.schema(o.PrefixItems[i], n.PrefixItems[i], fmt.Sprintf("%s/prefixItems/%d", where, i), dir)
	}
	d.schema(o.AdditionalProperties, n.AdditionalProperties, where+"/additionalProperties", dir)
	d.variants("oneOf", o.OneOf, n.OneOf, o, n, where, dir)
	d.variants("anyOf", o.AnyOf, n.AnyOf, o, n, where, dir)
	for i := range min(len(o.AllOf), len(n.AllOf)) {
		d.schema(o.AllOf[i], n.AllOf[i], fmt.Sprintf("%s/allOf/%d", where, i), dir)
	}
}

// accepts reports whether a value of JSON type t is one of types, an
// integer being a number too; no types accept any.
func accepts(types oas.Types, t string) bool {
	return len(types) == 0 || types.Has(t) || t == "integer" && types.Has("number")
}

func (d *differ) types(o, n *oas.Schema, where string, dir direction, text bool) {
	// As text, any value a client sends is a string, and any it receives
	// as a string reads as one.
	if text && (dir == request && accepts(n.Type, "string") || dir == response && accepts(o.Type, "string")) {
		return
	}
	if dir == request {
		if len(n.Type) > 0 && len(o.Type) == 0 {
			d.add(Error, RuleTypeNarrowed, o, n, "%s: only %s is accepted now", where, strings.Join(n.Type, ", "))
			return
		}
		for _, t := range o.Type {
			if !accepts(n.Type, t) {
				d.add(Error, RuleTypeNarrowed, o, n, "%s: %s is not accepted any more", where, t)
			}
		}
		return
	}
	if len(o.Type) > 0 && len(n.Type) == 0 {
		d.add(Error, RuleTypeWidened, o, n, "%s: may be of any type now", where)
		return
	}
	for _, t := range n.Type {
		if !accepts(o.Type, t) {
			d.add(Error, RuleTypeWidened, o, n, "%s: may be %s now", where, t)
		}
	}
}

// enumValues returns the values of an enum, as compact JSON.
func enumValues(s *oas.Schema) []string {
	var out []string
	for _, v := range s.Enum {
		var b bytes.Buffer
		if json.Compact(&b, v) != nil {
			b.Reset()
			b.Write(v)
		}
		out = append(out, b.String())
	}
	return out
}

func (d *differ) enums(o, n *oas.Schema, where string, dir direction) {
	before, after := enumValues(o), enumValues(n)
	missing := func(from, in []string) []string {
		var out []string
		for _, v := range from {
			if !slices.Contains(in, v) {
				out = append(out, v)
			}
		}
		return out
	}
	switch {
	case len(before) == 0 && len(after) == 0:
	case len(before) == 0:
		if dir == request {
			d.add(Error, RuleEnumNarrowed, o, n, "%s: only %s are accepted now", where, strings.Join(after, ", "))
		}
	case len(after) == 0:
		if dir == response {
			d.add(Warning, RuleEnumWidened, o, n, "%s: may be any value now, not only %s", where, strings.Join(before, ", "))
		}
	default:
		if removed := missing(before, after); len(removed) > 0 {
			sev := Info
			if dir == request {
				sev = Error
			}
			d.add(sev, RuleEnumNarrowed, o, n, "%s: %s removed from the enum", where, strings.Join(removed, ", "))
		}
		if added := missing(after, before); len(added) > 0 {
			sev := Info
			if dir == response {
				// Clients may switch on the values they know.
				sev = Warning
			}
			d.add(sev, RuleEnumWidened, o, n, "%s: %s added to the enum", where, strings.Join(added, ", "))
		}
	}
}

// bound is a lower or upper limit of a schema, as a number, nil without
// one.
type bound struct {
	name  string
	lower bool
	get   func(*oas.Schema) *big.Rat
}

func num(n json.Number) *big.Rat {
	if n == "" {
		return nil
	}
	r, ok := new(big.Rat).SetString(string(n))
	if !ok {
		return nil
	}
	return r
}

func count(p *int) *big.Rat {
	if p == nil {
		return nil
	}
	return big.NewRat(int64(*p), 1)
}

var bounds = []bound{
	{"minimum", true, func(s *oas.Schema) *big.Rat { return num(s.Minimum) }},
	{"exclusiveMinimum", true, func(s *oas.Schema) *big.Rat { return num(s.ExclusiveMinimum) }},
	{"maximum", false, func(s *oas.Schema) *big.Rat { return num(s.Maximum) }},
	{"exclusiveMaximum", false, func(s *oas.Schema) *big.Rat { return num(s.ExclusiveMaximum) }},
	{"minLength", true, func(s *oas.Schema) *big.Rat { return count(s.MinLength) }},
	{"maxLength", false, func(s *oas.Schema) *big.Rat { return count(s.MaxLength) }},
	{"minItems", true, func(s *oas.Schema) *big.Rat { return count(s.MinItems) }},
	{"maxItems", false, func(s *oas.Schema) *big.Rat { return count(s.MaxItems) }},
	{"minProperties", true, func(s *oas.Schema) *big.Rat { return count(s.MinProperties) }},
	{"maxProperties", false, func(s *oas.Schema) *big.Rat { return count(s.MaxProperties) }},
}

// constraints compares the limits, patterns and formats of values: one
// that accepts less breaks a client sending the value, one that allows
// more may surprise a client receiving it.
func (d *differ) constraints(o, n *oas.Schema, where string, dir direction) {
	tightened := func(what string, args ...any) {
		if dir == request {
			d.add(Error, RuleConstraintTightened, o, n, "%s: "+what, append([]any{where}, args...)...)
		}
	}
	loosened := func(what string, args ...any) {
		if dir == response {
			d.add(Warning, RuleConstraintLoosened, o, n, "%s: "+what, append([]any{where}, args...)...)
		}
	}
	for _, b := range bounds {
		before, after := b.get(o), b.get(n)
		switch {
		case before == nil && after == nil:
		case before == nil:
			tightened("%s %s was added", b.name, after.RatString())
		case after == nil:
			loosened("%s %s was removed", b.name, before.RatString())
		case b.lower && after.Cmp(before) > 0 || !b.lower && after.Cmp(before) < 0:
			tightened("%s is %s instead of %s", b.name, after.RatString(), before.RatString())
		case after.Cmp(before) != 0:
			loosened("%s is %s instead of %s", b.name, after.RatString(), before.RatString())
		}
	}
	for _, kw := range []struct{ name, before, after string }{
		{"pattern", o.Pattern, n.Pattern}, {"format", o.Format, n.Format}, {"multipleOf", string(o.MultipleOf), string(n.MultipleOf)},
	} {
		switch {
		case kw.before == kw.after:
		case kw.before == "":
			tightened("%s %q was added", kw.name, kw.after)
		case kw.after == "":
			loosened("%s %q was removed", kw.name, kw.before)
		default:
			tightened("%s is %q instead of %q", kw.name, kw.after, kw.before)
			loosened("%s is %q instead of %q", kw.name, kw.after, kw.before)
		}
	}
	if !o.UniqueItems && n.UniqueItems {
		tightened("items must be unique now")
	}
}

// properties compares the properties of objects: a client sending one
// breaks on a property it must send now, one receiving it on a property
// it may not get any more.
func (d *differ) properties(o, n *oas.Schema, where string, dir direction) {
	// A property the client does not send or does not get does not count:
	// read-only ones in requests, write-only ones in responses.
	skip := func(s *oas.Schema) bool {
		s = deref(s)
		return s != nil && (dir == request && s.ReadOnly || dir == response && s.WriteOnly)
	}
	closed := func(s *oas.Schema) bool {
		a := deref(s.AdditionalProperties)
		return a != nil && a.Bool != nil && !*a.Bool
	}
	for name, before := range o.Properties.All() {
		label := where + "/" + name
		after, ok := n.Properties.Get(name)
		if skip(before) {
			continue
		}
		wasRequired, isRequired := slices.Contains(o.Required, name), slices.Contains(n.Required, name)
		if !ok {
			switch {
			case dir == response:
				d.add(Error, RulePropertyRemoved, before, n, "%s: the property was removed", label)
			case closed(n):
				d.add(Error, RulePropertyRemoved, before, n, "%s: the property was removed, and others are not allowed", label)
			default:
				d.add(Warning, RulePropertyRemoved, before, n, "%s: the property was removed", label)
			}
			continue
		}
		switch {
		case dir == request && !wasRequired && isRequired:
			d.add(Error, RulePropertyRequired, before, after, "%s: the property is required now", label)
		case dir == response && wasRequired && !isRequired:
			d.add(Error, RulePropertyOptional, before, after, "%s: the property may be left out now", label)
		}
		d.schema(before, after, label, dir)
	}
	for name, after := range n.Properties.All() {
		if _, ok := o.Properties.Get(name); ok || skip(after) {
			continue
		}
		label := where + "/" + name
		if dir == request && slices.Contains(n.Required, name) {
			d.add(Error, RulePropertyRequired, o, after, "%s: a required property was added", label)
		} else {
			d.add(Info, RulePropertyAdded, o, after, "%s: the property was added", label)
		}
	}
	if dir == request && !closed(o) && closed(n) {
		d.add(Error, RuleConstraintTightened, o, n, "%s: properties not listed are not allowed now", where)
	}
}

// variants compares the alternatives of oneOf or anyOf: by position when
// there are as many, else by how many there are, fewer breaking a client
// sending one of the old ones, more possibly one receiving a new one.
func (d *differ) variants(keyword string, before, after []*oas.Schema, o, n *oas.Schema, where string, dir direction) {
	if len(before) == len(after) {
		for i := range before {
			d.schema(before[i], after[i], fmt.Sprintf("%s/%s/%d", where, keyword, i), dir)
		}
		return
	}
	switch {
	case dir == request && len(after) < len(before) && len(after) > 0:
		d.add(Error, RuleVariantsChanged, o, n, "%s: %s has %d alternatives instead of %d", where, keyword, len(after), len(before))
	case dir == response && len(after) > len(before) && len(before) > 0:
		d.add(Warning, RuleVariantsChanged, o, n, "%s: %s has %d alternatives instead of %d", where, keyword, len(after), len(before))
	default:
		d.add(Info, RuleVariantsChanged, o, n, "%s: %s has %d alternatives instead of %d", where, keyword, len(after), len(before))
	}
}
