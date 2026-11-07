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
	d.defaults(o, n, where)
	d.examples(schemaExamples(o), schemaExamples(n), o, n, where)
	d.constraints(o, n, where, dir)
	d.properties(o, n, where, dir)
	d.schema(o.Items, n.Items, where+"/items", dir)
	d.schema(o.AdditionalProperties, n.AdditionalProperties, where+"/additionalProperties", dir)
	d.discriminator(o, n, where, dir)
	d.variants("oneOf", o.OneOf, n.OneOf, o, n, where, dir)
	d.variants("anyOf", o.AnyOf, n.AnyOf, o, n, where, dir)
	d.keywords(o, n, where, dir)
}

// accepts reports whether a value of JSON type t is one of types, an
// integer being a number too; no types accept any.
func accepts(types oas.Types, t string) bool {
	return len(types) == 0 || types.Has(t) || t == "integer" && types.Has("number")
}

func (d *differ) types(o, n *oas.Schema, where string, dir direction, text bool) {
	// As text, any value a client sends is a string, and any it receives
	// as a string reads as one.
	if text && (d.sends(dir) && accepts(n.Type, "string") || !d.sends(dir) && accepts(o.Type, "string")) {
		return
	}
	if d.sends(dir) {
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

// defaults compares the defaults of values: a server assumes it for a
// property a client leaves out of a request, and a client fills it in
// when a response leaves it out, so a change may break either.
func (d *differ) defaults(o, n *oas.Schema, where string) {
	before, after := compact(o.Default), compact(n.Default)
	switch {
	case before == after:
	case len(o.Default) == 0:
		d.add(Warning, RuleDefaultChanged, o, n, "%s: default %s was added", where, after)
	case len(n.Default) == 0:
		d.add(Warning, RuleDefaultChanged, o, n, "%s: default %s was removed", where, before)
	default:
		d.add(Warning, RuleDefaultChanged, o, n, "%s: the default is %s instead of %s", where, after, before)
	}
}

// schemaExamples returns the examples of a schema, by name: "example",
// and "examples/0" and so on.
func schemaExamples(s *oas.Schema) map[string]string {
	out := map[string]string{}
	if len(s.Example) > 0 {
		out["example"] = compact(s.Example)
	}
	for i, v := range s.Examples {
		out[fmt.Sprintf("examples/%d", i)] = compact(v)
	}
	return out
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
		if d.sends(dir) {
			d.add(Error, RuleEnumNarrowed, o, n, "%s: only %s are accepted now", where, strings.Join(after, ", "))
		}
	case len(after) == 0:
		if !d.sends(dir) {
			d.add(Warning, RuleEnumWidened, o, n, "%s: may be any value now, not only %s", where, strings.Join(before, ", "))
		}
	default:
		if removed := missing(before, after); len(removed) > 0 {
			sev := Info
			if d.sends(dir) {
				sev = Error
			}
			d.add(sev, RuleEnumNarrowed, o, n, "%s: %s removed from the enum", where, strings.Join(removed, ", "))
		}
		if added := missing(after, before); len(added) > 0 {
			sev := Info
			if !d.sends(dir) {
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
	{"minContains", true, func(s *oas.Schema) *big.Rat { return count(s.MinContains) }},
	{"maxContains", false, func(s *oas.Schema) *big.Rat { return count(s.MaxContains) }},
}

// constraints compares the limits, patterns and formats of values: one
// that accepts less breaks a client sending the value, one that allows
// more may surprise a client receiving it.
func (d *differ) constraints(o, n *oas.Schema, where string, dir direction) {
	tightened := func(what string, args ...any) {
		if d.sends(dir) {
			d.add(Error, RuleConstraintTightened, o, n, "%s: "+what, append([]any{where}, args...)...)
		}
	}
	loosened := func(what string, args ...any) {
		if !d.sends(dir) {
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
			case !d.sends(dir):
				d.add(Error, RulePropertyRemoved, before, n, "%s: the property was removed", label)
			case closed(n):
				d.add(Error, RulePropertyRemoved, before, n, "%s: the property was removed, and others are not allowed", label)
			default:
				d.add(Warning, RulePropertyRemoved, before, n, "%s: the property was removed", label)
			}
			continue
		}
		switch {
		case d.sends(dir) && !wasRequired && isRequired:
			d.add(Error, RulePropertyRequired, before, after, "%s: the property is required now", label)
		case !d.sends(dir) && wasRequired && !isRequired:
			d.add(Error, RulePropertyOptional, before, after, "%s: the property may be left out now", label)
		}
		d.schema(before, after, label, dir)
	}
	for name, after := range n.Properties.All() {
		if _, ok := o.Properties.Get(name); ok || skip(after) {
			continue
		}
		label := where + "/" + name
		if d.sends(dir) && slices.Contains(n.Required, name) {
			d.add(Error, RulePropertyRequired, o, after, "%s: a required property was added", label)
		} else {
			d.add(Info, RulePropertyAdded, o, after, "%s: the property was added", label)
		}
	}
	if d.sends(dir) && !closed(o) && closed(n) {
		d.add(Error, RuleConstraintTightened, o, n, "%s: properties not listed are not allowed now", where)
	}
}

// variants compares the alternatives of oneOf or anyOf, paired by what
// they are rather than where: by the discriminator value leading to them,
// else the component they refer to, else the most alike in types and
// properties. An alternative without a pair was removed, which breaks a
// client sending it, or added, which may surprise one receiving it.
func (d *differ) variants(keyword string, before, after []*oas.Schema, o, n *oas.Schema, where string, dir direction) {
	pairs, removed, added := pairVariants(before, after, o, n)
	for _, p := range pairs {
		d.schema(before[p[0]], after[p[1]], fmt.Sprintf("%s/%s/%d", where, keyword, p[1]), dir)
	}
	for _, i := range removed {
		sev := Info
		if d.sends(dir) {
			sev = Error
		}
		d.add(sev, RuleVariantsChanged, before[i], n, "%s: %s alternative %s was removed", where, keyword, variantName(before[i], i))
	}
	for _, i := range added {
		sev := Info
		if !d.sends(dir) {
			sev = Warning
		}
		d.add(sev, RuleVariantsChanged, o, after[i], "%s: %s alternative %s was added", where, keyword, variantName(after[i], i))
	}
}

// variantName names an alternative by the component it refers to, else
// its position.
func variantName(s *oas.Schema, i int) string {
	if name := refName(s); name != "" {
		return name
	}
	return fmt.Sprintf("#%d", i)
}

// refName returns the last token of the reference of s, which names the
// component it refers to, or "".
func refName(s *oas.Schema) string {
	if s == nil || s.Ref == "" {
		return ""
	}
	_, frag, _ := strings.Cut(s.Ref, "#")
	if frag == "" {
		return s.Ref
	}
	return frag[strings.LastIndex(frag, "/")+1:]
}

// pairVariants pairs the alternatives of before and after, as indices,
// and returns those of each without a pair.
func pairVariants(before, after []*oas.Schema, o, n *oas.Schema) (pairs [][2]int, removed, added []int) {
	usedOld, usedNew := map[int]bool{}, map[int]bool{}
	pair := func(i, j int) {
		pairs = append(pairs, [2]int{i, j})
		usedOld[i], usedNew[j] = true, true
	}
	// By discriminator value.
	oldKeys, newKeys := mappingIndex(o, before), mappingIndex(n, after)
	for key, i := range oldKeys {
		if j, ok := newKeys[key]; ok && !usedOld[i] && !usedNew[j] {
			pair(i, j)
		}
	}
	// By the component referred to.
	for i, b := range before {
		if usedOld[i] || refName(b) == "" {
			continue
		}
		for j, a := range after {
			if !usedNew[j] && refName(a) == refName(b) {
				pair(i, j)
				break
			}
		}
	}
	// By likeness, the most alike first; position breaks ties. Two
	// components of different names are different alternatives.
	type candidate struct{ i, j, score int }
	var candidates []candidate
	for i, b := range before {
		for j, a := range after {
			if usedOld[i] || usedNew[j] || refName(b) != "" && refName(a) != "" {
				continue
			}
			if score := likeness(b, a); score > 0 {
				candidates = append(candidates, candidate{i, j, score})
			}
		}
	}
	slices.SortStableFunc(candidates, func(a, b candidate) int {
		if a.score != b.score {
			return b.score - a.score
		}
		return abs(a.i-a.j) - abs(b.i-b.j)
	})
	for _, c := range candidates {
		if !usedOld[c.i] && !usedNew[c.j] {
			pair(c.i, c.j)
		}
	}
	slices.SortFunc(pairs, func(a, b [2]int) int { return a[1] - b[1] })
	for i := range before {
		if !usedOld[i] {
			removed = append(removed, i)
		}
	}
	for j := range after {
		if !usedNew[j] {
			added = append(added, j)
		}
	}
	return pairs, removed, added
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// mappingIndex returns, by discriminator value, the index of the
// alternative of variants s's discriminator mapping leads to.
func mappingIndex(s *oas.Schema, variants []*oas.Schema) map[string]int {
	out := map[string]int{}
	if s.Discriminator == nil || s.Discriminator.Targets == nil {
		return out
	}
	for key, target := range s.Discriminator.Targets.All() {
		for i, v := range variants {
			if deref(v) == deref(target) {
				out[key] = i
				break
			}
		}
	}
	return out
}

// likeness scores how alike two alternatives are: their types, required
// properties and property names in common, plus one when both are of no
// type and have no properties, so that equal schemas pair. Zero means
// nothing in common.
func likeness(b, a *oas.Schema) int {
	b, a = deref(b), deref(a)
	if b == nil || a == nil {
		return 0
	}
	score := 0
	for _, t := range b.Type {
		if a.Type.Has(t) {
			score += 2
		}
	}
	for _, r := range b.Required {
		if slices.Contains(a.Required, r) {
			score += 2
		}
	}
	for name := range b.Properties.All() {
		if _, ok := a.Properties.Get(name); ok {
			score++
		}
	}
	if score == 0 && len(b.Type) == 0 && len(a.Type) == 0 && b.Properties.Len() == 0 && a.Properties.Len() == 0 {
		score = 1
	}
	return score
}

// discriminator compares the discriminators of unions: the property
// telling the alternatives apart, and the values of its mapping, judged
// as enum values are.
func (d *differ) discriminator(o, n *oas.Schema, where string, dir direction) {
	before, after := o.Discriminator, n.Discriminator
	if before == nil || after == nil {
		return
	}
	if before.PropertyName != after.PropertyName {
		d.add(Error, RuleDiscriminatorChanged, o, n, "%s: the discriminator is %q instead of %q", where, after.PropertyName, before.PropertyName)
		return
	}
	keys := func(m *oas.OrderedMap[string]) []string { return m.Keys() }
	oldKeys, newKeys := keys(before.Mapping), keys(after.Mapping)
	if len(oldKeys) == 0 || len(newKeys) == 0 {
		return
	}
	var removed, added []string
	for _, k := range oldKeys {
		if !slices.Contains(newKeys, k) {
			removed = append(removed, k)
		}
	}
	for _, k := range newKeys {
		if !slices.Contains(oldKeys, k) {
			added = append(added, k)
		}
	}
	if len(removed) > 0 {
		sev := Info
		if d.sends(dir) {
			sev = Error
		}
		d.add(sev, RuleDiscriminatorChanged, o, n, "%s: discriminator value %s removed from the mapping", where, quoteList(removed))
	}
	if len(added) > 0 {
		sev := Info
		if !d.sends(dir) {
			sev = Warning
		}
		d.add(sev, RuleDiscriminatorChanged, o, n, "%s: discriminator value %s added to the mapping", where, quoteList(added))
	}
}
