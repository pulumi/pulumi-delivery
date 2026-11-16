// Copyright 2026, Pulumi Corporation. All rights reserved.

package main

import "github.com/pulumi/pulumi-go-provider/infer"

func (r *WorkflowsRuntime) Annotate(a infer.Annotator) {
	a.Describe(r, "Selects the pool that runs the work of a member. In the private preview, all work runs on the hosted pool, so do not use this resource yet. Agent pools are not available.")
}

func (r *WorkflowsRuntimeArgs) Annotate(a infer.Annotator) {
	a.Describe(r, "Pool selection. In the private preview, all work runs on the hosted pool.")
	a.Describe(&r.AgentPool, "Pool identifier. The resource returns it unchanged as ref.")
}

func (r *Rule) Annotate(a infer.Annotator) {
	a.Describe(r, "Rule that a release must pass to enter a stage. A rule can contain other rules, up to three levels deep.")
	a.Describe(&r.Kind, "Kind of the rule. Valid values are approval, http, manual, all, and any.")
	a.Describe(&r.Name, "Rule name. Omit it to let Delivery assign one.")
	a.Describe(&r.Approvers, "Pulumi usernames or team names in the organization. If the list is empty, anyone with the pipeline:approve permission on the pipeline stack can approve.")
	a.Describe(&r.Count, "Number of approvals required. If you omit it or set it to zero, one approval is required. Applies to approval rules.")
	a.Describe(&r.URL, "URL to call. Required for http rules.")
	a.Describe(&r.Method, "HTTP request method. The default is GET.")
	a.Describe(&r.Headers, "HTTP request headers. Secret values stay secret.")
	a.Describe(&r.HeadersEnvironment, "Name of an ESC environment that exports values.headers. Delivery sends these headers first, then the headers of the rule.")
	a.Describe(&r.Body, "HTTP request body. Secret values stay secret.")
	a.Describe(&r.SuccessExpression, "JMESPath expression that Delivery evaluates on the status, headers and body of the HTTP response. The rule passes when the result is true. Required for http rules.")
	a.Describe(&r.Interval, "Time between HTTP requests, as a Go duration, such as 1m. Use a whole number of seconds. The minimum is 60 seconds.")
	a.Describe(&r.Timeout, "Timeout of each HTTP request, as a Go duration, such as 10s. Use a whole number of seconds from 1 to 20.")
	a.Describe(&r.Rules, "Child rules for all and any rules. At least one is required.")
	a.Describe(&r.Description, "Text for the person who continues a manual gate, for example what to check first.")
}

func (r *Approval) Annotate(a infer.Annotator) {
	a.Describe(r, `Builds an approval rule for the when property of a Stage. The gate waits until the required number of eligible people approve. If you omit count or set it to zero, one approval is required. Anyone eligible can approve, including the person who started the run. If you name no approvers, anyone with the pipeline:approve permission on the pipeline stack can approve. Approving needs the Approvals feature.

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
	a.Describe(&r.Approvers, "Pulumi usernames or team names in the organization. If the list is empty, anyone with the pipeline:approve permission on the pipeline stack can approve.")
	a.Describe(&r.Count, "Number of approvals required. If you omit it or set it to zero, one approval is required.")
}

func (r *HTTP) Annotate(a infer.Annotator) {
	a.Describe(r, `Builds an http rule for the when property of a Stage. The gate calls the URL until the success expression is true. Delivery does not support this rule in the private preview. Registration rejects a stage that uses it.

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
	a.Describe(&r.Headers, "HTTP request headers. Secret values stay secret.")
	a.Describe(&r.HeadersEnvironment, "Name of an ESC environment that exports values.headers. Delivery sends these headers first, then the headers of the rule.")
	a.Describe(&r.Body, "HTTP request body. Secret values stay secret.")
	a.Describe(&r.SuccessExpression, "JMESPath expression that Delivery evaluates on the status, headers and body of the HTTP response. The rule passes when the result is true. Required.")
	a.Describe(&r.Interval, "Time between HTTP requests, as a Go duration, such as 1m. Use a whole number of seconds. The minimum is 60 seconds.")
	a.Describe(&r.Timeout, "Timeout of each HTTP request, as a Go duration, such as 10s. Use a whole number of seconds from 1 to 20.")
}

func (r *Manual) Annotate(a infer.Annotator) {
	a.Describe(r, `Builds a manual rule for the when property of a Stage. A manual gate waits until someone continues it. Continue it in the console or with a POST to the continue route of the gate. It works on every plan. To require named approvers, use approval(), which needs the Approvals feature.

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
	a.Describe(&r.Name, "Rule name. If you omit it, Delivery uses manual. Names must be unique within a gate.")
	a.Describe(&r.Description, "Text for the person who continues the gate, for example what to check first.")
}

func (r *AllOf) Annotate(a infer.Annotator) {
	a.Describe(r, `Builds an all rule for the when property of a Stage. The rule passes when every child rule passes.

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
	a.Describe(r, `Builds an any rule for the when property of a Stage. The rule passes when one child rule passes.

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
	a.Describe(r, "Settings of an all or any rule.")
	a.Describe(&r.Name, "Rule name. Omit it to let Delivery assign one.")
	a.Describe(&r.Rules, "Child rules. At least one is required. A rule can contain other rules, up to three levels deep.")
}

func (r *Pipeline) Annotate(a infer.Annotator) {
	a.Describe(r, `Declares the pipeline. It gives the release number of the run. Set delivery:source in the stack config to tell Delivery where the program lives. A program has one Pipeline.

To register the pipeline, run PULUMI_DELIVERY_REGISTER=true pulumi up on the pipeline stack. Registration ends with exit code 10, because the members wait for a run.

## Example Usage

`+"```"+`typescript
import * as delivery from "@pulumi/delivery";

const pipeline = new delivery.Pipeline("shop");
export const release = pipeline.releaseNumber;
`+"```")
	a.Describe(&r.ReleaseNumber, "Number of the release of the run. It is unknown during registration and preview.")
}

func (r *PipelineArgs) Annotate(a infer.Annotator) {
	a.Describe(r, "Pipeline settings.")
	a.Describe(&r.ServicePrincipal, "Service principal that the pipeline runs as. Without it, Delivery generates one for the pipeline.")
}

func (r *PipelineServicePrincipal) Annotate(a infer.Annotator) {
	a.Describe(r, "Service principal that the pipeline runs as. Without it, Delivery generates one for the pipeline.")
	a.Describe(&r.UserID, "ID or login of a user-defined service principal. Cannot be set together with teamName.")
	a.Describe(&r.TeamName, "Name of a team. The pipeline runs as the service principal of the team, with the roles of the team. Not supported yet. Cannot be set together with userId.")
}

func (r *ProviderConfig) Annotate(a infer.Annotator) {
	a.Describe(&r.Source, "Location of the pipeline program, the recommended way to tell Delivery where the program lives.")
}

func (r *ProgramSource) Annotate(a infer.Annotator) {
	a.Describe(r, "Location of the pipeline program in a repository.")
	a.Describe(&r.URL, "Clone URL of the repository.")
	a.Describe(&r.Branch, "Branch that tracks the pipeline program. Omit it to let the service choose the branch.")
	a.Describe(&r.Directory, "Directory that contains the Pulumi.yaml of the pipeline, relative to the repository root. Omit it to let the service choose the directory.")
}

func (r *Stage) Annotate(a infer.Annotator) {
	a.Describe(r, `Declares a stage of the pipeline. The from stages set the order of promotion, and when adds an optional rule, such as an approval. A stage with several upstream stages waits until the same release converges in all of them.

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

const (
	imageDoc = "Container image that the member runs in. The default is the Pulumi image. Use it to pin the Pulumi CLI or language versions, for example pulumi/pulumi-nodejs:3.272.0. The image needs bash, curl, gzip and the Pulumi CLI on PATH."

	preRunCommandsDoc = "Bash commands that run in the project directory after the dependencies are installed and before the Pulumi command or the command of the job. Each command runs as its own step, so environment changes do not carry over to the next step."
)

func (r *Image) Annotate(a infer.Annotator) {
	a.Describe(r, "Container image that a member runs in.")
	a.Describe(&r.Reference, "Image reference, for example pulumi/pulumi-nodejs:3.272.0.")
	a.Describe(&r.Credentials, "Credentials for a private registry.")
}

func (r *ImageCredentials) Annotate(a infer.Annotator) {
	a.Describe(r, "Credentials for a private registry.")
	a.Describe(&r.Username, "Registry user name.")
	a.Describe(&r.Password, "Registry password or token. It is a secret.")
}

func (r *Job) Annotate(a infer.Annotator) {
	a.Describe(r, `Declares a command that a run executes in a stage. Delivery checks out the source at the commit of the run and runs the command in directory.

A resource that is not a Delivery resource waits for the job when it uses an output of the job.

The command can read these variables: DELIVERY_RELEASE, DELIVERY_RUN, DELIVERY_STAGE, DELIVERY_COMMIT and DELIVERY_PIPELINE. To publish outputs, write a JSON object to the file that DELIVERY_OUTPUTS names. Each key is one output. The file can be at most 1 MiB. To mark an output as secret, write it as {"value": ..., "secret": true}.

If one output is secret, the whole map is secret. An output is secret when the job marks it as secret or when it contains a secret environment value.

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
	a.Describe(&r.Outputs, "Outputs that the command wrote. They are unknown until the job runs.")
}

func (r *JobArgs) Annotate(a infer.Annotator) {
	a.Describe(r, "Job settings.")
	a.Describe(&r.Stage, "Stage that the job runs in.")
	a.Describe(&r.Source, "VCS source to check out.")
	a.Describe(&r.Directory, "Directory to run the command in, relative to the repository root.")
	a.Describe(&r.Command, "Command to run with sh -c. To publish outputs, the command writes them to the file that DELIVERY_OUTPUTS names.")
	a.Describe(&r.Environment, "Environment variables for the command. Secret values stay secret. The names PULUMI_ACCESS_TOKEN and PULUMI_BACKEND_URL, and all names that start with DELIVERY_, are reserved.")
	a.Describe(&r.Timeout, "Maximum run time as a Go duration, such as 30m. Delivery does not apply this value in the private preview. A job stops after 2 hours.")
	a.Describe(&r.Runtime, "Pool that runs the job. In the private preview, all work runs on the hosted pool, so omit it.")
	a.Describe(&r.Image, imageDoc)
	a.Describe(&r.PreRunCommands, preRunCommandsDoc)
}

func (r *Stacks) Annotate(a infer.Annotator) {
	a.Describe(r, `Updates one or more existing stacks in a stage. Each entry of stacks updates one target stack. Delivery checks out the source of the entry at the commit of the run and runs pulumi up on the target stack.

Create each target stack before the first run. A target stack must be an ordinary stack in the organization of the pipeline.

The entries of a group run together. They can read the new outputs of each other through stack references. The group is one unit for dependencies and retries. A resource that uses an output of one entry waits for the whole group. If one entry fails, a retry runs every entry again. Each entry uses one slot of the concurrent job limit of the organization. A group with more entries than the limit can block itself. Delivery fails it after 2 hours without progress.

If the output of one entry is secret, all outputs of the group are secret.

## Example Usage

`+"```"+`typescript
import * as delivery from "@pulumi/delivery";

new delivery.Stacks("dev", {
    stage: dev,
    stacks: {
        api: { stack: "shop-infra/dev", source: code, directory: "infra", config: { environment: "dev" } },
    },
});
`+"```")
	a.Describe(&r.Outputs, "Outputs of each entry, by entry name. They are unknown until the group runs.")
}

func (r *StacksArgs) Annotate(a infer.Annotator) {
	a.Describe(r, "Settings of a group of stacks.")
	a.Describe(&r.Stage, "Stage that the group runs in.")
	a.Describe(&r.Stacks, "Entries by name. A name can contain letters, digits, underscores and hyphens. Each entry updates one target stack.")
}

func (r *StacksEntry) Annotate(a infer.Annotator) {
	a.Describe(r, "One target stack that a Stacks resource updates.")
	a.Describe(&r.Stack, "Target stack as project/stack. It must exist before the first run, in the organization of the pipeline.")
	a.Describe(&r.Source, "VCS source to check out. The update uses the commit of the run for this source.")
	a.Describe(&r.Directory, "Directory of the stack project, relative to the repository root.")
	a.Describe(&r.Config, "Config values that Delivery sets on the target stack before the update. Values are plain strings. Delivery rejects secret values. Keep secrets in the config file of the target stack or in an ESC environment.")
	a.Describe(&r.Environment, "Environment variables for the update, for example cloud credentials. Secret values stay secret. The names PULUMI_ACCESS_TOKEN and PULUMI_BACKEND_URL, and all names that start with DELIVERY_, are reserved.")
	a.Describe(&r.Runtime, "Pool that runs the update. In the private preview, all work runs on the hosted pool, so omit it.")
	a.Describe(&r.Timeout, "Maximum run time of the update in seconds. The default is 7200 (2 hours).")
	a.Describe(&r.Image, imageDoc)
	a.Describe(&r.PreRunCommands, preRunCommandsDoc)
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
	a.Describe(r, `Declares a GitHub repository and branch. Members check out this source. The source locks its commit when the run first creates it, and every stage of the run gets that commit. The commit output holds this commit. It is unknown during registration and preview.

A source does not start runs unless you set trigger. Set trigger to true to start a run on every push to the branch. Set it to { paths } to start a run only when a push changes a matching file.

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
	a.Describe(&r.Repository, "Repository of the source. Use `owner/repo` on GitHub, `group/subgroup/repo` (the full path) on GitLab, `workspace/repo` on Bitbucket and `org/project/repo` on Azure DevOps. Pulumi Cloud picks the integration of the organization whose account (GitHub), group (GitLab), workspace (Bitbucket) or organization and project (Azure DevOps) holds the repository.")
	a.Describe(&r.Branch, "Branch of the source.")
	a.Describe(&r.Trigger, "Starts a run when someone pushes to the branch. Set true for every push, or { paths } for pushes that change a matching file. If you omit it, the source is part of the release but never starts a run.")
}

func (r *SourceTrigger) Annotate(a infer.Annotator) {
	a.Describe(r, "Limits the pushes that start a run.")
	a.Describe(&r.Paths, "Glob patterns of changed files. A push starts a run only when it changes a file that matches one of them.")
}

func (r *PipelineSource) Annotate(a infer.Annotator) {
	a.Describe(r, `Reads the release that a stage of another pipeline in the same organization deployed. The outputs are unknown during registration and preview. If one artifact is secret, the whole map is secret.

A job cannot check out a pipeline source.

A pipeline source does not start runs unless you set trigger: true. Then each new release of the upstream stage starts a run of this pipeline.

## Example Usage

`+"```"+`typescript
import * as delivery from "@pulumi/delivery";

const platform = new delivery.PipelineSource("platform", {
    pipeline: "platform-delivery/main",
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
	a.Describe(&r.Pipeline, "Upstream pipeline as project/stack, in the organization of this pipeline.")
	a.Describe(&r.Stage, "Stage of the upstream pipeline.")
	a.Describe(&r.Trigger, "Set true to start a run of this pipeline when the stage deploys a new release. If you omit it, the source is part of the release but never starts a run.")
}

func (r *Artifact) Annotate(a infer.Annotator) {
	a.Describe(r, `Publishes a value for the pipelines that read this stage with a PipelineSource. The artifact has the name of the resource. It succeeds when its stage admits the release and its value is known.

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
	a.Describe(&r.Value, "Value to publish. It can be any value, including a secret or a structure.")
}
