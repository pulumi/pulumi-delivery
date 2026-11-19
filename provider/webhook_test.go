// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
)

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
	signing := schema.Types["delivery:index:WebhookSigning"]
	assert.Equal(t, []string{"kind", "secret"}, signing.Required)
	assert.JSONEq(t, `{"type":"string","secret":true}`, stripDescription(t, signing.Properties["secret"]))
}
