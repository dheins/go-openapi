package oapi

import (
	"encoding/json"
	"net/textproto"
)

// Part is an item of a multipart body of positional parts (multipart/mixed
// and the like) with the headers of the part it is sent in or was read
// from: the counterpart of File.Header for parts holding JSON or text.
// Generated code uses it where prefixEncoding or itemEncoding declares
// part headers. In JSON, a Part is its value, so that schema keywords
// such as uniqueItems compare values and not headers.
type Part[T any] struct {
	Header textproto.MIMEHeader
	Value  T
}

// MIMEPart is implemented by *Part: it returns the part's headers and a
// pointer to its value, for the runtime reading and writing parts.
type MIMEPart interface {
	MIMEPart() (*textproto.MIMEHeader, any)
}

func (p *Part[T]) MIMEPart() (*textproto.MIMEHeader, any) {
	return &p.Header, &p.Value
}

func (p Part[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.Value)
}
