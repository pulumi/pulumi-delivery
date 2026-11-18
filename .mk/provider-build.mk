# Under useProviderBinarySchemaGen the generated bin/$(PROVIDER) rule has no prerequisites, so make would reuse a
# stale binary after source edits. This rule adds the real inputs and has no recipe, so it merges with the generated one.
# Do not list provider/cmd/$(PROVIDER)/schema.json: it is built from the binary and would be circular.
# The provider has no //go:embed inputs.
PROVIDER_SOURCES := $(shell find provider -name '*.go' -not -path '*/testdata/*') provider/go.mod provider/go.sum

bin/$(PROVIDER): $(PROVIDER_SOURCES)
