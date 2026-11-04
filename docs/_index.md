# Pulumi Delivery

Pulumi Delivery runs your delivery pipeline as a Pulumi program. The program declares sources, stages, stacks, jobs and approval gates. Pulumi Cloud runs each release through the stages in order.

## Status

Pulumi Delivery is in private preview. Behavior, the SDK and the API can change.

## Setup

### What you need

- Pulumi enables Delivery for your organization. Ask your Pulumi contact.
- A Pulumi CLI and a language SDK at the minimum versions below. These versions carry the support for waiting resources that Delivery needs.
  <!-- TODO: minimum Pulumi CLI version. Fill in when the stock release ships. -->
  <!-- TODO: minimum Pulumi language SDK version. Fill in when the stock release ships. -->
- Linux or macOS. The provider has no Windows build.
- A Pulumi Cloud backend. The provider reads `PULUMI_API` and `PULUMI_ACCESS_TOKEN` from the CLI. A file backend or a self-managed backend does not work. Self-hosted Pulumi Cloud does not support Delivery.
- Node.js, to run the TypeScript examples in this guide. The preview tests TypeScript only. Python and Go are untested in the preview. C# and Java are not supported.

Approval gates need the Approvals feature in your plan. You also need a second person who can approve. Name that person or team in `approval()`, or give them the organization update permission. A `manual()` gate works on every plan.

### Connect your version control

Delivery reads your repositories through the Pulumi integration for your forge. Set up one of these integrations.

- [GitHub app](https://www.pulumi.com/docs/integrations/version-control/github-app/)
- [GitLab](https://www.pulumi.com/docs/integrations/version-control/gitlab/)
- [Bitbucket](https://www.pulumi.com/docs/integrations/version-control/bitbucket/)
- [Azure DevOps](https://www.pulumi.com/docs/integrations/version-control/azure-devops-integration/)

Give the integration access to each repository that a source names. This includes the repository of the pipeline program.

If your organization has more than one integration for a forge, set `integration` on the source. Otherwise registration fails with "Several <forge> integrations exist. Set integration on the source."

### Install the plugin and the SDK

The Pulumi CLI downloads the provider plugin from get.pulumi.com when a program needs it. To install it by hand, run:

```sh
pulumi plugin install resource delivery <version>
```

To add the SDK, run this command in the directory of the pipeline program:

```sh
pulumi package add private/pulumi/delivery
```

<!-- TODO: confirm the exact form of the `pulumi package add` command and the package name. -->

The command writes an entry to the `packages` section of `Pulumi.yaml`. Keep that entry. The hosted runner reads it and runs `pulumi install` for your program.

### Create the stacks

Create the pipeline stack. This is the stack of the program that declares the pipeline. In this guide it is `acme/shop-delivery/main`.

Create each target stack before the first run. A target stack must be an ordinary stack in the organization of the pipeline. A run fails with "The target stack ... does not exist. Create it first." if a stack is missing. A pipeline stack or a run stack cannot be a target.

A target stack keeps its config and secrets in its own config file in the source repository, or in an ESC environment.

### Cloud credentials for member updates

Delivery does not use Deployment settings or OIDC for member updates in the preview. Pass credentials to a member in its `environment`. A secret value in `environment` stays secret. The console does not show its value.

<!-- TODO: add the recipe for ESC with OIDC if the walk-through confirms it. -->

Values in `config` are plain strings. Delivery rejects a secret config value when you register. Keep secrets in the config file of the target stack or in an ESC environment.

## Concepts

**The pipeline program and the pipeline stack.** The pipeline is a Pulumi program that lives in a repository. You register it with `pulumi up` on the pipeline stack. Registration ends with exit code 10, because the members of the pipeline wait for a run. This is the expected result.

**Runs.** A run takes one release through the stages. Each run has its own run stack, named `<pipeline stack>-r<run number>`. Delivery runs your program on that stack in several passes and starts the members as their turn comes. Run stacks do not appear in stack lists or search. Open a run stack from the page of its run. A manual `pulumi up` on a run stack is not a supported way to work.

**Stages.** A stage is a step of the pipeline. A stage can wait for other stages with `from`, and for a gate with `when`. A gate comes before the stage lock. A stage deploys one run at a time. A newer run replaces a run that waits at a gate or for the stage lock. A newer run does not replace a run that the stage already admitted.

**Members.** A member is a resource that does work in a stage. `Stacks` updates one or more existing stacks. `Job` runs a command. `Artifact` publishes a value for other pipelines.

**Sources and releases.** A source is a repository and branch. A source locks its commit when the run first creates it. Every stage of the run that uses the source gets that commit, so all stages of a run work on the same code. The sources you declare form the release. A release has a release number.

Read the release number from `pipeline.releaseNumber` and the commit of a source from `source.commit`. Both are unknown during registration and preview, and known in a run.

Declare every source at the top level of the program. A source that is created from a member output after the first pass cannot lock a commit for the run, so the run fails. The fix is to declare the source at the top level.

**Triggers.** A trigger is a setting on a source. It decides whether a change to that source starts a run. A source without `trigger` is part of the release but never starts a run. You can also start a run by hand. See [Triggers](#triggers).

## Getting started

This guide builds a pipeline with a `dev` stage and a `prod` stage. The `dev` stage updates a stack and runs a smoke check. The `prod` stage waits for an approval and then updates a second stack. You need two people, because the person who starts a run cannot approve it.

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

`delivery/Pulumi.yaml` names the project. Run `pulumi package add` in this directory to add the `packages` entry (see Setup).

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

Replace `acme/shop` and `your-colleague` with your repository and the Pulumi user name of a second person. Every Delivery resource names its `source` and `directory`, and its `stage`.

`delivery/smoke.sh` prints the endpoint of the `dev` stack. It writes one output to the file that `DELIVERY_OUTPUTS` names.

```sh
set -eu
echo "Checking $ENDPOINT"
echo "{\"checked\": \"$ENDPOINT\"}" > "$DELIVERY_OUTPUTS"
```

### Register the pipeline

Commit everything and push it. A run checks out the pipeline program from the repository, so the files must be there.

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

- "Registered. A run starts for commit abc1234 when this update finishes." The checkout is clean and the commit is the head of the branch. A run starts.
- "Registered. No run starts because commit abc1234 is not the head of branch main." Push your commit, or pull the latest commit and register again.
- "Registered. No run starts because the checkout has uncommitted changes. Commit the changes. Push the commit to start a run."
- "Registered. No run starts because the update has no git commit." Run `pulumi up` from a Git checkout.

### Watch the run

Open the Delivery page of your organization in the console and select the run. The `dev` stage updates `acme/shop-infra/dev` and runs the smoke job. The `prod` stage then waits at its gate.

You started this run with the registration update, so you cannot approve it. Ask the second person to approve the `prod` gate in the console. When they approve, the `prod` stage updates `acme/shop-infra/prod`.

From now on, a push to `main` starts a run, because the source has `trigger: true`. You can also start a run by hand with the "Run pipeline" button on the Delivery page, or with the API. The optional `key` stops duplicates. A second dispatch with the same key returns the first run.

```sh
curl -X POST \
  -H "Authorization: token $PULUMI_ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"key": "first-run"}' \
  https://api.pulumi.com/api/preview/stacks/acme/shop-delivery/main/delivery/dispatch
```

## Triggers

A trigger is the `trigger` setting of a source. The trigger belongs to the pipeline of the program, so it names no pipeline.

Set `trigger: true` on a `GitHubSource`, `GitLabSource`, `BitbucketSource` or `AzureDevOpsSource` to start a run on every push to the branch of the source:

```typescript
const app = new delivery.GitHubSource("app", { repository: "acme/shop", branch: "main", trigger: true });
```

Set `trigger: { paths: [...] }` to start a run only when a push changes a file that matches one of the paths:

```typescript
const app = new delivery.GitHubSource("app", {
    repository: "acme/shop",
    branch: "main",
    trigger: { paths: ["src/**", "delivery/**"] },
});
```

Leave `trigger` out of a source that must be part of the release but must not start runs. A shared library repository is an example:

```typescript
const lib = new delivery.GitHubSource("lib", { repository: "acme/shop-lib", branch: "main" });
```

Set `trigger: true` on a `PipelineSource` to start a run when the named stage of the other pipeline deploys a new release:

```typescript
const platform = new delivery.PipelineSource("platform", {
    pipeline: "acme/platform-delivery/main",
    stage: "prod",
    trigger: true,
});
```

`trigger` must be `true` or, on a source with a repository, an object with a non-empty `paths` list. `false` is an error: leave the setting out instead.

## Recover a run

These are two separate exercises.

### Retry a failed run

In this exercise a member fails because its target stack does not exist.

1. In `delivery/index.ts`, change the `stack` of the `dev` entry to `acme/shop-infra/dev-2`. Commit and push. The push starts a run. The `dev` member fails with "The target stack acme/shop-infra/dev-2 does not exist. Create it first." and the run fails.
2. Delivery never retries a member by itself. A person starts a retry.
3. Create the missing stack:

   ```sh
   pulumi stack init acme/shop-infra/dev-2
   ```

4. Retry the failed run in the console, or call the API:

   ```sh
   curl -X POST -H "Authorization: token $PULUMI_ACCESS_TOKEN" \
     https://api.pulumi.com/api/preview/stacks/acme/shop-delivery/main/delivery/runs/<run number>/retry
   ```

5. Change the `stack` of the entry back to `acme/shop-infra/dev`, commit and push.

A retry uses the same commit, the same recorded values and the same release as the failed run. A change that you push later does not reach the retry. A fix outside the repository, such as the missing stack, does.

A retry runs only the failed members again. Members that succeeded keep their results and do not run again. A `Stacks` group is one unit, so a retry runs all entries of a group again when one entry failed.

You cannot retry a run in these cases.

- A newer release was deployed to the stage of a failed member, or a newer run waits for that stage. Use rollback instead.
- The run stack no longer exists. Use rollback instead.

Only a failed run can be retried.

### Cancel an active run

Cancel a run while its `prod` gate waits, or while a member runs. Use the console, or the API:

```sh
curl -X POST -H "Authorization: token $PULUMI_ACCESS_TOKEN" \
  https://api.pulumi.com/api/preview/stacks/acme/shop-delivery/main/delivery/runs/<run number>/cancel
```

The run shows "canceled" at once. A cancel asks the runners to stop. A running update can take some minutes to stop. Wait until the update ends before you start recovery on its stack.

The update of a canceled member ends as canceled. The stack of that member may need `pulumi refresh` afterwards.

## Operate a stage

These actions work on one stage. They are separate from the getting-started guide. Each action creates a new run that uses the release, the recorded values and the outputs of an earlier run. It never reads a latest value. Each action skips the gate of the stage. Use the console, or the API under `preview/stacks/<org>/<project>/<stack>/delivery/stages/<stage>/`.

- Rollback deploys an earlier release to a stage and pauses the stage. The release must have deployed to the stage before. Newer runs wait until you resume the stage.
- Redeploy deploys the current release of the stage again.
- Force-promote moves a release to a stage without its gate.
- Pause stops a stage from admitting new runs. A run that holds the stage lock keeps it. Resume lets the stage admit runs again.

To try it, run the pipeline twice so that `dev` has two releases. Roll back `dev` to the first release. Check that the stage shows as paused. Resume the stage. The next run then deploys to `dev`.

## Writing pipelines

- Each member names `source`, `directory` and `stage`. The `stage` is required on `Stacks`, `Job` and `Artifact`.
- A program has one `Pipeline`. Stage names must be unique.
- `config` and `environment` values are plain strings in the declaration. `config` cannot hold secrets. A secret in `environment` stays secret.
- Normal code decides which stages and members a run has. A pipeline can use `if` and loops.
- A program can use `.apply` on member outputs, as in the guide.
- A resource that is not a Delivery resource gets a warning. It must use a member output to wait for the member, because `dependsOn` orders only Delivery members.
- The provider rejects error hooks on members. Retry is a person's action.
- A job writes its outputs as a JSON object to the file that `DELIVERY_OUTPUTS` names. Each key is one output. Write an output as `{"value": ..., "secret": true}` to mark it secret. An output is also secret when it contains the value of a secret environment variable of the job. The file can be at most 1 MiB.
- All Delivery work sees the identity of the run as environment variables. This holds for jobs and for the `pulumi up` of a `Stacks` member. The values are:

  | Variable | Value |
  | --- | --- |
  | `DELIVERY_RUN` | The run number. |
  | `DELIVERY_RELEASE` | The release number. |
  | `DELIVERY_STAGE` | The stage. |
  | `DELIVERY_COMMIT` | The commit of the source of the member. |
  | `DELIVERY_PIPELINE` | The pipeline stack. |
  | `DELIVERY_STACK` | The target stack of a member update. |

  The variables are not set in a manual `pulumi up`, so a program needs a fallback, for example `process.env.DELIVERY_RUN ?? "manual"`. An approval preview sets them too. A tag or a setting that uses them shows a change in every release. `PULUMI_ACCESS_TOKEN`, `PULUMI_BACKEND_URL` and all names that start with `DELIVERY_` are reserved. You cannot set them.
- A `PipelineSource` reads the release that a stage of another pipeline deployed. Both pipelines must be in the same organization. With `trigger: true`, a new release of that stage starts a run. Read the artifacts of the release from `artifacts`.
- Gates are rules in `when`. `approval()` needs approvals and the Approvals feature. `manual()` waits until someone continues the gate and works on every plan. `http()` polls a URL until a JMESPath `successExpression` is true. `allOf()` and `anyOf()` combine rules, nested up to three levels.
- `approval()` takes `approvers` (user names or team names) and `count`. The person who started the run can never approve it. A run that a person starts has a starter. A run that a push starts has none, so the person who pushed can approve it if they are an eligible approver. If you name no approvers, anyone with the organization update permission can approve. A name that matches no user or team keeps the gate waiting.
- `manual({ name, description })` takes an optional `name` and an optional `description`, the same as the other rules. Without a name the service uses the kind, `manual`, as the name. Names are unique within a gate. The console shows the description to the person who continues the gate. Continue a manual gate in the console, or with a POST to `.../delivery/runs/<run number>/gates/<gate id>/continue`:

  ```sh
  curl -X POST -H "Authorization: token $PULUMI_ACCESS_TOKEN" \
    https://api.pulumi.com/api/preview/stacks/acme/shop-delivery/main/delivery/runs/<run number>/gates/<gate id>/continue
  ```

  A gate with several manual conditions needs the name of the condition in the body: `-d '{"condition":"<manual name>"}'`. A gate with one manual condition needs no body.

  Use `manual()` when any person with access may decide. Use `approval()` when named people must approve, the starter must not approve, or you need several approvals.
- A system can continue a manual gate by itself. Pass the run number to that system through a resource that the stack updates anyway, and let the system continue the gate. An ECS blue/green deployment shows the pattern. The lifecycle hook of the deployment gets the run in `hookDetails`:

  ```typescript
  hookDetails: JSON.stringify({
      run: process.env.DELIVERY_RUN,
      release: process.env.DELIVERY_RELEASE,
      commit: process.env.DELIVERY_COMMIT,
  }),
  ```

  The hook function then lists the gates of the run with `GET .../delivery/runs/<run number>/gates`, takes the open gate of its stage, and continues it with `POST .../delivery/runs/<run number>/gates/<gate id>/continue` and the body `{"condition":"<manual name>"}`. The condition is only needed when the gate has several manual conditions. A 409 response that says the gate is not open means that someone already continued it. Resource tags work the same way for a system that reads tags. Do not write the run number into the environment variables of a Lambda function. That changes the function in every run.
- An approval gate shows a preview of the stack updates that the stage will run. The preview can be partial. "Not available" is not the same as "no changes".
- A `WebhookSource` is a source that an external system posts to. Its `url` output is where the system posts. The service verifies a post with `signing` (`hmac-sha256` over the raw body, or a shared `token`), checks it against the JMESPath `when`, and maps it with the JMESPath `map` to the `subject`, `version` and `data` of the run that the post starts. A matching post starts a run unless `trigger` is `false`. The `subject`, `version` and `data` outputs are known in a run. A webhook source is not a checkout source: a `Job` cannot check it out.
- A `LinearIssueSource` is a `WebhookSource` for the issue webhooks of Linear. Give it the `signingSecret` of the webhook, and point the webhook at its `url` output. A run starts when a user creates or updates an issue that carries every label in `labels` and is in one of `states`. `ignoreBots` (the default) skips changes that an app made. `data` holds `identifier`, `title`, `description`, `url`, `state`, `labels`, `team` and `action`.
- An `Agent` declares a task for a Custom Agent of the organization. It runs in its `stage`, works in the repository of `source` when one is set, and gets the `prompt`, which a program builds from the outputs of other members with `apply`. `resultSchema` is the JSON Schema of the typed `result`. `taskId` names the task in Pulumi Cloud. Both are unknown until the task has run.
- `decision({ agent, inputs })` is a gate rule that asks a Decision Agent of the organization. `inputs` are the fields of its prompt, usually built from member outputs with `apply`, so use `decisionOutput()` in TypeScript. The gate passes when the agent decides to continue. See `examples/neon` for a pipeline with a `LinearIssueSource`, an `Agent` and a decision gate.

## Permissions

- Write access to the branch of the pipeline program gives deploy access to every stack that the Delivery identity of your organization can reach. Protect that branch.
- Target stacks must be ordinary stacks in the organization of the pipeline.
- Each pass and member update runs with a short-lived token for its stack. The token has the default deployment role of the organization if one is set. A job gets no Pulumi token.
- An upstream pipeline (a `PipelineSource`) must be in the same organization. The Delivery identity of the organization needs read access to the upstream pipeline stack. The service checks this on each read. Your own access to both stacks is not enough.
- Any credential with write access to the pipeline stack can dispatch a run or continue a manual gate.
- Retry, cancel, pause, resume, promote, rollback and redeploy need a user token with write access to the pipeline stack. The preview has no separate permissions for these actions. Organization and team tokens cannot do them.
- Approving needs a user token, read access to the pipeline stack, and a place among the approvers of the gate.
- An approval preview runs with the same credential as an update. Only people with write access to the repository can add code that runs in a preview.

## Limits

| Limit | Value |
|---|---|
| Members in a pipeline | 200. A `Stacks` entry counts as one member. |
| Passes in a run | 10,000. The run then fails. |
| Concurrent jobs in your organization | 1 (Individual), 5 (Team), 25 (Enterprise), 150 (Business Critical). If your organization sets a Deployments concurrency, that value applies. |
| Progress of a `Stacks` group | 2 hours without progress fails the group. |
| Time limit of a member | 2 hours. Set `timeout` on a `Stacks` entry, in seconds. |
| Time limit of a pass | 1 hour |
| Retention of run stacks | 90 days after the run ends |
| Job outputs | 1 MiB |

Each entry of a `Stacks` group uses one concurrency slot. A group with more entries than the limit can block itself. The service records a warning with the registration for such a group.

A failed pass starts again up to 3 times in a row. A pass that makes progress resets the count.

## Known limitations

- `CustomVCSSource` and forges without webhooks are not available.
- Delivery does not use Deployment settings or OIDC for passes or member updates.
- Target stacks must exist before the first run.
- Pipeline config comes from the config file of the pipeline stack in the repository. Commit it. The preview supports and tests the default Pulumi Cloud secrets provider. Passphrase and KMS providers are untested.
- `pulumi.getStack()` in a pipeline program returns the name of the run stack (`<stack>-r<N>`). Do not derive resource names from it.
- Passes get no policy packs. Target stacks keep their own policy groups.
- Approval gates need the Approvals feature. Use `manual()` on other plans.
- Only hosted runners. No agent pools. No Windows.
- The minimum CLI and SDK versions apply.
- Run stacks do not appear in stack lists or search. Open them from the run page.
- Delivery deletes a run stack 90 days after its run ends. It does not run a destroy first. Resources in the pipeline program that are not Delivery resources stay in the cloud without state. A run that waits keeps its stack.
- Delivery does not detect a cycle between stages. Such a run waits until you cancel it.
- A `Stacks` group is one unit for dependencies and retries.
- The `artifacts` map of a `PipelineSource` is secret as a whole when one artifact is secret.
- A `timeout` on a `Job` has no effect yet. A job stops after 2 hours.
- You cannot delete a pipeline stack while runs are in progress.
- Deleting a pipeline stack stops the pipeline and deletes its run stacks. After you restore the stack, run `pulumi up` on the pipeline stack to register it again.
- You cannot move a pipeline stack to another organization. You cannot rename or move a run stack.
- The preview has no time-based triggers, no ESC triggers, no dispatch inputs and no `pulumi delivery` command.
- The routes under `preview` can change.

## Help

<!-- TODO: name the contact or Slack channel for preview users. -->
