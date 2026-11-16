.PHONY: provider schema sdk check-sdk check-programs pulumi-version

# Stock CLI used to generate the SDKs. CI installs this version.
PULUMI_VERSION := 3.272.0

# Version stamped into local builds and the committed SDK. A release takes its version from the git tag.
PROVIDER_VERSION ?= 0.0.0-dev

# The Java language plugin that generates sdk/java. The CLI does not bundle one, and a stray binary on PATH
# or a different download gives a different build.gradle, so the sdk target pins this one.
JAVA_PLUGIN_VERSION := 1.39.0

# Fails when the CLI on PATH is not the pinned one: other versions generate SDKs that do not compile.
define require-pulumi
	@pulumi version | grep -qx "v$(PULUMI_VERSION)" || { echo "pulumi $(PULUMI_VERSION) is required (found $$(pulumi version)); put it first on PATH"; exit 1; }
endef

provider:
	cd provider && GOTOOLCHAIN=auto go build -o bin/pulumi-resource-delivery .

# Writes schema.json from the built provider, without a version. A release takes its version from the git tag.
schema:
	$(require-pulumi)
	cd provider && GOTOOLCHAIN=auto go build -ldflags "-X main.Version=$(PROVIDER_VERSION)" -o bin/pulumi-resource-delivery .
	pulumi package get-schema ./provider/bin/pulumi-resource-delivery | jq --indent 2 'del(.version)' > schema.json.new
	mv schema.json.new schema.json

# Generates every SDK from schema.json, stamped with PROVIDER_VERSION. Never edit sdk/ by hand.
sdk:
	$(require-pulumi)
	pulumi plugin install language java $(JAVA_PLUGIN_VERSION)
	rm -rf sdk
	tmp=$$(mktemp -d) && jq --arg v "$(PROVIDER_VERSION)" '.version=$$v' schema.json > $$tmp/schema.json && \
	export PATH="$${PULUMI_HOME:-$$HOME/.pulumi}/plugins/language-java-v$(JAVA_PLUGIN_VERSION):$$PATH" && \
	for lang in nodejs python go dotnet java; do \
		pulumi package gen-sdk $$tmp/schema.json --language $$lang -o sdk $$( [ $$lang = nodejs ] && echo --local ) || exit 1; \
	done; rm -rf $$tmp

check-sdk: sdk
	@test -z "$$(git status --porcelain --untracked-files=all -- sdk)" || { git status --short -- sdk; git --no-pager diff -- sdk | head -80; echo "sdk/ is stale: run make sdk and commit"; exit 1; }

# Builds a Go and a .NET program against the generated SDKs. Both set a trigger on a source and on a PipelineSource,
# put a manual gate on a Stage, read the release number and the commit, and publish a structured, secret value in an
# Artifact. Run it after make sdk. Python needs no compile check.
check-programs:
	cd examples/compile/go && go vet ./...
	cd examples/compile/dotnet && dotnet build --nologo -v q
	python3 -c "import pathlib; [compile(f.read_text(), str(f), 'exec') for f in pathlib.Path('sdk/python/pulumi_delivery').rglob('*.py')]"

pulumi-version:
	@echo $(PULUMI_VERSION)
