# Pulumi Delivery

Delivery needs Pulumi CLI v3.272.0 or later.

The repository has these directories.

- `provider/`: Go resource provider.
- `sdk/`: SDK generated from `schema.json` (`make sdk`, never edited by hand).
- `examples/basic/`: a small pipeline program.
- `examples/compile/`: programs that check the generated SDKs compile.
- `docs/`: `_index.md` is the registry guide. `ONBOARDING.md` is the onboarding guide.

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

1. Push the tag `vX.Y.Z` or `vX.Y.Z-alpha.N`. The `release` workflow stamps that version into the plugin and uploads the archives. The repository holds no version; local builds and the committed SDK say `0.0.0-dev`.
2. A registry administrator publishes the schema and `docs/_index.md` as `pulumi/pulumi/delivery` with the registry API. Do this for each release.
