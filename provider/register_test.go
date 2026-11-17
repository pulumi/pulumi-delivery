// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"testing"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setMode(t *testing.T, mode string) {
	old := declarationMode
	declarationMode = mode
	t.Cleanup(func() { declarationMode = old })
}

func TestDeclarationsCarryTheRegisterMode(t *testing.T) {
	service := newFakeService(t)
	server := newServer(t)

	for _, mode := range []string{"register", ""} {
		setMode(t, mode)
		_, err := server.Construct(jobRequest(false))
		require.NoError(t, err)
		_, err = server.Construct(pipelineRequest(false))
		require.NoError(t, err)
	}

	declared := service.declared()
	require.Len(t, declared, 4)
	assert.Equal(t, "register", declared[0]["mode"])
	assert.Equal(t, "register", declared[1]["mode"])
	assert.NotContains(t, declared[2], "mode")
	assert.NotContains(t, declared[3], "mode")
}

func TestRegisterModeFromTheEnvironment(t *testing.T) {
	for value, want := range map[string]string{"true": "register", "1": "register", "TRUE": "register", "false": "", "": "", "yes": ""} {
		t.Setenv("PULUMI_DELIVERY_REGISTER", value)
		assert.Equal(t, want, registerModeFromEnvironment(), value)
	}
}

func TestConfigChangeUpdatesInPlace(t *testing.T) {
	server := newServer(t)
	check := func(source string) property.Map {
		inputs := property.NewMap(map[string]property.Value{})
		if source != "" {
			inputs = property.NewMap(map[string]property.Value{"source": property.New(source)})
		}
		checked, err := server.CheckConfig(p.CheckRequest{Inputs: inputs})
		require.NoError(t, err)
		return checked.Inputs
	}
	a := check(`{"url":"https://git.example.com/acme/shop"}`)
	b := check(`{"url":"https://git.example.com/acme/shop","branch":"main","directory":"delivery"}`)
	none := check("")

	for _, c := range []struct{ old, new property.Map }{{a, b}, {none, a}, {a, none}} {
		resp, err := server.DiffConfig(p.DiffRequest{State: c.old, OldInputs: c.old, Inputs: c.new})
		require.NoError(t, err)
		assert.True(t, resp.HasChanges)
		require.NotEmpty(t, resp.DetailedDiff)
		for key, diff := range resp.DetailedDiff {
			assert.Equal(t, p.Update, diff.Kind, key)
		}
	}

	resp, err := server.DiffConfig(p.DiffRequest{State: a, OldInputs: a, Inputs: a})
	require.NoError(t, err)
	assert.False(t, resp.HasChanges)

	resp, err = server.DiffConfig(p.DiffRequest{State: a, OldInputs: a, Inputs: a.Set("version", property.New("1.2.3"))})
	require.NoError(t, err)
	assert.Equal(t, p.Update, resp.DetailedDiff["version"].Kind)
}

// The engine checks the new config, diffs it against the old state, and configures, all on one provider process.
func TestSourceAddedAndRemovedThroughDiff(t *testing.T) {
	const source = `{"url":"https://git.example.com/acme/shop","directory":"delivery"}`
	inputs := func(source string) property.Map {
		if source == "" {
			return property.NewMap(map[string]property.Value{})
		}
		return property.NewMap(map[string]property.Value{"source": property.New(source)})
	}
	for _, c := range []struct {
		name     string
		old, new string
		want     any
	}{
		{"added", "", source, map[string]any{"url": "https://git.example.com/acme/shop", "directory": "delivery"}},
		{"removed", source, "", nil},
		{"branch", "", `{"url":"https://git.example.com/acme/shop","branch":"release"}`,
			map[string]any{"url": "https://git.example.com/acme/shop", "branch": "release"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			service := newFakeService(t)
			old, err := newServer(t).CheckConfig(p.CheckRequest{Inputs: inputs(c.old)})
			require.NoError(t, err)

			server := newServer(t)
			checked, err := server.CheckConfig(p.CheckRequest{Inputs: inputs(c.new)})
			require.NoError(t, err)
			diff, err := server.DiffConfig(p.DiffRequest{State: old.Inputs, OldInputs: old.Inputs, Inputs: checked.Inputs})
			require.NoError(t, err)
			assert.True(t, diff.HasChanges)
			assert.Equal(t, p.Update, diff.DetailedDiff["source"].Kind)
			require.NoError(t, server.Configure(p.ConfigureRequest{Args: checked.Inputs}))
			_, err = server.Construct(pipelineRequest(false))
			require.NoError(t, err)

			declared := service.declared()
			require.NotEmpty(t, declared)
			got := declared[len(declared)-1]["inputs"].(map[string]any)["programSource"]
			assert.Equal(t, c.want, got)
		})
	}
}

func TestPipelineDeclaresItsServicePrincipal(t *testing.T) {
	const unknownMarker = "04da6b54-80e4-46f7-96ec-b56ff0331ba9"
	object := func(fields map[string]property.Value) property.Map {
		return property.NewMap(map[string]property.Value{"servicePrincipal": property.New(property.NewMap(fields))})
	}
	str := func(v string) property.Value { return property.New(v) }
	unk := property.New(property.Computed)
	cases := []struct {
		name     string
		inputs   property.Map
		want     any
		wantTeam any
		wantErr  string
	}{
		{name: "absent"},
		{name: "null", inputs: property.NewMap(map[string]property.Value{"servicePrincipal": property.New(property.Null)})},
		{name: "userId", inputs: object(map[string]property.Value{"userId": str("service-principal:1")}), want: "service-principal:1"},
		{name: "teamName", inputs: object(map[string]property.Value{"teamName": str("platform")}), wantTeam: "platform"},
		{name: "null userId and teamName", inputs: object(map[string]property.Value{
			"userId": property.New(property.Null), "teamName": str("platform")}), wantTeam: "platform"},
		{name: "empty teamName", inputs: object(map[string]property.Value{"teamName": str("")}),
			wantErr: "servicePrincipal needs userId or teamName"},
		{name: "number teamName", inputs: object(map[string]property.Value{"teamName": property.New(5.0)}),
			wantErr: "servicePrincipal.teamName must be a string"},
		{name: "secret teamName", inputs: object(map[string]property.Value{"teamName": str("platform").WithSecret(true)}),
			wantTeam: map[string]any{"4dabf18193072939515e22adb298388d": "1b47061264138c4ac30d75fd1eb44270", "plaintext": `"platform"`}},
		{name: "secret object teamName", inputs: property.NewMap(map[string]property.Value{"servicePrincipal": property.New(property.NewMap(
			map[string]property.Value{"teamName": str("platform")})).WithSecret(true)}),
			wantTeam: map[string]any{"4dabf18193072939515e22adb298388d": "1b47061264138c4ac30d75fd1eb44270", "plaintext": `"platform"`}},
		{name: "unknown teamName", inputs: object(map[string]property.Value{"teamName": unk}), wantTeam: unknownMarker},
		{name: "both", inputs: object(map[string]property.Value{"userId": str("a"), "teamName": str("b")}),
			wantErr: "servicePrincipal takes userId or teamName, not both"},
		{name: "user and empty team", inputs: object(map[string]property.Value{"userId": str("x"), "teamName": str("")}),
			wantErr: "servicePrincipal takes userId or teamName, not both"},
		{name: "empty", inputs: object(map[string]property.Value{}), wantErr: "servicePrincipal needs userId or teamName"},
		{name: "empty userId", inputs: object(map[string]property.Value{"userId": str("")}),
			wantErr: "servicePrincipal needs userId or teamName"},
		{name: "number userId", inputs: object(map[string]property.Value{"userId": property.New(5.0)}),
			wantErr: "servicePrincipal.userId must be a string"},
		{name: "secret", inputs: object(map[string]property.Value{"userId": str("service-principal:2").WithSecret(true)}),
			want: map[string]any{"4dabf18193072939515e22adb298388d": "1b47061264138c4ac30d75fd1eb44270", "plaintext": `"service-principal:2"`}},
		{name: "secret object", inputs: property.NewMap(map[string]property.Value{"servicePrincipal": property.New(property.NewMap(
			map[string]property.Value{"userId": str("service-principal:2")})).WithSecret(true)}),
			want: map[string]any{"4dabf18193072939515e22adb298388d": "1b47061264138c4ac30d75fd1eb44270", "plaintext": `"service-principal:2"`}},
		{name: "unknown userId", inputs: object(map[string]property.Value{"userId": unk}), want: unknownMarker},
		{name: "unknown object", inputs: property.NewMap(map[string]property.Value{"servicePrincipal": unk}), want: unknownMarker},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			service := newFakeService(t)
			server := newServer(t)
			req := pipelineRequest(false)
			if c.inputs.Len() > 0 {
				req.Inputs = c.inputs
			}
			_, err := server.Construct(req)
			if c.wantErr != "" {
				require.ErrorContains(t, err, c.wantErr)
				assert.Empty(t, service.declared())
				return
			}
			require.NoError(t, err)
			declared := service.declared()
			require.Len(t, declared, 1)
			sent := declared[0]["inputs"].(map[string]any)
			if c.want == nil {
				assert.NotContains(t, sent, "servicePrincipal")
			} else {
				assert.Equal(t, c.want, sent["servicePrincipal"])
			}
			if c.wantTeam == nil {
				assert.NotContains(t, sent, "team")
			} else {
				assert.Equal(t, c.wantTeam, sent["team"])
				assert.NotContains(t, sent, "servicePrincipal")
			}
		})
	}

	t.Run("preview", func(t *testing.T) {
		for _, inputs := range []property.Map{
			object(map[string]property.Value{"userId": unk}),
			property.NewMap(map[string]property.Value{"servicePrincipal": unk}),
		} {
			req := pipelineRequest(true)
			req.Inputs = inputs
			_, err := newServer(t).Construct(req)
			require.NoError(t, err)
		}
		req := pipelineRequest(true)
		req.Inputs = object(map[string]property.Value{"teamName": unk})
		_, err := newServer(t).Construct(req)
		require.NoError(t, err)
	})
}
