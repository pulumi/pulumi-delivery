// Copyright 2026, Pulumi Corporation.  All rights reserved.

// Command pulumi-resource-delivery declares pipelines, stages, jobs and their sources for the
// Delivery service.
package main

import (
	"context"
	"fmt"
	"os"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
)

// Version is set by ldflags at release time.
var Version = "0.0.0-dev"

func main() {
	provider, err := newProvider()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := p.RunProvider(context.Background(), "delivery", Version, provider); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newProvider() (p.Provider, error) {
	config := &ProviderConfig{}
	provider, err := infer.NewProviderBuilder().
		WithConfig(infer.Config(config)).
		WithNamespace("delivery").
		WithDisplayName("Pulumi Delivery").
		WithDescription("Define Delivery pipelines, stages, jobs and sources in a Pulumi program.").
		WithPublisher("pulumi").
		WithRepository("https://github.com/pulumi/pulumi-delivery").
		WithHomepage("https://github.com/pulumi/pulumi-delivery").
		WithKeywords("pulumi", "delivery", "category/utility", "kind/native").
		WithPluginDownloadURL("https://get.pulumi.com/releases/plugins/").
		WithSupportPack(true).
		WithLanguageMap(map[string]any{
			"nodejs": map[string]any{"packageName": "@pulumi/delivery", "respectSchemaVersion": true},
			"python": map[string]any{"packageName": "pulumi_delivery", "respectSchemaVersion": true, "pyproject": map[string]any{"enabled": true}},
			"go":     map[string]any{"importBasePath": "github.com/pulumi/pulumi-delivery/sdk/go/delivery", "generateResourceContainerTypes": true, "respectSchemaVersion": true},
			"csharp": map[string]any{"rootNamespace": "Pulumi.Delivery", "respectSchemaVersion": true},
			"java":   map[string]any{"basePackage": "com.pulumi.delivery"},
		}).
		WithModuleMap(map[tokens.ModuleName]tokens.ModuleName{"provider": "index"}).
		WithResources(
			infer.Resource(&MemberRun{}),
			infer.Resource(&WorkflowsRuntime{}),
		).
		WithComponents(
			infer.ComponentF(notInferred[PipelineArgs, *Pipeline]),
			infer.ComponentF(notInferred[StageArgs, *Stage]),
			infer.ComponentF(notInferred[JobArgs, *Job]),
			infer.ComponentF(notInferred[StacksArgs, *Stacks]),
			infer.ComponentF(notInferred[ArtifactArgs, *Artifact]),
			infer.ComponentF(notInferred[PipelineSourceArgs, *PipelineSource]),
			infer.ComponentF(notInferred[SourceArgs, *GitHubSource]),
			infer.ComponentF(notInferred[SourceArgs, *GitLabSource]),
			infer.ComponentF(notInferred[SourceArgs, *BitbucketSource]),
			infer.ComponentF(notInferred[SourceArgs, *AzureDevOpsSource]),
		).
		WithFunctions(
			infer.Function(Approval{}),
			infer.Function(HTTP{}),
			infer.Function(Manual{}),
			infer.Function(AllOf{}),
			infer.Function(AnyOf{}),
		).
		Build()
	if err != nil {
		return p.Provider{}, err
	}
	provider = unknownOutputsInPreview(provider)
	provider.Construct = withDeliveryConstruct(config, provider.Construct)
	provider.GetSchema = withReferences(provider.GetSchema)
	return provider, nil
}
