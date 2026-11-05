package server

import (
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

const pageDoc = `{"openapi": "3.1.0", "info": {"title": "Pets <script>", "version": "1"},
"servers": [{"url": "https://api.example.com/v1"}, {"url": "/v1"}, {"url": "https://{env}.example.com"}, {"url": "https://api.example.com/v2"}],
"paths": {}}`

// getPage serves doc with opts and gets target.
func getPage(t *testing.T, target string, opts ...Option) *httptest.ResponseRecorder {
	t.Helper()
	c := NewConfig(opts...)
	c.ServeDocument([]byte(pageDoc))
	rec := httptest.NewRecorder()
	c.Handler().ServeHTTP(rec, httptest.NewRequest("GET", target, nil))
	return rec
}

func TestDocumentPage(t *testing.T) {
	nonce := regexp.MustCompile(`'nonce-([^']+)'`)
	var nonces []string
	for range 2 {
		rec := getPage(t, "/v1/docs", WithBasePath("/v1"), WithDocumentPage("/docs", SwaggerUI))
		body, csp := rec.Body.String(), rec.Header().Get("Content-Security-Policy")
		if rec.Code != 200 || rec.Header().Get("Content-Type") != "text/html; charset=utf-8" || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%d %v", rec.Code, rec.Header())
		}
		m := nonce.FindStringSubmatch(csp)
		if m == nil || !strings.Contains(body, `<script nonce="`+m[1]+`">`) {
			t.Fatalf("nonce of %q not in %s", csp, body)
		}
		nonces = append(nonces, m[1])
		for _, want := range []string{
			"script-src 'nonce-" + m[1] + "' https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.34.0/swagger-ui-bundle.js;",
			"style-src 'unsafe-inline' https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.34.0/swagger-ui.css;",
			// The origins of absolute servers, once; not templated ones.
			"connect-src 'self' https://api.example.com;",
			"frame-ancestors 'none'",
		} {
			if !strings.Contains(csp, want) {
				t.Errorf("CSP %q lacks %q", csp, want)
			}
		}
		for _, want := range []string{
			// The document's path is below the base path, and the title
			// is escaped.
			`<script id="goapi-config" type="application/json">{"url":"/v1/openapi.json"}</script>`,
			`<title>Pets &lt;script&gt;</title>`,
			`integrity="sha384-9Td7MKiIr2S83E1kZyyVi8KdJw/S8CpAvu0llLyMeg0xq/hDGiZJfVn0fV4llCmK" crossorigin="anonymous"`,
			`SwaggerUIBundle(`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("page lacks %q:\n%s", want, body)
			}
		}
	}
	if nonces[0] == nonces[1] {
		t.Error("the nonce is the same for two pages")
	}
	// The page implies the document, at /openapi.json.
	if rec := getPage(t, "/v1/openapi.json", WithBasePath("/v1"), WithDocumentPage("/docs", SwaggerUI)); rec.Code != 200 || rec.Body.String() != pageDoc {
		t.Errorf("document: %d", rec.Code)
	}
	// Or the one WithDocument sets.
	rec := getPage(t, "/docs", WithDocument("/spec.json"), WithDocumentPage("/docs", Redoc))
	if body := rec.Body.String(); !strings.Contains(body, `{"url":"/spec.json"}`) || !strings.Contains(body, "disableTelemetry: true") ||
		!strings.Contains(body, `import { init } from "https://cdn.jsdelivr.net/npm/redoc@3.0.1/bundles/redoc.standalone.js";`) ||
		!strings.Contains(body, `<script type="module" src="https://cdn.jsdelivr.net/npm/redoc@3.0.1/bundles/redoc.standalone.js" integrity="sha384-`) {
		t.Errorf("redoc page:\n%s", body)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "style-src 'unsafe-inline';") {
		t.Errorf("CSP without stylesheets: %q", csp)
	}

	// The application's own assets, without integrity hashes.
	rec = getPage(t, "/docs", WithDocumentPage("/docs", SwaggerUI), WithDocumentAssets("https://static.example.com/swagger/"))
	body, csp := rec.Body.String(), rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(body, `<script src="https://static.example.com/swagger/swagger-ui-bundle.js"></script>`) || strings.Contains(body, "integrity") ||
		!strings.Contains(csp, "https://static.example.com/swagger/swagger-ui.css") {
		t.Errorf("own assets:\n%s\n%s", csp, body)
	}

	// Without the option, there is no page.
	if rec := getPage(t, "/docs", WithDocument("/openapi.json")); rec.Code != 404 {
		t.Errorf("no page: %d", rec.Code)
	}
	defer func() {
		if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), "unknown UI") {
			t.Errorf("unknown UI: %v", r)
		}
	}()
	getPage(t, "/docs", WithDocumentPage("/docs", UI(9)))
}

// TestDocumentPageAssets checks the pinned hashes against the CDN. It
// downloads the assets, so it runs only with GOAPI_NET_TESTS=1.
func TestDocumentPageAssets(t *testing.T) {
	if os.Getenv("GOAPI_NET_TESTS") != "1" {
		t.Skip("set GOAPI_NET_TESTS=1 to check the assets on the CDN")
	}
	for _, ui := range pageUIs {
		for _, a := range ui.assets {
			resp, err := http.Get(ui.cdn + a.file)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || resp.StatusCode != 200 {
				t.Fatalf("%s: %d %v", a.file, resp.StatusCode, err)
			}
			sum := sha512.Sum384(data)
			if got := "sha384-" + base64.StdEncoding.EncodeToString(sum[:]); got != a.integrity {
				t.Errorf("%s%s: %s, pinned %s", ui.cdn, a.file, got, a.integrity)
			}
		}
	}
}
