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
      security: [{hook: []}, {}]
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
    hook: {type: http, scheme: bearer}
`, Options{Operations: true})

	var hooks []string
	for _, o := range m.Webhooks {
		var reqs []string
		for _, req := range o.Security {
			var names []string
			for _, u := range req {
				names = append(names, u.Scheme.Name)
			}
			reqs = append(reqs, "{"+strings.Join(names, " ")+"}")
		}
		hooks = append(hooks, fmt.Sprintf("%s %s %s %s %v", o.Webhook, o.Method, o.Name, o.RequestType, reqs))
	}
	// The second subscribe is taken by the operation, but the suffix sets
	// it apart; webhooks have their own requirements, not the document's.
	want := []string{"newPet POST NewPetWebhook NewPetWebhookRequest [{hook} {}]", "petGone POST SubscribeWebhook SubscribeWebhookRequest []"}
	if !slices.Equal(hooks, want) {
		t.Errorf("webhooks:\n%q\nwant\n%q", hooks, want)
	}
	// The API sends credentials of the webhooks' schemes, and checks those
	// of the operations'.
	if len(m.SenderSchemes) != 1 || m.SenderSchemes[0].Method != "Hook" || len(m.SecuritySchemes) != 1 || m.SecuritySchemes[0].Name != "key" {
		t.Errorf("sender schemes %+v, schemes %+v", m.SenderSchemes, m.SecuritySchemes)
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

func TestTypedEvents(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
paths:
  /feed:
    get:
      operationId: feed
      responses:
        "200":
          description: ok
          content:
            text/event-stream:
              itemSchema:
                oneOf:
                  - properties:
                      event: {const: added}
                      data: {type: string, contentMediaType: application/json, contentSchema: {$ref: "#/components/schemas/Pet"}}
                  - properties:
                      event: {enum: [gone]}
                      data: {contentMediaType: application/json, contentSchema: {type: object, properties: {id: {type: integer}}}}
                  - properties:
                      event: {const: ping}
                      data: {type: string, maxLength: 4}
  /one:
    post:
      operationId: one
      requestBody:
        content:
          text/event-stream:
            itemSchema:
              properties:
                data: {type: string, contentMediaType: application/json, contentSchema: {$ref: "#/components/schemas/Pet"}}
      responses: {"204": {description: ok}}
  /raw:
    get:
      operationId: raw
      responses:
        "200":
          description: ok
          content:
            text/event-stream:
              itemSchema:
                properties:
                  data: {type: string}
  /bad:
    get:
      operationId: bad
      responses:
        "200":
          description: ok
          content:
            text/event-stream:
              itemSchema:
                oneOf:
                  - properties: {event: {const: a}, data: {type: string}}
                  - properties: {event: {const: a}, data: {type: string}}
components:
  schemas:
    Pet: {type: object, properties: {name: {type: string}}}
`, Options{Operations: true})

	feed := operationNamed(t, m, "Feed").Responses[0]
	if feed.Events == nil || feed.Item == nil || feed.Item.Kind != Event {
		t.Fatalf("feed events = %+v, item %+v", feed.Events, feed.Item)
	}
	var kinds []string
	for _, k := range feed.Events.Kinds {
		typ := k.Variant.Type.Name
		if k.Variant.Type.Kind == Named {
			typ = k.Variant.Type.Type.Name
		}
		kinds = append(kinds, fmt.Sprintf("%s:%s:%t", k.Name, typ, k.Text))
	}
	// Inline data is named after its kind; a kind without a contentSchema
	// is text.
	if want := "added:Pet:false gone:Feed200EventGoneData:false ping:string:true"; strings.Join(kinds, " ") != want || feed.Events.Data.Type.Name != "Feed200EventData" {
		t.Errorf("kinds %q (union %s), want %q", kinds, feed.Events.Data.Type.Name, want)
	}

	one := operationNamed(t, m, "One").Body
	if ev := one.Media[0].Events; ev == nil || len(ev.Kinds) != 0 || ev.Data.Type.Name != "Pet" || one.Value.Kind != Stream || one.Value.Elem.Kind != Event {
		t.Errorf("one kind: %+v, field %+v", one.Media[0].Events, one.Value)
	}
	// Without a JSON contentSchema, events are raw, without a warning; two
	// kinds of one name are warned about.
	if raw := operationNamed(t, m, "Raw").Responses[0]; raw.Events != nil || raw.Item != nil {
		t.Errorf("raw events = %+v", raw.Events)
	}
	if bad := operationNamed(t, m, "Bad").Responses[0]; bad.Events != nil {
		t.Errorf("bad events = %+v", bad.Events)
	}
	var warnings []string
	for _, w := range m.Warnings {
		warnings = append(warnings, w.Message)
	}
	if all := strings.Join(warnings, "\n"); !strings.Contains(all, `the event "a" has two kinds`) || strings.Contains(all, "/raw") {
		t.Errorf("warnings:\n%s", all)
	}
}
