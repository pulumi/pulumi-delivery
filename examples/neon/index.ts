import * as pulumi from "@pulumi/pulumi";
import * as delivery from "@pulumi/delivery";

const config = new pulumi.Config();

// A run starts when a user creates or updates an issue with the label "neon" in the Todo state. Point the
// Linear webhook for issues at issuesUrl. The issue fields are known in a run.
const issues = new delivery.LinearIssueSource("issues", {
    signingSecret: config.requireSecret("linearSigningSecret"),
    labels: ["neon"],
    states: ["Todo"],
});
// The repository that the agent works in. It is part of the release but does not start runs.
const code = new delivery.GitHubSource("code", {repository: "acme/shop", branch: "main"});
const pipeline = new delivery.Pipeline("neon", {
    program: {source: code, directory: "delivery"},
});

const implement = new delivery.Stage("implement", {pipeline});
// The implementer agent works on the issue in the repository and returns the number of its pull request.
const fix = new delivery.Agent("fix", {
    stage: implement,
    agent: "implementer",
    source: code,
    prompt: pulumi.interpolate`Implement ${issues.data.apply(data => data["identifier"])}: ${issues.data.apply(data => data["title"])}

${issues.data.apply(data => data["description"])}`,
    resultSchema: {type: "object", properties: {pullRequest: {type: "integer"}}, required: ["pullRequest"]},
});

// The reviewer agent decides whether the pull request is good enough to merge.
const merge = new delivery.Stage("merge", {
    pipeline,
    from: [implement],
    when: delivery.decisionOutput({
        agent: "reviewer",
        inputs: {
            issue: issues.data.apply(data => data["identifier"]),
            pullRequest: fix.result.apply(result => result["pullRequest"]),
        },
    }),
});
new delivery.Job("merge", {
    stage: merge,
    source: code,
    directory: ".",
    command: pulumi.interpolate`gh pr merge ${fix.result.apply(result => result["pullRequest"])} --squash`,
});

export const issuesUrl = issues.url;
export const taskId = fix.taskId;
export const pullRequest = fix.result.apply(result => result["pullRequest"]);
