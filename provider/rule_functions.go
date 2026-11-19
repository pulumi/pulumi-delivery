// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
)

// HTTPMethod is the request method of an http rule.
type HTTPMethod string

var defaultHTTPMethod HTTPMethod = "GET"

func (HTTPMethod) Values() []infer.EnumValue[HTTPMethod] {
	return []infer.EnumValue[HTTPMethod]{
		{Name: "GET", Value: "GET"},
		{Name: "POST", Value: "POST"},
		{Name: "PUT", Value: "PUT"},
		{Name: "PATCH", Value: "PATCH"},
		{Name: "HEAD", Value: "HEAD"},
	}
}

func validateMethod(method string) error {
	for _, v := range HTTPMethod("").Values() {
		if method == string(v.Value) {
			return nil
		}
	}
	return fmt.Errorf("rule.method must be one of GET, POST, PUT, PATCH, HEAD")
}

type Rule struct {
	Kind      string   `pulumi:"kind"`
	Name      string   `pulumi:"name,optional"`
	Approvers []string `pulumi:"approvers,optional"`
	// Count is the number of approvals required. Omitted or zero defaults to one.
	Count             int               `pulumi:"count,optional"`
	URL               string            `pulumi:"url,optional"`
	Method            *HTTPMethod       `pulumi:"method,optional"`
	Headers           map[string]string `pulumi:"headers,optional"`
	Body              string            `pulumi:"body,optional"`
	SuccessExpression string            `pulumi:"successExpression,optional"`
	Interval          string            `pulumi:"interval,optional"`
	RequestTimeout    string            `pulumi:"requestTimeout,optional"`
	Timeout           string            `pulumi:"timeout,optional"`
	Rules             []Rule            `pulumi:"rules,optional"`
	// Description tells the person who continues a manual gate what to check.
	Description string                      `pulumi:"description,optional"`
	Questions   map[string]DecisionQuestion `pulumi:"questions,optional"`
	Context     map[string]any              `pulumi:"context,optional"`
}

// DecisionQuestion is a narrow judgment. MaximumRisk maps its probability to a deterministic gate outcome.
type DecisionQuestion struct {
	Instructions string  `pulumi:"instructions"`
	MaximumRisk  float64 `pulumi:"maximumRisk"`
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
	if name == "interval" && duration > time.Hour {
		return fmt.Errorf("rule.interval must be at most 1 hour")
	}
	if name == "requestTimeout" && duration > 20*time.Second {
		return fmt.Errorf("rule.requestTimeout must be at most 20 seconds")
	}
	if name == "timeout" && duration < time.Minute {
		return fmt.Errorf("rule.timeout must be at least 1 minute")
	}
	if name == "timeout" && duration > 168*time.Hour {
		return fmt.Errorf("rule.timeout must be at most 168 hours")
	}
	return nil
}

// validateWait checks that the timeout of an http rule is at least its interval. It skips a value that is empty
// or malformed, which validateDuration reports.
func validateWait(interval, timeout string) error {
	i, errI := time.ParseDuration(interval)
	t, errT := time.ParseDuration(timeout)
	if errI != nil || errT != nil || t >= i {
		return nil
	}
	return fmt.Errorf("rule.timeout must be at least the interval")
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
		if v, known := text("method"); known && !rule.Get("method").IsNull() {
			if err := validateMethod(v); err != nil {
				return err
			}
		}
		for _, name := range []string{"interval", "requestTimeout", "timeout"} {
			if v, known := text(name); known {
				if v == "" && rule.Get(name).IsString() {
					return fmt.Errorf("rule.%s must be a positive whole-second duration", name)
				}
				if err := validateDuration(name, v); err != nil {
					return err
				}
			}
		}
		interval, _ := text("interval")
		timeout, _ := text("timeout")
		if err := validateWait(interval, timeout); err != nil {
			return err
		}
	case "manual":
	case "decision":
		return validateDecisionValue(rule)
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
		if r.Method != nil {
			if err := validateMethod(string(*r.Method)); err != nil {
				return err
			}
		}
		if err := validateDuration("interval", r.Interval); err != nil {
			return err
		}
		if err := validateDuration("requestTimeout", r.RequestTimeout); err != nil {
			return err
		}
		if err := validateDuration("timeout", r.Timeout); err != nil {
			return err
		}
		return validateWait(r.Interval, r.Timeout)
	case "manual":
		return nil
	case "decision":
		return validateDecision(r)
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

func validateDecision(r Rule) error {
	if len(r.Questions) == 0 {
		return errors.New("decision needs at least one question")
	}
	names := make([]string, 0, len(r.Questions))
	for name := range r.Questions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		question := r.Questions[name]
		if err := validateDecisionQuestion(name, question.Instructions, question.MaximumRisk); err != nil {
			return err
		}
	}
	return nil
}

func validateDecisionValue(rule property.Map) error {
	questions, ok := rule.GetOk("questions")
	if !ok || questions.IsNull() {
		return errors.New("decision needs at least one question")
	}
	if questions.IsComputed() {
		return nil
	}
	if !questions.IsMap() || questions.AsMap().Len() == 0 {
		return errors.New("decision needs at least one question")
	}
	var validationErr error
	questions.AsMap().AllStable(func(name string, question property.Value) bool {
		if name == "" {
			validationErr = errors.New("decision question name must not be empty")
			return false
		}
		if question.IsComputed() {
			return true
		}
		if !question.IsMap() {
			validationErr = fmt.Errorf("decision question %q must be an object", name)
			return false
		}
		fields := question.AsMap()
		instructions := fields.Get("instructions")
		maximumRisk := fields.Get("maximumRisk")
		if instructions.IsComputed() || maximumRisk.IsComputed() {
			return true
		}
		if !instructions.IsString() {
			validationErr = fmt.Errorf("decision question %q needs instructions", name)
			return false
		}
		if !maximumRisk.IsNumber() {
			validationErr = fmt.Errorf("decision question %q maximumRisk must be between 0 and 1", name)
			return false
		}
		validationErr = validateDecisionQuestion(name, instructions.AsString(), maximumRisk.AsNumber())
		return validationErr == nil
	})
	return validationErr
}

func validateDecisionQuestion(name, instructions string, maximumRisk float64) error {
	if name == "" {
		return errors.New("decision question name must not be empty")
	}
	if instructions == "" {
		return fmt.Errorf("decision question %q needs instructions", name)
	}
	if !(maximumRisk >= 0 && maximumRisk <= 1) {
		return fmt.Errorf("decision question %q maximumRisk must be between 0 and 1", name)
	}
	return nil
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
	Name              string            `pulumi:"name,optional"`
	URL               string            `pulumi:"url"`
	Method            *HTTPMethod       `pulumi:"method,optional"`
	Headers           map[string]string `pulumi:"headers,optional"`
	Body              string            `pulumi:"body,optional"`
	SuccessExpression string            `pulumi:"successExpression"`
	Interval          string            `pulumi:"interval,optional"`
	RequestTimeout    string            `pulumi:"requestTimeout,optional"`
	Timeout           string            `pulumi:"timeout,optional"`
}

func (HTTP) Invoke(ctx context.Context, req infer.FunctionRequest[HTTPArgs]) (infer.FunctionResponse[Rule], error) {
	a := req.Input
	method, interval, requestTimeout, timeout := a.Method, a.Interval, a.RequestTimeout, a.Timeout
	if method == nil {
		m := defaultHTTPMethod
		method = &m
	}
	if interval == "" {
		interval = "1m"
	}
	if requestTimeout == "" {
		requestTimeout = "10s"
	}
	if timeout == "" {
		timeout = "24h"
	}
	rule := Rule{
		Kind:              "http",
		Name:              a.Name,
		URL:               a.URL,
		Method:            method,
		Headers:           a.Headers,
		Body:              a.Body,
		SuccessExpression: a.SuccessExpression,
		Interval:          interval,
		RequestTimeout:    requestTimeout,
		Timeout:           timeout,
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

type Decision struct{}
type DecisionArgs struct {
	Name        string                      `pulumi:"name,optional"`
	Description string                      `pulumi:"description,optional"`
	Questions   map[string]DecisionQuestion `pulumi:"questions"`
	Context     map[string]any              `pulumi:"context,optional"`
}

func (Decision) Invoke(ctx context.Context, req infer.FunctionRequest[DecisionArgs]) (infer.FunctionResponse[Rule], error) {
	a := req.Input
	rule := Rule{Kind: "decision", Name: a.Name, Description: a.Description, Questions: a.Questions, Context: a.Context}
	if err := validateRule(rule, 1); err != nil {
		return infer.FunctionResponse[Rule]{}, err
	}
	return infer.FunctionResponse[Rule]{Output: rule}, nil
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
