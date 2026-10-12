package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dheins/go-openapi/runtime/problem"
)

type userKey struct{}

func withUser(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, userKey{}, user)
}

func TestAuthorize(t *testing.T) {
	keyCheck := func(r *http.Request) SchemeCheck {
		return APIKeyCheck(r, "header", "X-API-Key", func(ctx context.Context, key string) (context.Context, error) {
			switch key {
			case "good":
				return withUser(ctx, "key-user"), nil
			case "limited":
				return nil, problem.New(http.StatusForbidden, "missing scope")
			}
			return nil, errors.New("unknown key hunter2")
		})
	}
	bearer := func(r *http.Request) SchemeCheck {
		return BearerCheck(r, func(ctx context.Context, token string) (context.Context, error) {
			if token != "t0k" {
				return nil, errors.New("bad token")
			}
			return withUser(ctx, "token-user"), nil
		})
	}
	tests := []struct {
		name      string
		headers   map[string]string
		anonymous bool
		status    int
		user      string
		challenge bool
	}{
		{"api key", map[string]string{"X-API-Key": "good"}, false, 0, "key-user", false},
		{"bearer", map[string]string{"Authorization": "bearer t0k"}, false, 0, "token-user", false},
		{"nothing", nil, false, 401, "", true},
		{"nothing, anonymous allowed", nil, true, 0, "", false},
		{"invalid, anonymous allowed", map[string]string{"X-API-Key": "nope"}, true, 401, "", true},
		{"problem from check", map[string]string{"X-API-Key": "limited"}, false, 403, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			for k, v := range tt.headers {
				r.Header.Set(k, v)
			}
			reqs := []Requirement{{keyCheck(r)}, {bearer(r)}}
			if tt.anonymous {
				reqs = append(reqs, Requirement{})
			}
			rec := httptest.NewRecorder()
			ctx, err := Authorize(context.Background(), rec, reqs)
			var p *problem.Problem
			switch {
			case tt.status == 0 && err != nil:
				t.Fatalf("unexpected error %v", err)
			case tt.status != 0 && (!errors.As(err, &p) || p.Status != tt.status):
				t.Fatalf("err = %v, want status %d", err, tt.status)
			}
			if p != nil && p.Detail == "unknown key hunter2" {
				t.Error("an error from a check leaked to the client")
			}
			if tt.status == 0 {
				user, _ := ctx.Value(userKey{}).(string)
				if user != tt.user {
					t.Errorf("user = %q, want %q", user, tt.user)
				}
			}
			if got := rec.Header().Values("WWW-Authenticate"); (len(got) > 0) != tt.challenge {
				t.Errorf("challenges = %v", got)
			}
		})
	}
}

func TestSchemeChecks(t *testing.T) {
	ok := func(ctx context.Context, _ ...string) (context.Context, error) { return ctx, nil }
	r := httptest.NewRequest("GET", "/?key=q", nil)
	r.SetBasicAuth("ann", "pw")
	r.AddCookie(&http.Cookie{Name: "session", Value: "c"})
	var gotUser, gotPass string
	basic := BasicCheck(r, func(ctx context.Context, u, p string) (context.Context, error) {
		gotUser, gotPass = u, p
		return ctx, nil
	})
	if _, err := basic.Check(context.Background()); err != nil || gotUser != "ann" || gotPass != "pw" {
		t.Errorf("basic: %v %q %q", err, gotUser, gotPass)
	}
	for _, in := range []string{"query", "cookie"} {
		name := map[string]string{"query": "key", "cookie": "session"}[in]
		check := APIKeyCheck(r, in, name, func(ctx context.Context, key string) (context.Context, error) { return ok(ctx, key) })
		if _, err := check.Check(context.Background()); err != nil {
			t.Errorf("api key in %s: %v", in, err)
		}
	}
	if _, err := BearerCheck(r, func(ctx context.Context, tok string) (context.Context, error) { return ok(ctx, tok) }).Check(context.Background()); !errors.Is(err, ErrNoCredentials) {
		t.Errorf("bearer with basic credentials: %v", err)
	}

	r.Header.Set("Authorization", "Digest abc=1")
	var creds string
	digest := AuthorizationCheck(r, "Digest", func(ctx context.Context, c string) (context.Context, error) {
		creds = c
		return ctx, nil
	})
	if _, err := digest.Check(context.Background()); err != nil || creds != "abc=1" {
		t.Errorf("digest: %v %q", err, creds)
	}

	mtls := TLSCheck(r, func(ctx context.Context, s *tls.ConnectionState) (context.Context, error) { return ctx, nil })
	if _, err := mtls.Check(context.Background()); !errors.Is(err, ErrNoCredentials) {
		t.Errorf("no TLS: %v", err)
	}
	r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{}}}
	if _, err := mtls.Check(context.Background()); err != nil {
		t.Errorf("TLS: %v", err)
	}
}
