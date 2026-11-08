package server

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

// forwardedURL serves a request from remote with header through a
// server trusting 10.0.0.0/8 and ::1 and returns its $url.
func forwardedURL(t *testing.T, remote string, header http.Header) string {
	t.Helper()
	cfg := NewConfig(WithTrustedProxies(netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("::1/128")))
	var got string
	cfg.Handle(Operation{Method: "POST", Path: "/subs"}, "/subs", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = RequestURL(RequestContext(r))
	}))
	r := httptest.NewRequest("POST", "http://app.internal:8080/subs?x=1", nil)
	r.RemoteAddr = remote
	r.Header = header
	cfg.Handler().ServeHTTP(httptest.NewRecorder(), r)
	return got
}

func TestForwardedURL(t *testing.T) {
	const own = "http://app.internal:8080/subs?x=1"
	for _, c := range []struct {
		name, remote string
		header       http.Header
		want         string
	}{
		{"no headers", "10.0.0.5:4000", http.Header{}, own},
		{"untrusted peer", "203.0.113.9:4000", http.Header{"X-Forwarded-Proto": {"https"}, "X-Forwarded-Host": {"evil.example"}}, own},
		{"X-Forwarded-*, one proxy", "10.0.0.5:4000", http.Header{
			"X-Forwarded-For": {"203.0.113.9"}, "X-Forwarded-Proto": {"https"}, "X-Forwarded-Host": {"api.example.com"}, "X-Forwarded-Prefix": {"/v1/"},
		}, "https://api.example.com/v1/subs?x=1"},
		{"without X-Forwarded-For", "10.0.0.5:4000", http.Header{"X-Forwarded-Proto": {"https"}, "X-Forwarded-Host": {"api.example.com"}}, "https://api.example.com/subs?x=1"},
		{"two trusted proxies, the client's values ignored", "10.0.0.5:4000", http.Header{
			"X-Forwarded-For":   {"10.9.9.9, 203.0.113.9", "10.0.0.7"},
			"X-Forwarded-Host":  {"evil.example, api.example.com, lb.internal"},
			"X-Forwarded-Proto": {"https"},
		}, "https://api.example.com/subs?x=1"},
		{"a port", "10.0.0.5:4000", http.Header{"X-Forwarded-Host": {"api.example.com:8443"}, "X-Forwarded-Port": {"443"}}, "http://api.example.com:443/subs?x=1"},
		{"Forwarded, quoted, IPv6", "[::1]:4000", http.Header{
			"Forwarded": {`for=203.0.113.9;proto=https;host="api.example.com", for="[::1]:4711";host=lb.internal`},
		}, "https://api.example.com/subs?x=1"},
		{"Forwarded over X-Forwarded-*", "10.0.0.5:4000", http.Header{
			"Forwarded": {"for=203.0.113.9;host=a.example"}, "X-Forwarded-Host": {"b.example"},
		}, "http://a.example/subs?x=1"},
		{"an obfuscated hop is not trusted", "10.0.0.5:4000", http.Header{
			"Forwarded": {"for=10.0.0.1;host=evil.example, for=_hidden;host=api.example.com"},
		}, "http://api.example.com/subs?x=1"},
		{"bad values fall back", "10.0.0.5:4000", http.Header{
			"X-Forwarded-Proto": {"gopher"}, "X-Forwarded-Host": {"a/b@c"}, "X-Forwarded-Prefix": {"v1?x"},
		}, own},
	} {
		if got := forwardedURL(t, c.remote, c.header); got != c.want {
			t.Errorf("%s: $url = %q, want %q", c.name, got, c.want)
		}
	}

	// Without trusted proxies, nothing is read.
	cfg := NewConfig()
	var got string
	cfg.Handle(Operation{Method: "GET", Path: "/a"}, "/a", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = RequestURL(RequestContext(r))
	}))
	r := httptest.NewRequest("GET", "http://app.internal/a", nil)
	r.RemoteAddr = "10.0.0.5:4000"
	r.Header.Set("X-Forwarded-Host", "api.example.com")
	cfg.Handler().ServeHTTP(httptest.NewRecorder(), r)
	if got != "http://app.internal/a" {
		t.Errorf("without trusted proxies: $url = %q", got)
	}
}

func FuzzForwarded(f *testing.F) {
	f.Add("for=203.0.113.9;proto=https;host=\"api.example.com\"", "10.0.0.1, 203.0.113.9", "https", "api.example.com:443", "443", "/v1")
	f.Add("", "", "", "[::1]:8080", "8443", "/")
	f.Fuzz(func(t *testing.T, fwd, xff, proto, host, port, prefix string) {
		h := http.Header{}
		for k, v := range map[string]string{"Forwarded": fwd, "X-Forwarded-For": xff, "X-Forwarded-Proto": proto, "X-Forwarded-Host": host, "X-Forwarded-Port": port, "X-Forwarded-Prefix": prefix} {
			if v != "" {
				h.Set(k, v)
			}
		}
		p := &proxies{trusted: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}}
		r := httptest.NewRequest("GET", "http://app.internal/a", nil)
		r.RemoteAddr = "10.0.0.5:4000"
		r.Header = h
		got := p.forwardedURL(r)
		if got == "" {
			return
		}
		u, err := url.Parse(got)
		if err != nil || u.Scheme != "http" && u.Scheme != "https" || u.Host == "" || !strings.HasSuffix(got, "/a") {
			t.Fatalf("$url = %q (%v)", got, err)
		}
	})
}
