// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/blang/semver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/pulumi/pulumi-delivery/provider/cloudclient"
	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/integration"
	"github.com/pulumi/pulumi/pkg/v3/resource/stack"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/config"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/rpcutil/rpcerror"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"
)

// fakeService is the Delivery part of the API of Pulumi Cloud.
type fakeService struct {
	*httptest.Server
	mu           sync.Mutex
	declarations []map[string]any
	requests     []string
	// run says whether the stack is a run stack.
	run bool
	// release is the release number that the run route reports. Zero means the run has none.
	release int
	// runError makes the run route fail with a 400.
	runError bool
	// memberStarts are the bodies of the member start requests. memberAnswers are the answers by entry: the
	// status code and the body. A member without an answer is waiting.
	memberStarts  []map[string]any
	memberAnswers map[string]memberAnswer
	// valueRequests are the bodies of the run value requests. pipelineAnswer is the answer to the kind
	// "pipeline"; without one the value is the commit of a source.
	valueRequests  []map[string]any
	pipelineAnswer *memberAnswer
}

type memberAnswer struct {
	code int
	body string
}

func newFakeService(t *testing.T) *fakeService {
	s := &fakeService{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		s.requests = append(s.requests, r.Method+" "+r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/delivery/declarations"):
			var declaration map[string]any
			require.NoError(t, json.Unmarshal(body, &declaration))
			s.declarations = append(s.declarations, declaration)
			_, _ = w.Write([]byte(`{"warnings":[]}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/delivery/run"):
			switch {
			case s.runError:
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"code":400,"message":"The Delivery provider is too old."}`))
			case s.run:
				run := map[string]any{"number": 1, "kind": "normal", "status": "in_progress", "observationNumber": 1}
				if s.release != 0 {
					run["releaseNumber"] = s.release
				}
				require.NoError(t, json.NewEncoder(w).Encode(run))
			default:
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"code":404,"message":"This stack is not a Delivery run stack."}`))
			}
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/delivery/members/start"):
			var start map[string]any
			require.NoError(t, json.Unmarshal(body, &start))
			s.memberStarts = append(s.memberStarts, start)
			answer, ok := s.memberAnswers[start["entry"].(string)]
			if !ok {
				answer = memberAnswer{http.StatusOK, `{"status":"waiting"}`}
			}
			w.WriteHeader(answer.code)
			_, _ = w.Write([]byte(answer.body))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/delivery/run/values"):
			var request map[string]any
			require.NoError(t, json.Unmarshal(body, &request))
			s.valueRequests = append(s.valueRequests, request)
			if request["kind"] == "pipeline" && s.pipelineAnswer != nil {
				w.WriteHeader(s.pipelineAnswer.code)
				_, _ = w.Write([]byte(s.pipelineAnswer.body))
				return
			}
			_, _ = w.Write([]byte(`{"value":{"commit":"abc123"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.Close)
	t.Setenv("PULUMI_API", s.URL)
	t.Setenv("PULUMI_ACCESS_TOKEN", "token")
	return s
}

func (s *fakeService) declared() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.declarations...)
}

func newServer(t *testing.T) integration.Server { return newServerCapturing(t, nil) }

// newServerCapturing records the inputs of the child resources that components register.
func newServerCapturing(t *testing.T, children *[]property.Map) integration.Server {
	t.Helper()
	provider, err := newProvider()
	require.NoError(t, err)
	// The engine registers children concurrently.
	var mu sync.Mutex
	server, err := integration.NewServer(t.Context(), "delivery", semver.MustParse("0.0.1"),
		integration.WithProvider(provider),
		integration.WithMocks(&integration.MockResourceMonitor{
			NewResourceF: func(args integration.MockResourceArgs) (string, property.Map, error) {
				if children != nil && args.TypeToken == typeMemberRun {
					mu.Lock()
					*children = append(*children, args.Inputs)
					mu.Unlock()
				}
				return args.Name, property.NewMap(map[string]property.Value{
					"outputs": property.New(property.NewMap(nil)),
				}), nil
			},
		}))
	require.NoError(t, err)
	return server
}

func urnOf(typ, name string) resource.URN {
	return resource.URN("urn:pulumi:stack::project::" + typ + "::" + name)
}

func reference(typ, name string) property.Value {
	return property.New(property.ResourceReference{URN: urnOf(typ, name), ID: property.New(property.Null)})
}

func jobRequest(dryRun bool) p.ConstructRequest {
	return p.ConstructRequest{
		Urn:          urnOf(typeJob, "build"),
		Organization: "org",
		DryRun:       dryRun,
		Dependencies: []resource.URN{urnOf("delivery:index:Job", "other")},
		Inputs: property.NewMap(map[string]property.Value{
			"stage":     reference(typeStage, "dev"),
			"source":    reference("delivery:index:GitHubSource", "code"),
			"directory": property.New("app"),
			"command":   property.New("make").WithSecret(true),
			"environment": property.New(property.NewMap(map[string]property.Value{
				"FROM_UPSTREAM": property.New(property.Computed),
			})).WithDependencies([]resource.URN{urnOf("delivery:index:Job", "other")}),
		}),
	}
}

func TestOneDeclarationForEachComponentInAnUpdate(t *testing.T) {
	service := newFakeService(t)
	server := newServer(t)

	_, err := server.Construct(jobRequest(false))
	require.NoError(t, err)

	declarations := service.declared()
	require.Len(t, declarations, 1)
	d := declarations[0]
	assert.EqualValues(t, 1, d["protocolVersion"])
	assert.Equal(t, string(urnOf(typeJob, "build")), d["urn"])
	assert.Equal(t, typeJob, d["type"])
	assert.Equal(t, []any{string(urnOf("delivery:index:Job", "other"))}, d["dependencies"])
	assert.Equal(t, map[string]any{"environment": []any{string(urnOf("delivery:index:Job", "other"))}}, d["propertyDependencies"])

	// The service decodes the inputs with DeserializeProperties: an unknown stays unknown and a secret stays secret.
	inputs, err := stack.DeserializeProperties(d["inputs"].(map[string]any), config.NewErrorCrypter("no ciphertext"))
	require.NoError(t, err)
	assert.True(t, inputs["command"].IsSecret())
	assert.Equal(t, "make", inputs["command"].SecretValue().Element.StringValue())
	assert.True(t, inputs["environment"].ObjectValue()["FROM_UPSTREAM"].IsComputed())
	assert.True(t, inputs["stage"].IsResourceReference())
	assert.Equal(t, urnOf(typeStage, "dev"), inputs["stage"].ResourceReferenceValue().URN)
}

func TestNoDeclarationInAPreview(t *testing.T) {
	service := newFakeService(t)
	server := newServer(t)

	_, err := server.Construct(jobRequest(true))
	require.NoError(t, err)

	assert.Empty(t, service.requests)
}

func TestOutputsOfAMemberDependOnItsChild(t *testing.T) {
	newFakeService(t)
	server := newServer(t)

	resp, err := server.Construct(jobRequest(false))
	require.NoError(t, err)

	child := urnOf(typeJob+"$"+typeMemberRun, "build")
	_, hasChildURN := resp.State.GetOk("childUrn")
	assert.False(t, hasChildURN, "the child URN is not a public output")
	assert.Contains(t, resp.State.Get("outputs").Dependencies(), child)
}

func TestMissingCredentials(t *testing.T) {
	t.Setenv("PULUMI_API", "")
	t.Setenv("PULUMI_ACCESS_TOKEN", "token")
	server := newServer(t)

	_, err := server.Construct(jobRequest(false))
	require.EqualError(t, err, "Delivery needs Pulumi Cloud. Run `pulumi login` to a Pulumi Cloud backend.")

	t.Setenv("PULUMI_API", "https://api.example.com")
	t.Setenv("PULUMI_ACCESS_TOKEN", "")
	_, err = server.Construct(jobRequest(false))
	require.EqualError(t, err, "Delivery needs Pulumi Cloud. Run `pulumi login` to a Pulumi Cloud backend.")

	// A preview does not need them.
	_, err = server.Construct(jobRequest(true))
	require.NoError(t, err)
}

func TestErrorHooksFailAndOtherHooksPass(t *testing.T) {
	service := newFakeService(t)
	server := newServer(t)

	request := jobRequest(false)
	request.ResourceHooks = &p.ResourceHooksBinding{BeforeCreate: []string{"before"}, AfterUpdate: []string{"after"}}
	_, err := server.Construct(request)
	require.NoError(t, err)

	request.ResourceHooks = &p.ResourceHooksBinding{OnError: []string{"retry"}}
	_, err = server.Construct(request)
	require.EqualError(t, err, "Delivery resources do not support error hooks. Use a retry or a new run.")
	assert.Len(t, service.declared(), 1, "a rejected component sends no declaration")
}

func TestServiceErrorFailsConstruct(t *testing.T) {
	rejecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":400,"message":"The stage urn:x of urn:y is not declared in this update."}`))
	}))
	t.Cleanup(rejecting.Close)
	t.Setenv("PULUMI_API", rejecting.URL)
	t.Setenv("PULUMI_ACCESS_TOKEN", "token")

	_, err := newServer(t).Construct(jobRequest(false))
	require.ErrorContains(t, err, "The stage urn:x of urn:y is not declared in this update.")
}

func sourceRequest(dryRun bool) p.ConstructRequest {
	return p.ConstructRequest{
		Urn:          urnOf("delivery:index:GitHubSource", "code"),
		Organization: "org",
		DryRun:       dryRun,
		Inputs: property.NewMap(map[string]property.Value{
			"repository": property.New("Acme/Shop"),
			"branch":     property.New("main"),
		}),
	}
}

func TestSourceInARunReturnsTheRecordedCommit(t *testing.T) {
	service := newFakeService(t)
	service.run = true

	resp, err := newServer(t).Construct(sourceRequest(false))
	require.NoError(t, err)

	assert.Equal(t, "abc123", resp.State.Get("commit").AsString())
	assert.Len(t, service.declared(), 1)
}

func TestSourceInARegistrationHasAnUnknownCommit(t *testing.T) {
	service := newFakeService(t)

	resp, err := newServer(t).Construct(sourceRequest(false))
	require.NoError(t, err)

	assert.True(t, resp.State.Get("commit").IsComputed())
	assert.Len(t, service.declared(), 1)
	assert.NotContains(t, strings.Join(service.requests, "\n"), "/run/values")

	// A preview has an unknown commit too, and sends nothing.
	service.requests = nil
	resp, err = newServer(t).Construct(sourceRequest(true))
	require.NoError(t, err)
	assert.True(t, resp.State.Get("commit").IsComputed())
	assert.Empty(t, service.requests)
}

func memberRunRequest(dryRun bool) p.CreateRequest {
	return p.CreateRequest{
		Urn:    urnOf(typeMemberRun, "build"),
		DryRun: dryRun,
		Properties: property.NewMap(map[string]property.Value{
			"organization": property.New("org"),
			"member":       property.New(string(urnOf(typeJob, "build"))),
			"declaration":  property.New(property.NewMap(map[string]property.Value{"directory": property.New("app")})),
		}),
	}
}

func requireAwaitError(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	rpcErr, ok := rpcerror.FromError(err)
	require.True(t, ok, "%v is not a status error", err)
	assert.Equal(t, codes.Aborted, rpcErr.Code())
	var awaited bool
	for _, detail := range rpcErr.Details() {
		_, isAwait := detail.(*pulumirpc.ErrorResourceAwaitFailed)
		awaited = awaited || isAwait
	}
	assert.True(t, awaited, "the status has no ErrorResourceAwaitFailed detail: %v", rpcErr.Details())
}

func TestChildAwaitsInARegistration(t *testing.T) {
	newFakeService(t) // GET run answers 404
	server := newServer(t)

	_, err := server.Create(memberRunRequest(false))
	requireAwaitError(t, err)
	assert.Contains(t, err.Error(), "registered")

	// A preview does not wait, and its outputs are unknown.
	resp, err := server.Create(memberRunRequest(true))
	require.NoError(t, err)
	assert.True(t, resp.Properties.Get("outputs").IsComputed())
}

func TestChildAwaitsInARun(t *testing.T) {
	service := newFakeService(t)
	service.run = true

	_, err := newServer(t).Create(memberRunRequest(false))
	requireAwaitError(t, err)
}

func TestChildFailsOnAnotherServiceError(t *testing.T) {
	service := newFakeService(t)
	service.runError = true

	_, err := newServer(t).Create(memberRunRequest(false))
	require.ErrorContains(t, err, "The Delivery provider is too old.")
	_, awaits := rpcerror.FromError(err)
	assert.False(t, awaits && strings.Contains(err.Error(), "registered"))
}

func TestDeclarationEncoding(t *testing.T) {
	// The unknown marker of the engine is what the service decodes as an unknown.
	encoded, err := stack.SerializeProperties(t.Context(),
		resource.ToResourcePropertyMap(property.NewMap(map[string]property.Value{"a": property.New(property.Computed)})),
		config.NopEncrypter, true)
	require.NoError(t, err)
	decoded, err := stack.DeserializeProperties(encoded, config.NewErrorCrypter("no ciphertext"))
	require.NoError(t, err)
	assert.True(t, decoded["a"].IsComputed())
}

func TestNestedDependenciesBelongToTheirProperty(t *testing.T) {
	service := newFakeService(t)
	other := urnOf("delivery:index:Job", "other")
	request := jobRequest(false)
	request.Dependencies = nil
	request.Inputs = request.Inputs.Set("environment", property.New(property.NewMap(map[string]property.Value{
		"A": property.New("x").WithDependencies([]resource.URN{other}),
		"B": property.New(property.NewArray([]property.Value{property.New("y").WithDependencies([]resource.URN{other})})),
	})))

	_, err := newServer(t).Construct(request)
	require.NoError(t, err)

	d := service.declared()[0]
	assert.Equal(t, map[string]any{"environment": []any{string(other)}}, d["propertyDependencies"])
}

func stageRequest(when property.Value) p.ConstructRequest {
	return p.ConstructRequest{
		Urn:          urnOf(typeStage, "prod"),
		Organization: "org",
		DryRun:       true,
		Inputs: property.NewMap(map[string]property.Value{
			"pipeline": reference(typePipeline, "shop"),
			"when":     when,
		}),
	}
}

func TestStageRulesWithSecretsAndUnknowns(t *testing.T) {
	newFakeService(t)
	server := newServer(t)
	rule := func(fields map[string]property.Value) property.Value { return property.New(property.NewMap(fields)) }

	// An http rule with a secret header.
	_, err := server.Construct(stageRequest(rule(map[string]property.Value{
		"kind":              property.New("http"),
		"url":               property.New("https://example.com"),
		"successExpression": property.New("ok"),
		"headers": property.New(property.NewMap(map[string]property.Value{
			"Authorization": property.New("secret").WithSecret(true),
		})),
	})))
	require.NoError(t, err)

	// A secret rule is checked: its shape is still visible.
	_, err = server.Construct(stageRequest(rule(map[string]property.Value{
		"kind": property.New("http"), "url": property.New(""),
	}).WithSecret(true)))
	require.ErrorContains(t, err, "rule.url is required")

	// An unknown rule, and an unknown part of a rule, are not checked in a preview.
	_, err = server.Construct(stageRequest(property.New(property.Computed)))
	require.NoError(t, err)
	_, err = server.Construct(stageRequest(rule(map[string]property.Value{
		"kind": property.New("http"), "url": property.New(property.Computed), "successExpression": property.New("ok"),
	})))
	require.NoError(t, err)
}

func TestUnknownProgramIsNotChecked(t *testing.T) {
	newFakeService(t)
	_, err := newServer(t).Construct(p.ConstructRequest{
		Urn:          urnOf(typePipeline, "shop"),
		Organization: "org",
		DryRun:       true,
		Inputs:       property.NewMap(map[string]property.Value{"program": property.New(property.Computed)}),
	})
	require.NoError(t, err)
}

func TestChildCarriesTheFullDeclaration(t *testing.T) {
	newFakeService(t)
	var children []property.Map
	_, err := newServerCapturing(t, &children).Construct(jobRequest(true))
	require.NoError(t, err)

	require.Len(t, children, 1)
	declaration := children[0].Get("declaration").AsMap()
	assert.EqualValues(t, 1, declaration.Get("protocolVersion").AsNumber())
	assert.Equal(t, typeJob, declaration.Get("type").AsString())
	assert.Equal(t, string(urnOf(typeJob, "build")), declaration.Get("urn").AsString())
	assert.Equal(t, 1, declaration.Get("dependencies").AsArray().Len())
	assert.True(t, declaration.Get("propertyDependencies").IsMap())
	inputs := declaration.Get("inputs").AsMap()
	assert.Equal(t, "make", inputs.Get("command").AsString())
	assert.True(t, inputs.Get("command").Secret(), "the preview hides the secret")
	assert.Equal(t, "app", inputs.Get("directory").AsString())
	assert.True(t, inputs.Get("stage").IsResourceReference())
}

func TestSchemaHidesInternals(t *testing.T) {
	resp, err := newServer(t).GetSchema(p.GetSchemaRequest{})
	require.NoError(t, err)
	var schema struct {
		Resources map[string]struct {
			Properties map[string]any `json:"properties"`
			Required   []string       `json:"required"`
			Inputs     []string       `json:"requiredInputs"`
		} `json:"resources"`
		Types map[string]struct {
			Required []string `json:"required"`
		} `json:"types"`
	}
	require.NoError(t, json.Unmarshal([]byte(resp.Schema), &schema))
	assert.NotContains(t, schema.Resources, typeMemberRun)
	assert.NotContains(t, schema.Resources[typeJob].Properties, "childUrn")
	assert.NotContains(t, schema.Resources[typeJob].Required, "childUrn")
	assert.Contains(t, schema.Resources[typeJob].Inputs, "stage")
	assert.Contains(t, schema.Resources[typeStacks].Inputs, "stage")
}

func TestSchemaOfTheTriggersAndTheReleaseNumber(t *testing.T) {
	resp, err := newServer(t).GetSchema(p.GetSchemaRequest{})
	require.NoError(t, err)
	var schema struct {
		Resources map[string]struct {
			Inputs     map[string]json.RawMessage `json:"inputProperties"`
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		} `json:"resources"`
		Types     map[string]json.RawMessage `json:"types"`
		Functions map[string]json.RawMessage `json:"functions"`
	}
	require.NoError(t, json.Unmarshal([]byte(resp.Schema), &schema))
	for typ := range sourceKinds {
		assert.JSONEq(t, `{"oneOf":[{"type":"boolean"},{"$ref":"#/types/delivery:index:SourceTrigger"}]}`,
			stripDescription(t, schema.Resources[typ].Inputs["trigger"]), typ)
		assert.Contains(t, schema.Resources[typ].Properties, "commit", typ)
		assert.Contains(t, schema.Resources[typ].Required, "commit", typ)
	}
	assert.Contains(t, schema.Types, "delivery:index:SourceTrigger")
	assert.NotContains(t, schema.Types, "delivery:index:Trigger")
	assert.NotContains(t, schema.Resources[typePipeline].Inputs, "triggers")
	assert.Contains(t, schema.Resources[typePipeline].Properties, "releaseNumber")
	assert.Contains(t, schema.Resources[typePipeline].Required, "releaseNumber")
	assert.Contains(t, schema.Functions, "delivery:index:manual")
	for _, gone := range []string{"push", "stageConverged", "signal"} {
		assert.NotContains(t, schema.Functions, "delivery:index:"+gone)
	}
}

func stripDescription(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var node map[string]any
	require.NoError(t, json.Unmarshal(raw, &node))
	delete(node, "description")
	out, err := json.Marshal(node)
	require.NoError(t, err)
	return string(out)
}

func triggerRequest(trigger property.Value) p.ConstructRequest {
	request := sourceRequest(false)
	request.Inputs = request.Inputs.Set("trigger", trigger)
	return request
}

func TestSourceTriggerIsDeclared(t *testing.T) {
	service := newFakeService(t)
	server := newServer(t)
	paths := property.New(property.NewMap(map[string]property.Value{
		"paths": property.New(property.NewArray([]property.Value{property.New("src/**"), property.New("delivery/**")})),
	}))

	for _, trigger := range []property.Value{property.New(true), paths} {
		_, err := server.Construct(triggerRequest(trigger))
		require.NoError(t, err)
	}
	_, err := server.Construct(sourceRequest(false))
	require.NoError(t, err)

	declarations := service.declared()
	require.Len(t, declarations, 3)
	assert.Equal(t, true, declarations[0]["inputs"].(map[string]any)["trigger"])
	assert.Equal(t, map[string]any{"paths": []any{"src/**", "delivery/**"}}, declarations[1]["inputs"].(map[string]any)["trigger"])
	assert.NotContains(t, declarations[2]["inputs"], "trigger")
}

func TestSourceTriggerIsChecked(t *testing.T) {
	newFakeService(t)
	server := newServer(t)
	paths := func(values ...property.Value) property.Value {
		return property.New(property.NewMap(map[string]property.Value{"paths": property.New(property.NewArray(values))}))
	}

	for name, trigger := range map[string]property.Value{
		"false":       property.New(false),
		"a string":    property.New("push"),
		"no paths":    property.New(property.NewMap(nil)),
		"empty paths": paths(),
		"empty path":  paths(property.New(" ")),
	} {
		_, err := server.Construct(triggerRequest(trigger))
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "trigger", name)
	}
	for name, trigger := range map[string]property.Value{
		"unknown":       property.New(property.Computed),
		"unknown paths": property.New(property.NewMap(map[string]property.Value{"paths": property.New(property.Computed)})),
		"unknown path":  paths(property.New(property.Computed)),
	} {
		_, err := server.Construct(triggerRequest(trigger))
		require.NoError(t, err, name)
	}
}

func pipelineRequest(dryRun bool) p.ConstructRequest {
	return p.ConstructRequest{
		Urn:          urnOf(typePipeline, "shop"),
		Organization: "org",
		DryRun:       dryRun,
		Inputs: property.NewMap(map[string]property.Value{
			"program": property.New(property.NewMap(map[string]property.Value{
				"source":    reference("delivery:index:GitHubSource", "code"),
				"directory": property.New("delivery"),
			})),
		}),
	}
}

func TestPipelineReleaseNumber(t *testing.T) {
	service := newFakeService(t)
	server := newServer(t)

	// A registration and a preview do not know it.
	resp, err := server.Construct(pipelineRequest(false))
	require.NoError(t, err)
	assert.True(t, resp.State.Get("releaseNumber").IsComputed())
	resp, err = server.Construct(pipelineRequest(true))
	require.NoError(t, err)
	assert.True(t, resp.State.Get("releaseNumber").IsComputed())

	// A run without a release does not either.
	service.run = true
	resp, err = server.Construct(pipelineRequest(false))
	require.NoError(t, err)
	assert.True(t, resp.State.Get("releaseNumber").IsComputed())

	service.release = 7
	resp, err = server.Construct(pipelineRequest(false))
	require.NoError(t, err)
	assert.EqualValues(t, 7, resp.State.Get("releaseNumber").AsNumber())
}

type recordingLog struct{ infos, warnings []string }

func (l *recordingLog) Info(msg string)    { l.infos = append(l.infos, msg) }
func (l *recordingLog) Warning(msg string) { l.warnings = append(l.warnings, msg) }

func TestReportDeclarationShowsTheMessageAndWarnings(t *testing.T) {
	log := &recordingLog{}
	reportDeclaration(log, cloudclient.DeclarationResponse{Message: "Registered. No run starts.", Warnings: []string{"a", "b"}})
	assert.Equal(t, []string{"Registered. No run starts."}, log.infos)
	assert.Equal(t, []string{"a", "b"}, log.warnings)

	log = &recordingLog{}
	reportDeclaration(log, cloudclient.DeclarationResponse{})
	assert.Empty(t, log.infos)
	assert.Empty(t, log.warnings)
}

func TestComponentsRecordTheirInputs(t *testing.T) {
	newFakeService(t)
	var mu sync.Mutex
	var recorded property.Map
	provider, err := newProvider()
	require.NoError(t, err)
	server, err := integration.NewServer(t.Context(), "delivery", semver.MustParse("0.0.1"),
		integration.WithProvider(provider),
		integration.WithMocks(&integration.MockResourceMonitor{
			NewResourceF: func(args integration.MockResourceArgs) (string, property.Map, error) {
				if args.TypeToken == typeStage {
					mu.Lock()
					recorded = args.Inputs
					mu.Unlock()
				}
				return args.Name, property.NewMap(nil), nil
			},
		}))
	require.NoError(t, err)

	_, err = server.Construct(p.ConstructRequest{
		Urn:          urnOf(typeStage, "dev"),
		Organization: "org",
		Inputs: property.NewMap(map[string]property.Value{
			"pipeline": reference(typePipeline, "main"),
			"token":    property.New("t").WithSecret(true),
		}),
	})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.NotNil(t, recorded.AsMap())
	assert.Contains(t, recorded.AsMap(), "pipeline")
	assert.True(t, recorded.AsMap()["token"].Secret())
}
