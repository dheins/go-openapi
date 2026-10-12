package oas

// Deref methods return the object a reference resolves to, or the receiver
// itself when it is not a reference (or has not been resolved).

func (p *PathItem) Deref() *PathItem {
	if p != nil && p.Resolved != nil {
		return p.Resolved
	}
	return p
}

func (p *Parameter) Deref() *Parameter {
	if p != nil && p.Resolved != nil {
		return p.Resolved
	}
	return p
}

func (r *RequestBody) Deref() *RequestBody {
	if r != nil && r.Resolved != nil {
		return r.Resolved
	}
	return r
}

func (m *MediaType) Deref() *MediaType {
	if m != nil && m.Resolved != nil {
		return m.Resolved
	}
	return m
}

func (r *Response) Deref() *Response {
	if r != nil && r.Resolved != nil {
		return r.Resolved
	}
	return r
}

func (c *Callback) Deref() *Callback {
	if c != nil && c.Resolved != nil {
		return c.Resolved
	}
	return c
}

func (e *Example) Deref() *Example {
	if e != nil && e.Resolved != nil {
		return e.Resolved
	}
	return e
}

func (l *Link) Deref() *Link {
	if l != nil && l.Resolved != nil {
		return l.Resolved
	}
	return l
}

func (h *Header) Deref() *Header {
	if h != nil && h.Resolved != nil {
		return h.Resolved
	}
	return h
}

func (s *SecurityScheme) Deref() *SecurityScheme {
	if s != nil && s.Resolved != nil {
		return s.Resolved
	}
	return s
}
