package oas

// JSON methods that carry "x-" extension fields through a round trip. Each
// converts to a local plain type so the standard encoder handles the
// declared fields without recursing back into these methods.

func (d *Document) UnmarshalJSON(data []byte) error {
	type plain Document
	return unmarshalExtensible(data, (*plain)(d), &d.Extensions)
}

func (d Document) MarshalJSON() ([]byte, error) {
	type plain Document
	return marshalExtensible(plain(d), d.Extensions)
}

func (i *Info) UnmarshalJSON(data []byte) error {
	type plain Info
	return unmarshalExtensible(data, (*plain)(i), &i.Extensions)
}

func (i Info) MarshalJSON() ([]byte, error) {
	type plain Info
	return marshalExtensible(plain(i), i.Extensions)
}

func (c *Contact) UnmarshalJSON(data []byte) error {
	type plain Contact
	return unmarshalExtensible(data, (*plain)(c), &c.Extensions)
}

func (c Contact) MarshalJSON() ([]byte, error) {
	type plain Contact
	return marshalExtensible(plain(c), c.Extensions)
}

func (l *License) UnmarshalJSON(data []byte) error {
	type plain License
	return unmarshalExtensible(data, (*plain)(l), &l.Extensions)
}

func (l License) MarshalJSON() ([]byte, error) {
	type plain License
	return marshalExtensible(plain(l), l.Extensions)
}

func (s *Server) UnmarshalJSON(data []byte) error {
	type plain Server
	return unmarshalExtensible(data, (*plain)(s), &s.Extensions)
}

func (s Server) MarshalJSON() ([]byte, error) {
	type plain Server
	return marshalExtensible(plain(s), s.Extensions)
}

func (s *ServerVariable) UnmarshalJSON(data []byte) error {
	type plain ServerVariable
	return unmarshalExtensible(data, (*plain)(s), &s.Extensions)
}

func (s ServerVariable) MarshalJSON() ([]byte, error) {
	type plain ServerVariable
	return marshalExtensible(plain(s), s.Extensions)
}

func (c *Components) UnmarshalJSON(data []byte) error {
	type plain Components
	return unmarshalExtensible(data, (*plain)(c), &c.Extensions)
}

func (c Components) MarshalJSON() ([]byte, error) {
	type plain Components
	return marshalExtensible(plain(c), c.Extensions)
}

func (pi *PathItem) UnmarshalJSON(data []byte) error {
	type plain PathItem
	return unmarshalExtensible(data, (*plain)(pi), &pi.Extensions)
}

func (pi PathItem) MarshalJSON() ([]byte, error) {
	type plain PathItem
	return marshalExtensible(plain(pi), pi.Extensions)
}

func (o *Operation) UnmarshalJSON(data []byte) error {
	type plain Operation
	return unmarshalExtensible(data, (*plain)(o), &o.Extensions)
}

func (o Operation) MarshalJSON() ([]byte, error) {
	type plain Operation
	return marshalExtensible(plain(o), o.Extensions)
}

func (p *Parameter) UnmarshalJSON(data []byte) error {
	type plain Parameter
	return unmarshalExtensible(data, (*plain)(p), &p.Extensions)
}

func (p Parameter) MarshalJSON() ([]byte, error) {
	type plain Parameter
	return marshalExtensible(plain(p), p.Extensions)
}

func (r *RequestBody) UnmarshalJSON(data []byte) error {
	type plain RequestBody
	return unmarshalExtensible(data, (*plain)(r), &r.Extensions)
}

func (r RequestBody) MarshalJSON() ([]byte, error) {
	type plain RequestBody
	return marshalExtensible(plain(r), r.Extensions)
}

func (m *MediaType) UnmarshalJSON(data []byte) error {
	type plain MediaType
	return unmarshalExtensible(data, (*plain)(m), &m.Extensions)
}

func (m MediaType) MarshalJSON() ([]byte, error) {
	type plain MediaType
	return marshalExtensible(plain(m), m.Extensions)
}

func (e *Encoding) UnmarshalJSON(data []byte) error {
	type plain Encoding
	return unmarshalExtensible(data, (*plain)(e), &e.Extensions)
}

func (e Encoding) MarshalJSON() ([]byte, error) {
	type plain Encoding
	return marshalExtensible(plain(e), e.Extensions)
}

func (r *Response) UnmarshalJSON(data []byte) error {
	type plain Response
	return unmarshalExtensible(data, (*plain)(r), &r.Extensions)
}

func (r Response) MarshalJSON() ([]byte, error) {
	type plain Response
	return marshalExtensible(plain(r), r.Extensions)
}

func (e *Example) UnmarshalJSON(data []byte) error {
	type plain Example
	return unmarshalExtensible(data, (*plain)(e), &e.Extensions)
}

func (e Example) MarshalJSON() ([]byte, error) {
	type plain Example
	return marshalExtensible(plain(e), e.Extensions)
}

func (l *Link) UnmarshalJSON(data []byte) error {
	type plain Link
	return unmarshalExtensible(data, (*plain)(l), &l.Extensions)
}

func (l Link) MarshalJSON() ([]byte, error) {
	type plain Link
	return marshalExtensible(plain(l), l.Extensions)
}

func (h *Header) UnmarshalJSON(data []byte) error {
	type plain Header
	return unmarshalExtensible(data, (*plain)(h), &h.Extensions)
}

func (h Header) MarshalJSON() ([]byte, error) {
	type plain Header
	return marshalExtensible(plain(h), h.Extensions)
}

func (t *Tag) UnmarshalJSON(data []byte) error {
	type plain Tag
	return unmarshalExtensible(data, (*plain)(t), &t.Extensions)
}

func (t Tag) MarshalJSON() ([]byte, error) {
	type plain Tag
	return marshalExtensible(plain(t), t.Extensions)
}

func (e *ExternalDocs) UnmarshalJSON(data []byte) error {
	type plain ExternalDocs
	return unmarshalExtensible(data, (*plain)(e), &e.Extensions)
}

func (e ExternalDocs) MarshalJSON() ([]byte, error) {
	type plain ExternalDocs
	return marshalExtensible(plain(e), e.Extensions)
}

func (s *SecurityScheme) UnmarshalJSON(data []byte) error {
	type plain SecurityScheme
	return unmarshalExtensible(data, (*plain)(s), &s.Extensions)
}

func (s SecurityScheme) MarshalJSON() ([]byte, error) {
	type plain SecurityScheme
	return marshalExtensible(plain(s), s.Extensions)
}

func (o *OAuthFlows) UnmarshalJSON(data []byte) error {
	type plain OAuthFlows
	return unmarshalExtensible(data, (*plain)(o), &o.Extensions)
}

func (o OAuthFlows) MarshalJSON() ([]byte, error) {
	type plain OAuthFlows
	return marshalExtensible(plain(o), o.Extensions)
}

func (o *OAuthFlow) UnmarshalJSON(data []byte) error {
	type plain OAuthFlow
	return unmarshalExtensible(data, (*plain)(o), &o.Extensions)
}

func (o OAuthFlow) MarshalJSON() ([]byte, error) {
	type plain OAuthFlow
	return marshalExtensible(plain(o), o.Extensions)
}

func (d *Discriminator) UnmarshalJSON(data []byte) error {
	type plain Discriminator
	return unmarshalExtensible(data, (*plain)(d), &d.Extensions)
}

func (d Discriminator) MarshalJSON() ([]byte, error) {
	type plain Discriminator
	return marshalExtensible(plain(d), d.Extensions)
}

func (x *XML) UnmarshalJSON(data []byte) error {
	type plain XML
	return unmarshalExtensible(data, (*plain)(x), &x.Extensions)
}

func (x XML) MarshalJSON() ([]byte, error) {
	type plain XML
	return marshalExtensible(plain(x), x.Extensions)
}
