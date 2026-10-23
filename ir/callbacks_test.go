package ir

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestWebhooksAndCallbacks(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
security: [{key: []}]
paths:
  /subscriptions/{tenant}:
    post:
      operationId: subscribe
      parameters:
        - {name: tenant, in: path, required: true, schema: {type: string}}
        - {name: cb, in: query, schema: {type: string}}
        - {name: X-Hook, in: header, schema: {type: string}}
      requestBody:
        content:
          application/json:
            schema: {type: object, properties: {url: {type: string}}}
      responses: {"201": {description: ok}}
      callbacks:
        onEvent:
          "{$request.body#/url}/events/{$method}":
            post:
              requestBody:
                content: {application/json: {schema: {type: object, properties: {id: {type: integer}}}}}
              responses: {"204": {description: ok}}
            delete:
              responses: {"204": {description: ok}}
          "$request.query.cb":
            put:
              operationId: replay
              responses: {"204": {description: ok}}
        audit:
          "https://{$request.path.tenant}.example.com/{$request.header.x-hook}?s={$response.body#/id}":
            post:
              responses: {"204": {description: ok}}
          "{$request.query.missing}":
            post:
              parameters:
                - {name: id, in: path, required: true, schema: {type: string}}
              responses: {"204": {description: ok}}
webhooks:
  newPet:
    post:
      security: [{key: []}]
      requestBody:
        content: {application/json: {schema: {$ref: "#/components/schemas/Pet"}}}
      responses: {"200": {description: ok}}
  petGone:
    post:
      operationId: subscribe
      responses: {"204": {description: ok}}
components:
  schemas:
    Pet: {type: object, properties: {name: {type: string}}}
  securitySchemes:
    key: {type: apiKey, in: header, name: X-Key}
`, Options{Operations: true})

	var hooks []string
	for _, o := range m.Webhooks {
		hooks = append(hooks, fmt.Sprintf("%s %s %s %s %v", o.Webhook, o.Method, o.Name, o.RequestType, o.Security))
	}
	// The second subscribe is taken by the operation, but the suffix sets
	// it apart; webhooks are not secured by the document's requirements.
	want := []string{"newPet POST NewPetWebhook NewPetWebhookRequest []", "petGone POST SubscribeWebhook SubscribeWebhookRequest []"}
	if !slices.Equal(hooks, want) {
		t.Errorf("webhooks:\n%q\nwant\n%q", hooks, want)
	}
	if b := m.Webhooks[0].Body; b == nil || b.Value.Type == nil || b.Value.Type.Name != "Pet" {
		t.Errorf("webhook body = %+v", b)
	}

	sub := operationNamed(t, m, "Subscribe")
	var got []string
	for _, c := range sub.Callbacks {
		var parts []string
		for _, p := range c.Callback.Parts {
			switch {
			case p.Body:
				parts = append(parts, "body"+p.Pointer)
			case p.Param != nil:
				parts = append(parts, p.Param.In+":"+p.Param.Name)
			default:
				parts = append(parts, fmt.Sprintf("%q", p.Text))
			}
		}
		got = append(got, fmt.Sprintf("%s %s %s %t [%s] %q %q", c.Callback.Name, c.Method, c.Name, c.Kind == Callback, strings.Join(parts, " "), c.Callback.Unsupported, c.Unsupported))
	}
	want = []string{
		`onEvent POST SubscribeOnEventPostCallback true [body/url "/events/POST"] "" ""`,
		`onEvent DELETE SubscribeOnEventDeleteCallback true [body/url "/events/POST"] "" ""`,
		`onEvent PUT ReplayCallback true [query:cb] "" ""`,
		`audit POST SubscribeAuditCallback true [] "$response.body#/id is not part of the request type" ""`,
		`audit POST SubscribeAuditCallback2 true [] "$request.query.missing refers to a query parameter the operation does not declare" "a webhook or callback has no path parameters"`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("callbacks:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if c := sub.Callbacks[0]; c.Path != "{$request.body#/url}/events/{$method}" || c.Pattern != "" || c.Body == nil {
		t.Errorf("callback = %+v", c)
	}

	var warnings []string
	for _, w := range m.Warnings {
		warnings = append(warnings, w.Location.Pointer+": "+w.Message)
	}
	for _, w := range []string{
		"/webhooks/newPet/post/security: security requirements of webhooks and callbacks are not applied; add credentials or signatures with client middleware",
		`/paths/~1subscriptions~1{tenant}/post/callbacks/audit/https:~1~1{$request.path.tenant}.example.com~1{$request.header.x-hook}?s={$response.body#~1id}/post: the URL "https://{$request.path.tenant}.example.com/{$request.header.x-hook}?s={$response.body#/id}" of callback "audit": $response.body#/id is not part of the request type; it is not computed, so the sender passes it`,
	} {
		if !slices.Contains(warnings, w) {
			t.Errorf("missing warning %s\nhave:\n%s", w, strings.Join(warnings, "\n"))
		}
	}
}

func TestCallbackURL(t *testing.T) {
	parent := &Operation{Method: "POST", Body: &Body{}, Params: []*Param{
		{In: "query", Name: "cb"}, {In: "header", Name: "X-Token"}, {In: "path", Name: "id"},
	}}
	tests := []struct {
		key, want, why string
	}{
		{"https://example.com/hooks", `"https://example.com/hooks"`, ""},
		{"{$request.query.cb}", "query:cb", ""},
		{"$request.query.cb", "query:cb", ""},
		{"{$request.query.cb}/a/{$request.path.id}?m={$method}", `query:cb "/a/" path:id "?m=POST"`, ""},
		{"{$request.header.x-token}", "header:X-Token", ""},
		{"{$request.body}", "body", ""},
		{"{$request.body#/a~1b%20c}", "body/a~1b c", ""},
		{"{$request.body#a}", "", "$request.body#a does not end in a JSON pointer"},
		{"{$request.query.cb", "", "an expression is not closed with }"},
		{"{$url}/x", "", "$url is not part of the request type"},
		{"{$statusCode}", "", "$statusCode is not part of the request type"},
		{"{$request.cookie.c}", "", `"$request.cookie.c" is not a runtime expression`},
		{"{$foo}", "", `"$foo" is not a runtime expression`},
		{"{$request.header.Other}", "", "$request.header.Other refers to a header parameter the operation does not declare"},
	}
	for _, tc := range tests {
		parts, why := callbackURL(tc.key, parent)
		var got []string
		for _, p := range parts {
			switch {
			case p.Body:
				got = append(got, "body"+p.Pointer)
			case p.Param != nil:
				got = append(got, p.Param.In+":"+p.Param.Name)
			default:
				got = append(got, fmt.Sprintf("%q", p.Text))
			}
		}
		if strings.Join(got, " ") != tc.want || why != tc.why {
			t.Errorf("%s: %s %q, want %s %q", tc.key, strings.Join(got, " "), why, tc.want, tc.why)
		}
	}
	if _, why := callbackURL("{$request.body}", &Operation{}); why != "$request.body refers to the body, which the operation does not have" {
		t.Errorf("no body: %q", why)
	}
}
