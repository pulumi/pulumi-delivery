// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"testing"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeclarationsCarryTheRegisterMode(t *testing.T) {
	service := newFakeService(t)
	server := newServer(t)

	for _, mode := range []string{"register", ""} {
		declarationMode = mode
		_, err := server.Construct(jobRequest(false))
		require.NoError(t, err)
		_, err = server.Construct(pipelineRequest(false))
		require.NoError(t, err)
	}
	declarationMode = ""

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
}

func TestPipelineDeclaresItsServicePrincipal(t *testing.T) {
	service := newFakeService(t)
	server := newServer(t)

	_, err := server.Construct(pipelineRequest(false))
	require.NoError(t, err)
	req := pipelineRequest(false)
	req.Inputs = property.NewMap(map[string]property.Value{"servicePrincipal": property.New("deployer")})
	_, err = server.Construct(req)
	require.NoError(t, err)

	declared := service.declared()
	require.Len(t, declared, 2)
	assert.NotContains(t, declared[0]["inputs"], "servicePrincipal")
	assert.Equal(t, "deployer", declared[1]["inputs"].(map[string]any)["servicePrincipal"])
}
