package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// UI is a renderer of the documentation page: see WithDocumentPage.
type UI int

const (
	// SwaggerUI renders the document with Swagger UI, which can also send
	// the operations' requests to the server.
	SwaggerUI UI = iota + 1
	// Redoc renders the document with Redoc, as reference documentation.
	Redoc
)

// pageAsset is a script or stylesheet of a page: its file, and its
// Subresource Integrity hash at the pinned version. A script may be an ES
// module.
type pageAsset struct {
	file, integrity string
	style, module   bool
}

// pageUI is how a page renders the document: from where, with which
// assets, and the body that starts it.
type pageUI struct {
	// cdn is the base URL of the assets, at an exact version, so that
	// their hashes hold.
	cdn    string
	assets []pageAsset
	body   string
}

var pageUIs = map[UI]pageUI{
	SwaggerUI: {
		cdn: "https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.34.0/",
		assets: []pageAsset{
			{file: "swagger-ui.css", integrity: "sha384-4c3vsE5f+trhFyHrFrLM1fFGUn531NIaC7dcqEWIJdoRQJ5CkJbSuSCwXLpaS1KF", style: true},
			{file: "swagger-ui-bundle.js", integrity: "sha384-9Td7MKiIr2S83E1kZyyVi8KdJw/S8CpAvu0llLyMeg0xq/hDGiZJfVn0fV4llCmK"},
		},
		body: `<div id="swagger-ui"></div>
<script nonce="{{.Nonce}}">
const config = JSON.parse(document.getElementById("goapi-config").textContent);
window.ui = SwaggerUIBundle({ url: config.url, dom_id: "#swagger-ui", deepLinking: true, validatorUrl: null });
</script>`,
	},
	// Redoc 3's bundle is an ES module. The page imports init from it,
	// rather than letting it render a <redoc> element, to turn off the
	// telemetry it otherwise sends to Redocly (which the policy blocks
	// anyway). The import gets the module the script element loaded,
	// checked against its hash, from the module map.
	Redoc: {
		cdn: "https://cdn.jsdelivr.net/npm/redoc@3.0.1/bundles/",
		assets: []pageAsset{
			{file: "redoc.standalone.js", integrity: "sha384-j07qRKHNNmcwU1/Y1elJc7pDLHXP/QfbQXTtSkbOJ7uYGJikqAzsFVbY2zR9FqOT", module: true},
		},
		body: `<div id="redoc"></div>
<script type="module" nonce="{{.Nonce}}">
import { init } from {{.Module}};
const config = JSON.parse(document.getElementById("goapi-config").textContent);
init(config.url, { disableTelemetry: true }, document.getElementById("redoc"));
</script>`,
	},
}

const pageHead = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
{{- range .Assets}}
{{- if .Style}}
<link rel="stylesheet" href="{{.URL}}"{{with .Integrity}} integrity="{{.}}" crossorigin="anonymous"{{end}}>
{{- else}}
<script{{if .Module}} type="module"{{end}} src="{{.URL}}"{{with .Integrity}} integrity="{{.}}" crossorigin="anonymous"{{end}}></script>
{{- end}}
{{- end}}
<script id="goapi-config" type="application/json">{{.Config}}</script>
<style>body { margin: 0; }</style>
</head>
<body>
`

// WithDocumentPage serves, at path below the base path, an HTML page
// rendering the document with ui, SwaggerUI or Redoc. The page loads the
// document from WithDocument's path, which is /openapi.json if it is not
// set. Its scripts come from jsDelivr, at a version pinned with their
// Subresource Integrity hashes, unless WithDocumentAssets says where else;
// the module embeds none. A Content-Security-Policy allows only them, the
// page's own script, and requests to the page's origin and to those of
// the document's servers, for Swagger UI to send.
func WithDocumentPage(path string, ui UI) Option {
	return func(c *Config) { c.DocumentPage, c.DocumentUI = path, ui }
}

// WithDocumentAssets loads the scripts and stylesheets of the
// documentation page from baseURL, the application's own copies, for a
// page used offline or behind a firewall: the files the swagger-ui-dist
// (swagger-ui.css, swagger-ui-bundle.js) or redoc (redoc.standalone.js)
// npm packages have, by those names, of the versions the page is written
// for: Swagger UI 5, and Redoc 3, whose bundle is an ES module. They are
// loaded without integrity hashes, since their content is not known.
func WithDocumentAssets(baseURL string) Option {
	return func(c *Config) { c.DocumentAssets = baseURL }
}

// documentPage returns the handler of the documentation page of doc,
// loaded from docPath. An unknown UI is a mistake of the program, which it
// panics for, as http.ServeMux does for a pattern it cannot route.
func (c *Config) documentPage(doc []byte, docPath string) http.Handler {
	ui, ok := pageUIs[c.DocumentUI]
	if !ok {
		panic(fmt.Sprintf("server.WithDocumentPage: unknown UI %d; use server.SwaggerUI or server.Redoc", c.DocumentUI))
	}
	var info struct {
		Info struct {
			Title string `json:"title"`
		} `json:"info"`
		Servers []struct {
			URL string `json:"url"`
		} `json:"servers"`
	}
	// A document that does not decode still gets a page, which says why.
	_ = json.Unmarshal(doc, &info)
	title := strings.TrimSpace(info.Info.Title)
	if title == "" {
		title = "API documentation"
	}
	type asset struct {
		URL, Integrity string
		Style, Module  bool
	}
	var assets []asset
	var scripts, styles []string
	var module string
	for _, a := range ui.assets {
		u, integrity := ui.cdn+a.file, a.integrity
		if c.DocumentAssets != "" {
			u, integrity = strings.TrimSuffix(c.DocumentAssets, "/")+"/"+a.file, ""
		}
		assets = append(assets, asset{URL: u, Integrity: integrity, Style: a.style, Module: a.module})
		if a.module {
			module = u
		}
		if a.style {
			styles = append(styles, u)
		} else {
			scripts = append(scripts, u)
		}
	}
	// Swagger UI sends requests to the servers of the document; relative
	// ones are the page's own origin.
	connect := []string{"'self'"}
	for _, s := range info.Servers {
		u, err := url.Parse(s.URL)
		if err != nil || u.Scheme == "" || u.Host == "" || strings.ContainsAny(u.Host, "{}") {
			continue
		}
		if origin := u.Scheme + "://" + u.Host; !slices.Contains(connect, origin) {
			connect = append(connect, origin)
		}
	}
	// The templates are constants, which parse.
	page := template.Must(template.New("page").Parse(pageHead + ui.body + "\n</body>\n</html>\n"))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonce, err := pageNonce()
		if err != nil {
			c.HandleError(w, r, err)
			return
		}
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Content-Security-Policy", strings.Join([]string{
			"default-src 'none'",
			strings.Join(append([]string{"script-src", "'nonce-" + nonce + "'"}, scripts...), " "),
			// The renderers set styles inline as they draw.
			strings.Join(append([]string{"style-src", "'unsafe-inline'"}, styles...), " "),
			"img-src 'self' data: https:",
			"font-src 'self' data:",
			"connect-src " + strings.Join(connect, " "),
			"worker-src blob:",
			"base-uri 'none'",
			"form-action 'none'",
			"frame-ancestors 'none'",
		}, "; "))
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		// The nonce is new for every response.
		h.Set("Cache-Control", "no-store")
		data := map[string]any{
			"Title":  title,
			"Assets": assets,
			"Nonce":  nonce,
			"Module": module,
			"Config": map[string]string{"url": docPath},
		}
		// The status line is sent; a failed write means the client is gone.
		_ = page.Execute(w, data)
	})
}

// pageNonce returns a new nonce for the page's own script.
func pageNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
