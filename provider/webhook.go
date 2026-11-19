// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/property"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	comProvider "github.com/pulumi/pulumi/sdk/v3/go/pulumi/provider"

	"github.com/pulumi/pulumi-delivery/provider/cloudclient"
)

// webhookValue is what a WebhookSource shows: the ref and the url are
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
	if typ := string(req.Urn.Type()); typ != typeWebhookSource {
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

// buildWebhookSource registers a WebhookSource. The wrapper sets the outputs.
func buildWebhookSource(ctx *pulumi.Context, name string, inputs comProvider.ConstructInputs,
	opts pulumi.ResourceOption,
) (*comProvider.ConstructResult, error) {
	return register(ctx, typeWebhookSource, name, inputs, &WebhookSource{}, opts, pulumi.Map{})
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

