// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/blang/semver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/integration"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
)

func pipelineSourceRequest(dryRun bool, pipeline string) p.ConstructRequest {
	return p.ConstructRequest{
		Urn:          urnOf(typePipelineSource, "platform"),
		Organization: "org",
		DryRun:       dryRun,
		Inputs: property.NewMap(map[string]property.Value{
			"pipeline": property.New(pipeline),
			"stage":    property.New("prod"),
		}),
	}
}

const upstreamValue = `{"value":{"releaseNumber":7,"runNumber":12,"artifacts":{
	"endpoint":"https://example.com",
	"ports":[80,443],
	"token":{"4dabf18193072939515e22adb298388d":"1b47061264138c4ac30d75fd1eb44270","plaintext":"\"s3cret\""}}}}`

func TestPipelineSourceHasUnknownOutputsInARegistrationAndAPreview(t *testing.T) {
	service := newFakeService(t)
	for _, dryRun := range []bool{false, true} {
		service.requests = nil
		resp, err := newServer(t).Construct(pipelineSourceRequest(dryRun, "acme/platform/main"))
		require.NoError(t, err)
		for _, name := range []string{"releaseNumber", "runNumber", "artifacts"} {
			assert.True(t, resp.State.Get(name).IsComputed(), "%s, dry run %v", name, dryRun)
			assert.False(t, resp.State.Get(name).Secret(), name)
		}
		assert.NotContains(t, service.requests, "POST /api/orgs/org/projects/project/stacks/stack/delivery/run/values")
		assert.Empty(t, service.valueRequests)
	}
}

func TestPipelineSourceInARunReadsTheValue(t *testing.T) {
	service := newFakeService(t)
	service.run = true
	service.pipelineAnswer = &memberAnswer{http.StatusOK, upstreamValue}

	resp, err := newServer(t).Construct(pipelineSourceRequest(false, "acme/platform/main"))
	require.NoError(t, err)

	require.Len(t, service.valueRequests, 1)
	assert.Equal(t, "pipeline", service.valueRequests[0]["kind"])
	assert.Equal(t, map[string]any{"pipeline": "acme/platform/main", "stage": "prod"}, service.valueRequests[0]["identity"])
	assert.EqualValues(t, 7, resp.State.Get("releaseNumber").AsNumber())
	assert.EqualValues(t, 12, resp.State.Get("runNumber").AsNumber())

	artifacts := resp.State.Get("artifacts")
	assert.True(t, artifacts.Secret(), "the map is secret when any artifact is")
	m := artifacts.AsMap()
	assert.Equal(t, "https://example.com", m.Get("endpoint").AsString())
	assert.False(t, m.Get("endpoint").Secret())
	require.True(t, m.Get("ports").IsArray())
	assert.Equal(t, 2, m.Get("ports").AsArray().Len())
	assert.Equal(t, "s3cret", m.Get("token").AsString())
	assert.True(t, m.Get("token").Secret())
}

func TestPipelineSourceMapIsNotSecretWithoutSecretArtifacts(t *testing.T) {
	service := newFakeService(t)
	service.run = true
	service.pipelineAnswer = &memberAnswer{http.StatusOK,
		`{"value":{"releaseNumber":1,"runNumber":2,"artifacts":{"endpoint":"https://example.com","ports":[80]}}}`}

	resp, err := newServer(t).Construct(pipelineSourceRequest(false, "acme/platform/main"))
	require.NoError(t, err)
	assert.False(t, resp.State.Get("artifacts").Secret())
	assert.False(t, resp.State.Get("artifacts").AsMap().Get("endpoint").Secret())
}

func TestConstructLogRedactsSecrets(t *testing.T) {
	log := filepath.Join(t.TempDir(), "construct.log")
	t.Setenv("DELIVERY_CONSTRUCT_LOG", log)
	secret := property.New("hunter2").WithSecret(true)
	logConstruct(urnOf(typeArtifact, "a"), p.ConstructResponse{State: property.NewMap(map[string]property.Value{
		"top":     secret,
		"nested":  property.New(property.NewMap(map[string]property.Value{"k": secret, "plain": property.New("visible")})),
		"list":    property.New(property.NewArray([]property.Value{secret})),
		"unknown": property.New(property.Computed).WithSecret(true),
		"whole":   property.New(property.NewMap(map[string]property.Value{"k": property.New("hidden2")})).WithSecret(true),
	})})
	got, err := os.ReadFile(log)
	require.NoError(t, err)
	assert.NotContains(t, string(got), "hunter2")
	assert.NotContains(t, string(got), "hidden2")
	assert.Contains(t, string(got), "[secret]")
	assert.Contains(t, string(got), "visible")
	assert.Contains(t, string(got), "Computed")
}

func TestPipelineSourceFailsWithTheServiceMessage(t *testing.T) {
	service := newFakeService(t)
	service.run = true
	service.pipelineAnswer = &memberAnswer{http.StatusConflict,
		`{"code":409,"message":"Stage prod of pipeline acme/platform/main has no deployed release."}`}

	_, err := newServer(t).Construct(pipelineSourceRequest(false, "acme/platform/main"))
	require.ErrorContains(t, err, "Stage prod of pipeline acme/platform/main has no deployed release.")
}

func TestPipelineSourceValidation(t *testing.T) {
	newFakeService(t)
	for _, bad := range []string{"acme/platform", "acme//main", "a/b/c/d", ""} {
		_, err := newServer(t).Construct(pipelineSourceRequest(true, bad))
		require.Error(t, err, bad)
	}
	// An unknown pipeline passes: the service checks it at registration.
	request := pipelineSourceRequest(true, "")
	request.Inputs = request.Inputs.Set("pipeline", property.New(property.Computed))
	_, err := newServer(t).Construct(request)
	require.NoError(t, err)
	// A run does not read the value of an unknown pipeline.
	service := newFakeService(t)
	service.run = true
	request.DryRun = false
	_, err = newServer(t).Construct(request)
	require.NoError(t, err)
	assert.Empty(t, service.valueRequests)
}

func artifactRequest(dryRun bool, value property.Value) p.ConstructRequest {
	return p.ConstructRequest{
		Urn:          urnOf(typeArtifact, "clusterEndpoint"),
		Organization: "org",
		DryRun:       dryRun,
		Inputs: property.NewMap(map[string]property.Value{
			"stage": reference(typeStage, "prod"),
			"value": value,
		}),
	}
}

func TestArtifactRegistersOneChildWithEntryNone(t *testing.T) {
	service := newFakeService(t)
	var children []property.Map
	_, err := newServerCapturing(t, &children).Construct(artifactRequest(false, property.New("x")))
	require.NoError(t, err)

	declarations := service.declared()
	require.Len(t, declarations, 1)
	assert.Equal(t, typeArtifact, declarations[0]["type"])
	require.Len(t, children, 1)
	assert.Equal(t, string(urnOf(typeArtifact, "clusterEndpoint")), children[0].Get("member").AsString())
	_, hasEntry := children[0].GetOk("entry")
	assert.False(t, hasEntry, "an Artifact has no entry")
	declaration := children[0].Get("declaration").AsMap()
	assert.Equal(t, typeArtifact, declaration.Get("type").AsString())
	assert.Equal(t, "x", declaration.Get("inputs").AsMap().Get("value").AsString())
}

func TestArtifactValueDependsOnTheChild(t *testing.T) {
	newFakeService(t)
	child := urnOf(typeArtifact+"$"+typeMemberRun, "clusterEndpoint")
	resp, err := newServer(t).Construct(artifactRequest(false, property.New("x")))
	require.NoError(t, err)
	_, hasChildURN := resp.State.GetOk("childUrn")
	assert.False(t, hasChildURN)
	assert.Contains(t, resp.State.Get("value").Dependencies(), child)
}

// finishState: a missing value stays unknown, and only an actual secret makes the value secret.
func TestFinishStateOfAnArtifact(t *testing.T) {
	child := string(urnOf(typeArtifact+"$"+typeMemberRun, "clusterEndpoint"))
	withChild := property.NewMap(map[string]property.Value{"childUrn": property.New(child)})

	missing := finishState(typeArtifact, withChild, "", nil, nil, nil, artifactRequest(false, property.New("x")).Inputs)
	assert.True(t, missing.Get("value").IsComputed())
	assert.False(t, missing.Get("value").Secret(), "an unknown value is not secret")
	assert.Equal(t, []resource.URN{resource.URN(child)}, missing.Get("value").Dependencies())

	known := withChild.Set("value", property.New(property.NewMap(map[string]property.Value{"host": property.New("h")})))
	plain := finishState(typeArtifact, known, "", nil, nil, nil, artifactRequest(false, property.New("x")).Inputs)
	assert.False(t, plain.Get("value").Secret())
	assert.True(t, plain.Get("value").IsMap(), "a structure keeps its shape")

	inner := property.New(property.NewMap(map[string]property.Value{"k": property.New("v").WithSecret(true)}))
	for _, input := range []property.Value{property.New("x").WithSecret(true), inner} {
		secret := finishState(typeArtifact, known, "", nil, nil, nil, artifactRequest(false, input).Inputs)
		assert.True(t, secret.Get("value").Secret())
		assert.True(t, secret.Get("value").IsMap())
	}

	// A secret input that is unknown is not secret in the response.
	computed := withChild.Set("value", property.New(property.Computed).WithSecret(true))
	assert.False(t, finishState(typeArtifact, computed, "", nil, nil, nil, artifactRequest(false, property.New("x").WithSecret(true)).Inputs).
		Get("value").Secret(), "an unknown output is not secret, also when the input is")
	unknown := finishState(typeArtifact, withChild, "", nil, nil, nil, artifactRequest(false, property.New(property.Computed).WithSecret(true)).Inputs)
	assert.False(t, unknown.Get("value").Secret())
}

func TestArtifactValueIsRequiredAndMayBeUnknown(t *testing.T) {
	newFakeService(t)
	request := artifactRequest(true, property.New(property.Computed))
	_, err := newServer(t).Construct(request)
	require.NoError(t, err)

	request.Inputs = request.Inputs.Delete("value")
	_, err = newServer(t).Construct(request)
	require.ErrorContains(t, err, "value is required")
}

func TestArtifactRefusesErrorHooks(t *testing.T) {
	newFakeService(t)
	request := artifactRequest(true, property.New("x"))
	request.ResourceHooks = &p.ResourceHooksBinding{OnError: []string{"retry"}}
	_, err := newServer(t).Construct(request)
	require.EqualError(t, err, "Delivery resources do not support error hooks. Use a retry or a new run.")
}

func artifactChildRequest() p.CreateRequest {
	return p.CreateRequest{
		Urn: urnOf(typeArtifact+"$"+typeMemberRun, "clusterEndpoint"),
		Properties: property.NewMap(map[string]property.Value{
			"organization": property.New("org"),
			"member":       property.New(string(urnOf(typeArtifact, "clusterEndpoint"))),
			"declaration":  property.New(property.NewMap(nil)),
		}),
	}
}

func TestArtifactChildInARunSucceedsWithTheValue(t *testing.T) {
	service := newFakeService(t)
	service.run = true
	answer(service, "", http.StatusOK, `{"status":"succeeded","outputs":{"value":{"host":"h","ports":[443]}}}`)

	resp, err := newServer(t).Create(artifactChildRequest())
	require.NoError(t, err)

	require.Len(t, service.memberStarts, 1)
	assert.Equal(t, "", service.memberStarts[0]["entry"])
	value := resp.Properties.Get("outputs").AsMap().Get("value").AsMap()
	assert.Equal(t, "h", value.Get("host").AsString())
	assert.Equal(t, 1, value.Get("ports").AsArray().Len())
}

func TestPipelineSourceTrigger(t *testing.T) {
	service := newFakeService(t)
	server := newServer(t)
	request := func(trigger property.Value) p.ConstructRequest {
		return p.ConstructRequest{
			Urn: urnOf(typePipelineSource, "platform"), Organization: "org", DryRun: false,
			Inputs: property.NewMap(map[string]property.Value{
				"pipeline": property.New("acme/platform/main"), "stage": property.New("prod"), "trigger": trigger,
			}),
		}
	}

	_, err := server.Construct(request(property.New(true)))
	require.NoError(t, err)
	declarations := service.declared()
	require.Len(t, declarations, 1)
	assert.Equal(t, true, declarations[0]["inputs"].(map[string]any)["trigger"])

	for name, trigger := range map[string]property.Value{
		"false": property.New(false),
		"paths": property.New(property.NewMap(map[string]property.Value{"paths": property.New(property.NewArray([]property.Value{property.New("a")}))})),
	} {
		_, err := server.Construct(request(trigger))
		require.ErrorContains(t, err, "trigger must be true", name)
	}
	_, err = server.Construct(request(property.New(property.Computed)))
	require.NoError(t, err)
}

func TestSchemaOfUpstreams(t *testing.T) {
	provider, err := newProvider()
	require.NoError(t, err)
	server, err := integration.NewServer(t.Context(), "delivery", semver.MustParse("0.0.1"), integration.WithProvider(provider))
	require.NoError(t, err)
	resp, err := server.GetSchema(p.GetSchemaRequest{})
	require.NoError(t, err)
	var schema struct {
		Resources map[string]struct {
			Inputs map[string]json.RawMessage `json:"inputProperties"`
			Props  map[string]json.RawMessage `json:"properties"`
		} `json:"resources"`
		Types     map[string]struct{ Properties map[string]json.RawMessage } `json:"types"`
		Functions map[string]struct {
			Outputs json.RawMessage `json:"outputs"`
		} `json:"functions"`
	}
	require.NoError(t, json.Unmarshal([]byte(resp.Schema), &schema))

	assert.JSONEq(t, `{"type":"boolean"}`, stripDescription(t, schema.Resources[typePipelineSource].Inputs["trigger"]))
	assert.NotContains(t, string(schema.Resources[typeJob].Inputs["source"]), typePipelineSource)
	assert.NotContains(t, string(schema.Types["delivery:index:StacksEntry"].Properties["source"]), typePipelineSource)
	assert.NotContains(t, string(schema.Types["delivery:index:ProgramSource"].Properties["source"]), typePipelineSource)
	assert.Contains(t, string(schema.Resources[typeArtifact].Inputs["stage"]), typeStage)
	assert.NotContains(t, schema.Resources[typeArtifact].Props, "childUrn")
}
