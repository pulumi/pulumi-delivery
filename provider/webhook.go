// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	comProvider "github.com/pulumi/pulumi/sdk/v3/go/pulumi/provider"

	"github.com/pulumi/pulumi-delivery/provider/cloudclient"
)

// webhookValue is what a WebhookSource (or a LinearIssueSource, which owns one) shows: the ref and the url are
// known before a run, and the rest is what the run recorded for the post that started it.
type webhookValue struct {
	ref, url string
	// run is false before a run: the subject, version and data are then unknown.
	run              bool
	subject, version string
	data             property.Map
}

// state sets the outputs of a webhook source. Without a value (a preview, or an input that is not known) every
// output but the ref is unknown.
func (v *webhookValue) state(state property.Map) property.Map {
	known := func(name, value string) property.Map {
		if value == "" {
			return state.Set(name, property.New(property.Computed))
		}
		return state.Set(name, property.New(value))
	}
	if v == nil {
		v = &webhookValue{}
	}
	state = known("ref", v.ref)
	state = known("url", v.url)
	if !v.run {
		for _, name := range []string{"subject", "version", "data"} {
			state = state.Set(name, property.New(property.Computed))
		}
		return state
	}
	return state.Set("subject", property.New(v.subject)).
		Set("version", property.New(v.version)).
		Set("data", property.New(v.data))
}

// runStackName matches the name of a run stack: <pipeline stack>-r<run number>.
var runStackName = regexp.MustCompile(`-r[0-9]+$`)

// previewWebhookSource is the value of a webhook source in a preview. The url needs no credential, so it is
// known when the CLI set PULUMI_API, except on a run stack: the preview of a run does not know the run number
// that the stack name carries.
func previewWebhookSource(req p.ConstructRequest) *webhookValue {
	name := string(req.Urn.Name())
	out := &webhookValue{ref: "webhook:" + name}
	api := os.Getenv("PULUMI_API")
	if api == "" || runStackName.MatchString(string(req.Urn.Stack())) {
		return out
	}
	client := cloudclient.New(api, req.Organization, string(req.Urn.Project()), string(req.Urn.Stack()), "")
	out.url = client.WebhookURL(name, nil)
	return out
}

// readWebhookSource returns the value of a webhook source in an update, or nil for another component. In a
// registration only the ref and the url are known. In a run the service gives the subject, version and data of
// the post that started the run.
func readWebhookSource(ctx context.Context, client *cloudclient.Client, req p.ConstructRequest) (*webhookValue, error) {
	if typ := string(req.Urn.Type()); typ != typeWebhookSource && typ != typeLinearIssueSource {
		return nil, nil
	}
	run, err := client.GetRun(ctx)
	if err != nil {
		return nil, err
	}
	name := string(req.Urn.Name())
	out := &webhookValue{ref: "webhook:" + name, url: client.WebhookURL(name, run)}
	if run == nil {
		return out, nil
	}
	value, err := client.WebhookValue(ctx, name)
	if err != nil {
		return nil, err
	}
	out.run = true
	out.subject, _ = value["subject"].(string)
	out.version, _ = value["version"].(string)
	data, _ := value["data"].(map[string]any)
	out.data = resource.FromResourcePropertyValue(resource.NewPropertyValue(data)).AsMap()
	return out, nil
}

// webhookSourceResource is the Go SDK handle of the WebhookSource that a LinearIssueSource registers.
type webhookSourceResource struct {
	pulumi.ResourceState
	Ref     pulumi.StringOutput `pulumi:"ref"`
	URL     pulumi.StringOutput `pulumi:"url"`
	Subject pulumi.StringOutput `pulumi:"subject"`
	Version pulumi.StringOutput `pulumi:"version"`
	Data    pulumi.MapOutput    `pulumi:"data"`
}

// buildWebhookSource registers a WebhookSource. The wrapper sets the outputs.
func buildWebhookSource(ctx *pulumi.Context, name string, opts pulumi.ResourceOption) (*comProvider.ConstructResult, error) {
	return register(ctx, typeWebhookSource, name, &WebhookSource{}, opts, pulumi.Map{})
}

// buildLinearIssueSource registers a LinearIssueSource and its WebhookSource child, named like the component. The
// child is what the service sees: the component itself is not declared. The wrapper reads the value of the
// webhook for the component once the child is declared, so the component shows the same outputs.
func buildLinearIssueSource(ctx *pulumi.Context, name string, inputs comProvider.ConstructInputs,
	opts pulumi.ResourceOption,
) (*comProvider.ConstructResult, error) {
	source := &LinearIssueSource{}
	if err := ctx.RegisterComponentResource(typeLinearIssueSource, name, source, opts); err != nil {
		return nil, err
	}
	// The raw inputs, not a typed copy: the Go SDK cannot copy an unknown list into a typed input in a preview.
	inputMap, err := inputs.Map()
	if err != nil {
		return nil, err
	}
	ignoreBots := pulumi.Input(pulumi.Bool(true))
	if value, ok := inputMap["ignoreBots"]; ok {
		ignoreBots = value
	}
	when := pulumi.All(inputMap["labels"], inputMap["states"], ignoreBots).ApplyT(func(args []any) string {
		ignore, _ := args[2].(bool)
		return linearWhen(stringList(args[0]), stringList(args[1]), ignore)
	}).(pulumi.StringOutput)
	child := &webhookSourceResource{}
	if err := ctx.RegisterRemoteComponentResource(typeWebhookSource, name, pulumi.Map{
		"signing": pulumi.Map{"kind": pulumi.String("hmac-sha256"), "secret": inputMap["signingSecret"]},
		"map":     pulumi.String(linearMap),
		"when":    when,
		"trigger": pulumi.Bool(true),
	}, child, pulumi.Parent(source)); err != nil {
		return nil, err
	}
	if err := ctx.RegisterResourceOutputs(source, pulumi.Map{}); err != nil {
		return nil, err
	}
	return comProvider.NewConstructResult(source)
}

// linearMap maps a post of the issue webhook of Linear to the subject, version and data of a run.
const linearMap = "{subject: data.id, version: data.updatedAt || createdAt, data: {identifier: data.identifier, " +
	"title: data.title, description: data.description, url: data.url, state: data.state.name, " +
	"labels: data.labels[].name || `[]`, team: data.team.key, action: action}}"

// linearWhen is the when of a LinearIssueSource: a post about an issue that was created or updated, by a user
// when ignoreBots, with every label and in one of the states.
func linearWhen(labels, states []string, ignoreBots bool) string {
	clauses := []string{"type == 'Issue'", "contains(`[\"create\",\"update\"]`, action)"}
	if ignoreBots {
		clauses = append(clauses, "actor.type == 'user'")
	}
	for _, label := range labels {
		clauses = append(clauses, fmt.Sprintf("contains(data.labels[].name || `[]`, %s)", jmesString(label)))
	}
	if len(states) > 0 {
		clauses = append(clauses, fmt.Sprintf("contains(%s, data.state.name)", jmesList(states)))
	}
	return strings.Join(clauses, " && ")
}

// jmesString is a JMESPath raw string literal.
func jmesString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `\'`) + "'"
}

// jmesList is a JMESPath JSON literal of a list of strings.
func jmesList(values []string) string {
	encoded, err := json.Marshal(values)
	if err != nil {
		return "`[]`"
	}
	return "`" + strings.ReplaceAll(string(encoded), "`", "\\`") + "`"
}

// stringList is the strings of a decoded list input. A missing or unknown list is empty.
func stringList(value any) []string {
	var out []string
	switch list := value.(type) {
	case []string:
		return list
	case []any:
		for _, item := range list {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// webhookSigningKinds are the kinds of signature that the service verifies.
var webhookSigningKinds = []string{"hmac-sha256", "token"}

// validateWebhookSource checks the inputs of a WebhookSource: the map is required, and signing, when set, names
// a kind and a secret. Unknown values pass.
func validateWebhookSource(inputs property.Map) error {
	if err := requiredUnlessUnknown(inputs, stringInput(inputs, "map"), "map"); err != nil {
		return err
	}
	signing, ok := inputs.GetOk("signing")
	if !ok || signing.IsNull() || signing.IsComputed() {
		return nil
	}
	if !signing.IsMap() {
		return errors.New("signing must be an object with kind and secret")
	}
	fields := signing.AsMap()
	if err := requiredUnlessUnknown(fields, stringInput(fields, "kind"), "kind"); err != nil {
		return fmt.Errorf("signing.%w", err)
	}
	if kind := stringInput(fields, "kind"); kind != "" && !slices.Contains(webhookSigningKinds, kind) {
		return fmt.Errorf("signing.kind must be one of %v, not %q", webhookSigningKinds, kind)
	}
	if err := requiredUnlessUnknown(fields, stringInput(fields, "secret"), "secret"); err != nil {
		return fmt.Errorf("signing.%w", err)
	}
	return nil
}

// validateLinearIssueSource checks the inputs of a LinearIssueSource: the signing secret is required, and labels
// and states are lists of names. Unknown values pass.
func validateLinearIssueSource(inputs property.Map) error {
	if err := requiredUnlessUnknown(inputs, stringInput(inputs, "signingSecret"), "signingSecret"); err != nil {
		return err
	}
	for _, name := range []string{"labels", "states"} {
		list, ok := inputs.GetOk(name)
		if !ok || list.IsNull() || list.IsComputed() {
			continue
		}
		if !list.IsArray() {
			return fmt.Errorf("%s must be a list of names", name)
		}
		for _, item := range list.AsArray().AsSlice() {
			if !item.IsComputed() && !item.IsString() {
				return fmt.Errorf("%s must be a list of names", name)
			}
		}
	}
	return nil
}
