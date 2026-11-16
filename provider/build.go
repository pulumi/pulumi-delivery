// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"encoding/json"
	"errors"
	"maps"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	comProvider "github.com/pulumi/pulumi/sdk/v3/go/pulumi/provider"
)

// buildComponent is the component code. A Job registers its internal child; the other components only
// declare themselves.
func buildComponent(ctx *pulumi.Context, typ, name string, inputs comProvider.ConstructInputs,
	opts pulumi.ResourceOption, d declaration, organization, commit string,
) (*comProvider.ConstructResult, error) {
	switch typ {
	case typePipeline:
		return register(ctx, typ, name, inputs, &Pipeline{}, opts, pulumi.Map{})
	case typeStage:
		return register(ctx, typ, name, inputs, &Stage{}, opts, pulumi.Map{})
	case typeJob:
		return buildJob(ctx, name, inputs, opts, d, organization)
	case typeStacks:
		return buildStacks(ctx, name, inputs, opts, d, organization)
	case typeArtifact:
		return buildArtifact(ctx, name, inputs, opts, d, organization)
	case typeAgent:
		return buildAgent(ctx, name, inputs, opts, d, organization)
	case typePipelineSource:
		// The wrapper sets the outputs.
		return register(ctx, typ, name, inputs, &PipelineSource{}, opts, pulumi.Map{})
	case typeWebhookSource:
		return buildWebhookSource(ctx, name, opts)
	case typeLinearIssueSource:
		return buildLinearIssueSource(ctx, name, inputs, opts)
	}
	// A source. Its commit output is replaced by the wrapper when the commit is unknown.
	source := &GitHubSource{}
	source.Commit = pulumi.String(commit).ToStringOutput()
	return register(ctx, typ, name, inputs, source, opts, pulumi.Map{"commit": source.Commit})
}

// register records the inputs of the component in the state, so that a change of an input shows in a preview.
func register(ctx *pulumi.Context, typ, name string, inputs comProvider.ConstructInputs,
	component pulumi.ComponentResource, opts pulumi.ResourceOption, outputs pulumi.Map,
) (*comProvider.ConstructResult, error) {
	inputMap, err := inputs.Map()
	if err != nil {
		return nil, err
	}
	if err := ctx.RegisterComponentResourceV2(typ, name, inputMap, component, opts); err != nil {
		return nil, err
	}
	if err := ctx.RegisterResourceOutputs(component, outputs); err != nil {
		return nil, err
	}
	return comProvider.NewConstructResult(component)
}

func buildJob(ctx *pulumi.Context, name string, inputs comProvider.ConstructInputs,
	opts pulumi.ResourceOption, d declaration, organization string,
) (*comProvider.ConstructResult, error) {
	job := &Job{}
	inputMap, err := inputs.Map()
	if err != nil {
		return nil, err
	}
	if err := ctx.RegisterComponentResourceV2(typeJob, name, inputMap, job, opts); err != nil {
		return nil, err
	}
	child := &memberRunResource{}
	if err := ctx.RegisterResource(typeMemberRun, name, pulumi.Map{
		"organization": pulumi.String(organization),
		"member":       pulumi.String(d.urn),
		"declaration":  d.forChild(inputMap),
	}, child, pulumi.Parent(job)); err != nil {
		return nil, err
	}
	// The Job output type is map<string, string>, so a structured value is encoded as JSON.
	job.Outputs = child.Outputs.ApplyT(func(outputs map[string]any) map[string]string {
		out := make(map[string]string, len(outputs))
		for key, value := range outputs {
			if s, ok := value.(string); ok {
				out[key] = s
			} else if b, err := json.Marshal(value); err == nil {
				out[key] = string(b)
			}
		}
		return out
	}).(pulumi.StringMapOutput)
	job.ChildUrn = child.URN().ApplyT(func(u pulumi.URN) string { return string(u) }).(pulumi.StringOutput)
	if err := ctx.RegisterResourceOutputs(job, pulumi.Map{"outputs": job.Outputs, "childUrn": job.ChildUrn}); err != nil {
		return nil, err
	}
	return comProvider.NewConstructResult(job)
}

// buildAgent registers the child of an Agent, like a Job. The result and the task id are two outputs of the
// child.
func buildAgent(ctx *pulumi.Context, name string, inputs comProvider.ConstructInputs,
	opts pulumi.ResourceOption, d declaration, organization string,
) (*comProvider.ConstructResult, error) {
	agent := &Agent{}
	if err := ctx.RegisterComponentResource(typeAgent, name, agent, opts); err != nil {
		return nil, err
	}
	inputMap, err := inputs.Map()
	if err != nil {
		return nil, err
	}
	child := &memberRunResource{}
	if err := ctx.RegisterResource(typeMemberRun, name, pulumi.Map{
		"organization": pulumi.String(organization),
		"member":       pulumi.String(d.urn),
		"declaration":  d.forChild(inputMap),
	}, child, pulumi.Parent(agent)); err != nil {
		return nil, err
	}
	agent.Result = child.Outputs.ApplyT(func(outputs map[string]any) map[string]any {
		result, _ := outputs["result"].(map[string]any)
		return result
	}).(pulumi.MapOutput)
	agent.TaskId = child.Outputs.ApplyT(func(outputs map[string]any) string {
		taskID, _ := outputs["taskId"].(string)
		return taskID
	}).(pulumi.StringOutput)
	agent.ChildUrn = child.URN().ApplyT(func(u pulumi.URN) string { return string(u) }).(pulumi.StringOutput)
	if err := ctx.RegisterResourceOutputs(agent, pulumi.Map{
		"result": agent.Result, "taskId": agent.TaskId, "childUrn": agent.ChildUrn,
	}); err != nil {
		return nil, err
	}
	return comProvider.NewConstructResult(agent)
}

// buildArtifact registers the child of an Artifact. The value output reads the child through Unsecret,
// so that it is never a secret without a value, which the engine rejects. The wrapper marks a known value
// secret when the input is.
func buildArtifact(ctx *pulumi.Context, name string, inputs comProvider.ConstructInputs,
	opts pulumi.ResourceOption, d declaration, organization string,
) (*comProvider.ConstructResult, error) {
	artifact := &Artifact{}
	inputMap, err := inputs.Map()
	if err != nil {
		return nil, err
	}
	if err := ctx.RegisterComponentResourceV2(typeArtifact, name, inputMap, artifact, opts); err != nil {
		return nil, err
	}
	child := &memberRunResource{}
	if err := ctx.RegisterResource(typeMemberRun, name, pulumi.Map{
		"organization": pulumi.String(organization),
		"member":       pulumi.String(d.urn),
		"declaration":  d.forChild(inputMap),
	}, child, pulumi.Parent(artifact)); err != nil {
		return nil, err
	}
	artifact.Value = pulumi.Unsecret(child.Outputs).ApplyT(func(outputs map[string]any) any {
		return outputs["value"]
	}).(pulumi.AnyOutput)
	artifact.ChildUrn = child.URN().ApplyT(func(u pulumi.URN) string { return string(u) }).(pulumi.StringOutput)
	if err := ctx.RegisterResourceOutputs(artifact, pulumi.Map{"value": artifact.Value, "childUrn": artifact.ChildUrn}); err != nil {
		return nil, err
	}
	return comProvider.NewConstructResult(artifact)
}

// buildStacks registers one child for each entry. The name of a child is <component>#<entry>: an entry
// name cannot contain '#', so two components never produce the same child name.
func buildStacks(ctx *pulumi.Context, name string, inputs comProvider.ConstructInputs,
	opts pulumi.ResourceOption, d declaration, organization string,
) (*comProvider.ConstructResult, error) {
	stacks := &Stacks{}
	inputMap, err := inputs.Map()
	if err != nil {
		return nil, err
	}
	if err := ctx.RegisterComponentResourceV2(typeStacks, name, inputMap, stacks, opts); err != nil {
		return nil, err
	}
	// The entry names come from the request: the type of the decoded input depends on its dependencies and
	// secrecy. An entry is never dropped.
	if len(d.entries) == 0 {
		return nil, errors.New("stacks must be a known map with at least one entry")
	}
	var args struct {
		Stacks pulumi.MapMapInput `pulumi:"stacks"`
	}
	if err := inputs.CopyTo(&args); err != nil {
		return nil, err
	}
	all := args.Stacks.ToMapMapOutput()
	var childOutputs []any
	childUrns := pulumi.StringMap{}
	for _, entry := range d.entries {
		// The child carries the full declaration, narrowed to its own entry.
		own := d
		own.propertyDependencies = map[string][]string{}
		for key, found := range d.propertyDependencies {
			if !strings.HasPrefix(key, "stacks.") || key == "stacks."+entry {
				own.propertyDependencies[key] = found
			}
		}
		ownInputs := pulumi.Map{}
		maps.Copy(ownInputs, inputMap)
		// Indexing the map loses the secret marks nested in the entry, so an entry that holds a secret is secret as a whole.
		var stacksEntry pulumi.Input = all.MapIndex(pulumi.String(entry))
		if d.secretEntries[entry] {
			stacksEntry = pulumi.ToSecret(stacksEntry)
		}
		ownInputs["stacks"] = pulumi.Map{entry: stacksEntry}
		child := &memberRunResource{}
		if err := ctx.RegisterResource(typeMemberRun, name+"#"+entry, pulumi.Map{
			"organization": pulumi.String(organization),
			"member":       pulumi.String(d.urn),
			"entry":        pulumi.String(entry),
			"declaration":  own.forChild(ownInputs),
		}, child, pulumi.Parent(stacks)); err != nil {
			return nil, err
		}
		childOutputs = append(childOutputs, child.Outputs)
		childUrns[entry] = child.URN().ApplyT(func(u pulumi.URN) string { return string(u) }).(pulumi.StringOutput)
	}
	stacks.Outputs = groupOutputs(d.entries, childOutputs)
	stacks.ChildUrns = childUrns.ToStringMapOutput()
	if err := ctx.RegisterResourceOutputs(stacks, pulumi.Map{"outputs": stacks.Outputs, "childUrns": stacks.ChildUrns}); err != nil {
		return nil, err
	}
	return comProvider.NewConstructResult(stacks)
}

// groupOutputs is the outputs of a Stacks resource: one value for the whole group, secret iff a child is. A map
// that mixes a known secret entry and an awaiting one cannot go to the engine (a secret without a value), so the
// secret mark is dropped while any entry waits, and the apply, which runs only for known values, sets it again.
func groupOutputs(entries []string, childOutputs []any) pulumi.MapMapOutput {
	return pulumi.Unsecret(pulumi.All(childOutputs...)).ApplyT(func(values []any) pulumi.MapMapOutput {
		out := pulumi.MapMap{}
		secret := false
		for i, entry := range entries {
			m, _ := values[i].(map[string]any)
			out[entry] = pulumi.ToMap(m)
			secret = secret || pulumi.IsSecret(childOutputs[i].(pulumi.Output))
		}
		if secret {
			return pulumi.ToSecret(out).(pulumi.MapMapOutput)
		}
		return out.ToMapMapOutput()
	}).(pulumi.MapMapOutput)
}
