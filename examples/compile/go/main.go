// Copyright 2026, Pulumi Corporation. All rights reserved.

// Command compile checks that a Go program builds against the generated SDK. It is never run.
package main

import (
	"github.com/pulumi/pulumi-delivery/sdk/go/delivery"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		code, err := delivery.NewGitHubSource(ctx, "code", &delivery.GitHubSourceArgs{
			Repository: pulumi.String("acme/shop"), Branch: pulumi.String("main"),
			Trigger: delivery.SourceTriggerArgs{Paths: pulumi.StringArray{pulumi.String("src/**")}},
		})
		if err != nil {
			return err
		}
		platform, err := delivery.NewPipelineSource(ctx, "platform", &delivery.PipelineSourceArgs{
			Pipeline: pulumi.String("platform-delivery/main"), Stage: pulumi.String("prod"),
			Trigger: pulumi.Bool(true),
		})
		if err != nil {
			return err
		}
		// A source that is part of the release and never starts a run.
		_, err = delivery.NewGitHubSource(ctx, "docs", &delivery.GitHubSourceArgs{
			Repository: pulumi.String("acme/docs"), Branch: pulumi.String("main"),
		})
		if err != nil {
			return err
		}
		pipeline, err := delivery.NewPipeline(ctx, "shop", nil)
		if err != nil {
			return err
		}
		// manual returns a plain result in Go: the rule is built as RuleArgs.
		_, _ = delivery.Manual(ctx, &delivery.ManualArgs{Name: pulumi.StringRef("dashboards"), Description: pulumi.StringRef("Check the dashboards first.")})
		prod, err := delivery.NewStage(ctx, "prod", &delivery.StageArgs{
			Pipeline: pipeline,
			When:     delivery.RuleArgs{Kind: pulumi.String(delivery.RuleKindManual), Name: pulumi.String("dashboards"), Description: pulumi.String("Check the dashboards first.")},
		})
		if err != nil {
			return err
		}
		// A structured, secret value.
		_, err = delivery.NewArtifact(ctx, "clusterEndpoint", &delivery.ArtifactArgs{
			Stage: prod,
			Value: pulumi.ToSecret(pulumi.Map{"host": pulumi.String("example.com"), "ports": pulumi.Array{pulumi.Int(443)}}),
		})
		if err != nil {
			return err
		}
		ctx.Export("release", pipeline.ReleaseNumber)
		ctx.Export("commit", code.Commit)
		ctx.Export("endpoint", platform.Artifacts.MapIndex(pulumi.String("clusterEndpoint")))
		return nil
	})
}
