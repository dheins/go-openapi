package oapi

import (
	"encoding/json"
	"errors"
	"unicode/utf8"
)

// File is binary content: a "format: binary" value, such as a file part
// of a multipart request body.
//
// In JSON, which cannot hold bytes, a File is written as a string of its
// data, and Name and ContentType are not kept. Data that is not valid
// UTF-8 cannot be such a string without being changed, so encoding it is
// an error; for binary data in JSON, use contentEncoding: base64, which is
// generated as []byte.
type File struct {
	Name        string
	ContentType string
	Data        []byte
}

// errBinaryJSON is returned for data a JSON string would not keep.
var errBinaryJSON = errors.New("binary data is not valid UTF-8 and cannot be a JSON string; use contentEncoding: base64 ([]byte)")

func (f File) MarshalJSON() ([]byte, error) {
	if !utf8.Valid(f.Data) {
		return nil, errBinaryJSON
	}
	return json.Marshal(string(f.Data))
}

func (f *File) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*f = File{Data: []byte(s)}
	return nil
}
