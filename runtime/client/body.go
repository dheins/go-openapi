package client

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"net/url"
	"reflect"
	"strings"

	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/server"
)

// JSONBody sets the body to v encoded as JSON, leaving out read-only
// properties.
func (r *Request) JSONBody(contentType string, v any) {
	data, err := oapi.EncodeRequest(v)
	if err != nil {
		r.fail(fmt.Errorf("body: %w", err))
		return
	}
	r.body, r.contentType = data, contentType
}

// FormBody sets the body to v, an object, as form fields: nested objects
// with brackets (a[b]=v), arrays as repeated fields.
func (r *Request) FormBody(contentType string, v any) {
	fields, err := objectFields(v, true)
	if err != nil {
		r.fail(fmt.Errorf("body: %w", err))
		return
	}
	form := url.Values{}
	for _, f := range fields {
		for _, val := range f.values {
			form.Add(f.name(), val)
		}
	}
	r.body, r.contentType = []byte(form.Encode()), contentType
}

// TextBody sets the body to s.
func (r *Request) TextBody(contentType, s string) {
	if !strings.Contains(strings.ToLower(contentType), "charset=") {
		contentType += "; charset=utf-8"
	}
	r.body, r.contentType = []byte(s), contentType
}

// BytesBody sets the body to data.
func (r *Request) BytesBody(contentType string, data []byte) {
	r.body, r.contentType = data, contentType
}

// XMLBody sets the body to v encoded as JSON, leaving out read-only
// properties, and written as the XML document root describes.
func (r *Request) XMLBody(contentType string, v any, root *server.XMLNode) {
	data, err := oapi.EncodeRequest(v)
	if err == nil {
		data, err = server.JSONToXML(data, root)
	}
	if err != nil {
		r.fail(fmt.Errorf("body: %w", err))
		return
	}
	r.body, r.contentType = data, contentType
}

// PartsBody sets the body to items as a multipart body of positional
// parts (multipart/mixed and the like), as p describes.
func PartsBody[T any](r *Request, contentType string, items []T, p server.Parts) {
	data, ct, err := server.EncodeParts(items, contentType, p, oapi.Request)
	if err != nil {
		r.fail(fmt.Errorf("body: %w", err))
		return
	}
	r.body, r.contentType = data, ct
}

// MultipartBody sets the body to v, a struct, as multipart/form-data: a
// part per property, files (properties of type file in obj) with their
// name, content type and headers, others as text, or as JSON for objects,
// with the first content type the property's encoding names. Absent
// optional properties are left out.
func (r *Request) MultipartBody(v any, obj server.Property) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		r.fail(fmt.Errorf("body: a multipart body must be a struct, not %T", v))
		return
	}
	for i := range rv.NumField() {
		name, _, _ := strings.Cut(rv.Type().Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		prop := obj.Properties[name]
		fv, present := presentValue(rv.Field(i))
		if !present {
			continue
		}
		var err error
		if prop.Type == "file" {
			err = writeFiles(mw, name, fv, prop)
		} else {
			err = writeValue(mw, name, fv, prop)
		}
		if err != nil {
			r.fail(fmt.Errorf("body: %s: %w", name, err))
			return
		}
	}
	if err := mw.Close(); err != nil {
		r.fail(err)
		return
	}
	r.body, r.contentType = buf.Bytes(), mw.FormDataContentType()
}

// presentValue returns the value a field holds: through Optional (if
// Set) and pointers (if not nil); false when there is none.
func presentValue(v reflect.Value) (reflect.Value, bool) {
	for {
		switch {
		case v.Kind() == reflect.Pointer:
			if v.IsNil() {
				return v, false
			}
			v = v.Elem()
		case v.Kind() == reflect.Struct && v.FieldByName("Set").IsValid() && v.FieldByName("Value").IsValid() && v.Type().PkgPath() == optionalPkg:
			if !v.FieldByName("Set").Bool() {
				return v, false
			}
			v = v.FieldByName("Value")
		default:
			return v, true
		}
	}
}

var optionalPkg = reflect.TypeFor[oapi.Optional[int]]().PkgPath()

var fileType = reflect.TypeFor[oapi.File]()

// writeFiles writes a file part per oapi.File in v (a File or []File).
func writeFiles(mw *multipart.Writer, name string, v reflect.Value, prop server.Property) error {
	var files []oapi.File
	switch {
	case v.Type() == fileType:
		files = []oapi.File{v.Interface().(oapi.File)}
	case v.Kind() == reflect.Slice && v.Type().Elem() == fileType:
		files = v.Interface().([]oapi.File)
	default:
		return fmt.Errorf("expected oapi.File, got %s", v.Type())
	}
	for _, f := range files {
		h := textproto.MIMEHeader{}
		for k, vs := range f.Header {
			h[k] = vs
		}
		disposition := fmt.Sprintf("form-data; name=%q", name)
		if f.Name != "" {
			disposition += fmt.Sprintf("; filename=%q", f.Name)
		}
		h.Set("Content-Disposition", disposition)
		ct := f.ContentType
		if ct == "" {
			ct = declaredType(prop, "application/octet-stream")
		}
		h.Set("Content-Type", ct)
		pw, err := mw.CreatePart(h)
		if err != nil {
			return err
		}
		// Writing to a buffer cannot fail.
		_, _ = pw.Write(f.Data)
	}
	return nil
}

// writeValue writes the part (or parts, for an array) of a value that is
// not a file: its JSON text, unquoted for a string, JSON for an object.
func writeValue(mw *multipart.Writer, name string, v reflect.Value, prop server.Property) error {
	data, err := oapi.EncodeRequest(v.Interface())
	if err != nil {
		return err
	}
	values := []string{textOf(data)}
	if len(data) > 0 && data[0] == '[' {
		if values, err = arrayValues(data); err != nil {
			return err
		}
	}
	ct := "text/plain; charset=utf-8"
	if len(data) > 0 && data[0] == '{' {
		ct = "application/json"
	}
	ct = declaredType(prop, ct)
	for _, val := range values {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf("form-data; name=%q", name))
		h.Set("Content-Type", ct)
		pw, err := mw.CreatePart(h)
		if err != nil {
			return err
		}
		// Writing to a buffer cannot fail.
		_, _ = pw.Write([]byte(val))
	}
	return nil
}

// declaredType returns the first content type a part's encoding names, if
// it is not a range, or def.
func declaredType(prop server.Property, def string) string {
	if len(prop.ContentTypes) > 0 && !strings.Contains(prop.ContentTypes[0], "*") {
		return prop.ContentTypes[0]
	}
	return def
}
