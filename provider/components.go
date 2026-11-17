// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import (
	"errors"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// The component types below describe the schema. Their Construct runs in construct.go, because the
// wrapper needs the raw inputs (unknowns, secrets and resource references) of the request.
//
// The inputs that refer to other resources have the type any. The Go SDK decodes a reference into it,
// and patchReferences replaces the schema type of each such input with a reference to the resource.

type PipelineArgs struct {
	ServicePrincipal *PipelineServicePrincipal `pulumi:"servicePrincipal,optional"`
	Runtime          any                       `pulumi:"runtime,optional"`
}

type PipelineServicePrincipal struct {
	UserID   *string `pulumi:"userId,optional"`
	TeamName *string `pulumi:"teamName,optional"`
}

// ProviderConfig is the configuration of the provider, set in the stack config as delivery:source. It tells
// the service where the pipeline program lives.
type ProviderConfig struct {
	Source *ProgramSource `pulumi:"source,optional"`
}

type ProgramSource struct {
	URL       string `pulumi:"url"`
	Branch    string `pulumi:"branch,optional"`
	Directory string `pulumi:"directory,optional"`
}
type Pipeline struct {
	pulumi.ResourceState
	// ReleaseNumber is the number of the release of the run. It is unknown during registration and preview.
	ReleaseNumber pulumi.IntOutput `pulumi:"releaseNumber"`
}

type AgentArgs struct {
	Stage             any            `pulumi:"stage"`
	AgentDefinitionID string         `pulumi:"agentDefinitionId"`
	Prompt            string         `pulumi:"prompt"`
	ResponseSchema    map[string]any `pulumi:"responseSchema"`
}

type Agent struct {
	pulumi.ResourceState
	// Response is the structured final response produced for this member in a Delivery run.
	Response pulumi.AnyOutput `pulumi:"response"`
	// ChildUrn is an internal dependency carrier and is removed from the public schema.
	ChildUrn pulumi.StringOutput `pulumi:"childUrn"`
}

type StageArgs struct {
	Pipeline any   `pulumi:"pipeline"`
	From     []any `pulumi:"from,optional"`
	When     *Rule `pulumi:"when,optional"`
}
type Stage struct{ pulumi.ResourceState }

type JobArgs struct {
	Stage          any               `pulumi:"stage"`
	Source         any               `pulumi:"source"`
	Directory      string            `pulumi:"directory"`
	Command        string            `pulumi:"command"`
	Environment    map[string]string `pulumi:"environment,optional"`
	Timeout        string            `pulumi:"timeout,optional"`
	Runtime        any               `pulumi:"runtime,optional"`
	Image          *Image            `pulumi:"image,optional"`
	PreRunCommands []string          `pulumi:"preRunCommands,optional"`
}
type Job struct {
	pulumi.ResourceState
	// Outputs are the outputs of the job.
	Outputs pulumi.StringMapOutput `pulumi:"outputs"`
	// ChildUrn is the URN of the internal child resource of the job.
	ChildUrn pulumi.StringOutput `pulumi:"childUrn"`
}

// StacksEntry is one stack that a Stacks resource updates. Timeout is in seconds.
type StacksEntry struct {
	Stack          string            `pulumi:"stack"`
	Source         any               `pulumi:"source"`
	Directory      string            `pulumi:"directory"`
	Config         map[string]string `pulumi:"config,optional"`
	Environment    map[string]string `pulumi:"environment,optional"`
	Timeout        int               `pulumi:"timeout,optional"`
	Runtime        any               `pulumi:"runtime,optional"`
	Image          *Image            `pulumi:"image,optional"`
	PreRunCommands []string          `pulumi:"preRunCommands,optional"`
}

// Image is the container image that a member runs in.
type Image struct {
	Reference   string            `pulumi:"reference"`
	Credentials *ImageCredentials `pulumi:"credentials,optional"`
}

// ImageCredentials sign in to a private registry.
type ImageCredentials struct {
	Username string `pulumi:"username"`
	Password string `pulumi:"password" provider:"secret"`
}
type StacksArgs struct {
	Stage  any                    `pulumi:"stage"`
	Stacks map[string]StacksEntry `pulumi:"stacks"`
}
type Stacks struct {
	pulumi.ResourceState
	// Outputs are the outputs of each entry.
	Outputs pulumi.MapMapOutput `pulumi:"outputs"`
	// ChildUrns are the URNs of the internal child resources, by entry.
	ChildUrns pulumi.StringMapOutput `pulumi:"childUrns"`
}

type SourceArgs struct {
	Repository string `pulumi:"repository"`
	Branch     string `pulumi:"branch"`
	// Trigger starts a run on a push to the branch. The schema type is true or SourceTrigger (schema_refs.go).
	Trigger *SourceTrigger `pulumi:"trigger,optional"`
}

// SourceTrigger limits the pushes that start a run to those that change a matching path.
type SourceTrigger struct {
	Paths []string `pulumi:"paths"`
}
type GitHubSource struct {
	pulumi.ResourceState
	Commit pulumi.StringOutput `pulumi:"commit"`
}
type GitLabSource struct {
	pulumi.ResourceState
	Commit pulumi.StringOutput `pulumi:"commit"`
}
type BitbucketSource struct {
	pulumi.ResourceState
	Commit pulumi.StringOutput `pulumi:"commit"`
}
type AzureDevOpsSource struct {
	pulumi.ResourceState
	Commit pulumi.StringOutput `pulumi:"commit"`
}

type PipelineSourceArgs struct {
	// Pipeline is the upstream pipeline as project/stack.
	Pipeline string `pulumi:"pipeline"`
	// Stage is the stage of the upstream pipeline that this source reads.
	Stage string `pulumi:"stage"`
	// Trigger starts a run when the stage deploys a new release.
	Trigger bool `pulumi:"trigger,optional"`
}
type PipelineSource struct {
	pulumi.ResourceState
	// ReleaseNumber is the release that the stage of the upstream pipeline deployed.
	ReleaseNumber pulumi.IntOutput `pulumi:"releaseNumber"`
	// RunNumber is the run of the upstream pipeline that converged the stage.
	RunNumber pulumi.IntOutput `pulumi:"runNumber"`
	// Artifacts are the artifacts of the release, by name.
	Artifacts pulumi.MapOutput `pulumi:"artifacts"`
}

type ArtifactArgs struct {
	Stage any `pulumi:"stage"`
	// Value is the value to publish. The artifact is named like the resource.
	Value any `pulumi:"value"`
}
type Artifact struct {
	pulumi.ResourceState
	// Value is the published value. It is secret when the input is.
	Value pulumi.AnyOutput `pulumi:"value"`
	// ChildUrn is the URN of the internal child resource of the artifact.
	ChildUrn pulumi.StringOutput `pulumi:"childUrn"`
}

// notInferred stands in for the Construct of a component. The wrapper in construct.go handles every
// component type of this provider.
func notInferred[A any, O pulumi.ComponentResource](*pulumi.Context, string, A, ...pulumi.ResourceOption) (O, error) {
	var none O
	return none, errors.New("Delivery components are constructed by the Construct wrapper")
}

// sourceKinds maps the type of each source component to the kind in its identity.
var sourceKinds = map[string]string{
	"delivery:index:GitHubSource":      "github",
	"delivery:index:GitLabSource":      "gitlab",
	"delivery:index:BitbucketSource":   "bitbucket",
	"delivery:index:AzureDevOpsSource": "azure_devops",
}

const (
	typePipeline = "delivery:index:Pipeline"
	typeAgent    = "delivery:index:Agent"
	typeStage    = "delivery:index:Stage"
	typeJob      = "delivery:index:Job"
	typeStacks   = "delivery:index:Stacks"
	typeArtifact = "delivery:index:Artifact"

	typePipelineSource = "delivery:index:PipelineSource"
	typeMemberRun      = "delivery:index:MemberRun"
)
