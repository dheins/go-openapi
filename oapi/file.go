package oapi

import "encoding/json"

// File is binary content: a "format: binary" value, such as a file part
// of a multipart request body.
//
// In JSON, which cannot hold bytes, a File is written as a string of its
// data, and Name and ContentType are not kept.
type File struct {
	Name        string
	ContentType string
	Data        []byte
}

func (f File) MarshalJSON() ([]byte, error) {
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
