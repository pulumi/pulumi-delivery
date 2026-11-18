// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
)

type Rule struct {
	Kind      string   `pulumi:"kind"`
	Name      string   `pulumi:"name,optional"`
	Approvers []string `pulumi:"approvers,optional"`
	// Count is the number of approvals required. Omitted or zero defaults to one.
	Count              int               `pulumi:"count,optional"`
	URL                string            `pulumi:"url,optional"`
	Method             string            `pulumi:"method,optional"`
	Headers            map[string]string `pulumi:"headers,optional"`
	HeadersEnvironment string            `pulumi:"headersEnvironment,optional"`
	Body               string            `pulumi:"body,optional"`
	SuccessExpression  string            `pulumi:"successExpression,optional"`
	Interval           string            `pulumi:"interval,optional"`
	Timeout            string            `pulumi:"timeout,optional"`
	Rules              []Rule            `pulumi:"rules,optional"`
	// Description tells the person who continues a manual gate what to check.
	Description string `pulumi:"description,optional"`
}

// validateDuration checks an optional whole-second duration of an http rule.
func validateDuration(name, value string) error {
	if value == "" {
		return nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 || duration%time.Second != 0 {
		return fmt.Errorf("rule.%s must be a positive whole-second duration", name)
	}
	if name == "interval" && duration < time.Minute {
		return fmt.Errorf("rule.interval must be at least 60 seconds")
	}
	if name == "timeout" && duration > 20*time.Second {
		return fmt.Errorf("rule.timeout must be at most 20 seconds")
	}
	return nil
}

// validateRuleValue checks a rule in the raw inputs. It skips what is unknown and looks through secrets, so
// it does not copy the rule into a plain struct.
func validateRuleValue(value property.Value, depth int) error {
	if value.IsComputed() || value.IsNull() {
		return nil
	}
	if depth > 3 {
		return fmt.Errorf("rules may nest at most three levels")
	}
	if !value.IsMap() {
		return fmt.Errorf("a rule must be an object")
	}
	rule := value.AsMap()
	text := func(name string) (string, bool) {
		v := rule.Get(name)
		if v.IsString() {
			return v.AsString(), true
		}
		return "", !v.IsComputed()
	}
	kind, known := text("kind")
	if !known {
		return nil
	}
	if !slices.Contains(ruleKinds, kind) {
		return fmt.Errorf("unsupported rule kind %q", kind)
	}
	switch kind {
	case "approval":
		if count := rule.Get("count"); count.IsNumber() && count.AsNumber() < 0 {
			return fmt.Errorf("approval count must be positive")
		}
	case "http":
		for _, name := range []string{"url", "successExpression"} {
			if v, known := text(name); known {
				if err := required("rule."+name, v); err != nil {
					return err
				}
			}
		}
		for _, name := range []string{"interval", "timeout"} {
			if v, known := text(name); known {
				if err := validateDuration(name, v); err != nil {
					return err
				}
			}
		}
	case "manual":
	case "all", "any":
		rules := rule.Get("rules")
		if rules.IsComputed() {
			return nil
		}
		if !rules.IsArray() || rules.AsArray().Len() == 0 {
			return fmt.Errorf("%s rule must contain at least one rule", kind)
		}
		for _, child := range rules.AsArray().AsSlice() {
			if err := validateRuleValue(child, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unsupported rule kind %q", kind)
	}
	return nil
}

func validateRule(r Rule, d int) error {
	if d > 3 {
		return fmt.Errorf("rules may nest at most three levels")
	}
	if !slices.Contains(ruleKinds, r.Kind) {
		return fmt.Errorf("unsupported rule kind %q", r.Kind)
	}
	switch r.Kind {
	case "approval":
		if r.Count < 0 {
			return fmt.Errorf("approval count must be positive")
		}
		return nil
	case "http":
		if err := required("rule.url", r.URL); err != nil {
			return err
		}
		if err := required("rule.successExpression", r.SuccessExpression); err != nil {
			return err
		}
		if err := validateDuration("interval", r.Interval); err != nil {
			return err
		}
		if err := validateDuration("timeout", r.Timeout); err != nil {
			return err
		}
		return nil
	case "manual":
		return nil
	case "all", "any":
		if len(r.Rules) == 0 {
			return fmt.Errorf("%s rule must contain at least one rule", r.Kind)
		}
		for _, c := range r.Rules {
			if err := validateRule(c, d+1); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported rule kind %q", r.Kind)
	}
}

// Each rule-builder function below returns a Rule (kind plus the rest of its fields), so its
// result is structurally assignable to a Stage's `when` input in every generated SDK language.
// Validation reuses validateRule, the same check StageArgs runs on a literal rule.

type Approval struct{}
type ApprovalArgs struct {
	Name      string   `pulumi:"name,optional"`
	Approvers []string `pulumi:"approvers,optional"`
	Count     int      `pulumi:"count,optional"`
}

func (Approval) Invoke(ctx context.Context, req infer.FunctionRequest[ApprovalArgs]) (infer.FunctionResponse[Rule], error) {
	a := req.Input
	count := a.Count
	if count == 0 {
		count = 1
	}
	rule := Rule{Kind: "approval", Name: a.Name, Approvers: a.Approvers, Count: count}
	if err := validateRule(rule, 1); err != nil {
		return infer.FunctionResponse[Rule]{}, err
	}
	return infer.FunctionResponse[Rule]{Output: rule}, nil
}

type HTTP struct{}
type HTTPArgs struct {
	Name               string            `pulumi:"name,optional"`
	URL                string            `pulumi:"url"`
	Method             string            `pulumi:"method,optional"`
	Headers            map[string]string `pulumi:"headers,optional"`
	HeadersEnvironment string            `pulumi:"headersEnvironment,optional"`
	Body               string            `pulumi:"body,optional"`
	SuccessExpression  string            `pulumi:"successExpression"`
	Interval           string            `pulumi:"interval,optional"`
	Timeout            string            `pulumi:"timeout,optional"`
}

func (HTTP) Invoke(ctx context.Context, req infer.FunctionRequest[HTTPArgs]) (infer.FunctionResponse[Rule], error) {
	a := req.Input
	rule := Rule{
		Kind:               "http",
		Name:               a.Name,
		URL:                a.URL,
		Method:             a.Method,
		Headers:            a.Headers,
		HeadersEnvironment: a.HeadersEnvironment,
		Body:               a.Body,
		SuccessExpression:  a.SuccessExpression,
		Interval:           a.Interval,
		Timeout:            a.Timeout,
	}
	if err := validateRule(rule, 1); err != nil {
		return infer.FunctionResponse[Rule]{}, err
	}
	return infer.FunctionResponse[Rule]{Output: rule}, nil
}

type Manual struct{}
type ManualArgs struct {
	Name        string `pulumi:"name,optional"`
	Description string `pulumi:"description,optional"`
}

func (Manual) Invoke(ctx context.Context, req infer.FunctionRequest[ManualArgs]) (infer.FunctionResponse[Rule], error) {
	rule := Rule{Kind: "manual", Name: req.Input.Name, Description: req.Input.Description}
	if err := validateRule(rule, 1); err != nil {
		return infer.FunctionResponse[Rule]{}, err
	}
	return infer.FunctionResponse[Rule]{Output: rule}, nil
}

type AllOf struct{}
type AnyOf struct{}
type CombinedRuleArgs struct {
	Name  string `pulumi:"name,optional"`
	Rules []Rule `pulumi:"rules"`
}

func (AllOf) Invoke(ctx context.Context, req infer.FunctionRequest[CombinedRuleArgs]) (infer.FunctionResponse[Rule], error) {
	return combinedRule("all", req.Input)
}

func (AnyOf) Invoke(ctx context.Context, req infer.FunctionRequest[CombinedRuleArgs]) (infer.FunctionResponse[Rule], error) {
	return combinedRule("any", req.Input)
}

func combinedRule(kind string, a CombinedRuleArgs) (infer.FunctionResponse[Rule], error) {
	rule := Rule{Kind: kind, Name: a.Name, Rules: a.Rules}
	if err := validateRule(rule, 1); err != nil {
		return infer.FunctionResponse[Rule]{}, err
	}
	return infer.FunctionResponse[Rule]{Output: rule}, nil
}
