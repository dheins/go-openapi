// Package bad is a fixture of codescan: directives and tags it reports.
package bad

//goapi:schema
type Tags struct {
	A string `json:"a" goapi:"minLength=-1"`
	B int    `json:"b" goapi:"maximum=high"`
	C string `json:"c" goapi:"readOnly=yes"`
	D string `json:"d" goapi:"color=red"`
	E int    `json:"e" goapi:"default=abc"`
	F string `json:"f" goapi:"pattern"`
}

//goapi:schema One Two
type Args struct{}

//goapi:enum
//goapi:frobnicate
type Empty string

//goapi:schema
type Uses struct {
	E Empty `json:"e"`
}

//goapi:schema "unterminated
type Quote struct{}

// Shape has variants without the discriminator, and two with one value.
//
//goapi:oneOf discriminator=kind
type Shape interface{ area() float64 }

type Square struct {
	Side float64 `json:"side"`
}

func (Square) area() float64 { return 0 }

type Circle struct {
	Kind string `json:"kind"`
}

func (Circle) area() float64 { return 0 }

//goapi:discriminatorValue Circle
type Oval struct {
	Kind string `json:"kind"`
}

func (Oval) area() float64 { return 0 }

//goapi:oneOf
type NotAnInterface struct{}

//goapi:discriminatorValue x
type Lonely struct{}

// Nobody has no variants.
//
//goapi:anyOf
type Nobody interface{ nobody() }

//goapi:oneOf color=red
type Odd interface{ odd() }

//goapi:schema
type Unions struct {
	S Shape  `json:"s"`
	N Nobody `json:"n"`
}
