# Pulumi Delivery

Pulumi Delivery runs your delivery pipeline as a Pulumi program. The program declares sources, stages, stacks, jobs and approval gates. Pulumi Cloud runs each release through the stages in order.

## Status

Pulumi Delivery is in private preview. Behavior, the SDK, the API and the routes under `preview` can change.

## Setup

### What you need

- Pulumi enables Delivery for your organization. Ask your Pulumi contact.
- Pulumi CLI v3.272.0 or later.
- Linux or macOS. The provider has no Windows build.
- A Pulumi Cloud backend. Delivery does not work with a file backend or with self-hosted Pulumi Cloud.

### Connect your version control

Set up the Pulumi integration for your forge.

- [GitHub app](https://www.pulumi.com/docs/integrations/version-control/github-app/)
- [GitLab](https://www.pulumi.com/docs/integrations/version-control/gitlab/)
- [Bitbucket](https://www.pulumi.com/docs/integrations/version-control/bitbucket/)
- [Azure DevOps](https://www.pulumi.com/docs/integrations/version-control/azure-devops-integration/)

Give the integration access to each repository that a source names, including the repository of the pipeline program. Pulumi Cloud picks the integration of your organization whose account (GitHub), group (GitLab), workspace (Bitbucket) or organization and project (Azure DevOps) holds the repository.

Write the `repository` of a source like this:

- GitHub: `owner/repo`
- GitLab: `group/subgroup/repo`, the full path
- Bitbucket: `workspace/repo`
- Azure DevOps: `org/project/repo`

### Add the SDK

Run this command in the directory of the pipeline program:

```sh
pulumi package add delivery
```

The command adds the latest version of the SDK and writes it to the `packages` section of `Pulumi.yaml`, for example `delivery: delivery@<version>`. Keep that entry. The hosted runner reads it and runs `pulumi install` for your program. To add a specific version, run `pulumi package add delivery@<version>`.

The Pulumi CLI downloads the provider plugin when a program needs it.

### Create the stacks

Create the pipeline stack, which is the stack of the program that declares the pipeline. In this guide it is `acme/shop-delivery/main`.

Create each target stack before the first run. A target stack must be an ordinary stack in the organization of the pipeline. A pipeline stack or a run stack cannot be a target. A run fails with "The target stack ... does not exist. Create it first." if a stack is missing.

### Config and cloud credentials

A target stack keeps its config and secrets in its own config file in the source repository, or in an ESC environment. Values in the `config` of a member are plain strings. Delivery rejects a secret config value when you register.

Delivery does not use Deployment settings and does not do an OIDC exchange of its own. You have two ways to give a member cloud credentials.

- Pass the credentials in the `environment` of the member. A secret value in `environment` stays secret.
- Use an ESC environment with OIDC, for example one that uses `fn::open::aws-login` with `oidc`. Import the environment in the config file of the target stack, for example `Pulumi.dev.yaml`:

  ```yaml
  environment:
    - aws-login/dev-sandbox
  ```

A member update runs with a short-lived token that has the default deployment role of the organization. That role must have permission to open the ESC environment. Otherwise the update fails with "you do not have permission to open environment '<org>/<project>/<env>'".

## Concepts

- **Pipeline program and pipeline stack.** The pipeline is a Pulumi program in a repository. You register it with `pulumi up` on the pipeline stack. Registration ends with exit code 10 because the members wait for a run. This is the expected result.
- **Run.** A run takes one release through the stages. Each run has its own run stack, named `<pipeline stack>-r<run number>`. Run stacks do not appear in stack lists or search. Open one from the page of its run. Do not run `pulumi up` on a run stack.
- **Stage.** A stage is a step of the pipeline. It waits for other stages with `from` and for a gate with `when`. A stage deploys one run at a time. A newer run replaces a run that waits for the stage or a gate, but not a run that the stage already admitted.
- **Member.** A member is a resource that does work in a stage. `Stacks` updates one or more existing stacks. `Job` runs a command. `Artifact` publishes a value for other pipelines.
- **Source and release.** A source is a repository and branch. A source locks its commit when the run first creates it, so all stages of a run work on the same code. The sources you declare form the release, which has a release number. Read it from `pipeline.releaseNumber`, and the commit of a source from `source.commit`. Both are unknown during registration and preview, and known in a run.
- **Trigger.** A trigger is a setting on a source that decides whether a change starts a run. See [Triggers](#triggers).

Declare every source at the top level of the program. A source that a member output creates after the first pass cannot lock a commit, so the run fails.

## Getting started

This guide builds a pipeline with a `dev` stage and a `prod` stage. The `dev` stage updates a stack and runs a smoke check. The `prod` stage waits for an approval and then updates a second stack.

### Repository layout

```
delivery/        the pipeline program
  Pulumi.yaml
  package.json
  index.ts
  smoke.sh
infra/           the project of the target stacks
  Pulumi.yaml
```

The repository is `acme/shop` on GitHub, and the branch is `main`.

### The target stack project

`infra/Pulumi.yaml` declares one config value and one output:

```yaml
name: shop-infra
runtime: yaml
config:
  environment:
    type: string
outputs:
  endpoint: https://${environment}.shop.example.com
```

Create the two target stacks:

```sh
cd infra
pulumi stack init acme/shop-infra/dev
pulumi stack init acme/shop-infra/prod
```

### The pipeline program

`delivery/Pulumi.yaml` names the project. Run `pulumi package add` in this directory to add the `packages` entry.

```yaml
name: shop-delivery
runtime: nodejs
```

`delivery/package.json`:

```json
{
  "name": "shop-delivery",
  "private": true,
  "dependencies": {
    "@pulumi/pulumi": "^3.0.0"
  },
  "devDependencies": {
    "typescript": "^5.0.0",
    "@types/node": "^22.0.0"
  }
}
```

`delivery/index.ts`:

```typescript
import * as delivery from "@pulumi/delivery";

const app = new delivery.GitHubSource("app", { repository: "acme/shop", branch: "main", trigger: true });

const pipeline = new delivery.Pipeline("shop", {
    program: { source: app, directory: "delivery" },
});

const dev = new delivery.Stage("dev", { pipeline });
const prod = new delivery.Stage("prod", {
    pipeline,
    from: [dev],
    when: delivery.approval({ approvers: ["your-colleague"] }),
});

const devStacks = new delivery.Stacks("dev", {
    stage: dev,
    stacks: {
        app: {
            stack: "acme/shop-infra/dev",
            source: app,
            directory: "infra",
            config: { environment: "dev" },
        },
    },
});

new delivery.Job("smoke", {
    stage: dev,
    source: app,
    directory: "delivery",
    command: "sh smoke.sh",
    environment: {
        ENDPOINT: devStacks.outputs.apply(outputs => String(outputs["app"]["endpoint"])),
    },
});

new delivery.Stacks("prod", {
    stage: prod,
    stacks: {
        app: {
            stack: "acme/shop-infra/prod",
            source: app,
            directory: "infra",
            config: { environment: "prod" },
        },
    },
});
```

Replace `acme/shop` and `your-colleague` with your repository and the Pulumi user name of the person who approves `prod`. It can be your own.

`delivery/smoke.sh` prints the endpoint of the `dev` stack. It writes one output to the file that `DELIVERY_OUTPUTS` names.

```sh
set -eu
echo "Checking $ENDPOINT"
echo "{\"checked\": \"$ENDPOINT\"}" > "$DELIVERY_OUTPUTS"
```

### Register the pipeline

Commit everything and push it. A run checks out the pipeline program from the repository.

```sh
cd delivery
npm install
pulumi stack init acme/shop-delivery/main
git add -A && git commit -m "Add the delivery pipeline" && git push
```

Register the pipeline:

```sh
pulumi up
```

The update ends with exit code 10 and one of these messages.

- "Registered. A run starts for commit abc1234 when this update finishes."
- "Registered. No run starts because commit abc1234 is not the head of branch main." Push your commit, or pull the latest commit and register again.
- "Registered. No run starts because the checkout has uncommitted changes. Commit the changes. Push the commit to start a run."
- "Registered. No run starts because the update has no git commit." Run `pulumi up` from a Git checkout.

### Watch the run

Open the Delivery page of your organization in the console and select the run. The `dev` stage updates `acme/shop-infra/dev` and runs the smoke job. The `prod` stage then waits at its gate.

Ask `your-colleague` to approve the `prod` gate in the console. The `prod` stage then updates `acme/shop-infra/prod`.

From now on, a push to `main` starts a run. You can also start a run with the "Run pipeline" button on the Delivery page, or with the API. A second dispatch with the same optional `key` returns the first run.

```sh
curl -X POST \
  -H "Authorization: token $PULUMI_ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"key": "first-run"}' \
  https://api.pulumi.com/api/preview/stacks/acme/shop-delivery/main/delivery/dispatch
```

## Triggers

A source without `trigger` is part of the release but never starts a run. Use this for a shared library repository.

Set `trigger: true` on a `GitHubSource`, `GitLabSource`, `BitbucketSource` or `AzureDevOpsSource` to start a run on every push to the branch.

Set `trigger: { paths: [...] }` to start a run only when a push changes a file that matches one of the paths:

```typescript
const app = new delivery.GitHubSource("app", {
    repository: "acme/shop",
    branch: "main",
    trigger: { paths: ["src/**", "delivery/**"] },
});
```

Set `trigger: true` on a `PipelineSource` to start a run when the named stage of another pipeline deploys a new release. Both pipelines must be in the same organization.

```typescript
const platform = new delivery.PipelineSource("platform", {
    pipeline: "acme/platform-delivery/main",
    stage: "prod",
    trigger: true,
});
```

`trigger` must be `true` or, on a source with a repository, an object with a non-empty `paths` list. `false` is an error. Leave the setting out instead.

## Patterns

The examples in this section assume the `delivery` import, a source `repo` and a `pipeline` as in [Getting started](#getting-started). A stage reads only the outputs of its own members, its sources and the pipelines it reads with a `PipelineSource`. Do not pass the output of a member in one stage to a member in a later stage.

### One stage per environment from a helper function

Write a function that adds the members of one environment to a stage. Call it once for each stage.

```typescript
function deploy(stage: delivery.Stage, name: string, region: string) {
    const app = new delivery.Stacks(name, {
        stage,
        stacks: { app: { stack: `acme/shop-app/${name}`, source: repo, directory: "app" } },
    });
    new delivery.Job(`smoke-${name}`, {
        stage,
        source: repo,
        directory: "delivery",
        command: "sh smoke.sh",
        environment: {
            ENDPOINT: app.outputs.apply(o => String(o["app"]["endpoint"])),
            EXPECTED_REGION: region,
        },
    });
}

const dev = new delivery.Stage("dev", { pipeline });
deploy(dev, "dev", "eu-central-1");
const staging = new delivery.Stage("staging", { pipeline, from: [dev] });
deploy(staging, "staging", "eu-west-1");
const prod = new delivery.Stage("prod", { pipeline, from: [staging], when: delivery.approval() });
deploy(prod, "prod", "us-west-2");
```

Use this pattern when the environments differ only in a few values. Resource names must be unique, so build them from the environment name. The region comes from the config file of each target stack (`aws:region` in `Pulumi.<stack>.yaml`), not from the pipeline. The `region` argument only tells the smoke test which region to expect.

### Smoke test after each deploy

Put a `Job` in the same stage as the `Stacks` member. The job reads the outputs of that member. It does not read the outputs of an earlier stage. The example above passes the endpoint in `ENDPOINT`. This is the `smoke.sh` for it:

```sh
set -eu
for attempt in 1 2 3 4 5; do
  if curl -fsS "$ENDPOINT/health" > /dev/null; then
    echo "{\"checked\": \"$ENDPOINT\"}" > "$DELIVERY_OUTPUTS"
    exit 0
  fi
  sleep 10
done
echo "Smoke test failed for $ENDPOINT" >&2
exit 1
```

A job that exits with a non-zero code fails, and its stage fails. A stage that depends on it with `from` then does not start. Use `curl -fsS` so that an HTTP error status gives a non-zero exit code. Retry in the script, because a new deployment can need some seconds to answer. See [Retry a failed run](#retry-a-failed-run) to run the job again.

### Stacks that deploy together

Put the stacks in one `Stacks` group when they depend on each other and you want to update them in the same step.

```typescript
new delivery.Stacks("platform", {
    stage: dev,
    stacks: {
        network: { stack: "acme/shop-network/dev", source: repo, directory: "network" },
        app: { stack: "acme/shop-app/dev", source: repo, directory: "app" },
    },
});
```

The program of the `app` stack reads the outputs of the `network` stack with a stack reference:

```typescript
const network = new pulumi.StackReference("acme/shop-network/dev");
const subnetId = network.getOutput("subnetId");
```

The entries of a group run together, and they read the new outputs of each other. A group is one unit for dependencies and a retry, and each entry uses one concurrency slot. See [Limits](#limits). Keep a group small. Put stacks that need no shared step in separate groups.

### Combined gates

Use `allOf()` when a stage needs more than one rule. The stage waits until all rules pass. Build the rule with `allOfOutput()` and the `Output` form of each rule, because `rules` does not accept the promise that a plain rule function returns.

```typescript
const prod = new delivery.Stage("prod", {
    pipeline,
    from: [staging],
    when: delivery.allOfOutput({
        rules: [
            delivery.approvalOutput({ approvers: ["your-colleague"] }),
            delivery.manualOutput({ name: "dashboards", description: "Check the dashboards first." }),
        ],
    }),
});
```

Use `anyOf()` when one of the rules is enough. The `manual()` rule gives the person who continues the gate a description of what to check. See [Gates](#gates) for the other rules and for how to continue a manual gate.

### Upstream and downstream pipelines

The upstream pipeline publishes an `Artifact` in a stage. The downstream pipeline reads it with a `PipelineSource`.

In the upstream pipeline:

```typescript
new delivery.Artifact("clusterEndpoint", { stage: prod, value: cluster.endpoint });
```

In the downstream pipeline:

```typescript
const platform = new delivery.PipelineSource("platform", {
    pipeline: "acme/platform-delivery/main",
    stage: "prod",
    trigger: true,
});

new delivery.Stacks("app", {
    stage: dev,
    stacks: {
        app: {
            stack: "acme/shop-app/dev",
            source: repo,
            directory: "app",
            config: { clusterEndpoint: platform.artifacts["clusterEndpoint"] },
        },
    },
});
```

Both pipelines must be in the same organization. With `trigger: true`, each new release of the upstream stage starts a run of the downstream pipeline. The `artifacts` map is secret as a whole when one artifact is secret. A secret value in `config` is rejected, so pass such a value in `environment`.

### One repository, several pipelines

A monorepo can hold the programs of several pipelines. Give each pipeline its own program directory, its own pipeline stack and a source that triggers only on its own paths.

```typescript
// delivery/shop/index.ts
const repo = new delivery.GitHubSource("repo", {
    repository: "acme/monorepo",
    branch: "main",
    trigger: { paths: ["shop/**", "delivery/shop/**"] },
});
const pipeline = new delivery.Pipeline("shop", {
    program: { source: repo, directory: "delivery/shop" },
});
```

The pipeline in `delivery/search/index.ts` is the same, with `search/**` and `delivery/search/**` as the paths. A push that changes only `search/` then starts only the `search` pipeline. Every pipeline needs its own pipeline stack, for example `acme/shop-delivery/main` and `acme/search-delivery/main`. Register each one with `pulumi up` in its own directory.

## Recover a run

Delivery never retries a member by itself. A person retries a failed run or cancels an active run. Use the console, or these API routes:

```sh
curl -X POST -H "Authorization: token $PULUMI_ACCESS_TOKEN" \
  https://api.pulumi.com/api/preview/stacks/acme/shop-delivery/main/delivery/runs/<run number>/retry

curl -X POST -H "Authorization: token $PULUMI_ACCESS_TOKEN" \
  https://api.pulumi.com/api/preview/stacks/acme/shop-delivery/main/delivery/runs/<run number>/cancel
```

### Retry a failed run

First fix the cause of the failure, for example create a missing target stack. A retry runs only the failed members again. A `Stacks` group is one unit, so a retry runs all entries of a group again when one entry failed. Members that succeeded keep their results.

A retry uses the same commit, the same recorded values and the same release as the failed run. A change that you push later does not reach the retry. A fix outside the repository, for example a missing stack, does.

Only a failed run can be retried. Use rollback instead when a newer release was deployed to the stage of a failed member, a newer run waits for that stage, or the run stack no longer exists.

### Cancel an active run

You can cancel a run while a gate waits or while a member runs. The run shows "canceled" at once, but a running update can take some minutes to stop. Wait until the update ends before you start recovery on its stack. The stack of a canceled member can need `pulumi refresh`.

## Operate a stage

These actions create a new run for one stage. The new run uses the release, the recorded values and the outputs of an earlier run. It does not read the latest values. The actions skip the gate of the stage. Use the console, or the API under `preview/stacks/<org>/<project>/<stack>/delivery/stages/<stage>/`.

- Rollback deploys an earlier release that deployed to the stage before, and pauses the stage. Newer runs wait until you resume the stage.
- Redeploy deploys the current release of the stage again.
- Force-promote moves a release to a stage.
- Pause stops a stage from admitting new runs. A run that holds the stage keeps it. Resume lets the stage admit runs again.

## Writing pipelines

- Each member names `source`, `directory` and `stage`. A program has one `Pipeline`. Stage names are unique.
- `config` and `environment` values are plain strings. `config` cannot hold secrets. A secret in `environment` stays secret.
- Normal code decides which stages and members a run has. You can use `if`, loops and `.apply` on member outputs.
- A resource that is not a Delivery resource must use a member output to wait for a member. `dependsOn` orders only Delivery members.
- The provider rejects error hooks on members.
- A `PipelineSource` reads the release that a stage of another pipeline deployed. Read its artifacts from `artifacts`.
- `pulumi.getStack()` returns the name of the run stack. Do not derive resource names from it.

### Job outputs

A job writes its outputs as a JSON object, at most 1 MiB, to the file that `DELIVERY_OUTPUTS` names. Each key is one output. Write an output as `{"value": ..., "secret": true}` to mark it secret. An output is also secret when it contains the value of a secret environment variable of the job.

### Environment variables

Jobs and the `pulumi up` of a `Stacks` member see the identity of the run as environment variables.

| Variable | Value |
| --- | --- |
| `DELIVERY_RUN` | The run number. |
| `DELIVERY_RELEASE` | The release number. |
| `DELIVERY_STAGE` | The stage. |
| `DELIVERY_COMMIT` | The commit of the source of the member. |
| `DELIVERY_PIPELINE` | The pipeline stack. |
| `DELIVERY_STACK` | The target stack of a member update. |

The names `PULUMI_ACCESS_TOKEN`, `PULUMI_BACKEND_URL` and all names that start with `DELIVERY_` are reserved. Do not set them. A member update gets a Pulumi token for its stack. A job gets no Pulumi token.

The variables are not set in a manual `pulumi up`, so use a fallback, for example `process.env.DELIVERY_RUN ?? "manual"`. A tag or setting that uses them changes in every release.

### Gates

Gates are rules in `when`.

- `approval()` needs approvals and the Approvals feature.
- `manual()` waits until someone continues the gate. It works on every plan.
- `allOf()` and `anyOf()` combine rules. You can nest them up to three levels.

`approval()` takes `approvers` (user names or team names) and `count`. Anyone eligible can approve, including the person who started the run or pushed the commit. If you name no approvers, anyone with the organization update permission can approve. A name that matches no user or team keeps the gate waiting. An approval gate shows a preview of the stack updates. The preview can be partial, and "Not available" is not the same as "no changes".

`manual()` takes an optional `name` and `description`, as the other rules do. The default name is `manual`. Names are unique within a gate. The console shows the description to the person who continues the gate. Use `approval()` when named people must approve or you need several approvals. Otherwise use `manual()`.

#### Continue a manual gate

Continue a manual gate in the console, or with a POST to the `continue` route:

```sh
curl -X POST -H "Authorization: token $PULUMI_ACCESS_TOKEN" -H "Content-Type: application/json" -d '{}' \
  https://api.pulumi.com/api/preview/stacks/acme/shop-delivery/main/delivery/runs/<run number>/gates/<gate id>/continue
```

The body is a JSON object. A request without a body gets a 400 response. A gate with several manual conditions needs the name of the condition: `-d '{"condition":"<manual name>"}'`. A gate with one manual condition takes an empty object. A 409 response that says the gate is not open means that someone already continued it.

A system can continue a gate by itself. Read the run with `GET .../delivery/runs/<run number>`. In `stages`, find the stage of the deployment. If `stages[].gate.status` is `open`, use `stages[].gate.id`. Continue that gate with `POST .../delivery/runs/<run number>/gates/<gate id>/continue`. An approval condition in `stages[].gate.conditions` has a `changeRequestId`. Approve it with `POST /api/change-requests/<org>/<change request id>/approve`, the header `Content-Type: application/json` and the body `{}`. Approving needs a user token (see [Permissions](#permissions)). Pass the run number to the system through a resource that the stack updates anyway. For an ECS blue/green deployment, put it in the `hookDetails` of the lifecycle hook:

```typescript
hookDetails: JSON.stringify({
    run: process.env.DELIVERY_RUN,
    release: process.env.DELIVERY_RELEASE,
    commit: process.env.DELIVERY_COMMIT,
}),
```

Do not write the run number into the environment variables of a Lambda function. That changes the function in every run.

## Permissions

- Write access to the branch of the pipeline program gives deploy access to every stack that the Delivery identity of your organization can reach. Protect that branch.
- The Delivery identity of the organization needs read access to the upstream pipeline stack of a `PipelineSource`. Your own access is not enough.
- Any credential with write access to the pipeline stack can dispatch a run or continue a manual gate. Retry, cancel, pause, resume, promote, rollback and redeploy need a user token with that access. Organization and team tokens cannot do them.
- Approving needs a user token, read access to the pipeline stack, and a place among the approvers of the gate.
- An approval preview runs with the same credential as an update. Only people with write access to the repository can add code that runs in a preview.

## Limits

| Limit | Value |
|---|---|
| Members in a pipeline | 200 (a `Stacks` entry counts as one) |
| Passes in a run | 10,000, then the run fails |
| Time limit of a pass | 1 hour |
| Time limit of a member | 2 hours. Set `timeout` on a `Stacks` entry, in seconds. |
| Job outputs | 1 MiB |
| Run stacks | Deleted 90 days after the run ends |

A pass is one execution of the pipeline program on the run stack. A failed pass starts again up to 3 times in a row. A pass that makes progress resets the count.

Each entry of a `Stacks` group uses one concurrency slot of your organization. A group with more entries than the concurrency limit of your organization can block itself, and registration records a warning for it.

## Known limitations

- `CustomVCSSource` and forges without webhooks are not available.
- Only hosted runners. No agent pools. No Windows.
- Passes get no policy packs. Target stacks keep their own policy groups.
- The config of the pipeline stack comes from its config file in the repository. Commit it. Only the default Pulumi Cloud secrets provider is tested.
- Delivery does not detect a cycle between stages. Such a run waits until you cancel it.
- The `artifacts` map of a `PipelineSource` is secret as a whole when one artifact is secret.
- A `timeout` on a `Job` has no effect yet. A job stops after 2 hours.
- Delivery deletes a run stack without a destroy. Resources in the pipeline program that are not Delivery resources stay in the cloud without state. A run that waits keeps its stack.
- You cannot delete a pipeline stack while runs are in progress. Deleting a pipeline stack stops the pipeline and deletes its run stacks. After you restore the stack, run `pulumi up` on it to register the pipeline again.
- You cannot move a pipeline stack to another organization. You cannot rename or move a run stack.
- The `http()` rule is not available in the preview. Registration rejects a stage with an `http()` rule.
- The preview has no time-based triggers, no ESC triggers, no dispatch inputs and no `pulumi delivery` command.
