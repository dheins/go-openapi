package oapi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/mail"
	"net/netip"
	"net/url"
	"reflect"
	"regexp"
	"sync"
	"time"
	"unicode/utf8"
)

// IsZero reports whether v is its type's zero value. Validate methods
// skip the zero values of properties written even when unset
// (x-omitempty: false), which stand for absent ones.
func IsZero[T any](v T) bool {
	return reflect.ValueOf(&v).Elem().IsZero()
}

// HasKey reports whether m has the key k.
func HasKey[K ~string, V any](m map[K]V, k K) bool {
	_, ok := m[k]
	return ok
}

// StringLength returns the length of s as JSON Schema counts it: in
// Unicode code points, not bytes.
func StringLength(s string) int {
	return utf8.RuneCountInString(s)
}

// MultipleOf reports whether v is a multiple of m, allowing for floating
// point rounding.
func MultipleOf(v, m float64) bool {
	if m == 0 {
		return false
	}
	q := v / m
	return math.Abs(q-math.Round(q)) <= 1e-9*math.Max(1, math.Abs(q))
}

// Duplicate returns the indexes of the first two equal items, comparing
// them as JSON values, and whether there were any.
func Duplicate[T any](items []T) (int, int, bool) {
	seen := make(map[string]int, len(items))
	for i, item := range items {
		key, err := canonical(item)
		if err != nil {
			continue
		}
		if j, ok := seen[key]; ok {
			return j, i, true
		}
		seen[key] = i
	}
	return 0, 0, false
}

// EqualsJSON reports whether v, marshalled to JSON, equals one of the JSON
// literals. Numbers compare by value, so 1 equals 1.0.
func EqualsJSON(v any, literals ...string) bool {
	got, err := canonical(v)
	if err != nil {
		return false
	}
	for _, lit := range literals {
		var parsed any
		if json.Unmarshal([]byte(lit), &parsed) != nil {
			continue
		}
		want, err := canonical(parsed)
		if err == nil && want == got {
			return true
		}
	}
	return false
}

// canonical renders v as JSON with numbers normalised and object keys
// sorted (encoding/json sorts map keys), so equal JSON values give equal
// strings.
func canonical(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	var generic any
	if err := json.Unmarshal(data, &generic); err != nil {
		return "", err
	}
	out, err := json.Marshal(generic)
	return string(out), err
}

var (
	uuidPattern     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	hostnamePattern = regexp.MustCompile(`^(?i:[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)(\.(?i:[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?))*\.?$`)
)

// formats are the checks RegisterFormat registers, by format.
var formats sync.Map

// RegisterFormat makes CheckFormat check strings of a format with check,
// which returns why a string is not of the format. It replaces the
// built-in check of a format, and an earlier registration. It is safe to
// call concurrently, but usually called from an init function, before
// any value is checked.
func RegisterFormat(format string, check func(string) error) {
	if check == nil {
		formats.Delete(format)
		return
	}
	formats.Store(format, check)
}

// CheckFormat checks s against a JSON Schema or OpenAPI format, with the
// check RegisterFormat registered for it, if any. Unknown formats are
// accepted, as the specification requires.
func CheckFormat(format, s string) error {
	if check, ok := formats.Load(format); ok {
		if err := check.(func(string) error)(s); err != nil {
			return fmt.Errorf("must be a valid %s: %w", format, err)
		}
		return nil
	}
	ok := true
	switch format {
	case "date-time":
		_, err := time.Parse(time.RFC3339Nano, s)
		ok = err == nil
	case "date":
		_, err := ParseDate(s)
		ok = err == nil
	case "time":
		_, err := time.Parse("15:04:05Z07:00", s)
		if err != nil {
			_, err = time.Parse("15:04:05.999999999Z07:00", s)
		}
		ok = err == nil
	case "email":
		addr, err := mail.ParseAddress(s)
		ok = err == nil && addr.Address == s
	case "hostname":
		ok = len(s) <= 253 && hostnamePattern.MatchString(s)
	case "ipv4":
		addr, err := netip.ParseAddr(s)
		ok = err == nil && addr.Is4()
	case "ipv6":
		addr, err := netip.ParseAddr(s)
		ok = err == nil && addr.Is6()
	case "uri":
		u, err := url.Parse(s)
		ok = err == nil && u.IsAbs()
	case "uri-reference":
		_, err := url.Parse(s)
		ok = err == nil
	case "uuid":
		ok = uuidPattern.MatchString(s)
	case "byte":
		_, err := base64.StdEncoding.DecodeString(s)
		ok = err == nil
	case "regex":
		_, err := regexp.Compile(s)
		ok = err == nil
	}
	if !ok {
		return fmt.Errorf("must be a valid %s", format)
	}
	return nil
}
