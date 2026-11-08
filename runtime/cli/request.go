package cli

import (
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/dheins/go-openapi/oapi"
)

// multi is a flag that may be given several times.
type multi []string

func (m *multi) String() string { return strings.Join(*m, ",") }

func (m *multi) Set(v string) error {
	*m = append(*m, v)
	return nil
}

// request builds the request of cmd from the flags given.
func request[C any](g *given, cmd *Command[C]) (any, error) {
	req := cmd.Request()
	v := reflect.ValueOf(req).Elem()
	for _, p := range cmd.Params {
		vals := *g.params[p.Flag]
		if len(vals) == 0 {
			if p.Required {
				return nil, fmt.Errorf("-%s is required", p.Flag)
			}
			continue
		}
		field := v.FieldByName(p.Field)
		if !field.IsValid() {
			return nil, fmt.Errorf("the request has no field %s", p.Field)
		}
		if err := setParam(field, vals, p.Kind); err != nil {
			return nil, fmt.Errorf("-%s: %w", p.Flag, err)
		}
	}
	if len(cmd.Media) == 0 {
		return req, nil
	}
	if g.body == "" && len(g.files) == 0 && len(g.parts) == 0 {
		if cmd.BodyRequired {
			return nil, errors.New("-body is required")
		}
		return req, nil
	}
	m := cmd.Media[0]
	if g.contentType != "" {
		// A multipart document's media type names its boundary too.
		base, _, _ := strings.Cut(g.contentType, ";")
		found := false
		for _, candidate := range cmd.Media {
			if strings.EqualFold(candidate.MediaType, strings.TrimSpace(base)) {
				m, found = candidate, true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("-content-type %q is not one of the operation's", g.contentType)
		}
	}
	if cmd.ContentType != "" {
		v.FieldByName(cmd.ContentType).SetString(m.MediaType)
	}
	field := v.FieldByName(m.Field)
	if !field.IsValid() {
		return nil, fmt.Errorf("the request has no field %s", m.Field)
	}
	if m.Stream != nil || (m.Kind == "partstream" && len(g.parts) == 0) {
		return req, setStream(settable(field), m, g)
	}
	if m.Kind == "parts" || m.Kind == "partstream" {
		if err := setPartsBody(settable(field), g); err != nil {
			return nil, err
		}
		return req, nil
	}
	if len(g.parts) > 0 {
		return nil, fmt.Errorf("-part is for bodies of parts, not %s", m.MediaType)
	}
	data, err := readSource(g.body)
	if err != nil {
		return nil, fmt.Errorf("-body: %w", err)
	}
	if err := setBody(field, m, data, g.files); err != nil {
		return nil, fmt.Errorf("-body: %w", err)
	}
	return req, nil
}

// setStream sets target, a streamed body, to the stream of -body, read
// as it is sent.
func setStream(target reflect.Value, m Media, g *given) error {
	if len(g.parts) > 0 {
		return fmt.Errorf("-part is for bodies of parts, not %s", m.MediaType)
	}
	src, err := openSource(g.body)
	if err != nil {
		return fmt.Errorf("-body: %w", err)
	}
	g.closers = append(g.closers, src)
	stream := reflect.Value{}
	if m.Stream != nil {
		stream = reflect.ValueOf(m.Stream(src))
	} else {
		stream = partStream(target.Type(), src, g.contentType)
	}
	if !stream.Type().AssignableTo(target.Type()) {
		return fmt.Errorf("-body: a %s is not a %s", stream.Type(), target.Type())
	}
	target.Set(stream)
	return nil
}

// setPartsBody sets a body of positional parts from the -part flags, or
// from -body, a multipart document.
func setPartsBody(target reflect.Value, g *given) error {
	if len(g.parts) > 0 {
		if g.body != "" {
			return errors.New("give the parts with -part or as -body, not both")
		}
		parts, err := readParts(g.parts)
		if err != nil {
			return err
		}
		return setParts(target, parts)
	}
	data, err := readSource(g.body)
	if err != nil {
		return fmt.Errorf("-body: %w", err)
	}
	parts, err := multipartParts(data, g.contentType)
	if err != nil {
		return fmt.Errorf("-body: %w", err)
	}
	if err := setParts(target, parts); err != nil {
		return fmt.Errorf("-body: %w", err)
	}
	return nil
}

// readSource reads a flag's value: the contents of @file, stdin for -, or
// the value itself.
func readSource(v string) ([]byte, error) {
	switch {
	case v == "-":
		return io.ReadAll(os.Stdin)
	case strings.HasPrefix(v, "@"):
		return os.ReadFile(v[1:])
	}
	return []byte(v), nil
}

// settable returns the value inside v that a given flag sets: v, the
// Value of an oapi.Optional (now set), the target of a pointer (now
// allocated), or the Value of a valid oapi.Nullable.
func settable(v reflect.Value) reflect.Value {
	for {
		switch {
		case v.Kind() == reflect.Pointer:
			if v.IsNil() {
				v.Set(reflect.New(v.Type().Elem()))
			}
			v = v.Elem()
		case v.Kind() == reflect.Struct && isWrapper(v, "Set"):
			v.FieldByName("Set").SetBool(true)
			v = v.FieldByName("Value")
		case v.Kind() == reflect.Struct && isWrapper(v, "Valid"):
			v.FieldByName("Valid").SetBool(true)
			v = v.FieldByName("Value")
		default:
			return v
		}
	}
}

// isWrapper reports whether v is an oapi.Optional (flag "Set") or
// oapi.Nullable (flag "Valid"): a struct of Value and the flag.
func isWrapper(v reflect.Value, flagName string) bool {
	t := v.Type()
	if t.NumField() != 2 || t.PkgPath() != reflect.TypeFor[oapi.Optional[int]]().PkgPath() {
		return false
	}
	f, ok := t.FieldByName(flagName)
	_, hasValue := t.FieldByName("Value")
	return ok && hasValue && f.Type.Kind() == reflect.Bool
}

// setParam sets field from the values of its flag.
func setParam(field reflect.Value, vals []string, kind Kind) error {
	target := settable(field)
	switch kind {
	case JSON:
		return json.Unmarshal([]byte(vals[len(vals)-1]), target.Addr().Interface())
	case List:
		if target.Kind() != reflect.Slice {
			return fmt.Errorf("%s is not a list", target.Type())
		}
		var items []string
		for _, v := range vals {
			items = append(items, strings.Split(v, ",")...)
		}
		out := reflect.MakeSlice(target.Type(), len(items), len(items))
		for i, item := range items {
			if err := setText(settable(out.Index(i)), item); err != nil {
				return err
			}
		}
		target.Set(out)
		return nil
	}
	return setText(target, vals[len(vals)-1])
}

// setText sets v to the value s is the text of.
func setText(v reflect.Value, s string) error {
	if u, ok := v.Addr().Interface().(encoding.TextUnmarshaler); ok {
		return u.UnmarshalText([]byte(s))
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString(s)
	case reflect.Bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return fmt.Errorf("%q is not a boolean", s)
		}
		v.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(s, 10, v.Type().Bits())
		if err != nil {
			return fmt.Errorf("%q is not an integer", s)
		}
		v.SetInt(n)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(s, v.Type().Bits())
		if err != nil {
			return fmt.Errorf("%q is not a number", s)
		}
		v.SetFloat(f)
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			v.SetBytes([]byte(s))
			return nil
		}
		fallthrough
	default:
		// Anything else is given as JSON.
		return json.Unmarshal([]byte(s), v.Addr().Interface())
	}
	return nil
}

// setBody sets the body field from data, as m's kind has it, and the
// files of a multipart body.
func setBody(field reflect.Value, m Media, data []byte, files map[string]string) error {
	if !field.IsValid() {
		return fmt.Errorf("the request has no field %s", m.Field)
	}
	target := settable(field)
	switch m.Kind {
	case "text":
		return setText(target, string(data))
	case "binary":
		switch {
		case target.Type() == reflect.TypeFor[oapi.File]():
			target.Set(reflect.ValueOf(oapi.File{Data: data, ContentType: m.MediaType}))
		case target.Kind() == reflect.Slice && target.Type().Elem().Kind() == reflect.Uint8:
			target.SetBytes(data)
		default:
			return fmt.Errorf("cannot send bytes as %s", target.Type())
		}
		return nil
	}
	if len(data) == 0 {
		data = []byte("{}")
	}
	if m.Kind == "multipart" && len(m.Files) > 0 {
		// A file's property is present, for decoding, as JSON has it.
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(data, &obj); err != nil {
			return err
		}
		for _, f := range m.Files {
			if _, ok := files[f.Flag]; ok {
				obj[f.Property] = json.RawMessage(`""`)
			}
		}
		var err error
		if data, err = json.Marshal(obj); err != nil {
			return err
		}
	}
	if err := oapi.DecodeRequest(data, target.Addr().Interface()); err != nil {
		return err
	}
	for _, f := range m.Files {
		src, ok := files[f.Flag]
		if !ok {
			continue
		}
		if !strings.HasPrefix(src, "@") {
			return fmt.Errorf("-%s: give a file as @path", f.Flag)
		}
		content, err := os.ReadFile(src[1:])
		if err != nil {
			return fmt.Errorf("-%s: %w", f.Flag, err)
		}
		file := oapi.File{Name: filepath.Base(src[1:]), ContentType: mime.TypeByExtension(filepath.Ext(src)), Data: content}
		fv := target.FieldByName(f.Field)
		if !fv.IsValid() {
			return fmt.Errorf("the body has no field %s", f.Field)
		}
		dst := settable(fv)
		switch {
		case dst.Type() == reflect.TypeFor[oapi.File]():
			dst.Set(reflect.ValueOf(file))
		case dst.Type() == reflect.TypeFor[[]oapi.File]():
			dst.Set(reflect.Append(dst, reflect.ValueOf(file)))
		default:
			return fmt.Errorf("-%s: the property is a %s, not a file", f.Flag, dst.Type())
		}
	}
	return nil
}
