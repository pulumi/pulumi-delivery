// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"context"
	"math"
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
		"kind": "decision",
		"questions": map[string]any{"unsafe": map[string]any{
			"instructions": "Does preview.value describe an unsafe change?",
			"maximumRisk":  0.05,
		}},
		"context": map[string]any{"changeTicket": "CHG-123"},
	}, declarations[0]["inputs"].(map[string]any)["when"])
}

func TestDecisionValidation(t *testing.T) {
	validQuestion := DecisionQuestion{Instructions: "Is this safe?", MaximumRisk: 0.5}
	for _, tt := range []struct {
		name      string
		questions map[string]DecisionQuestion
		wantErr   string
	}{
		{name: "empty questions", wantErr: "decision needs at least one question"},
		{name: "empty question name", questions: map[string]DecisionQuestion{"": validQuestion}, wantErr: "name must not be empty"},
		{name: "empty instructions", questions: map[string]DecisionQuestion{"risk": {MaximumRisk: 0.5}}, wantErr: `question "risk" needs instructions`},
		{name: "risk below zero", questions: map[string]DecisionQuestion{"risk": {Instructions: "Is this safe?", MaximumRisk: -0.01}}, wantErr: `question "risk" maximumRisk must be between 0 and 1`},
		{name: "risk above one", questions: map[string]DecisionQuestion{"risk": {Instructions: "Is this safe?", MaximumRisk: 1.01}}, wantErr: `question "risk" maximumRisk must be between 0 and 1`},
		{name: "risk is nan", questions: map[string]DecisionQuestion{"risk": {Instructions: "Is this safe?", MaximumRisk: math.NaN()}}, wantErr: `question "risk" maximumRisk must be between 0 and 1`},
		{name: "risk boundaries", questions: map[string]DecisionQuestion{
			"always deny":  {Instructions: "Is this safe?", MaximumRisk: 0},
			"always allow": {Instructions: "Is this safe?", MaximumRisk: 1},
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDecision(Rule{Kind: "decision", Questions: tt.questions})
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestLiteralDecisionRuleValidation(t *testing.T) {
	decisionValue := func(questions property.Value) property.Value {
		return property.New(property.NewMap(map[string]property.Value{
			"kind":      property.New("decision"),
			"questions": questions,
		}))
	}
	decision := func(questions map[string]property.Value) property.Value {
		return decisionValue(property.New(property.NewMap(questions)))
	}
	question := func(instructions string, maximumRisk float64) property.Value {
		return property.New(property.NewMap(map[string]property.Value{
			"instructions": property.New(instructions),
			"maximumRisk":  property.New(maximumRisk),
		}))
	}
	nested := func(kind string, child property.Value) property.Value {
		return property.New(property.NewMap(map[string]property.Value{
			"kind":  property.New(kind),
			"rules": property.New([]property.Value{child}),
		}))
	}

	for _, tt := range []struct {
		name    string
		when    property.Value
		wantErr string
	}{
		{name: "empty questions", when: decision(nil), wantErr: "decision needs at least one question"},
		{name: "empty question name", when: decision(map[string]property.Value{"": question("Is this safe?", 0.5)}), wantErr: "question name must not be empty"},
		{name: "question is not an object", when: decision(map[string]property.Value{"risk": property.New("not an object")}), wantErr: `question "risk" must be an object`},
		{name: "empty instructions", when: decision(map[string]property.Value{"risk": question("", 0.5)}), wantErr: `question "risk" needs instructions`},
		{name: "missing maximum risk", when: decision(map[string]property.Value{"risk": property.New(property.NewMap(map[string]property.Value{"instructions": property.New("Is this safe?")}))}), wantErr: `question "risk" maximumRisk must be between 0 and 1`},
		{name: "risk above one", when: decision(map[string]property.Value{"risk": question("Is this safe?", 5)}), wantErr: `question "risk" maximumRisk must be between 0 and 1`},
		{name: "nested all", when: nested("all", decision(map[string]property.Value{"risk": question("Is this safe?", -0.1)})), wantErr: `question "risk" maximumRisk must be between 0 and 1`},
		{name: "nested any", when: nested("any", decision(map[string]property.Value{"risk": question("Is this safe?", math.NaN())})), wantErr: `question "risk" maximumRisk must be between 0 and 1`},
		{name: "unknown questions", when: decisionValue(property.New(property.Computed))},
		{name: "unknown question", when: decision(map[string]property.Value{"risk": property.New(property.Computed)})},
		{name: "unknown instructions", when: decision(map[string]property.Value{"risk": property.New(property.NewMap(map[string]property.Value{
			"instructions": property.New(property.Computed),
			"maximumRisk":  property.New(0.5),
		}))})},
		{name: "unknown maximum risk", when: decision(map[string]property.Value{"risk": property.New(property.NewMap(map[string]property.Value{
			"instructions": property.New("Is this safe?"),
			"maximumRisk":  property.New(property.Computed),
		}))})},
		{name: "unknown question does not hide invalid question", when: decision(map[string]property.Value{
			"unknown": property.New(property.Computed),
			"invalid": question("Is this safe?", 5),
		}), wantErr: `question "invalid" maximumRisk must be between 0 and 1`},
		{name: "boundary values", when: decision(map[string]property.Value{
			"always deny":  question("Is this safe?", 0),
			"always allow": question("Is this safe?", 1),
		})},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := newServer(t)
			_, err := server.Construct(stageRequest(tt.when))
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
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
