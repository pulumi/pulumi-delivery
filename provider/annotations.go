// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import "github.com/pulumi/pulumi-go-provider/infer"

func (r *WorkflowsRuntime) Annotate(a infer.Annotator) {
	a.Describe(r, "Selects the pool that runs the work of a member. The preview runs on the hosted pool only, so do not use this resource in the preview. Agent pools are not available yet.")
}

func (r *WorkflowsRuntimeArgs) Annotate(a infer.Annotator) {
	a.Describe(r, "Pool selection. The preview runs on the hosted pool only.")
	a.Describe(&r.AgentPool, "Pool identifier. Returned unchanged as the ref of the resource.")
}

func (r *Rule) Annotate(a infer.Annotator) {
	a.Describe(r, "Stage entry rule. Rules may nest at most three levels.")
	a.Describe(&r.Kind, "Rule kind: approval, http, manual, all, or any.")
	a.Describe(&r.Name, "Rule name. Omit it to let Delivery assign one.")
	a.Describe(&r.Approvers, "Pulumi usernames or team names in the organization. An empty list lets anyone with the organization update permission approve. The person who started the run can never approve it, and no setting changes that.")
	a.Describe(&r.Count, "Number of approvals required. For approval rules, omitted or zero means one.")
	a.Describe(&r.URL, "URL to call for an http rule. Required for that kind.")
	a.Describe(&r.Method, "HTTP request method. The default is GET.")
	a.Describe(&r.Headers, "HTTP request headers. Pulumi secret values remain secret.")
	a.Describe(&r.HeadersEnvironment, "Name of an ESC environment that exports values.headers. Its headers go first, then the headers of the rule.")
	a.Describe(&r.Body, "HTTP request body. Pulumi secret values remain secret.")
	a.Describe(&r.SuccessExpression, "JMESPath expression that the service evaluates over the status, headers and body of the HTTP response. The rule passes when it is true. Required for http rules.")
	a.Describe(&r.Interval, "HTTP polling interval as a Go duration, such as 1m. Must be a positive whole number of seconds, at least 60 seconds.")
	a.Describe(&r.Timeout, "HTTP request timeout as a Go duration, such as 10s. Must be a positive whole number of seconds, at most 20 seconds.")
	a.Describe(&r.Rules, "Child rules for all or any. At least one child is required.")
	a.Describe(&r.Description, "Text for the person who continues a manual gate, for example what to check first.")
}

func (r *Approval) Annotate(a infer.Annotator) {
	a.Describe(r, `Builds an approval rule for a Stage's when. The gate waits until the required number of eligible people approve. Omitted or zero count means one approval. The person who started the run cannot approve it. Without named approvers, anyone with the organization update permission can approve. Approving needs the Approvals feature.

## Example Usage

`+"```"+`typescript
import * as delivery from "@pulumi/delivery";

const prod = new delivery.Stage("prod", {
    pipeline,
    from: [dev],
    when: delivery.approval({ approvers: ["your-username"] }),
});
`+"```")
}

func (r *ApprovalArgs) Annotate(a infer.Annotator) {
	a.Describe(r, "Approval rule settings.")
	a.Describe(&r.Name, "Rule name. Omit it to let Delivery assign one.")
	a.Describe(&r.Approvers, "Pulumi usernames or team names in the organization. An empty list lets anyone with the organization update permission approve. The person who started the run can never approve it, and no setting changes that.")
	a.Describe(&r.Count, "Number of approvals required. Omitted or zero means one.")
}

func (r *HTTP) Annotate(a infer.Annotator) {
	a.Describe(r, `Builds an http rule for a Stage's when.

## Example Usage

`+"```"+`typescript
import * as delivery from "@pulumi/delivery";

const prod = new delivery.Stage("prod", {
    pipeline,
    from: [dev],
    when: delivery.http({ url: "https://example.com/check", successExpression: "status == 'ok'" }),
});
`+"```")
}

func (r *HTTPArgs) Annotate(a infer.Annotator) {
	a.Describe(r, "HTTP rule settings.")
	a.Describe(&r.Name, "Rule name. Omit it to let Delivery assign one.")
	a.Describe(&r.URL, "URL to call. Required.")
	a.Describe(&r.Method, "HTTP request method. The default is GET.")
	a.Describe(&r.Headers, "HTTP request headers. Pulumi secret values remain secret.")
	a.Describe(&r.HeadersEnvironment, "Name of an ESC environment that exports values.headers. Its headers go first, then the headers of the rule.")
	a.Describe(&r.Body, "HTTP request body. Pulumi secret values remain secret.")
	a.Describe(&r.SuccessExpression, "JMESPath expression that the service evaluates over the status, headers and body of the HTTP response. The rule passes when it is true. Required.")
	a.Describe(&r.Interval, "HTTP polling interval as a Go duration, such as 1m. Must be a positive whole number of seconds, at least 60 seconds.")
	a.Describe(&r.Timeout, "HTTP request timeout as a Go duration, such as 10s. Must be a positive whole number of seconds, at most 20 seconds.")
}

func (r *Manual) Annotate(a infer.Annotator) {
	a.Describe(r, `Builds a manual rule for a Stage's when. A manual gate waits until someone continues it, from the console or with a POST to the continue route of the gate. It works on every plan. Use approval() when you need named approvers and the Approvals feature.

## Example Usage

`+"```"+`typescript
import * as delivery from "@pulumi/delivery";

const prod = new delivery.Stage("prod", {
    pipeline,
    from: [dev],
    when: delivery.manual({ name: "dashboards", description: "Check the dashboards before you continue." }),
});
`+"```")
}

func (r *ManualArgs) Annotate(a infer.Annotator) {
	a.Describe(r, "Manual rule settings.")
	a.Describe(&r.Name, "Rule name. Omit it to let Delivery use the kind, manual. Names are unique within a gate.")
	a.Describe(&r.Description, "Text for the person who continues the gate, for example what to check first.")
}

func (r *AllOf) Annotate(a infer.Annotator) {
	a.Describe(r, `Builds an all rule for a Stage's when: passes once every child rule passes.

## Example Usage

`+"```"+`typescript
import * as delivery from "@pulumi/delivery";

const prod = new delivery.Stage("prod", {
    pipeline,
    from: [dev],
    when: delivery.allOfOutput({
        rules: [delivery.approvalOutput({ approvers: ["your-username"] })],
    }),
});
`+"```")
}

func (r *AnyOf) Annotate(a infer.Annotator) {
	a.Describe(r, `Builds an any rule for a Stage's when: passes once one child rule passes.

## Example Usage

`+"```"+`typescript
import * as delivery from "@pulumi/delivery";

const prod = new delivery.Stage("prod", {
    pipeline,
    from: [dev],
    when: delivery.anyOfOutput({
        rules: [delivery.approvalOutput({ approvers: ["your-username"] }), delivery.manualOutput()],
    }),
});
`+"```")
}

func (r *CombinedRuleArgs) Annotate(a infer.Annotator) {
	a.Describe(r, "All/any rule settings.")
	a.Describe(&r.Name, "Rule name. Omit it to let Delivery assign one.")
	a.Describe(&r.Rules, "Child rules. At least one is required. Rules may nest at most three levels.")
}

func (r *Pipeline) Annotate(a infer.Annotator) {
	a.Describe(r, `Declares the pipeline: the source and directory of the pipeline program itself, and the release number of the run. A program has one Pipeline.

Run pulumi up on the pipeline stack to register it. Registration ends with exit code 10, because the members wait for a run.

## Example Usage

`+"```"+`typescript
import * as delivery from "@pulumi/delivery";

const code = new delivery.GitHubSource("code", {
    repository: "acme/shop",
    branch: "main",
    trigger: true,
});
const pipeline = new delivery.Pipeline("shop", {
    program: { source: code, directory: "delivery" },
});
export const release = pipeline.releaseNumber;
`+"```")
	a.Describe(&r.ReleaseNumber, "Number of the release of the run. It is unknown during registration and preview.")
}

func (r *PipelineArgs) Annotate(a infer.Annotator) {
	a.Describe(r, "Pipeline settings.")
	a.Describe(&r.Program, "Source and directory of the pipeline program.")
}

func (r *ProgramSource) Annotate(a infer.Annotator) {
	a.Describe(r, "Checkout location of the pipeline program.")
	a.Describe(&r.Source, "VCS source of the pipeline program.")
	a.Describe(&r.Directory, "Directory containing the pipeline Pulumi.yaml, relative to the repository root.")
}

func (r *Stage) Annotate(a infer.Annotator) {
	a.Describe(r, `Declares a stage of the pipeline. The from stages define promotion edges, and when adds an optional rule such as approval. A stage with several upstream stages waits for the same release to converge in all of them.

## Example Usage

`+"```"+`typescript
import * as delivery from "@pulumi/delivery";

const dev = new delivery.Stage("dev", { pipeline });
new delivery.Stage("prod", {
    pipeline,
    from: [dev],
    when: delivery.approval({ approvers: ["your-username"] }),
});
`+"```")
}

func (r *StageArgs) Annotate(a infer.Annotator) {
	a.Describe(r, "Stage settings.")
	a.Describe(&r.Pipeline, "Pipeline that the stage belongs to.")
	a.Describe(&r.From, "Upstream stages. Omit for an entry stage.")
	a.Describe(&r.When, "Optional rule that must pass before the release enters this stage.")
}

func (r *Job) Annotate(a infer.Annotator) {
	a.Describe(r, `Declares a command that a run executes in a stage. The service checks out the source at the commit of the run and runs the command in directory.

A resource that is not a Delivery resource waits for the job when it uses an output of the job.

The command sees these variables: DELIVERY_RELEASE, DELIVERY_RUN, DELIVERY_STAGE, DELIVERY_COMMIT and DELIVERY_PIPELINE. To publish outputs, the command writes a JSON object to the file that DELIVERY_OUTPUTS names. Each key is one output. The file can be at most 1 MiB. To mark an output as secret, write it as {"value": ..., "secret": true}.

If any output is secret, the whole map is secret. An output is secret when the job marks it or when it contains a secret environment value.

## Example Usage

`+"```"+`typescript
import * as delivery from "@pulumi/delivery";

new delivery.Job("smoke", {
    stage: prod,
    source: code,
    directory: "delivery",
    command: "./scripts/smoke.sh",
});
`+"```")
	a.Describe(&r.Outputs, "Outputs that the command wrote. They are unknown until the job has run.")
}

func (r *JobArgs) Annotate(a infer.Annotator) {
	a.Describe(r, "Job settings.")
	a.Describe(&r.Stage, "Stage that the job runs in. A pipeline requires it.")
	a.Describe(&r.Source, "VCS source to check out.")
	a.Describe(&r.Directory, "Directory to run the command in, relative to the repository root.")
	a.Describe(&r.Command, "Command to run. It runs with sh -c. The command can write outputs to the file that DELIVERY_OUTPUTS names.")
	a.Describe(&r.Environment, "Environment variables for the command. A secret value stays secret. The names PULUMI_ACCESS_TOKEN and PULUMI_BACKEND_URL, and all names that start with DELIVERY_, are reserved.")
	a.Describe(&r.Timeout, "Maximum run time as a Go duration, such as 30m. The preview does not apply this value yet. A job stops after 2 hours.")
}

func (r *Stacks) Annotate(a infer.Annotator) {
	a.Describe(r, `Updates one or more existing stacks in a stage. Each entry of stacks is one update of a target stack. Delivery checks out the source of the entry at the commit of the run and runs pulumi up on the target stack.

Create each target stack before the first run. A target stack must be an ordinary stack in the organization of the pipeline.

The entries of a group run together and can read each other's new outputs through stack references. The group is one unit for dependencies and retries: a resource that uses an output of one entry waits for the whole group, and a retry runs every entry again if one entry failed. Each entry uses one slot of the concurrent job limit of the organization. A group with more entries than the limit can block itself, and Delivery fails it after 2 hours without progress.

The outputs of the group are secret as a whole if the output of one entry is secret.

## Example Usage

`+"```"+`typescript
import * as delivery from "@pulumi/delivery";

new delivery.Stacks("dev", {
    stage: dev,
    stacks: {
        api: { stack: "acme/shop-infra/dev", source: code, directory: "infra", config: { environment: "dev" } },
    },
});
`+"```")
	a.Describe(&r.Outputs, "Outputs of each entry, by entry name. They are unknown until the group has run.")
}

func (r *StacksArgs) Annotate(a infer.Annotator) {
	a.Describe(r, "Settings of a group of stacks.")
	a.Describe(&r.Stage, "Stage that the group runs in. A pipeline requires it.")
	a.Describe(&r.Stacks, "Entries by name. Names can use letters, digits, underscores and hyphens. Each entry updates one target stack.")
}

func (r *StacksEntry) Annotate(a infer.Annotator) {
	a.Describe(r, "One target stack that a Stacks resource updates.")
	a.Describe(&r.Stack, "Target stack as organization/project/stack. It must exist before the first run, in the organization of the pipeline.")
	a.Describe(&r.Source, "VCS source to check out. The update uses the commit of the run for this source.")
	a.Describe(&r.Directory, "Directory of the stack project, relative to the repository root.")
	a.Describe(&r.Config, "Config values that Delivery sets on the target stack before the update. Values are plain strings, and Delivery rejects secret values. Keep secrets in the config file of the target stack or in an ESC environment.")
	a.Describe(&r.Environment, "Environment variables for the update, for example cloud credentials. A secret value stays secret. The names PULUMI_ACCESS_TOKEN and PULUMI_BACKEND_URL, and all names that start with DELIVERY_, are reserved.")
	a.Describe(&r.Runtime, "Pool that runs the update. The preview runs on the hosted pool only, so omit it.")
	a.Describe(&r.Timeout, "Maximum run time of the update in seconds. The default is 7200 (2 hours).")
}

func (r *MemberRun) Annotate(a infer.Annotator) {
	a.Describe(r, "Internal child resource of a member such as a Job. It is not in the schema.")
}

func (r *MemberRunArgs) Annotate(a infer.Annotator) {
	a.Describe(r, "Declaration of the member that owns this resource.")
	a.Describe(&r.Organization, "Organization of the stack.")
	a.Describe(&r.Member, "URN of the member that owns this resource.")
	a.Describe(&r.Declaration, "Inputs of the member.")
}

func (r *MemberRunState) Annotate(a infer.Annotator) {
	a.Describe(r, "Result of the member.")
	a.Describe(&r.Outputs, "Outputs of the member. An output is secret only when the service marks it.")
}

func (r *GitHubSource) Annotate(a infer.Annotator) {
	a.Describe(r, `Declares a GitHub repository and branch. Members check out this source. The source locks its commit when the run first creates it, and every stage of the run gets that commit. The commit output is that commit. It is unknown during registration and preview.

A source does not start runs unless you set trigger. Set trigger to true to start a run on every push to the branch, or to { paths } to start one only when a push changes a matching file.

## Example Usage

`+"```"+`typescript
import * as delivery from "@pulumi/delivery";

const code = new delivery.GitHubSource("code", {
    repository: "acme/shop",
    branch: "main",
    trigger: { paths: ["src/**", "delivery/**"] },
});
`+"```")
	a.Describe(&r.Commit, "Commit of the run for this source. It is unknown during registration and preview.")
}

func (r *GitLabSource) Annotate(a infer.Annotator) {
	a.Describe(r, "Declares a GitLab repository and branch. See GitHubSource.")
	a.Describe(&r.Commit, "Commit of the run for this source. It is unknown during registration and preview.")
}

func (r *BitbucketSource) Annotate(a infer.Annotator) {
	a.Describe(r, "Declares a Bitbucket repository and branch. See GitHubSource.")
	a.Describe(&r.Commit, "Commit of the run for this source. It is unknown during registration and preview.")
}

func (r *AzureDevOpsSource) Annotate(a infer.Annotator) {
	a.Describe(r, "Declares an Azure DevOps repository and branch. See GitHubSource.")
	a.Describe(&r.Commit, "Commit of the run for this source. It is unknown during registration and preview.")
}

func (r *SourceArgs) Annotate(a infer.Annotator) {
	a.Describe(r, "Source settings.")
	a.Describe(&r.Repository, "Repository identifier understood by the VCS integration.")
	a.Describe(&r.Branch, "Branch of the source.")
	a.Describe(&r.Integration, "Name of the VCS integration. Omit it when the organization has one integration for this forge.")
	a.Describe(&r.Trigger, "Starts a run on a push to the branch. Set true for every push, or { paths } for pushes that change a matching file. Omit it for a source that is part of the release but never starts a run.")
}

func (r *SourceTrigger) Annotate(a infer.Annotator) {
	a.Describe(r, "Limits the pushes that start a run.")
	a.Describe(&r.Paths, "Glob patterns of changed files. A push starts a run only when it changes a file that matches.")
}

func (r *PipelineSource) Annotate(a infer.Annotator) {
	a.Describe(r, `Reads the release that a stage of another pipeline in the same organization deployed. The outputs are unknown during registration and preview. If any artifact is secret, the whole map is secret.

A pipeline source is not a checkout source: a job cannot check it out.

A pipeline source does not start runs unless you set trigger: true. Then a new release of the upstream stage starts a run of this pipeline.

## Example Usage

`+"```"+`typescript
import * as delivery from "@pulumi/delivery";

const platform = new delivery.PipelineSource("platform", {
    pipeline: "acme/platform-delivery/main",
    stage: "prod",
    trigger: true,
});
const endpoint = platform.artifacts.apply(a => a["clusterEndpoint"]);
`+"```")
	a.Describe(&r.ReleaseNumber, "Number of the release that the stage of the upstream pipeline deployed.")
	a.Describe(&r.RunNumber, "Number of the run of the upstream pipeline that converged the stage.")
	a.Describe(&r.Artifacts, "Artifacts of the release, by name.")
}

func (r *PipelineSourceArgs) Annotate(a infer.Annotator) {
	a.Describe(r, "Pipeline source settings.")
	a.Describe(&r.Pipeline, "Upstream pipeline as org/project/stack.")
	a.Describe(&r.Stage, "Stage of the upstream pipeline.")
	a.Describe(&r.Trigger, "Set true to start a run of this pipeline when the stage deploys a new release. Omit it for a source that is part of the release but never starts a run.")
}

func (r *Artifact) Annotate(a infer.Annotator) {
	a.Describe(r, `Publishes a value for the pipelines that read this stage with a PipelineSource. The artifact is named like the resource. It succeeds once its stage is admitted and its value is known.

## Example Usage

`+"```"+`typescript
import * as delivery from "@pulumi/delivery";

new delivery.Artifact("clusterEndpoint", {
    stage: prod,
    value: cluster.endpoint,
});
`+"```")
}

func (r *ArtifactArgs) Annotate(a infer.Annotator) {
	a.Describe(r, "Artifact settings.")
	a.Describe(&r.Stage, "Stage that publishes the artifact.")
	a.Describe(&r.Value, "Value to publish. It can be any value, also a secret or a structure.")
}
