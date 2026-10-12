// Package oapi contains the types and helpers that generated models use:
// optional and nullable values, dates, validation errors and decoding
// helpers. Generated code depends on this package and the standard library
// only.
package oapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// Optional holds a value that may be absent, as an optional property is.
// The zero value is absent. Fields of this type are tagged omitzero, so an
// absent value is left out of the JSON output.
type Optional[T any] struct {
	Value T
	Set   bool
}

// Some returns a present Optional holding v.
func Some[T any](v T) Optional[T] {
	return Optional[T]{Value: v, Set: true}
}

// Get returns the value and whether it is present.
func (o Optional[T]) Get() (T, bool) {
	return o.Value, o.Set
}

// Or returns the value if present, otherwise def.
func (o Optional[T]) Or(def T) T {
	if o.Set {
		return o.Value
	}
	return def
}

// IsZero reports whether the value is absent; it is what omitzero checks.
func (o Optional[T]) IsZero() bool {
	return !o.Set
}

func (o Optional[T]) MarshalJSON() ([]byte, error) {
	if !o.Set {
		return []byte("null"), nil
	}
	return json.Marshal(o.Value)
}

// UnmarshalJSON is only called when the property is present, so it marks
// the value as set. Whether null is acceptable is decided by the
// enclosing object's decoder.
func (o *Optional[T]) UnmarshalJSON(data []byte) error {
	o.Set = true
	return json.Unmarshal(data, &o.Value)
}

// Nullable holds a value that may be null. As with sql.NullString, the
// zero value is null; Valid reports whether Value holds a value.
type Nullable[T any] struct {
	Value T
	Valid bool
}

// NewNullable returns a non-null Nullable holding v.
func NewNullable[T any](v T) Nullable[T] {
	return Nullable[T]{Value: v, Valid: true}
}

// Get returns the value and whether it is not null.
func (n Nullable[T]) Get() (T, bool) {
	return n.Value, n.Valid
}

func (n Nullable[T]) MarshalJSON() ([]byte, error) {
	if !n.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(n.Value)
}

func (n *Nullable[T]) UnmarshalJSON(data []byte) error {
	if isNull(data) {
		*n = Nullable[T]{}
		return nil
	}
	n.Valid = true
	return json.Unmarshal(data, &n.Value)
}

// Date is a calendar date without a time zone, the "date" format
// (RFC 3339 full-date, such as "2024-02-29").
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

const dateLayout = "2006-01-02"

// ParseDate parses a full-date such as "2024-02-29".
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return Date{}, fmt.Errorf("invalid date %q: expected YYYY-MM-DD", s)
	}
	return DateOf(t), nil
}

// DateOf returns the date of t in t's location.
func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return Date{Year: y, Month: m, Day: d}
}

// In returns midnight at the start of the date in loc.
func (d Date) In(loc *time.Location) time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, loc)
}

func (d Date) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day)
}

func (d Date) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

func (d *Date) UnmarshalText(data []byte) error {
	parsed, err := ParseDate(string(data))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

func isNull(data []byte) bool {
	return string(bytes.TrimSpace(data)) == "null"
}
