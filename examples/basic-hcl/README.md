# basic-hcl: the basic pipeline in Pulumi HCL

`main.tf` is `examples/basic` written for the [Pulumi HCL](https://github.com/pulumi/pulumi-language-hcl) language host (`runtime: hcl`). It declares the same sources, stages, gate, jobs, artifact and Stacks group, and registers the same inputs.

## Writing Delivery in HCL

- Types follow the HCL naming rule for Pulumi packages: `delivery:index:GitHubSource` is `delivery_github_source`, `delivery:index:Stacks` is `delivery_stacks`. Attributes are snake_case (`release_number`). Map keys, such as Stacks entry names and `config` keys, keep their case.
- Pass a resource where the schema takes one: `source = delivery_github_source.code`, `from = [delivery_stage.dev]`.
- Write stage entry rules as object literals with a `kind` (`approval`, `manual`, `http`, `all`, `any`) and the arguments of the function of the same name. The rule functions (`allOf`, `approval`, ...) only build these objects. As HCL data sources they need `dynamic` blocks to nest, which reads worse than the literal.

## Requirements

- A delivery provider that accepts the explicit nulls HCL sends for unset object fields, such as `runtime` on a Stacks entry. Releases up to 0.0.1-alpha.3 reject them with `runtime is required and must be a resource`; until a release has the fix, install a local build (see "Install the plugin" in the repository README) and set `version` in `main.tf` to match it.
- A Pulumi HCL language host that keeps resource references to a union of resource types ([pulumi/pulumi-hcl#821](https://github.com/pulumi/pulumi-hcl/pull/821)). `program.source`, `Job.source` and `StacksEntry.source` are a `oneOf` of the four source types. Current hosts send such a reference as a plain object, which the provider rejects with `source is required and must be a resource`.

## Known differences from the TypeScript program

- HCL reports dependencies for the whole `stacks` map, not per entry, so every entry is declared to depend on every resource the map references. Here `worker` is declared to depend on `platform`, which only `api` reads.
- Delivery runs the pipeline program on its hosted runner during a run. Whether that runner can run `runtime: hcl` programs is not established. Registration from your machine works with the requirements above.
