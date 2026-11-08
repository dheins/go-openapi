package server

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/textproto"
	"net/url"
	"strings"
)

// WithTrustedProxies trusts the forwarding headers (Forwarded, or
// X-Forwarded-For, -Proto, -Host, -Port and -Prefix) of requests from
// these addresses, such as netip.MustParsePrefix("10.0.0.0/8"): $url,
// which RequestURL returns, is then the URL the client asked the first of
// these proxies for. Headers of other clients are never read.
func WithTrustedProxies(prefixes ...netip.Prefix) Option {
	return func(c *Config) { c.TrustedProxies = append(c.TrustedProxies, prefixes...) }
}

type proxiesKey struct{}

// proxies is what RequestURL needs to read forwarding headers.
type proxies struct {
	trusted []netip.Prefix
	logger  *slog.Logger
}

// withProxies puts the trusted proxies of c in the context of every
// request to next, for RequestURL.
func (c *Config) withProxies(next http.Handler) http.Handler {
	p := &proxies{trusted: c.TrustedProxies, logger: c.Logger}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), proxiesKey{}, p)))
	})
}

func (p *proxies) trusts(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, pre := range p.trusted {
		if pre.Contains(addr) {
			return true
		}
	}
	return false
}

// hop is what a proxy says about the request it received: from whom,
// and the scheme, host and path prefix it was sent to. Empty values are
// not given.
type hop struct {
	from                      string
	proto, host, port, prefix string
}

// forwardedURL returns the URL of r as the client sent it to the first
// trusted proxy, or "" if r is not from a trusted proxy. Each proxy
// appends what it received, so the entries are walked from the right
// while the address they came from is trusted.
func (p *proxies) forwardedURL(r *http.Request) string {
	peer, ok := hostAddr(r.RemoteAddr)
	if !ok || !p.trusts(peer) {
		return ""
	}
	hops := forwardedHops(r.Header)
	var used *hop
	for i := len(hops) - 1; i >= 0; i-- {
		used = &hops[i]
		if from, ok := hostAddr(hops[i].from); !ok || !p.trusts(from) {
			break
		}
	}
	if used == nil {
		return ""
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host, prefix := r.Host, ""
	switch {
	case used.proto == "http" || used.proto == "https":
		scheme = used.proto
	case used.proto != "":
		p.bad("proto", used.proto)
	}
	if used.host != "" {
		if validHost(used.host) {
			host = used.host
		} else {
			p.bad("host", used.host)
		}
	}
	if used.port != "" {
		name := host
		if h, _, err := net.SplitHostPort(host); err == nil {
			name = h
		}
		if withPort := net.JoinHostPort(strings.Trim(name, "[]"), used.port); validHost(withPort) {
			host = withPort
		} else {
			p.bad("port", used.port)
		}
	}
	if used.prefix != "" {
		if validPrefix(used.prefix) {
			prefix = strings.TrimSuffix(used.prefix, "/")
		} else {
			p.bad("prefix", used.prefix)
		}
	}
	return scheme + "://" + host + prefix + r.URL.RequestURI()
}

// bad logs a forwarded value RequestURL does not use.
func (p *proxies) bad(what, value string) {
	if p.logger != nil {
		p.logger.Debug("ignoring a forwarded value", "field", what, "value", value)
	}
}

// validHost reports whether h is a host, with an optional port, that a
// URL can hold.
func validHost(h string) bool {
	if h == "" || strings.ContainsAny(h, "/?#@ \t\\") {
		return false
	}
	u, err := url.Parse("http://" + h)
	return err == nil && u.Host == h && u.Hostname() != ""
}

// validPrefix reports whether p is a path a proxy may strip: absolute,
// with valid escapes and no query or fragment.
func validPrefix(p string) bool {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "?# 	") {
		return false
	}
	_, err := url.ParseRequestURI(p)
	return err == nil
}

// hostAddr returns the IP address of s, an address with or without a
// port, as RemoteAddr and the for parameter give it.
func hostAddr(s string) (netip.Addr, bool) {
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr(), true
	}
	a, err := netip.ParseAddr(strings.Trim(s, "[]"))
	return a, err == nil
}

// forwardedHops returns the hops the forwarding headers of h describe,
// in order: the elements of Forwarded or, without it, the entries of
// X-Forwarded-For with the X-Forwarded-Proto, -Host, -Port and -Prefix
// entries at the same place from the right (a header with fewer entries
// gives its first to the hops it has none for).
func forwardedHops(h http.Header) []hop {
	if fwd := headerList(h, "Forwarded"); len(fwd) > 0 {
		hops := make([]hop, len(fwd))
		for i, el := range fwd {
			for _, pair := range splitQuoted(el, ';') {
				k, v, _ := strings.Cut(pair, "=")
				v = unquote(strings.TrimSpace(v))
				switch strings.ToLower(strings.TrimSpace(k)) {
				case "for":
					hops[i].from = v
				case "proto":
					hops[i].proto = strings.ToLower(v)
				case "host":
					hops[i].host = v
				}
			}
		}
		return hops
	}
	from := headerList(h, "X-Forwarded-For")
	proto, host, port, prefix := headerList(h, "X-Forwarded-Proto"), headerList(h, "X-Forwarded-Host"), headerList(h, "X-Forwarded-Port"), headerList(h, "X-Forwarded-Prefix")
	n := len(from)
	if n == 0 && len(proto)+len(host)+len(port)+len(prefix) > 0 {
		// Without X-Forwarded-For, the headers describe one hop, from
		// whom is not said.
		n = 1
	}
	hops := make([]hop, n)
	at := func(list []string, i int) string {
		// i is the place from the right of the hop.
		switch {
		case len(list) == 0:
			return ""
		case i < len(list):
			return list[len(list)-1-i]
		}
		return list[0]
	}
	for i := range hops {
		right := len(hops) - 1 - i
		hops[i] = hop{from: at(from, right), proto: strings.ToLower(at(proto, right)), host: at(host, right), port: at(port, right), prefix: at(prefix, right)}
	}
	return hops
}

// headerList returns the comma-separated entries of every line of the
// header name, trimmed, outside quoted strings.
func headerList(h http.Header, name string) []string {
	var out []string
	for _, line := range h.Values(textproto.CanonicalMIMEHeaderKey(name)) {
		for _, e := range splitQuoted(line, ',') {
			if e = strings.TrimSpace(e); e != "" {
				out = append(out, e)
			}
		}
	}
	return out
}

// splitQuoted splits s at sep outside quoted strings.
func splitQuoted(s string, sep byte) []string {
	var out []string
	start, quoted := 0, false
	for i := 0; i < len(s); i++ {
		switch {
		case quoted && s[i] == '\\':
			i++
		case s[i] == '"':
			quoted = !quoted
		case s[i] == sep && !quoted:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// unquote returns v, a token or quoted string, as its text.
func unquote(v string) string {
	if len(v) < 2 || v[0] != '"' || v[len(v)-1] != '"' {
		return v
	}
	var b strings.Builder
	for i := 1; i < len(v)-1; i++ {
		if v[i] == '\\' && i+1 < len(v)-1 {
			i++
		}
		b.WriteByte(v[i])
	}
	return b.String()
}
