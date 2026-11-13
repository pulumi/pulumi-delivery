// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"fmt"
	"sync"
	"testing"

	"github.com/blang/semver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/integration"
	"github.com/pulumi/pulumi/pkg/v3/resource/stack"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/config"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/urn"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func stacksEntry(stack string, extra map[string]property.Value) property.Value {
	fields := map[string]property.Value{
		"stack":     property.New(stack),
		"source":    reference("delivery:index:GitHubSource", "code"),
		"directory": property.New("infra"),
	}
	for k, v := range extra {
		fields[k] = v
	}
	return property.New(property.NewMap(fields))
}

func stacksRequest(name string, dryRun bool, entries map[string]property.Value) p.ConstructRequest {
	return p.ConstructRequest{
		Urn:          urnOf(typeStacks, name),
		Organization: "org",
		DryRun:       dryRun,
		Inputs: property.NewMap(map[string]property.Value{
			"stage":  reference(typeStage, "dev"),
			"stacks": property.New(property.NewMap(entries)),
		}),
	}
}

func twoEntries() map[string]property.Value {
	return map[string]property.Value{
		"api":    stacksEntry("org/shop/api", nil),
		"worker": stacksEntry("org/shop/worker", nil),
	}
}

func childURN(component, entry string) resource.URN {
	return urnOf(typeStacks+"$"+typeMemberRun, component+"#"+entry)
}

func TestStacksSendOneDeclarationAndPreviewSendsNothing(t *testing.T) {
	service := newFakeService(t)
	server := newServer(t)

	_, err := server.Construct(stacksRequest("app", true, twoEntries()))
	require.NoError(t, err)
	assert.Empty(t, service.requests)

	_, err = server.Construct(stacksRequest("app", false, twoEntries()))
	require.NoError(t, err)
	declarations := service.declared()
	require.Len(t, declarations, 1)
	assert.Equal(t, typeStacks, declarations[0]["type"])
	stacks := declarations[0]["inputs"].(map[string]any)["stacks"].(map[string]any)
	assert.Len(t, stacks, 2)
}

func TestStacksRegisterOneChildForEachEntry(t *testing.T) {
	newFakeService(t)
	var children []property.Map
	_, err := newServerCapturing(t, &children).Construct(stacksRequest("app", true, twoEntries()))
	require.NoError(t, err)

	require.Len(t, children, 2)
	entries := map[string]property.Map{}
	for _, child := range children {
		entry := child.Get("entry").AsString()
		entries[entry] = child
		assert.Equal(t, string(urnOf(typeStacks, "app")), child.Get("member").AsString())
		// The child carries its own entry, not the group.
		inputs := child.Get("declaration").AsMap().Get("inputs").AsMap().Get("stacks").AsMap()
		assert.Equal(t, 1, inputs.Len())
		_, ok := inputs.GetOk(entry)
		assert.True(t, ok)
	}
	assert.Contains(t, entries, "api")
	assert.Contains(t, entries, "worker")
}

func TestStacksChildrenReturnTheAwaitErrorInARegistration(t *testing.T) {
	newFakeService(t)
	for _, entry := range []string{"api", "worker"} {
		request := memberRunRequest(false)
		request.Urn = childURN("app", entry)
		_, err := newServer(t).Create(request)
		requireAwaitError(t, err)
	}
}

func TestStacksOutputsDependOnEveryChild(t *testing.T) {
	newFakeService(t)
	resp, err := newServer(t).Construct(stacksRequest("app", false, twoEntries()))
	require.NoError(t, err)

	_, hasChildURNs := resp.State.GetOk("childUrns")
	assert.False(t, hasChildURNs, "the child URNs are not public outputs")
	outputs := resp.State.Get("outputs")
	assert.ElementsMatch(t, []resource.URN{childURN("app", "api"), childURN("app", "worker")}, outputs.Dependencies())
	api := outputs.AsMap().Get("api")
	assert.Equal(t, []resource.URN{childURN("app", "api")}, api.Dependencies())
}

func TestStacksChildNamesAreStable(t *testing.T) {
	newFakeService(t)
	names := func(component string, entries ...string) []string {
		in := map[string]property.Value{}
		for _, entry := range entries {
			in[entry] = stacksEntry("org/shop/"+entry, nil)
		}
		var out []string
		server := newMockServer(t, &out)
		_, err := server.Construct(stacksRequest(component, true, in))
		require.NoError(t, err)
		return out
	}
	assert.ElementsMatch(t, []string{"app-api#prod"}, names("app-api", "prod"))
	assert.ElementsMatch(t, []string{"app#api-prod"}, names("app", "api-prod"))
	// Adding an entry keeps the existing names.
	assert.Subset(t, names("app", "api", "worker", "batch"), names("app", "api", "worker"))
}

func newMockServer(t *testing.T, names *[]string) integration.Server {
	t.Helper()
	provider, err := newProvider()
	require.NoError(t, err)
	// The engine registers children concurrently.
	var mu sync.Mutex
	server, err := integration.NewServer(t.Context(), "delivery", semver.MustParse("0.0.1"),
		integration.WithProvider(provider),
		integration.WithMocks(&integration.MockResourceMonitor{
			NewResourceF: func(args integration.MockResourceArgs) (string, property.Map, error) {
				if args.TypeToken == typeMemberRun {
					mu.Lock()
					*names = append(*names, args.Name)
					mu.Unlock()
				}
				return args.Name, property.NewMap(nil), nil
			},
		}))
	require.NoError(t, err)
	return server
}

func TestStacksPropertyDependenciesBelongToTheirEntry(t *testing.T) {
	service := newFakeService(t)
	other := urnOf("delivery:index:Job", "other")
	entries := twoEntries()
	entries["api"] = stacksEntry("org/shop/api", map[string]property.Value{
		"config": property.New(property.NewMap(map[string]property.Value{
			"image": property.New("x").WithDependencies([]resource.URN{other}),
		})),
	})

	_, err := newServer(t).Construct(stacksRequest("app", false, entries))
	require.NoError(t, err)

	pd := service.declared()[0]["propertyDependencies"].(map[string]any)
	assert.Equal(t, []any{string(other)}, pd["stacks.api"])
	assert.NotContains(t, pd, "stacks.worker")
	assert.NotContains(t, pd, "stacks")
}

func TestStacksStructuredAndSecretOutputsKeepTheirShape(t *testing.T) {
	newFakeService(t)
	provider, err := newProvider()
	require.NoError(t, err)
	server, err := integration.NewServer(t.Context(), "delivery", semver.MustParse("0.0.1"),
		integration.WithProvider(provider),
		integration.WithMocks(&integration.MockResourceMonitor{
			NewResourceF: func(args integration.MockResourceArgs) (string, property.Map, error) {
				return args.Name, property.NewMap(map[string]property.Value{
					"outputs": property.New(property.NewMap(map[string]property.Value{
						"ports": property.New(property.NewArray([]property.Value{property.New(80.0), property.New(443.0)})),
						"token": property.New("t"),
					})),
				}), nil
			},
		}))
	require.NoError(t, err)

	resp, err := server.Construct(stacksRequest("app", false, twoEntries()))
	require.NoError(t, err)
	api := resp.State.Get("outputs").AsMap().Get("api")
	require.True(t, api.IsMap(), "%v", api)
	ports := api.AsMap().Get("ports")
	require.True(t, ports.IsArray())
	assert.Equal(t, 2, ports.AsArray().Len())
	assert.False(t, resp.State.Get("outputs").Secret(), "plain outputs stay plain")
}

// childOutputsServer answers every MemberRun child with the outputs that the entry (or the Job) is given. A secret
// value reaches the SDK as a nested secret, like the provider state of a MemberRun.
func childOutputsServer(t *testing.T, outputs func(name string) property.Map) integration.Server {
	t.Helper()
	provider, err := newProvider()
	require.NoError(t, err)
	server, err := integration.NewServer(t.Context(), "delivery", semver.MustParse("0.0.1"),
		integration.WithProvider(provider),
		integration.WithMocks(&integration.MockResourceMonitor{
			NewResourceF: func(args integration.MockResourceArgs) (string, property.Map, error) {
				return args.Name, property.NewMap(map[string]property.Value{"outputs": property.New(outputs(args.Name))}), nil
			},
		}))
	require.NoError(t, err)
	return server
}

func outputsWith(secret bool) property.Map {
	return property.NewMap(map[string]property.Value{
		"url":   property.New("https://x"),
		"token": property.New("t").WithSecret(secret),
	})
}

func TestJobOutputsAreSecretOnlyWhenOneIsSecret(t *testing.T) {
	for _, secret := range []bool{false, true} {
		newFakeService(t)
		server := childOutputsServer(t, func(string) property.Map { return outputsWith(secret) })
		resp, err := server.Construct(jobRequest(false))
		require.NoError(t, err)
		outputs := resp.State.Get("outputs")
		assert.Equal(t, secret, outputs.Secret(), "secret output: %v", secret)
		assert.Equal(t, "https://x", outputs.AsMap().Get("url").AsString())
	}
}

func TestStacksOutputsAreSecretWhenAnyEntryHasASecret(t *testing.T) {
	for name, secrets := range map[string][2]bool{
		"none": {false, false}, "one": {false, true}, "both": {true, true},
	} {
		t.Run(name, func(t *testing.T) {
			newFakeService(t)
			server := childOutputsServer(t, func(child string) property.Map {
				return outputsWith(secrets[map[string]int{"app#api": 0, "app#worker": 1}[child]])
			})
			resp, err := server.Construct(stacksRequest("app", false, twoEntries()))
			require.NoError(t, err)
			assert.Equal(t, secrets[0] || secrets[1], resp.State.Get("outputs").Secret())
			assert.Equal(t, "https://x", resp.State.Get("outputs").AsMap().Get("api").AsMap().Get("url").AsString())
		})
	}
}

func TestStacksValidation(t *testing.T) {
	newFakeService(t)
	server := newServer(t)
	construct := func(entries map[string]property.Value) error {
		_, err := server.Construct(stacksRequest("app", true, entries))
		return err
	}

	require.NoError(t, construct(twoEntries()))
	require.ErrorContains(t, construct(map[string]property.Value{}), "at least one entry")
	require.ErrorContains(t, construct(map[string]property.Value{"a#b": stacksEntry("org/p/s", nil)}), "entry name")
	for _, bad := range []string{"s", "a/b/c/d", "/s", "p/", "org//s"} {
		require.ErrorContains(t, construct(map[string]property.Value{"a": stacksEntry(bad, nil)}), fmt.Sprintf("stack must be project/stack, not %q", bad), bad)
	}
	require.NoError(t, construct(map[string]property.Value{"a": stacksEntry("p/s", nil)}))
	require.ErrorContains(t, construct(map[string]property.Value{
		"a": stacksEntry("p/s", nil), "b": stacksEntry("org/p/s", nil),
	}), `entries "a" and "b" both update stack org/p/s`)
	require.ErrorContains(t, construct(map[string]property.Value{
		"a": stacksEntry("org/p/s", nil), "b": stacksEntry("org/p/s", nil),
	}), `entries "a" and "b" both update stack org/p/s`)
	// Unknown stacks are skipped.
	unknownStack := stacksEntry("x", nil).AsMap().Set("stack", property.New(property.Computed))
	require.NoError(t, construct(map[string]property.Value{
		"a": property.New(unknownStack), "b": property.New(unknownStack),
	}))

	request := stacksRequest("app", true, twoEntries())
	request.ResourceHooks = &p.ResourceHooksBinding{OnError: []string{"retry"}}
	_, err := server.Construct(request)
	require.EqualError(t, err, "Delivery resources do not support error hooks. Use a retry or a new run.")
}

func TestSchemaOfStacks(t *testing.T) {
	resp, err := newServer(t).GetSchema(p.GetSchemaRequest{})
	require.NoError(t, err)
	assert.Contains(t, resp.Schema, "delivery:index:Stacks")
	assert.NotContains(t, resp.Schema, "childUrns")
}

func TestStacksWithDependenciesOrSecretStillGetOneChildPerEntry(t *testing.T) {
	newFakeService(t)
	other := urnOf("delivery:index:Job", "other")
	for name, wrap := range map[string]func(property.Value) property.Value{
		"dependencies": func(v property.Value) property.Value { return v.WithDependencies([]resource.URN{other}) },
		"secret":       func(v property.Value) property.Value { return v.WithSecret(true) },
	} {
		t.Run(name, func(t *testing.T) {
			request := stacksRequest("app", true, twoEntries())
			request.Inputs = request.Inputs.Set("stacks", wrap(request.Inputs.Get("stacks")))
			var children []property.Map
			_, err := newServerCapturing(t, &children).Construct(request)
			require.NoError(t, err)
			assert.Len(t, children, 2)
		})
	}
}

func TestStacksNeedAKnownMap(t *testing.T) {
	newFakeService(t)
	request := stacksRequest("app", true, twoEntries())
	request.Inputs = request.Inputs.Set("stacks", property.New(property.Computed))
	_, err := newServer(t).Construct(request)
	require.ErrorContains(t, err, "known map")
}

func TestStacksEntryGetsItsOwnDependencies(t *testing.T) {
	service := newFakeService(t)
	a, b, c := urnOf("delivery:index:GitHubSource", "a"), urnOf("delivery:index:GitHubSource", "b"), urnOf("delivery:index:Job", "c")
	build := func(topLevel ...resource.URN) map[string]any {
		entries := map[string]property.Value{
			"api":    stacksEntry("org/shop/api", nil).WithDependencies([]resource.URN{a}),
			"worker": stacksEntry("org/shop/worker", nil).WithDependencies([]resource.URN{b}),
		}
		request := stacksRequest("app", false, entries)
		request.Inputs = request.Inputs.Set("stacks", property.New(property.NewMap(entries)).WithDependencies(topLevel))
		_, err := newServer(t).Construct(request)
		require.NoError(t, err)
		declarations := service.declared()
		return declarations[len(declarations)-1]["propertyDependencies"].(map[string]any)
	}
	pd := build(a, b)
	assert.Equal(t, []any{string(a)}, pd["stacks.api"])
	assert.Equal(t, []any{string(b)}, pd["stacks.worker"])
	// A dependency that no entry shows goes to every entry.
	pd = build(a, b, c)
	assert.Equal(t, []any{string(a), string(c)}, pd["stacks.api"])
	assert.Equal(t, []any{string(b), string(c)}, pd["stacks.worker"])
}

func TestStacksChildCarriesTheFullDeclarationOfItsEntry(t *testing.T) {
	newFakeService(t)
	other := urnOf("delivery:index:Job", "other")
	entries := twoEntries()
	entries["api"] = entries["api"].WithDependencies([]resource.URN{other})
	entries["worker"] = entries["worker"].WithDependencies([]resource.URN{other})
	request := stacksRequest("app", true, entries)
	request.Inputs = request.Inputs.Set("stage", reference(typeStage, "dev").WithDependencies([]resource.URN{other}))
	var children []property.Map
	_, err := newServerCapturing(t, &children).Construct(request)
	require.NoError(t, err)

	require.Len(t, children, 2)
	for _, child := range children {
		entry, notEntry := child.Get("entry").AsString(), "api"
		if entry == "api" {
			notEntry = "worker"
		}
		declaration := child.Get("declaration").AsMap()
		inputs := declaration.Get("inputs").AsMap()
		assert.True(t, inputs.Get("stage").IsResourceReference(), "the stage stays")
		assert.Equal(t, 1, inputs.Get("stacks").AsMap().Len())
		pd := declaration.Get("propertyDependencies").AsMap()
		_, ok := pd.GetOk("stage")
		assert.True(t, ok, "the stage dependencies stay")
		_, ok = pd.GetOk("stacks." + entry)
		assert.True(t, ok)
		_, ok = pd.GetOk("stacks." + notEntry)
		assert.False(t, ok)
	}
}

// The outputs of a group are one value that holds every entry, and they resolve once every entry is known. (The
// engine rejects a secret without a value, which an unknown secret output sends; the SDK mocks do not reproduce
// that, so the awaiting case is covered by the engine tests in u1_test.go.)
func TestStacksGroupOutputsHoldEveryEntry(t *testing.T) {
	var got map[string]map[string]any
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		api := pulumi.ToSecret(pulumi.Map{"pool": pulumi.String("blue")}).(pulumi.MapOutput)
		worker := pulumi.ToSecret(pulumi.Map{"pool": pulumi.String("green")}).(pulumi.MapOutput)
		ctx.Export("done", groupOutputs([]string{"api", "worker"}, []any{api, worker}).ApplyT(func(v map[string]map[string]any) int {
			got = v
			return 0
		}))
		return nil
	}, pulumi.WithMocks("project", "stack", sdkMocks{}))
	require.NoError(t, err)
	assert.Equal(t, map[string]map[string]any{"api": {"pool": "blue"}, "worker": {"pool": "green"}}, got)
}

type sdkMocks struct{}

func (sdkMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	return args.Name, args.Inputs, nil
}
func (sdkMocks) Call(pulumi.MockCallArgs) (resource.PropertyMap, error) { return nil, nil }

func TestFinishStacksKeepsUnknownOutputsUnknown(t *testing.T) {
	children := property.NewMap(map[string]property.Value{
		"api": property.New(string(childURN("app", "api"))), "worker": property.New(string(childURN("app", "worker"))),
	})
	state := property.NewMap(map[string]property.Value{
		"childUrns": property.New(children),
		"outputs":   property.New(property.Computed),
	})
	// The SDK leaves an unknown value out of the state: a missing value is unknown too.
	for _, in := range []property.Map{state, state.Delete("outputs")} {
		finished := finishStacks(in, children)

		outputs := finished.Get("outputs")
		assert.True(t, outputs.IsComputed(), "%v", outputs)
		assert.ElementsMatch(t, []resource.URN{childURN("app", "api"), childURN("app", "worker")}, outputs.Dependencies())
		_, hasChildURNs := finished.GetOk("childUrns")
		assert.False(t, hasChildURNs)
	}
}

// The engine derives the URN of a child from the full type chain of the parent. A member inside another
// component has a longer chain.
func TestMemberChildURNKeepsTheParentTypeChain(t *testing.T) {
	member := urn.New("stack", "project", "my:mod:Outer", typeJob, "build")
	got := memberChildURN(MemberRunArgs{Member: string(member)})
	assert.Equal(t, "urn:pulumi:stack::project::my:mod:Outer$"+typeJob+"$"+typeMemberRun+"::build", string(got))

	stacks := urn.New("stack", "project", "my:mod:Outer", typeStacks, "app")
	got = memberChildURN(MemberRunArgs{Member: string(stacks), Entry: "api"})
	assert.Equal(t, "urn:pulumi:stack::project::my:mod:Outer$"+typeStacks+"$"+typeMemberRun+"::app#api", string(got))
}

func TestFinishStateKeepsJobOutputsUnknown(t *testing.T) {
	child := string(urnOf(typeJob+"$"+typeMemberRun, "build"))
	// The SDK leaves an unknown value out of the state: a missing value is unknown too.
	finished := finishState(typeJob, property.NewMap(map[string]property.Value{"childUrn": property.New(child)}), "", nil, nil, property.Map{})

	outputs := finished.Get("outputs")
	assert.True(t, outputs.IsComputed(), "%v", outputs)
	assert.Equal(t, []resource.URN{resource.URN(child)}, outputs.Dependencies())
	_, hasChildURN := finished.GetOk("childUrn")
	assert.False(t, hasChildURN)
}

func TestTwoPartStackIsQualifiedWithTheOrganization(t *testing.T) {
	service := newFakeService(t)
	var children []property.Map
	_, err := newServerCapturing(t, &children).Construct(stacksRequest("app", false, map[string]property.Value{"a": stacksEntry("p/s", nil)}))
	require.NoError(t, err)
	declarations := service.declared()
	require.Len(t, declarations, 1)
	stacks := declarations[0]["inputs"].(map[string]any)["stacks"].(map[string]any)
	assert.Equal(t, "org/p/s", stacks["a"].(map[string]any)["stack"])
	// The MemberRun child carries the qualified name too, so switching forms leaves it unchanged.
	require.Len(t, children, 1)
	child := children[0].Get("declaration").AsMap().Get("inputs").AsMap().Get("stacks").AsMap().Get("a").AsMap()
	assert.Equal(t, "org/p/s", child.Get("stack").AsString())
}

func TestQualifyStackNamesKeepsSecretsAndDependencies(t *testing.T) {
	dep := urn.URN("urn:pulumi:s::p::t::n")
	marked := func(s string) property.Value {
		return property.New(s).WithSecret(true).WithDependencies([]urn.URN{dep})
	}
	stacks := property.NewMap(map[string]property.Value{
		"a": property.New(property.NewMap(map[string]property.Value{"stack": marked("p/s")})).WithSecret(true).WithDependencies([]urn.URN{dep}),
		"b": property.New(property.Computed),
		"c": property.New(property.NewMap(map[string]property.Value{"stack": property.New(property.Computed)})),
	})
	inputs := qualifyStackNames(typeStacks, property.NewMap(map[string]property.Value{
		"stacks": property.New(stacks).WithSecret(true).WithDependencies([]urn.URN{dep}),
	}), "org")
	assert.True(t, inputs.Get("stacks").Secret())
	assert.Equal(t, []urn.URN{dep}, inputs.Get("stacks").Dependencies())
	entries := inputs.Get("stacks").AsMap()
	a := entries.Get("a")
	assert.True(t, a.Secret())
	assert.Equal(t, []urn.URN{dep}, a.Dependencies())
	assert.Equal(t, marked("org/p/s"), a.AsMap().Get("stack"))
	assert.True(t, entries.Get("b").IsComputed())
	assert.True(t, entries.Get("c").AsMap().Get("stack").IsComputed())

	source := qualifyStackNames(typePipelineSource, property.NewMap(map[string]property.Value{"pipeline": marked("platform/main")}), "org")
	assert.Equal(t, marked("org/platform/main"), source.Get("pipeline"))
}

func TestQualifyStackNamesLeavesOtherValuesUnchanged(t *testing.T) {
	for _, name := range []string{"s", "a/b/c", "a/b/c/d", "/s", "p/", ""} {
		inputs := property.NewMap(map[string]property.Value{"pipeline": property.New(name)})
		assert.Equal(t, name, qualifyStackNames(typePipelineSource, inputs, "org").Get("pipeline").AsString(), name)
	}
	inputs := property.NewMap(map[string]property.Value{"pipeline": property.New("p/s")})
	assert.Equal(t, "p/s", qualifyStackNames(typePipelineSource, inputs, "").Get("pipeline").AsString())
}

func runnerSettings() map[string]property.Value {
	return map[string]property.Value{
		"image": property.New(property.NewMap(map[string]property.Value{
			"reference": property.New("pulumi/pulumi-nodejs:3.272.0"),
			"credentials": property.New(property.NewMap(map[string]property.Value{
				"username": property.New("robot"),
				"password": property.New("hunter2").WithSecret(true),
			})),
		})),
		"preRunCommands": property.New(property.NewArray([]property.Value{property.New("npm ci"), property.New("make gen")})),
	}
}

func checkRunnerSettings(t *testing.T, d map[string]any, inputsOf func(resource.PropertyMap) resource.PropertyMap) {
	decoded, err := stack.DeserializeProperties(d["inputs"].(map[string]any), config.NewErrorCrypter("no ciphertext"))
	require.NoError(t, err)
	in := inputsOf(decoded)
	image := in["image"].ObjectValue()
	assert.Equal(t, "pulumi/pulumi-nodejs:3.272.0", image["reference"].StringValue())
	credentials := image["credentials"].ObjectValue()
	assert.Equal(t, "robot", credentials["username"].StringValue())
	assert.True(t, credentials["password"].IsSecret())
	assert.Equal(t, "hunter2", credentials["password"].SecretValue().Element.StringValue())
	assert.Equal(t, []resource.PropertyValue{resource.NewProperty("npm ci"), resource.NewProperty("make gen")}, in["preRunCommands"].ArrayValue())
}

func TestJobRunnerSettingsAreDeclared(t *testing.T) {
	service := newFakeService(t)
	request := jobRequest(false)
	for k, v := range runnerSettings() {
		request.Inputs = request.Inputs.Set(k, v)
	}
	request.Inputs = request.Inputs.Set("runtime", reference("delivery:index:WorkflowsRuntime", "pool"))
	_, err := newServer(t).Construct(request)
	require.NoError(t, err)

	d := service.declared()[0]
	checkRunnerSettings(t, d, func(m resource.PropertyMap) resource.PropertyMap { return m })
	decoded, err := stack.DeserializeProperties(d["inputs"].(map[string]any), config.NewErrorCrypter("no ciphertext"))
	require.NoError(t, err)
	assert.True(t, decoded["runtime"].IsResourceReference())
}

func TestStacksEntryRunnerSettingsAreDeclared(t *testing.T) {
	service := newFakeService(t)
	entries := twoEntries()
	entries["api"] = stacksEntry("org/shop/api", runnerSettings())
	_, err := newServer(t).Construct(stacksRequest("app", false, entries))
	require.NoError(t, err)

	checkRunnerSettings(t, service.declared()[0], func(m resource.PropertyMap) resource.PropertyMap {
		return m["stacks"].ObjectValue()["api"].ObjectValue()
	})
}

func TestImageNeedsAReference(t *testing.T) {
	newFakeService(t)
	server := newServer(t)
	empty := map[string]property.Value{"image": property.New(property.NewMap(map[string]property.Value{"reference": property.New("")}))}

	request := jobRequest(true)
	request.Inputs = request.Inputs.Set("image", empty["image"])
	_, err := server.Construct(request)
	require.ErrorContains(t, err, "image.reference")

	_, err = server.Construct(stacksRequest("app", true, map[string]property.Value{"a": stacksEntry("org/p/s", empty)}))
	require.ErrorContains(t, err, "image.reference")
}

func plainPassword() map[string]property.Value {
	settings := runnerSettings()
	credentials := property.New(property.NewMap(map[string]property.Value{
		"username": property.New("robot"),
		"password": property.New("hunter2"),
	}))
	settings["image"] = property.New(property.NewMap(map[string]property.Value{
		"reference":   property.New("pulumi/pulumi-nodejs:3.272.0"),
		"credentials": credentials,
	}))
	return settings
}

func TestJobPlainRegistryPasswordIsDeclaredSecret(t *testing.T) {
	service := newFakeService(t)
	request := jobRequest(false)
	for k, v := range plainPassword() {
		request.Inputs = request.Inputs.Set(k, v)
	}
	_, err := newServer(t).Construct(request)
	require.NoError(t, err)

	checkRunnerSettings(t, service.declared()[0], func(m resource.PropertyMap) resource.PropertyMap { return m })
}

func TestStacksEntryPlainRegistryPasswordIsDeclaredSecret(t *testing.T) {
	service := newFakeService(t)
	entries := twoEntries()
	entries["api"] = stacksEntry("org/shop/api", plainPassword())
	_, err := newServer(t).Construct(stacksRequest("app", false, entries))
	require.NoError(t, err)

	checkRunnerSettings(t, service.declared()[0], func(m resource.PropertyMap) resource.PropertyMap {
		return m["stacks"].ObjectValue()["api"].ObjectValue()
	})
}

func TestJobRuntimeMustBeAWorkflowsRuntimeReference(t *testing.T) {
	newFakeService(t)
	server := newServer(t)
	for name, runtime := range map[string]property.Value{
		"plain string": property.New("pool"),
		"wrong type":   reference("delivery:index:GitHubSource", "code"),
	} {
		t.Run(name, func(t *testing.T) {
			request := jobRequest(true)
			request.Inputs = request.Inputs.Set("runtime", runtime)
			_, err := server.Construct(request)
			require.ErrorContains(t, err, "runtime")
		})
	}
}

func TestUnknownImageAndReferencePassInPreview(t *testing.T) {
	newFakeService(t)
	server := newServer(t)
	for name, image := range map[string]property.Value{
		"image":     property.New(property.Computed),
		"reference": property.New(property.NewMap(map[string]property.Value{"reference": property.New(property.Computed)})),
	} {
		t.Run(name, func(t *testing.T) {
			request := jobRequest(true)
			request.Inputs = request.Inputs.Set("image", image)
			_, err := server.Construct(request)
			require.NoError(t, err)
		})
	}
}

// secretAt reports whether the value at path is a secret, or sits inside one.
func secretAt(v property.Value, path ...string) bool {
	for _, key := range path {
		if v.Secret() {
			return true
		}
		v = v.AsMap().Get(key)
	}
	return v.Secret()
}

func TestStacksEntrySecretsStaySecretInTheChild(t *testing.T) {
	newFakeService(t)
	other := urnOf("delivery:index:Job", "other")
	for name, wrap := range map[string]func(property.Value) property.Value{
		"known":        func(v property.Value) property.Value { return v },
		"dependencies": func(v property.Value) property.Value { return v.WithDependencies([]resource.URN{other}) },
		"secret":       func(v property.Value) property.Value { return v.WithSecret(true) },
	} {
		t.Run(name, func(t *testing.T) {
			environment := property.NewMap(map[string]property.Value{"TOKEN": property.New("s3cret").WithSecret(true)})
			request := stacksRequest("app", false, map[string]property.Value{
				"api": stacksEntry("org/shop/api", map[string]property.Value{"environment": property.New(environment)}),
			})
			request.Inputs = request.Inputs.Set("stacks", wrap(request.Inputs.Get("stacks")))
			var children []property.Map
			_, err := newServerCapturing(t, &children).Construct(request)
			require.NoError(t, err)
			require.Len(t, children, 1)
			declaration := children[0].Get("declaration")
			assert.True(t, secretAt(declaration, "inputs", "stacks", "api", "environment", "TOKEN"), "%v", declaration)
		})
	}
}

// Pulumi HCL sends every unset field of an object as null.
func TestNullOptionalInputsAreOmitted(t *testing.T) {
	newFakeService(t)
	server := newServer(t)
	null := property.New(property.Null)
	for _, field := range []string{"runtime", "image", "environment", "timeout", "preRunCommands", "config"} {
		t.Run(field, func(t *testing.T) {
			entries := map[string]property.Value{"a": stacksEntry("org/p/s", map[string]property.Value{field: null})}
			_, err := server.Construct(stacksRequest("app", true, entries))
			require.NoError(t, err)
			job := jobRequest(true)
			job.Inputs = job.Inputs.Set(field, null)
			_, err = server.Construct(job)
			require.NoError(t, err)
		})
	}
}

func TestDropNulls(t *testing.T) {
	null := property.New(property.Null)
	dep := urnOf(typeStage, "dev")
	obj := func(fields map[string]property.Value) property.Value { return property.New(property.NewMap(fields)) }

	got := dropNulls(property.NewMap(map[string]property.Value{
		"plain":    null,
		"secret":   null.WithSecret(true),
		"depends":  null.WithDependencies([]urn.URN{dep}),
		"image":    obj(map[string]property.Value{"reference": property.New("r"), "credentials": null}),
		"flagged":  obj(map[string]property.Value{"a": null}).WithSecret(true).WithDependencies([]urn.URN{dep}),
		"list":     property.New(property.NewArray([]property.Value{obj(map[string]property.Value{"a": null})})),
		"stacks":   obj(map[string]property.Value{"gone": null}),
		"computed": property.New(property.Computed),
	}))

	assert.Equal(t, property.NewMap(map[string]property.Value{
		"secret":   null.WithSecret(true),
		"depends":  null.WithDependencies([]urn.URN{dep}),
		"image":    obj(map[string]property.Value{"reference": property.New("r")}),
		"flagged":  obj(map[string]property.Value{}).WithSecret(true).WithDependencies([]urn.URN{dep}),
		"list":     property.New(property.NewArray([]property.Value{obj(map[string]property.Value{})})),
		"stacks":   obj(map[string]property.Value{}),
		"computed": property.New(property.Computed),
	}), got)
}
