# AGENTS.md

Guidance for AI coding agents that work in this repository. `README.md` describes what Pulumi Delivery is, the repository layout and the make targets; read it first.

## What this repository is

The Pulumi Delivery provider (`provider/`, Go, built on `pulumi-go-provider`'s `infer`) and its generated SDKs (`sdk/`). The Delivery service itself is part of Pulumi Cloud and is not in this repository.

This repository is public. Do not put internal links, ticket numbers, customer names or internal host names in code, comments, commit messages or pull requests.

## Generated files: never edit by hand

| Files | Source | Regenerate with |
|---|---|---|
| `provider/cmd/pulumi-resource-delivery/schema.json` | the built provider | `make generate` |
| `sdk/` (except `sdk/go.mod` and `sdk/go.sum`) | the schema | `make generate` |
| `Makefile`, `.github/`, `.config/`, `scripts/`, `.golangci.yml` | `.ci-mgmt.yaml` | `make ci-mgmt` |

Commit the regenerated files in the same change as their source. CI fails when the committed schema or SDKs differ from a fresh generation.

To customize the build, change `.ci-mgmt.yaml`, `.mk/*.mk` or `sdk-hooks.mk`, not the generated files.

## Tools

Tools come from [mise](https://mise.jdx.dev/) and are pinned in `.config/mise.toml`. The Pulumi CLI version is the version of `github.com/pulumi/pulumi/pkg/v3` in `provider/go.mod`, so the CLI and the code generator always match.

A Pulumi CLI or language plugin from another installation on `PATH` (for example from Homebrew) takes precedence over the pinned one and generates different SDKs. Generate like this:

```sh
PULUMI_IGNORE_AMBIENT_PLUGINS=true mise exec -- make generate
```

## Commands

- `make build`: build the provider and all SDKs.
- `make generate`: rewrite the schema and all SDKs.
- `make test_provider`: provider unit tests. Run these after every provider change.
- `make TESTTAGS=<language> test`: example tests for `go`, `nodejs`, `python`, `dotnet` or `java`. They compile a program against the SDK and run `pulumi preview` on it. Run `make build` first; the tests find the provider in `bin/`. The YAML and HCL previews run with `TESTTAGS=go`.

A preview sends nothing to Pulumi Cloud, so these tests need no credentials. Use a preview on a file backend (`PULUMI_BACKEND_URL=file://<dir>`) to check a program quickly.

## The schema

`infer` derives the schema from the Go types in `provider/`. Two files then shape it:

- `provider/annotations.go`: descriptions of resources, functions and properties. These are customer-facing documentation.
- `provider/schema_refs.go`: patches that `infer` cannot express, such as inputs that refer to a resource, union types and enums.

The schema is a public contract. A change to a resource, a function, a property name or a property type changes all five SDKs. Say so in the pull request, and keep changes backward compatible unless the pull request explains why not. Published package names cannot change.

When you change the schema, add or update the schema assertions in `provider/provider_test.go`.

## Rules for gates

`ruleKinds` in `provider/schema_refs.go` is the list of valid rule kinds. `validateRule` and `validateRuleValue` in `provider/rule_functions.go` reject every other kind. A new kind must be added to the list and to both validators.

## Conventions

Hand-written source files start with `// Copyright <year>, Pulumi Corporation. All rights reserved.`

## Releases

Do not push tags and do not publish packages. A maintainer does releases. `README.md` describes the release flow.
