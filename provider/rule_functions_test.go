// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
)

func TestApprovalDefaultsCountToOne(t *testing.T) {
	resp, err := Approval{}.Invoke(context.Background(), infer.FunctionRequest[ApprovalArgs]{
		Input: ApprovalArgs{Approvers: []string{"alice"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Output.Kind != "approval" || resp.Output.Count != 1 {
		t.Fatalf("rule = %#v", resp.Output)
	}
}

func TestApprovalRejectsNegativeCount(t *testing.T) {
	_, err := Approval{}.Invoke(context.Background(), infer.FunctionRequest[ApprovalArgs]{
		Input: ApprovalArgs{Count: -1},
	})
	if err == nil {
		t.Fatal("expected an error for a negative count")
	}
}

func TestHTTPRequiresURLAndSuccessExpression(t *testing.T) {
	_, err := HTTP{}.Invoke(context.Background(), infer.FunctionRequest[HTTPArgs]{
		Input: HTTPArgs{URL: "https://example.com"},
	})
	if err == nil {
		t.Fatal("expected an error for a missing successExpression")
	}
	resp, err := HTTP{}.Invoke(context.Background(), infer.FunctionRequest[HTTPArgs]{
		Input: HTTPArgs{URL: "https://example.com", SuccessExpression: "status == 'ok'"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Output.Kind != "http" || resp.Output.URL != "https://example.com" {
		t.Fatalf("rule = %#v", resp.Output)
	}
}

func TestManualIsAlwaysValid(t *testing.T) {
	resp, err := Manual{}.Invoke(context.Background(), infer.FunctionRequest[ManualArgs]{Input: ManualArgs{Name: "dashboards", Description: "check the dashboards"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Output.Kind != "manual" || resp.Output.Name != "dashboards" || resp.Output.Description != "check the dashboards" {
		t.Fatalf("rule = %#v", resp.Output)
	}
}

// A manual gate reaches the service as the rule in the when of its stage.
func TestManualRuleOnTheWire(t *testing.T) {
	service := newFakeService(t)
	server := newServer(t)

	for _, args := range []map[string]property.Value{{"name": property.New("dashboards"), "description": property.New("check the dashboards")}, {}} {
		invoked, err := server.Invoke(p.InvokeRequest{Token: "delivery:index:manual", Args: property.NewMap(args)})
		require.NoError(t, err)
		require.Empty(t, invoked.Failures)

		request := stageRequest(property.New(invoked.Return))
		request.DryRun = false
		_, err = server.Construct(request)
		require.NoError(t, err)
	}

	declarations := service.declared()
	require.Len(t, declarations, 2)
	assert.Equal(t, map[string]any{"kind": "manual", "name": "dashboards", "description": "check the dashboards"},
		declarations[0]["inputs"].(map[string]any)["when"])
	assert.Equal(t, map[string]any{"kind": "manual"}, declarations[1]["inputs"].(map[string]any)["when"])
}

func TestDecisionRequiresAnAgent(t *testing.T) {
	_, err := Decision{}.Invoke(context.Background(), infer.FunctionRequest[DecisionArgs]{Input: DecisionArgs{Name: "review"}})
	require.EqualError(t, err, "rule.agent is required")

	inputs := map[string]any{"pullRequest": 12.0, "nested": map[string]any{"ok": true}}
	resp, err := Decision{}.Invoke(context.Background(), infer.FunctionRequest[DecisionArgs]{
		Input: DecisionArgs{Name: "review", Agent: "reviewer", Inputs: inputs},
	})
	require.NoError(t, err)
	assert.Equal(t, Rule{Kind: "decision", Name: "review", Agent: "reviewer", Inputs: inputs}, resp.Output)

	// A decision rule nests like any other rule.
	resp, err = AllOf{}.Invoke(context.Background(), infer.FunctionRequest[CombinedRuleArgs]{
		Input: CombinedRuleArgs{Rules: []Rule{{Kind: "decision", Agent: "reviewer"}, {Kind: "manual"}}},
	})
	require.NoError(t, err)
	assert.Equal(t, "decision", resp.Output.Rules[0].Kind)
	_, err = AllOf{}.Invoke(context.Background(), infer.FunctionRequest[CombinedRuleArgs]{
		Input: CombinedRuleArgs{Rules: []Rule{{Kind: "decision"}}},
	})
	require.EqualError(t, err, "rule.agent is required")
}

// A decision rule reaches the service with its agent and inputs, which may be unknown in a preview.
func TestDecisionRuleOnTheWire(t *testing.T) {
	service := newFakeService(t)
	server := newServer(t)
	rule := func(fields map[string]property.Value) property.Value { return property.New(property.NewMap(fields)) }

	invoked, err := server.Invoke(p.InvokeRequest{Token: "delivery:index:decision", Args: property.NewMap(map[string]property.Value{
		"agent":  property.New("reviewer"),
		"inputs": property.New(property.NewMap(map[string]property.Value{"pullRequest": property.New(12.0)})),
	})})
	require.NoError(t, err)
	require.Empty(t, invoked.Failures)
	request := stageRequest(property.New(invoked.Return))
	request.DryRun = false
	_, err = server.Construct(request)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"kind": "decision", "agent": "reviewer", "inputs": map[string]any{"pullRequest": 12.0}},
		service.declared()[0]["inputs"].(map[string]any)["when"])

	// Unknown inputs pass, and an unknown agent is not checked.
	for _, when := range []property.Value{
		rule(map[string]property.Value{"kind": property.New("decision"), "agent": property.New("reviewer"), "inputs": property.New(property.Computed)}),
		rule(map[string]property.Value{"kind": property.New("decision"), "agent": property.New(property.Computed)}),
	} {
		_, err = server.Construct(stageRequest(when))
		require.NoError(t, err)
	}
	for message, when := range map[string]property.Value{
		"rule.agent is required":        rule(map[string]property.Value{"kind": property.New("decision")}),
		"rule.inputs must be an object": rule(map[string]property.Value{"kind": property.New("decision"), "agent": property.New("reviewer"), "inputs": property.New("fields")}),
	} {
		_, err = server.Construct(stageRequest(when))
		require.EqualError(t, err, message)
	}
}

func TestAllOfAndAnyOfRequireAtLeastOneRule(t *testing.T) {
	if _, err := (AllOf{}).Invoke(context.Background(), infer.FunctionRequest[CombinedRuleArgs]{}); err == nil {
		t.Fatal("expected an error for an empty rules list")
	}
	if _, err := (AnyOf{}).Invoke(context.Background(), infer.FunctionRequest[CombinedRuleArgs]{}); err == nil {
		t.Fatal("expected an error for an empty rules list")
	}

	children := []Rule{{Kind: "approval", Count: 1}, {Kind: "manual"}}
	resp, err := (AllOf{}).Invoke(context.Background(), infer.FunctionRequest[CombinedRuleArgs]{Input: CombinedRuleArgs{Rules: children}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Output.Kind != "all" || len(resp.Output.Rules) != 2 {
		t.Fatalf("rule = %#v", resp.Output)
	}

	resp, err = (AnyOf{}).Invoke(context.Background(), infer.FunctionRequest[CombinedRuleArgs]{Input: CombinedRuleArgs{Rules: children}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Output.Kind != "any" {
		t.Fatalf("rule = %#v", resp.Output)
	}
}
