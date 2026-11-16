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

func agentRequest(dryRun bool) p.ConstructRequest {
	return p.ConstructRequest{
		Urn:          urnOf(typeAgent, "fix"),
		Organization: "org",
		DryRun:       dryRun,
		Inputs: property.NewMap(map[string]property.Value{
			"stage":             reference(typeStage, "implement"),
			"agentDefinitionId": property.New("11111111-2222-3333-4444-555555555555"),
			// The prompt is built from the outputs of another member, so it is unknown in the first pass.
			"prompt": property.New(property.Computed).WithDependencies([]resource.URN{urnOf("delivery:index:Job", "other")}),
			"responseSchema": property.New(property.NewMap(map[string]property.Value{
				"type": property.New("object"),
				"properties": property.New(property.NewMap(map[string]property.Value{
					"pullRequest": property.New(property.NewMap(map[string]property.Value{"type": property.New("integer")})),
				})),
			})),
		}),
	}
}

func TestAgentIsDeclaredWithItsRawInputs(t *testing.T) {
	service := newFakeService(t)
	var children []property.Map

	_, err := newServerCapturing(t, &children).Construct(agentRequest(false))
	require.NoError(t, err)

	declarations := service.declared()
	require.Len(t, declarations, 1)
	d := declarations[0]
	assert.Equal(t, typeAgent, d["type"])
	assert.Equal(t, string(urnOf(typeAgent, "fix")), d["urn"])
	assert.Equal(t, map[string]any{"prompt": []any{string(urnOf("delivery:index:Job", "other"))}}, d["propertyDependencies"])
	inputs := d["inputs"].(map[string]any)
	assert.Equal(t, "11111111-2222-3333-4444-555555555555", inputs["agentDefinitionId"])
	assert.Equal(t, map[string]any{"type": "object", "properties": map[string]any{"pullRequest": map[string]any{"type": "integer"}}}, inputs["responseSchema"])
	// The prompt is forwarded as the unknown marker.
	assert.Contains(t, inputs, "prompt")
	assert.NotEqual(t, "", inputs["prompt"])

	// The child carries the same declaration, like the child of a Job.
	require.Len(t, children, 1)
	declaration := children[0].Get("declaration").AsMap()
	assert.Equal(t, typeAgent, declaration.Get("type").AsString())
	assert.Equal(t, "11111111-2222-3333-4444-555555555555",
		declaration.Get("inputs").AsMap().Get("agentDefinitionId").AsString())
	assert.True(t, declaration.Get("inputs").AsMap().Get("prompt").IsComputed())
}

func TestAgentOutputsDependOnItsChildAndAreUnknownWhileItWaits(t *testing.T) {
	newFakeService(t)
	server := newServer(t)

	for _, dryRun := range []bool{false, true} {
		resp, err := server.Construct(agentRequest(dryRun))
		require.NoError(t, err)

		child := urnOf(typeAgent+"$"+typeMemberRun, "fix")
		_, hasChildURN := resp.State.GetOk("childUrn")
		assert.False(t, hasChildURN, "the child URN is not a public output")
		for _, name := range []string{"response", "taskId"} {
			assert.Contains(t, resp.State.Get(name).Dependencies(), child, "%s, dry run %v", name, dryRun)
		}
	}

	// The SDK leaves an unknown output out of the state: both outputs are then unknown and depend on the child.
	child := urnOf(typeAgent+"$"+typeMemberRun, "fix")
	finished := finishState(typeAgent, property.NewMap(map[string]property.Value{"childUrn": property.New(string(child))}),
		"", nil, nil, nil, property.Map{})
	for _, name := range []string{"response", "taskId"} {
		assert.True(t, finished.Get(name).IsComputed(), name)
		assert.Equal(t, []resource.URN{child}, finished.Get(name).Dependencies(), name)
	}
	_, hasChildURN := finished.GetOk("childUrn")
	assert.False(t, hasChildURN)
}

func TestAgentChildInARunSucceedsWithTheResponseAndTheTaskId(t *testing.T) {
	service := newFakeService(t)
	service.run = true
	service.memberAnswers = map[string]memberAnswer{"": {http.StatusOK,
		`{"status":"succeeded","outputs":{"response":{"pullRequest":42},"taskId":"task-7"}}`}}

	request := memberRunRequest(false)
	request.Properties = request.Properties.Set("member", property.New(string(urnOf(typeAgent, "fix"))))
	resp, err := newServer(t).Create(request)
	require.NoError(t, err)

	outputs := resp.Properties.Get("outputs").AsMap()
	assert.Equal(t, "task-7", outputs.Get("taskId").AsString())
	assert.EqualValues(t, 42, outputs.Get("response").AsMap().Get("pullRequest").AsNumber())
	start := service.memberStarts[0]
	assert.Equal(t, string(urnOf(typeAgent, "fix")), start["urn"])
	assert.Equal(t, "", start["entry"])
}

func TestAgentValidation(t *testing.T) {
	newFakeService(t)
	server := newServer(t)
	with := func(name string, value property.Value) p.ConstructRequest {
		request := agentRequest(true)
		request.Inputs = request.Inputs.Set(name, value)
		return request
	}
	without := func(name string) p.ConstructRequest {
		request := agentRequest(true)
		request.Inputs = request.Inputs.Delete(name)
		return request
	}

	for message, request := range map[string]p.ConstructRequest{
		"stage is required and must be a resource":   without("stage"),
		"agentDefinitionId is required":              with("agentDefinitionId", property.New(" ")),
		"prompt is required":                         with("prompt", property.New("")),
		"responseSchema is required":                 without("responseSchema"),
		"responseSchema must be a JSON Schema object": with("responseSchema", property.New("object")),
	} {
		_, err := server.Construct(request)
		require.EqualError(t, err, message)
	}
	for name, request := range map[string]p.ConstructRequest{
		"unknown agent":           with("agentDefinitionId", property.New(property.Computed)),
		"unknown stage":           with("stage", property.New(property.Computed)),
		"unknown response schema": with("responseSchema", property.New(property.Computed)),
	} {
		_, err := server.Construct(request)
		require.NoError(t, err, name)
	}

	// An Agent is a member: it refuses error hooks.
	request := agentRequest(false)
	request.ResourceHooks = &p.ResourceHooksBinding{OnError: []string{"retry"}}
	_, err := server.Construct(request)
	require.EqualError(t, err, "Delivery resources do not support error hooks. Use a retry or a new run.")
}

func TestSchemaOfTheAgent(t *testing.T) {
	resp, err := newServer(t).GetSchema(p.GetSchemaRequest{})
	require.NoError(t, err)
	var schema struct {
		Resources map[string]struct {
			Inputs     map[string]json.RawMessage `json:"inputProperties"`
			Required   []string                   `json:"requiredInputs"`
			Properties map[string]json.RawMessage `json:"properties"`
			Outputs    []string                   `json:"required"`
		} `json:"resources"`
		Functions map[string]struct {
			Inputs struct {
				Required []string `json:"required"`
			} `json:"inputs"`
		} `json:"functions"`
	}
	require.NoError(t, json.Unmarshal([]byte(resp.Schema), &schema))
	agent := schema.Resources[typeAgent]
	assert.Equal(t, []string{"stage", "agentDefinitionId", "prompt", "responseSchema"}, agent.Required)
	assert.Equal(t, []string{"response", "taskId"}, agent.Outputs)
	assert.NotContains(t, agent.Properties, "childUrn")
	assert.NotContains(t, agent.Inputs, "source")
	assert.JSONEq(t, `{"$ref":"#/resources/delivery:index:Stage"}`, stripDescription(t, agent.Inputs["stage"]))
	assert.Equal(t, []string{"agent"}, schema.Functions["delivery:index:decision"].Inputs.Required)
}
