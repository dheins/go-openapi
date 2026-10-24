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
