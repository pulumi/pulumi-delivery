// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
)

func jobChildRequest(dryRun bool) p.CreateRequest {
	request := memberRunRequest(dryRun)
	request.Urn = urnOf(typeJob+"$"+typeMemberRun, "build")
	return request
}

func stacksChildRequest(dryRun bool, entry string) p.CreateRequest {
	request := memberRunRequest(dryRun)
	request.Urn = childURN("app", entry)
	request.Properties = request.Properties.
		Set("member", property.New(string(urnOf(typeStacks, "app")))).
		Set("entry", property.New(entry))
	return request
}

func updateOf(c p.CreateRequest) p.UpdateRequest {
	return p.UpdateRequest{
		ID: "build", Urn: c.Urn, DryRun: c.DryRun, Inputs: c.Properties, OldInputs: c.Properties,
		State: c.Properties.Set("outputs", property.New(property.NewMap(nil)).WithSecret(true)),
	}
}

func answer(service *fakeService, entry string, code int, body string) {
	if service.memberAnswers == nil {
		service.memberAnswers = map[string]memberAnswer{}
	}
	service.memberAnswers[entry] = memberAnswer{code, body}
}

func TestMemberPreviewReturnsUnknownOutputsForCreateAndUpdate(t *testing.T) {
	service := newFakeService(t)
	service.run = true
	server := newServer(t)

	created, err := server.Create(jobChildRequest(true))
	require.NoError(t, err)
	assert.True(t, created.Properties.Get("outputs").IsComputed())

	updated, err := server.Update(updateOf(jobChildRequest(true)))
	require.NoError(t, err)
	outputs := updated.Properties.Get("outputs")
	assert.True(t, outputs.IsComputed())
	assert.False(t, outputs.Secret(), "an unknown output is not secret")
	assert.Empty(t, service.memberStarts)
	assert.Empty(t, service.requests)
}

func TestMemberRegistrationStartsNothing(t *testing.T) {
	service := newFakeService(t) // GET run answers 404
	server := newServer(t)

	_, err := server.Create(jobChildRequest(false))
	requireAwaitError(t, err)
	_, err = server.Update(updateOf(jobChildRequest(false)))
	requireAwaitError(t, err)
	assert.Empty(t, service.memberStarts)
}

func TestMemberStartBodyForAJobAndAStacksEntry(t *testing.T) {
	service := newFakeService(t)
	service.run = true
	server := newServer(t)

	_, err := server.Create(jobChildRequest(false))
	requireAwaitError(t, err)
	request := stacksChildRequest(false, "api")
	_, err = server.Create(request)
	requireAwaitError(t, err)

	require.Len(t, service.memberStarts, 2)
	assert.Equal(t, map[string]any{
		"protocolVersion": float64(2), "urn": string(urnOf(typeJob, "build")), "entry": "",
		"childUrn": string(urnOf(typeJob+"$"+typeMemberRun, "build")),
	}, service.memberStarts[0])
	assert.Equal(t, map[string]any{
		"protocolVersion": float64(2), "urn": string(urnOf(typeStacks, "app")), "entry": "api",
		"childUrn": string(request.Urn),
	}, service.memberStarts[1])
}

func TestMemberStatusesMapToTheAwaitError(t *testing.T) {
	for status, reason := range map[string]string{
		"waiting": "The run is ending.", "running": "It is running.", "failed": "The job failed.",
	} {
		t.Run(status, func(t *testing.T) {
			service := newFakeService(t)
			service.run = true
			answer(service, "", http.StatusOK, `{"status":"`+status+`","reason":"`+reason+`"}`)
			server := newServer(t)

			_, err := server.Create(jobChildRequest(false))
			requireAwaitError(t, err)
			assert.Contains(t, err.Error(), reason)
			_, err = server.Update(updateOf(jobChildRequest(false)))
			requireAwaitError(t, err)
			assert.Contains(t, err.Error(), reason)
		})
	}
}

func TestMemberSucceededReturnsTheOutputs(t *testing.T) {
	service := newFakeService(t)
	service.run = true
	answer(service, "api", http.StatusOK, fmt.Sprintf(`{"status":"succeeded","outputs":{
		"pool":"blue",
		"token":{"%s":"%s","plaintext":"\"hunter2\""},
		"nested":{"ids":[1,2],"name":"x"}}}`, resource.SigKey, resource.SecretSig))
	server := newServer(t)

	for _, run := range []func() (property.Map, error){
		func() (property.Map, error) {
			r, err := server.Create(stacksChildRequest(false, "api"))
			return r.Properties, err
		},
		func() (property.Map, error) {
			r, err := server.Update(updateOf(stacksChildRequest(false, "api")))
			return r.Properties, err
		},
	} {
		props, err := run()
		require.NoError(t, err)
		outputs := props.Get("outputs")
		assert.False(t, outputs.Secret(), "only the marked output is secret")
		got := outputs.AsMap()
		assert.Equal(t, "blue", got.Get("pool").AsString())
		assert.False(t, got.Get("pool").Secret())
		assert.Equal(t, "hunter2", got.Get("token").AsString())
		assert.True(t, got.Get("token").Secret())
		assert.False(t, got.Get("nested").Secret())
		assert.Equal(t, "x", got.Get("nested").AsMap().Get("name").AsString())
		assert.Equal(t, 2, got.Get("nested").AsMap().Get("ids").AsArray().Len())
	}
}

func TestMemberStartClientErrorFailsWithTheServiceMessage(t *testing.T) {
	service := newFakeService(t)
	service.run = true
	answer(service, "", http.StatusConflict, `{"code":409,"message":"Declare the member first."}`)

	_, err := newServer(t).Create(jobChildRequest(false))
	require.ErrorContains(t, err, "Declare the member first.")
	assert.NotContains(t, err.Error(), "registered")
}

func TestMemberRunDiffAlwaysReportsAChange(t *testing.T) {
	newFakeService(t)
	request := jobChildRequest(false)
	resp, err := newServer(t).Diff(p.DiffRequest{
		ID: "build", Urn: request.Urn, Inputs: request.Properties, OldInputs: request.Properties,
		State: request.Properties.Set("outputs", property.New(property.NewMap(nil))),
	})
	require.NoError(t, err)
	assert.True(t, resp.HasChanges)
}
