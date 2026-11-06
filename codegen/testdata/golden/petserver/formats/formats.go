// Package formats holds the Go types the petserver fixture maps string
// formats to, with x-go-formats and the Formats option.
package formats

import (
	"errors"
	"fmt"
	"regexp"
)

var petCode = regexp.MustCompile(`^[A-Z]{3}-[0-9]+$`)

// PetCode is a pet-code: three capital letters, a dash and digits.
type PetCode string

func (c PetCode) MarshalText() ([]byte, error) {
	if !petCode.MatchString(string(c)) {
		return nil, fmt.Errorf("%q is not a pet code", string(c))
	}
	return []byte(c), nil
}

func (c *PetCode) UnmarshalText(data []byte) error {
	if !petCode.Match(data) {
		return errors.New("must be three capital letters, a dash and digits")
	}
	*c = PetCode(data)
	return nil
}

// Color is a color: #rrggbb.
type Color struct {
	R, G, B uint8
}

func (c Color) MarshalText() ([]byte, error) {
	return fmt.Appendf(nil, "#%02x%02x%02x", c.R, c.G, c.B), nil
}

func (c *Color) UnmarshalText(data []byte) error {
	if len(data) != 7 {
		return errors.New("must be #rrggbb")
	}
	if _, err := fmt.Sscanf(string(data), "#%02x%02x%02x", &c.R, &c.G, &c.B); err != nil {
		return errors.New("must be #rrggbb")
	}
	return nil
}
