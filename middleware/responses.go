package middleware

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dheins/go-openapi/internal/schemas"
	"github.com/dheins/go-openapi/ir"
	"github.com/dheins/go-openapi/oas"
	"github.com/dheins/go-openapi/runtime/problem"
	"github.com/dheins/go-openapi/runtime/server"
)

// ResponseMode says what WithResponseValidation does with a response
// that does not match the document.
type ResponseMode int

const (
	// Unchecked sends responses as the handler writes them, the default.
	Unchecked ResponseMode = iota
	// LogResponses logs what is wrong with a response, at warning level,
	// and sends it.
	LogResponses
	// RejectResponses logs what is wrong with a response and sends a 500
	// problem instead.
	RejectResponses
)

// WithResponseValidation checks the responses of handlers against the
// document: the status, the media type, the body against its schema
// (write-only properties need not be there) for JSON, XML, form, text,
// JSON Lines and server-sent event bodies, and the headers, which must be
// there if required and match their schemas. Responses are buffered to be
// checked, until the handler flushes one, which sends it from then on:
// the items of a JSON Lines or event stream are still checked as they are
// written, each sent once it is complete. A stream has sent its status
// already, so with RejectResponses an invalid item is not sent, and it
// and every later write fail instead.
func WithResponseValidation(mode ResponseMode) Option {
	return func(s *settings) { s.responses = mode }
}

// response is a response of an operation, as the middleware checks it.
type response struct {
	media []responseMedia
	// headers are the names of the required headers, and values check the
	// values of the declared ones.
	headers []string
	values  []*param
}

// responseMedia is a media type of a response and how to check its body:
// kind is json, xml, form, text, or a stream (jsonl or sse), with what
// that kind needs; a nil schema and stream check nothing.
type responseMedia struct {
	mediaType string
	kind      string
	schema    *jsonschema.Schema
	xml       *server.XMLNode
	prop      server.Property
	stream    *stream
}

// responses returns the responses of op, o in the IR, by status ("200",
// "4XX", "default").
func (b *builder) responses(o *ir.Operation, op *oas.Operation) map[string]*response {
	out := map[string]*response{}
	if op.Responses == nil {
		return out
	}
	for status, r := range op.Responses.Items.All() {
		r = r.Deref()
		if r == nil {
			continue
		}
		var variants []*ir.Response
		for _, v := range o.Responses {
			if strings.EqualFold(v.Status, status) {
				variants = append(variants, v)
			}
		}
		resp := &response{}
		for mt, m := range r.Content.All() {
			resp.media = append(resp.media, b.responseMedia(mt, m.Deref(), variants))
		}
		for name, h := range r.Headers.All() {
			if h = h.Deref(); h != nil && h.Required && !strings.EqualFold(name, "Content-Type") {
				resp.headers = append(resp.headers, name)
			}
		}
		if len(variants) > 0 {
			// Every variant has the response's headers, as the IR types
			// them; it leaves out those it cannot read.
			for _, h := range variants[0].Headers {
				def, _ := r.Headers.Get(h.Name)
				resp.values = append(resp.values, b.header(h, def.Deref()))
			}
		}
		out[strings.ToUpper(status)] = resp
	}
	return out
}

// responseMedia returns how to check a body of media type mt, m in the
// document, of a response whose IR variants are variants.
func (b *builder) responseMedia(mt string, m *oas.MediaType, variants []*ir.Response) responseMedia {
	rm := responseMedia{mediaType: mt}
	if m == nil {
		return rm
	}
	var v *ir.Response
	for _, candidate := range variants {
		if candidate.MediaType == mt {
			v = candidate
		}
	}
	switch {
	case isJSON(mt):
		rm.kind = "json"
	case v != nil:
		rm.kind = v.Kind
	case mediaBase(mt) == "application/x-www-form-urlencoded":
		// Generated servers do not write forms, so the IR has no variant
		// for them.
		rm.kind = "form"
	}
	switch rm.kind {
	case "json", "xml", "form", "text":
		if m.Schema != nil {
			rm.schema = b.set.Compile(m.Schema)
		}
	}
	switch rm.kind {
	case "xml":
		rm.xml = xmlNode(v.XML)
	case "form":
		rm.prop = formProperty(m.Schema, map[*oas.Schema]*server.Property{})
	case "text":
		rm.prop = server.Property{Type: schemaType(m.Schema)}
	case "jsonl", "sse":
		rm.stream = b.stream(rm.kind, m, v.Events)
	}
	return rm
}

// header returns how to check the value of the response header h, def in
// the document: as a header parameter is read.
func (b *builder) header(h *ir.Header, def *oas.Header) *param {
	p := &ir.Param{In: "header", Name: h.Name, Style: "simple", Explode: h.Explode, Array: h.Array, Object: h.Object,
		Content: h.Content, ContentKind: h.ContentKind, XML: h.XML}
	if h.Fields != nil {
		p.Properties, p.Additional, p.Shape = h.Fields.Properties, h.Fields.Additional, h.Fields.Shape
	}
	return b.param(p, def.Schema, def.Content)
}

// formProperty describes the fields of the object schema s, for a form
// response: shapes are the objects described so far, which recursive
// schemas refer to.
func formProperty(s *oas.Schema, shapes map[*oas.Schema]*server.Property) server.Property {
	s = deref(s)
	depth := -1
	for schemaType(s) == "array" {
		depth++
		s = deref(itemsOf(s))
	}
	out := server.Property{Array: depth >= 0, Depth: max(depth, 0)}
	switch typ := schemaType(s); typ {
	case "object":
	case "":
		out.Type = "any"
		return out
	default:
		out.Type = typ
		return out
	}
	if def, ok := shapes[s]; ok {
		out.Ref = def
		return out
	}
	def := &server.Property{Type: "object"}
	shapes[s] = def
	if s.Properties.Len() > 0 {
		def.Properties = map[string]server.Property{}
		for name, p := range s.Properties.All() {
			def.Properties[name] = formProperty(p, shapes)
		}
	}
	if ap := s.AdditionalProperties; ap != nil && (ap.Bool == nil || *ap.Bool) {
		a := formProperty(ap, shapes)
		def.Additional = &a
	}
	out.Ref = def
	return out
}

// isJSON reports whether a media type is JSON: application/json, a +json
// suffix or a range of them.
func isJSON(mt string) bool {
	base := mediaBase(mt)
	return base == "application/json" || strings.HasSuffix(base, "+json")
}

// mediaBase returns a media type in lower case without parameters.
func mediaBase(mt string) string {
	base, _, _ := strings.Cut(strings.ToLower(mt), ";")
	return strings.TrimSpace(base)
}

// declared returns the response of a status: its own, its range's
// ("4XX"), or the default one.
func declared(responses map[string]*response, status int) (*response, bool) {
	code := strconv.Itoa(status)
	for _, key := range []string{code, code[:1] + "XX", "DEFAULT"} {
		if r, ok := responses[key]; ok {
			return r, true
		}
	}
	return nil, false
}

// checkResponse hands the request to next, buffering what it writes, and
// sends the response if it matches the document; otherwise it logs why,
// and with RejectResponses sends a 500 problem instead.
func (op *operation) checkResponse(cfg *server.Config, set *schemas.Set, mode ResponseMode, w http.ResponseWriter, r *http.Request, next http.Handler) {
	report := func(status int, problems []string) {
		cfg.Log(r.Context(), slog.LevelWarn, "the response does not match the document",
			"method", r.Method, "path", r.URL.Path, "status", status, "problems", strings.Join(problems, "; "))
	}
	bw := &bufferedWriter{w: w, header: http.Header{}}
	bw.items = func() *itemWriter {
		s := op.responseStream(bw.status, bw.header)
		if s == nil {
			return nil
		}
		return &itemWriter{w: w, s: s, set: set, sp: splitter{events: s.events}, reject: mode == RejectResponses,
			report: func(problem string) { report(bw.status, []string{problem}) }}
	}
	next.ServeHTTP(bw, r)
	if bw.streaming {
		if bw.checked != nil {
			bw.checked.end()
		}
		return
	}
	if bw.status == 0 {
		bw.status = http.StatusOK
	}
	problems := op.responseProblems(set, bw.status, bw.header, bw.body.Bytes())
	if len(problems) > 0 {
		report(bw.status, problems)
		if mode == RejectResponses {
			cfg.HandleError(w, r, problem.New(http.StatusInternalServerError, ""))
			return
		}
	}
	bw.send()
}

// match returns the declared media type of a response's Content-Type,
// and the problem if there is none.
func (resp *response) match(status int, contentType string) (*responseMedia, string) {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, fmt.Sprintf("content type %q is not a media type", contentType)
	}
	for i := range resp.media {
		if server.MatchMediaType(mt, resp.media[i].mediaType) {
			return &resp.media[i], ""
		}
	}
	return nil, fmt.Sprintf("content type %s is not declared for status %d", mt, status)
}

// responseStream returns how to check the items of a response of op, if
// it is a declared JSON Lines or event stream.
func (op *operation) responseStream(status int, header http.Header) *stream {
	resp, ok := declared(op.responses, status)
	if !ok {
		return nil
	}
	if m, _ := resp.match(status, header.Get("Content-Type")); m != nil {
		return m.stream
	}
	return nil
}

// responseProblems returns what is wrong with a response of op.
func (op *operation) responseProblems(set *schemas.Set, status int, header http.Header, body []byte) []string {
	resp, ok := declared(op.responses, status)
	if !ok {
		return []string{fmt.Sprintf("status %d is not declared", status)}
	}
	problems := resp.headerProblems(set, header)
	if len(resp.media) == 0 {
		if len(body) > 0 {
			problems = append(problems, "the response declares no content")
		}
		return problems
	}
	if len(body) == 0 && header.Get("Content-Type") == "" {
		return problems
	}
	m, problem := resp.match(status, header.Get("Content-Type"))
	if m == nil {
		return append(problems, problem)
	}
	if err := m.check(set, body, header.Get("Content-Type")); err != nil {
		problems = append(problems, "body: "+err.Error())
	}
	return problems
}

// headerProblems returns what is wrong with the headers of a response.
func (resp *response) headerProblems(set *schemas.Set, header http.Header) []string {
	var problems []string
	for _, name := range resp.headers {
		if header.Get(name) == "" {
			problems = append(problems, fmt.Sprintf("header %s is required", name))
		}
	}
	// The values are read as a request's header parameters are.
	r := &http.Request{Header: header}
	var b server.Binder
	var params Params
	for _, p := range resp.values {
		p.bind(set, r, server.Query{}, nil, &b, &params)
	}
	if b.Failed() {
		for _, e := range b.Problem().Errors {
			where := "header " + e.Parameter
			if e.Pointer != "" {
				where += " " + e.Pointer
			}
			problems = append(problems, where+": "+e.Detail)
		}
	}
	return problems
}

// check returns what is wrong with a body of m, sent with contentType.
func (m *responseMedia) check(set *schemas.Set, body []byte, contentType string) error {
	if m.stream != nil {
		sp := &splitter{events: m.stream.events}
		sp.write(body)
		index := 0
		for item, ok := sp.next(true); ok; item, ok = sp.next(true) {
			counted, err := m.stream.check(set, item, index, schemas.Response)
			if err != nil {
				return err
			}
			if counted {
				index++
			}
		}
		return nil
	}
	if m.schema == nil {
		return nil
	}
	var doc []byte
	var err error
	switch m.kind {
	case "json":
		doc = body
	case "xml":
		doc, err = server.XMLToJSON(body, m.xml)
	case "form":
		var fields server.FieldValues
		if fields, err = server.FormFields(body); err == nil {
			doc, err = server.ObjectJSON(fields, m.prop)
		}
	case "text":
		doc, err = server.ValuesJSON(server.Values{Items: []string{string(body)}}, server.Scalar, m.prop)
	default:
		return nil
	}
	if err != nil {
		return err
	}
	_, err = check(set, m.schema, doc, schemas.Response)
	return err
}

// bufferedWriter holds a response until it is checked, or until the
// handler flushes it, which streams it from then on, checking the items
// of a stream as they are written.
type bufferedWriter struct {
	w         http.ResponseWriter
	header    http.Header
	status    int
	body      bytes.Buffer
	streaming bool
	// items returns the writer checking the items of the response, if it
	// is a stream, which checked is from the first flush on.
	items   func() *itemWriter
	checked *itemWriter
}

func (b *bufferedWriter) Header() http.Header {
	if b.streaming {
		return b.w.Header()
	}
	return b.header
}

func (b *bufferedWriter) WriteHeader(status int) {
	switch {
	case b.streaming:
		b.w.WriteHeader(status)
	case b.status == 0 && status >= 200:
		b.status = status
	}
}

func (b *bufferedWriter) Write(p []byte) (int, error) {
	switch {
	case b.checked != nil:
		return b.checked.Write(p)
	case b.streaming:
		return b.w.Write(p)
	}
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(p)
}

// Flush sends what is buffered and streams the rest, checking the items
// of a stream.
func (b *bufferedWriter) Flush() {
	if !b.streaming {
		if b.status == 0 {
			b.status = http.StatusOK
		}
		b.streaming = true
		if b.checked = b.items(); b.checked != nil {
			b.writeHeader()
			// The status line is sent; a failed write means the client is
			// gone, or the item was rejected and is logged.
			_, _ = b.checked.Write(b.body.Bytes())
		} else {
			b.send()
		}
	}
	if f, ok := b.w.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap gives http.ResponseController access to the underlying writer.
func (b *bufferedWriter) Unwrap() http.ResponseWriter { return b.w }

// send writes the buffered response.
func (b *bufferedWriter) send() {
	b.writeHeader()
	// The status line is sent; a failed write means the client is gone.
	_, _ = b.w.Write(b.body.Bytes())
}

func (b *bufferedWriter) writeHeader() {
	for k, v := range b.header {
		b.w.Header()[k] = v
	}
	b.w.WriteHeader(b.status)
}

// errRejected is the error of writes to a stream whose item was rejected.
var errRejected = errors.New("middleware: the response stream has an invalid item and was ended")

// itemWriter writes a streamed response item by item, each once it is
// complete and checked: an invalid one is reported, and with reject not
// sent, ending the stream.
type itemWriter struct {
	w      io.Writer
	s      *stream
	set    *schemas.Set
	sp     splitter
	index  int
	reject bool
	report func(problem string)
	err    error
}

func (iw *itemWriter) Write(p []byte) (int, error) {
	if iw.err != nil {
		return 0, iw.err
	}
	iw.sp.write(p)
	for item, ok := iw.sp.next(false); ok; item, ok = iw.sp.next(false) {
		if err := iw.item(item); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// end writes what is left once the handler is done: the last line of
// JSON Lines may have no line end.
func (iw *itemWriter) end() {
	for item, ok := iw.sp.next(true); ok && iw.err == nil; item, ok = iw.sp.next(true) {
		// A failed write is reported or means the client is gone.
		_ = iw.item(item)
	}
}

func (iw *itemWriter) item(item []byte) error {
	counted, err := iw.s.check(iw.set, item, iw.index, schemas.Response)
	if counted {
		iw.index++
	}
	if err != nil {
		iw.report("body: " + err.Error())
		if iw.reject {
			iw.err = errRejected
			return iw.err
		}
	}
	if _, err := iw.w.Write(item); err != nil {
		iw.err = err
		return err
	}
	return nil
}
