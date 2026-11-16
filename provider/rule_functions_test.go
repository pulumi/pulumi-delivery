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

func TestDecisionRuleOnTheWire(t *testing.T) {
	service := newFakeService(t)
	server := newServer(t)

	invoked, err := server.Invoke(p.InvokeRequest{Token: "delivery:index:decision", Args: property.NewMap(map[string]property.Value{
		"evidence": property.New([]property.Value{property.New(map[string]property.Value{"source": property.New("deliveryPreview")})}),
		"questions": property.New(map[string]property.Value{"unsafe": property.New(map[string]property.Value{
			"instructions": property.New("Does preview.value describe an unsafe change?"),
			"maximumRisk":  property.New(0.05),
		})}),
		"context": property.New(map[string]property.Value{"changeTicket": property.New("CHG-123")}),
	})})
	require.NoError(t, err)
	require.Empty(t, invoked.Failures)

	request := stageRequest(property.New(invoked.Return))
	request.DryRun = false
	_, err = server.Construct(request)
	require.NoError(t, err)

	declarations := service.declared()
	require.Len(t, declarations, 1)
	assert.Equal(t, map[string]any{
		"kind":     "decision",
		"evidence": []any{map[string]any{"source": "deliveryPreview"}},
		"questions": map[string]any{"unsafe": map[string]any{
			"instructions": "Does preview.value describe an unsafe change?",
			"maximumRisk":  0.05,
		}},
		"context": map[string]any{"changeTicket": "CHG-123"},
	}, declarations[0]["inputs"].(map[string]any)["when"])
}

func TestDecisionRejectsUnsupportedEvidence(t *testing.T) {
	for _, source := range []string{"agentInvocation", "http", "policyFindings"} {
		t.Run(source, func(t *testing.T) {
			_, err := Decision{}.Invoke(context.Background(), infer.FunctionRequest[DecisionArgs]{Input: DecisionArgs{
				Evidence: []DecisionEvidence{{Source: source}},
				Questions: map[string]DecisionQuestion{
					"unsafe": {Instructions: "Does the preview show a dangerous change?", MaximumRisk: 0.05},
				},
			}})
			require.ErrorContains(t, err, "unsupported decision evidence source")
		})
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
