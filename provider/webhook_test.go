// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jmespath/go-jmespath"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
)

// linearPost is a post of the issue webhook of Linear, with the fields that the map and the when read.
func linearPost(t *testing.T, action string, edits map[string]any) map[string]any {
	t.Helper()
	post := map[string]any{
		"action":    action,
		"type":      "Issue",
		"createdAt": "2026-03-01T10:00:00.000Z",
		"actor":     map[string]any{"id": "u1", "name": "Ada", "type": "user"},
		"data": map[string]any{
			"id":          "9f1c",
			"identifier":  "ENG-42",
			"title":       "Fix the login page",
			"description": "The button is off by one pixel.",
			"url":         "https://linear.app/acme/issue/ENG-42",
			"updatedAt":   "2026-03-02T11:00:00.000Z",
			"state":       map[string]any{"name": "Todo"},
			"labels":      []any{map[string]any{"name": "neon"}, map[string]any{"name": "bug"}},
			"team":        map[string]any{"key": "ENG"},
		},
	}
	for key, value := range edits {
		if strings.HasPrefix(key, "data.") {
			data := post["data"].(map[string]any)
			if value == nil {
				delete(data, strings.TrimPrefix(key, "data."))
			} else {
				data[strings.TrimPrefix(key, "data.")] = value
			}
			continue
		}
		if value == nil {
			delete(post, key)
		} else {
			post[key] = value
		}
	}
	return post
}

func evaluate(t *testing.T, expression string, post map[string]any) any {
	t.Helper()
	// The service evaluates the expressions over the decoded JSON body.
	encoded, err := json.Marshal(post)
	require.NoError(t, err)
	var decoded any
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	result, err := jmespath.Search(expression, decoded)
	require.NoError(t, err, expression)
	return result
}

func TestLinearMapYieldsTheSubjectVersionAndData(t *testing.T) {
	result := evaluate(t, linearMap, linearPost(t, "update", nil))
	assert.Equal(t, map[string]any{
		"subject": "9f1c",
		"version": "2026-03-02T11:00:00.000Z",
		"data": map[string]any{
			"identifier":  "ENG-42",
			"title":       "Fix the login page",
			"description": "The button is off by one pixel.",
			"url":         "https://linear.app/acme/issue/ENG-42",
			"state":       "Todo",
			"labels":      []any{"neon", "bug"},
			"team":        "ENG",
			"action":      "update",
		},
	}, result)

	// The version falls back to createdAt, and missing labels map to an empty list.
	result = evaluate(t, linearMap, linearPost(t, "create", map[string]any{"data.updatedAt": nil, "data.labels": nil}))
	assert.Equal(t, "2026-03-01T10:00:00.000Z", result.(map[string]any)["version"])
	assert.Equal(t, []any{}, result.(map[string]any)["data"].(map[string]any)["labels"])
}

func TestLinearWhenBuildsTheExpression(t *testing.T) {
	assert.Equal(t, "type == 'Issue' && contains(`[\"create\",\"update\"]`, action) && actor.type == 'user' && "+
		"contains(data.labels[].name || `[]`, 'neon') && contains(`[\"Todo\",\"In Progress\"]`, data.state.name)",
		linearWhen([]string{"neon"}, []string{"Todo", "In Progress"}, true))
	assert.Equal(t, "type == 'Issue' && contains(`[\"create\",\"update\"]`, action)", linearWhen(nil, nil, false))
	assert.Equal(t, "type == 'Issue' && contains(`[\"create\",\"update\"]`, action) && "+
		"contains(data.labels[].name || `[]`, 'a') && contains(data.labels[].name || `[]`, 'b')",
		linearWhen([]string{"a", "b"}, nil, false))

	// Names are quoted for JMESPath: a quote in a label, and a backtick in a state.
	assert.Equal(t, `'it\'s'`, jmesString("it's"))
	assert.Equal(t, "`[\"a\\`b\"]`", jmesList([]string{"a`b"}))
	assert.Contains(t, linearWhen([]string{"it's"}, []string{"a`b"}, false), `'it\'s'`)
	assert.Equal(t, "it's", evaluate(t, jmesString("it's"), nil))
	assert.Equal(t, []any{"a`b"}, evaluate(t, jmesList([]string{"a`b"}), nil))
}

func TestLinearWhenMatchesIssuePosts(t *testing.T) {
	when := linearWhen([]string{"neon"}, []string{"Todo", "In Progress"}, true)
	truthy := func(post map[string]any) bool {
		result := evaluate(t, when, post)
		b, ok := result.(bool)
		return ok && b
	}

	assert.True(t, truthy(linearPost(t, "create", nil)))
	assert.True(t, truthy(linearPost(t, "update", map[string]any{"data.state": map[string]any{"name": "In Progress"}})))

	for name, post := range map[string]map[string]any{
		"remove":        linearPost(t, "remove", nil),
		"a comment":     linearPost(t, "create", map[string]any{"type": "Comment"}),
		"missing label": linearPost(t, "create", map[string]any{"data.labels": []any{map[string]any{"name": "bug"}}}),
		"no labels":     linearPost(t, "create", map[string]any{"data.labels": nil}),
		"wrong state":   linearPost(t, "create", map[string]any{"data.state": map[string]any{"name": "Done"}}),
		"an app actor":  linearPost(t, "create", map[string]any{"actor": map[string]any{"id": "a1", "type": "app"}}),
		"no actor":      linearPost(t, "create", map[string]any{"actor": nil}),
	} {
		assert.False(t, truthy(post), name)
	}

	// Without ignoreBots an app may make the change, and without states any state passes.
	assert.True(t, evaluate(t, linearWhen([]string{"neon"}, nil, false),
		linearPost(t, "update", map[string]any{"actor": nil, "data.state": map[string]any{"name": "Done"}})).(bool))
}

func webhookRequest(dryRun bool) p.ConstructRequest {
	return p.ConstructRequest{
		Urn:          urnOf(typeWebhookSource, "orders"),
		Organization: "acme",
		DryRun:       dryRun,
		Inputs: property.NewMap(map[string]property.Value{
			"signing": property.New(property.NewMap(map[string]property.Value{
				"kind":   property.New("hmac-sha256"),
				"secret": property.New("s3cret").WithSecret(true),
			})),
			"map":     property.New("{subject: order.id, version: order.updatedAt, data: order}"),
			"when":    property.New("event == 'order.created'"),
			"trigger": property.New(true),
		}),
	}
}

const webhookValueBody = `{"value":{"subject":"o-1","version":"2026-03-02T11:00:00.000Z","data":{"id":"o-1","total":12.5,"items":["a","b"]}}}`

func TestWebhookSourceIsDeclaredAndKnowsItsURL(t *testing.T) {
	service := newFakeService(t)
	server := newServer(t)

	// A registration declares the source. The ref and the url are known, the rest waits for a run.
	resp, err := server.Construct(webhookRequest(false))
	require.NoError(t, err)
	declarations := service.declared()
	require.Len(t, declarations, 1)
	assert.Equal(t, typeWebhookSource, declarations[0]["type"])
	inputs := declarations[0]["inputs"].(map[string]any)
	assert.Equal(t, "event == 'order.created'", inputs["when"])
	assert.Equal(t, true, inputs["trigger"])
	assert.Equal(t, "hmac-sha256", inputs["signing"].(map[string]any)["kind"])
	assert.Equal(t, "webhook:orders", resp.State.Get("ref").AsString())
	assert.Equal(t, service.URL+"/api/preview/delivery/webhooks/acme/project/stack/orders", resp.State.Get("url").AsString())
	for _, name := range []string{"subject", "version", "data"} {
		assert.True(t, resp.State.Get(name).IsComputed(), name)
	}
	assert.Empty(t, service.valueRequests)

	// A preview sends nothing. The url needs no credential.
	service.requests = nil
	t.Setenv("PULUMI_ACCESS_TOKEN", "")
	resp, err = server.Construct(webhookRequest(true))
	require.NoError(t, err)
	assert.Empty(t, service.requests)
	assert.Equal(t, "webhook:orders", resp.State.Get("ref").AsString())
	assert.Equal(t, service.URL+"/api/preview/delivery/webhooks/acme/project/stack/orders", resp.State.Get("url").AsString())
	assert.True(t, resp.State.Get("subject").IsComputed())

	// Without PULUMI_API, and on a run stack, the preview does not know the url.
	t.Setenv("PULUMI_API", "")
	resp, err = server.Construct(webhookRequest(true))
	require.NoError(t, err)
	assert.True(t, resp.State.Get("url").IsComputed())
	t.Setenv("PULUMI_API", service.URL)
	request := webhookRequest(true)
	request.Urn = resource.URN("urn:pulumi:stack-r12::project::" + typeWebhookSource + "::orders")
	resp, err = server.Construct(request)
	require.NoError(t, err)
	assert.True(t, resp.State.Get("url").IsComputed())
	assert.Equal(t, "webhook:orders", resp.State.Get("ref").AsString())
}

func TestWebhookSourceInARunReadsTheValue(t *testing.T) {
	service := newFakeService(t)
	service.run = true
	service.webhookAnswer = &memberAnswer{http.StatusOK, webhookValueBody}

	// The run stack is stack-r1: the url names the pipeline stack.
	request := webhookRequest(false)
	request.Urn = resource.URN("urn:pulumi:stack-r1::project::" + typeWebhookSource + "::orders")
	resp, err := newServer(t).Construct(request)
	require.NoError(t, err)

	assert.Equal(t, service.URL+"/api/preview/delivery/webhooks/acme/project/stack/orders", resp.State.Get("url").AsString())
	assert.Equal(t, "o-1", resp.State.Get("subject").AsString())
	assert.Equal(t, "2026-03-02T11:00:00.000Z", resp.State.Get("version").AsString())
	data := resp.State.Get("data").AsMap()
	assert.Equal(t, "o-1", data.Get("id").AsString())
	assert.EqualValues(t, 12.5, data.Get("total").AsNumber())
	assert.Equal(t, 2, data.Get("items").AsArray().Len())
	require.Len(t, service.valueRequests, 1)
	assert.Equal(t, "webhook", service.valueRequests[0]["kind"])
	assert.Equal(t, map[string]any{"name": "orders"}, service.valueRequests[0]["identity"])
}

func TestWebhookSourceFailsWithTheServiceMessage(t *testing.T) {
	service := newFakeService(t)
	service.run = true
	service.webhookAnswer = &memberAnswer{http.StatusBadRequest, `{"code":400,"message":"The webhook orders is not a source of this run."}`}

	_, err := newServer(t).Construct(webhookRequest(false))
	require.ErrorContains(t, err, "The webhook orders is not a source of this run.")
}

func TestWebhookSourceValidation(t *testing.T) {
	newFakeService(t)
	server := newServer(t)
	with := func(name string, value property.Value) p.ConstructRequest {
		request := webhookRequest(true)
		request.Inputs = request.Inputs.Set(name, value)
		return request
	}
	signing := func(fields map[string]property.Value) property.Value { return property.New(property.NewMap(fields)) }

	for message, request := range map[string]p.ConstructRequest{
		"map is required": with("map", property.New("")),
		"signing must be an object with kind and secret":               with("signing", property.New("hmac")),
		"signing.kind is required":                                     with("signing", signing(map[string]property.Value{"secret": property.New("x")})),
		"signing.kind must be one of [hmac-sha256 token], not \"md5\"": with("signing", signing(map[string]property.Value{"kind": property.New("md5"), "secret": property.New("x")})),
		"signing.secret is required":                                   with("signing", signing(map[string]property.Value{"kind": property.New("token")})),
	} {
		_, err := server.Construct(request)
		require.EqualError(t, err, message)
	}
	for name, request := range map[string]p.ConstructRequest{
		"unknown map":     with("map", property.New(property.Computed)),
		"unknown signing": with("signing", property.New(property.Computed)),
		"unknown kind":    with("signing", signing(map[string]property.Value{"kind": property.New(property.Computed), "secret": property.New("x")})),
		"unknown secret":  with("signing", signing(map[string]property.Value{"kind": property.New("token"), "secret": property.New(property.Computed)})),
		"no signing":      with("signing", property.New(property.Null)),
		"trigger false":   with("trigger", property.New(false)),
	} {
		_, err := server.Construct(request)
		require.NoError(t, err, name)
	}

	// A webhook source is not a checkout source.
	job := jobRequest(true)
	job.Inputs = job.Inputs.Set("source", reference(typeWebhookSource, "orders"))
	_, err := server.Construct(job)
	require.ErrorContains(t, err, "source must be one of")
	require.ErrorContains(t, err, "not "+typeWebhookSource)
}

func linearRequest(dryRun bool) p.ConstructRequest {
	return p.ConstructRequest{
		Urn:          urnOf(typeLinearIssueSource, "issues"),
		Organization: "acme",
		DryRun:       dryRun,
		Inputs: property.NewMap(map[string]property.Value{
			"signingSecret": property.New("s3cret").WithSecret(true),
			"labels":        property.New(property.NewArray([]property.Value{property.New("neon")})),
			"states":        property.New(property.NewArray([]property.Value{property.New("Todo"), property.New("In Progress")})),
			"ignoreBots":    property.New(true),
		}),
	}
}

func TestLinearIssueSourceRegistersAWebhookSourceChild(t *testing.T) {
	service := newFakeService(t)
	var children []property.Map

	resp, err := newServerCapturingType(t, typeWebhookSource, &children).Construct(linearRequest(false))
	require.NoError(t, err)

	// The component itself is not declared: its child is, when the engine constructs it.
	assert.Empty(t, service.declared())
	require.Len(t, children, 1)
	child := children[0]
	assert.Equal(t, "hmac-sha256", child.Get("signing").AsMap().Get("kind").AsString())
	assert.Equal(t, "s3cret", child.Get("signing").AsMap().Get("secret").AsString())
	assert.True(t, child.Get("signing").AsMap().Get("secret").Secret() || child.Get("signing").Secret())
	assert.Equal(t, linearMap, child.Get("map").AsString())
	assert.Equal(t, linearWhen([]string{"neon"}, []string{"Todo", "In Progress"}, true), child.Get("when").AsString())
	assert.True(t, child.Get("trigger").AsBool())

	// The outputs are those of the webhook named like the component.
	assert.Equal(t, "webhook:issues", resp.State.Get("ref").AsString())
	assert.Equal(t, service.URL+"/api/preview/delivery/webhooks/acme/project/stack/issues", resp.State.Get("url").AsString())
	assert.True(t, resp.State.Get("subject").IsComputed())
}

func TestLinearIssueSourceDefaultsAndUnknownLists(t *testing.T) {
	newFakeService(t)
	var children []property.Map
	server := newServerCapturingType(t, typeWebhookSource, &children)

	// Without labels, states and ignoreBots (the SDK applies the default), bots are ignored and nothing else is checked.
	request := linearRequest(true)
	request.Inputs = property.NewMap(map[string]property.Value{"signingSecret": property.New("s3cret")})
	_, err := server.Construct(request)
	require.NoError(t, err)
	require.Len(t, children, 1)
	assert.Equal(t, linearWhen(nil, nil, true), children[0].Get("when").AsString())

	// An unknown list in a preview does not break the child: its when is unknown.
	request = linearRequest(true)
	request.Inputs = request.Inputs.Set("labels", property.New(property.Computed))
	_, err = server.Construct(request)
	require.NoError(t, err)
	require.Len(t, children, 2)
	assert.True(t, children[1].Get("when").IsComputed())

	request = linearRequest(true)
	request.Inputs = request.Inputs.Set("ignoreBots", property.New(false))
	_, err = server.Construct(request)
	require.NoError(t, err)
	assert.Equal(t, linearWhen([]string{"neon"}, []string{"Todo", "In Progress"}, false), children[2].Get("when").AsString())
}

func TestLinearIssueSourceInARunReadsTheValue(t *testing.T) {
	service := newFakeService(t)
	service.run = true
	service.webhookAnswer = &memberAnswer{http.StatusOK,
		`{"value":{"subject":"9f1c","version":"2026-03-02T11:00:00.000Z","data":{"identifier":"ENG-42","labels":["neon"]}}}`}

	resp, err := newServer(t).Construct(linearRequest(false))
	require.NoError(t, err)
	assert.Equal(t, "9f1c", resp.State.Get("subject").AsString())
	assert.Equal(t, "ENG-42", resp.State.Get("data").AsMap().Get("identifier").AsString())
	assert.Equal(t, map[string]any{"name": "issues"}, service.valueRequests[0]["identity"])
}

func TestLinearIssueSourceValidation(t *testing.T) {
	newFakeService(t)
	server := newServer(t)
	with := func(name string, value property.Value) p.ConstructRequest {
		request := linearRequest(true)
		request.Inputs = request.Inputs.Set(name, value)
		return request
	}

	for message, request := range map[string]p.ConstructRequest{
		"signingSecret is required":      with("signingSecret", property.New("")),
		"labels must be a list of names": with("labels", property.New("neon")),
		"states must be a list of names": with("states", property.New(property.NewArray([]property.Value{property.New(1.0)}))),
	} {
		_, err := server.Construct(request)
		require.EqualError(t, err, message)
	}
	for name, request := range map[string]p.ConstructRequest{
		"unknown secret": with("signingSecret", property.New(property.Computed)),
		"unknown labels": with("labels", property.New(property.Computed)),
		"unknown state":  with("states", property.New(property.NewArray([]property.Value{property.New(property.Computed)}))),
	} {
		_, err := server.Construct(request)
		require.NoError(t, err, name)
	}

	// A Linear issue source is not a checkout source either.
	job := jobRequest(true)
	job.Inputs = job.Inputs.Set("source", reference(typeLinearIssueSource, "issues"))
	_, err := server.Construct(job)
	require.ErrorContains(t, err, "not "+typeLinearIssueSource)
}

func TestSchemaOfTheWebhookSources(t *testing.T) {
	resp, err := newServer(t).GetSchema(p.GetSchemaRequest{})
	require.NoError(t, err)
	var schema struct {
		Resources map[string]struct {
			Inputs   map[string]json.RawMessage `json:"inputProperties"`
			Required []string                   `json:"requiredInputs"`
			Outputs  []string                   `json:"required"`
		} `json:"resources"`
		Types map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		} `json:"types"`
	}
	require.NoError(t, json.Unmarshal([]byte(resp.Schema), &schema))
	outputs := []string{"ref", "url", "subject", "version", "data"}
	webhook := schema.Resources[typeWebhookSource]
	assert.Equal(t, []string{"map"}, webhook.Required)
	assert.Equal(t, outputs, webhook.Outputs)
	assert.JSONEq(t, `{"type":"boolean","default":true}`, stripDescription(t, webhook.Inputs["trigger"]))
	assert.JSONEq(t, `{"$ref":"#/types/delivery:index:WebhookSigning"}`, stripDescription(t, webhook.Inputs["signing"]))
	linear := schema.Resources[typeLinearIssueSource]
	assert.Equal(t, []string{"signingSecret"}, linear.Required)
	assert.Equal(t, outputs, linear.Outputs)
	assert.JSONEq(t, `{"type":"string","secret":true}`, stripDescription(t, linear.Inputs["signingSecret"]))
	assert.JSONEq(t, `{"type":"boolean","default":true}`, stripDescription(t, linear.Inputs["ignoreBots"]))
	signing := schema.Types["delivery:index:WebhookSigning"]
	assert.Equal(t, []string{"kind", "secret"}, signing.Required)
	assert.JSONEq(t, `{"type":"string","secret":true}`, stripDescription(t, signing.Properties["secret"]))
}
