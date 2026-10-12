package ir

import (
	"strconv"
	"strings"
	"unicode"
)

// initialisms are written in upper case in Go names, following the Go
// code review conventions ("petId" becomes "PetID").
var initialisms = map[string]bool{
	"ACL": true, "API": true, "ASCII": true, "CPU": true, "CSS": true, "CSV": true, "DNS": true,
	"GIF": true, "JPEG": true, "PDF": true, "PNG": true, "SVG": true, "YAML": true,
	"EOF": true, "GUID": true, "HTML": true, "HTTP": true, "HTTPS": true, "ID": true,
	"IP": true, "JSON": true, "JWT": true, "LHS": true, "OS": true, "QPS": true,
	"RAM": true, "RHS": true, "RPC": true, "SLA": true, "SMTP": true, "SQL": true,
	"SSH": true, "TCP": true, "TLS": true, "TTL": true, "UDP": true, "UI": true,
	"UID": true, "URI": true, "URL": true, "UTF8": true, "UUID": true, "VM": true,
	"XML": true, "XMPP": true, "XSRF": true, "XSS": true,
}

// GoName converts a schema, property or enum value name into an exported
// Go identifier: "pet_store-item" becomes "PetStoreItem", "petId" becomes
// "PetID". A name starting with a digit gets an "N" prefix; a name with
// no letters or digits becomes "Empty".
func GoName(name string) string {
	var b strings.Builder
	for _, word := range splitWords(name) {
		upper := strings.ToUpper(word)
		if initialisms[upper] {
			b.WriteString(upper)
			continue
		}
		if base, ok := strings.CutSuffix(upper, "S"); ok && initialisms[base] {
			// Plural initialisms: "ids" becomes "IDs".
			b.WriteString(base + "s")
			continue
		}
		r := []rune(word)
		b.WriteString(string(unicode.ToUpper(r[0])) + string(r[1:]))
	}
	out := b.String()
	switch {
	case out == "":
		return "Empty"
	case unicode.IsDigit([]rune(out)[0]):
		return "N" + out
	}
	return out
}

// splitWords splits at non-alphanumeric characters and at lower-to-upper
// case changes, keeping runs of capitals together ("HTTPServer" gives
// "HTTP", "Server").
func splitWords(s string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, string(cur))
			cur = nil
		}
	}
	rs := []rune(s)
	for i, r := range rs {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if len(cur) > 0 && unicode.IsUpper(r) {
			prev := cur[len(cur)-1]
			nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	return words
}

// namespace hands out unique identifiers.
type namespace map[string]bool

// claim returns name, or name with the smallest numeric suffix that is
// still free, and marks it as used.
func (ns namespace) claim(name string) string {
	unique := name
	for i := 2; ns[unique]; i++ {
		unique = name + strconv.Itoa(i)
	}
	ns[unique] = true
	return unique
}
