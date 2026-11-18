# Pulumi Delivery

Pulumi Delivery lets you write your release pipeline as a Pulumi program. Pulumi Cloud runs each release through your stages in order: it updates stacks, runs jobs, and waits at the gates you define.

This repository holds the Delivery provider and its SDKs for TypeScript, Python, Go, C# and Java. You can also write a pipeline in YAML or HCL.

Pulumi Delivery is in public preview. Behavior and the SDK can change before general availability.

- [Documentation](https://www.pulumi.com/docs/delivery/)
- [Get started](https://www.pulumi.com/docs/delivery/get-started/): register your first pipeline and deploy a release through `dev`, `staging` and `prod`.
- [Concepts](https://www.pulumi.com/docs/delivery/concepts/): how pipelines, runs, stages, stacks, jobs, artifacts, sources and releases fit together.

## Example

This pipeline deploys an app from the `acme/shop` repository through three environments. Every push to `main` starts a run, and the run pauses before `prod` until someone continues it.

```typescript
import * as delivery from "@pulumi/delivery";

const repo = new delivery.GitHubSource("repo", {
    repository: "acme/shop",
    branch: "main",
    trigger: true,
});

const pipeline = new delivery.Pipeline("shop");

const dev = new delivery.Stage("dev", { pipeline });
const staging = new delivery.Stage("staging", { pipeline, from: [dev] });
const prod = new delivery.Stage("prod", {
    pipeline,
    from: [staging],
    when: delivery.manual({ description: "Check staging before you ship to prod." }),
});

for (const [env, stage] of Object.entries({ dev, staging, prod })) {
    new delivery.Stacks(env, {
        stage,
        stacks: {
            app: { stack: `shop-app/${env}`, source: repo, directory: "app" },
        },
    });
}
```

`examples/basic` is a program of this kind in TypeScript. `examples/python`, `examples/yaml` and `examples/hcl` have one in Python, YAML and HCL.

## Concepts

- **Pipeline.** A Pulumi project that declares one `Pipeline` resource. It lives in a repository next to the code it deploys.
- **Stage.** A phase of the pipeline, for example an environment such as `dev`. A stage runs after its upstream stages succeed and its gate passes.
- **Gate.** A rule in the `when` property of a stage, such as a required approval or a manual step.
- **Stacks.** Updates one or more existing stacks in a stage. Delivery updates stacks; it does not create them.
- **Job.** Runs a shell command in a stage, such as a test suite or a script.
- **Source and trigger.** A source is a repository and a branch. A trigger on a source decides whether a change starts a run.
- **Run and release.** A run takes one release through the stages. A release pins one commit of each source, so every stage works from the same code.
- **Artifact.** A named value that a stage publishes for a release. Other pipelines read it with a `PipelineSource`.

## Use it

You need a Pulumi Cloud organization and Pulumi CLI v3.277.0 or later.

Create the pipeline program as a Pulumi project of its own and install the Delivery SDK:

```sh
mkdir delivery && cd delivery
pulumi new typescript --name shop-delivery --stack main --yes
npm install @pulumi/delivery
```

The SDKs for the other languages are `pulumi_delivery` on PyPI, `Pulumi.Delivery` on NuGet, `com.pulumi:delivery` on Maven and the Go module `github.com/pulumi/pulumi-delivery/sdk`. The installed SDK brings the matching provider plugin with it.

Then work with the pipeline in two ways:

- **Register it** with `pulumi delivery register`. Pulumi Cloud stores the pipeline, and from then on its triggers start runs. Run the command from a Git checkout with no uncommitted changes, at a commit that is pushed to the branch the pipeline tracks. If the stack config has no `delivery:source` setting, the command writes the program's location to it. Commit the config file after that.
- **Run it locally** with `pulumi up`. The pipeline program runs on your machine, and the stack updates and jobs run in Pulumi Cloud. Use this to iterate on a pipeline from a branch.

The [get started guide](https://www.pulumi.com/docs/delivery/get-started/) has every step.

## Repository layout

- `provider/`: Go resource provider. `provider/cmd/pulumi-resource-delivery/schema.json` is the committed schema, written from the built provider.
- `sdk/`: SDKs for Node.js, Python, .NET, Go and Java, generated from the schema (`make generate`, never edited by hand). `sdk/go.mod` is the Go SDK module and is kept by hand.
- `examples/basic/`, `examples/python/`, `examples/yaml/`, `examples/hcl/`: a small pipeline program in TypeScript, Python, YAML and HCL.
- `examples/compile/`: Go and .NET programs that check the generated SDKs compile. `examples/*_test.go` runs those checks and a `pulumi preview` of every program, one file per language. A preview sends nothing to Pulumi Cloud, so the tests need no credentials.
- `docs/`: `_index.md` is the registry guide. `ONBOARDING.md` is the onboarding guide.

## Develop

CI files (`Makefile`, `.github/workflows/`, `.config/`, `scripts/`) are generated by [pulumi/ci-mgmt](https://github.com/pulumi/ci-mgmt) from `.ci-mgmt.yaml`. Do not edit them; change `.ci-mgmt.yaml`, `.mk/*.mk` or `sdk-hooks.mk` instead.

Tools (Go, Node.js, Python, .NET, Java, Gradle, the Pulumi CLI and others) come from [mise](https://mise.jdx.dev/) and are pinned in `.config/mise.toml`. Install mise and run `make`; it installs the tools.

- `make build`: build the provider and build and install all SDKs.
- `make generate`: rewrite the schema and all SDKs. Commit the result.
- `make test_provider`: run the provider tests.
- `make test`: run the example tests in `examples/`. Run `make build` first and put `bin/` on `PATH`. `make TESTTAGS=go test` runs one language (`nodejs`, `python`, `dotnet`, `go`, `java`).

## Install the plugin

Releases and prereleases are on get.pulumi.com. They are not in the registry index.

```sh
pulumi plugin install resource delivery <version>
```

To install a local build:

```sh
pulumi plugin install resource delivery <version> -f pulumi-resource-delivery-v<version>-darwin-arm64.tar.gz
```

## Release

1. Push the tag `vX.Y.Z` or `vX.Y.Z-alpha.N`. The `ci` workflow builds the plugin for each platform, uploads it to get.pulumi.com and publishes the SDKs. The repository holds no version; local builds and the committed SDK say `0.0.0-alpha.0+dev`.
2. A stable tag (`vX.Y.Z`) also creates a GitHub release and asks [pulumi/registry](https://github.com/pulumi/registry) to rebuild the package page from `docs/_index.md` and the schema at that tag. A prerelease tag does neither.

Every push to `main` also publishes: it uploads an alpha build of the plugin and publishes alpha versions of the Node.js, Python and .NET SDKs.
