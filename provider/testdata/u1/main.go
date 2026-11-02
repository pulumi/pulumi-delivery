// Copyright 2026, Pulumi Corporation. All rights reserved.

// Program of the U1 test (u1_test.go): a Job that waits, and a resource of another provider that uses an output of the Job.
package main

import (
	"os"

	"github.com/pulumi/pulumi-delivery/sdk/go/delivery"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type markerFile struct{ pulumi.CustomResourceState }

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		code, err := delivery.NewGitHubSource(ctx, "code", &delivery.GitHubSourceArgs{
			Repository: pulumi.String("acme/shop"), Branch: pulumi.String("main"),
		})
		if err != nil {
			return err
		}
		pipeline, err := delivery.NewPipeline(ctx, "shop", &delivery.PipelineArgs{
			Program: &delivery.ProgramSourceArgs{Source: code, Directory: pulumi.String("delivery")},
		})
		if err != nil {
			return err
		}
		dev, err := delivery.NewStage(ctx, "dev", &delivery.StageArgs{Pipeline: pipeline})
		if err != nil {
			return err
		}
		if os.Getenv("U1_CASE") == "artifact" {
			return artifactCase(ctx, dev)
		}
		if kase := os.Getenv("U1_CASE"); kase != "" {
			return stacksCase(ctx, kase, code, dev)
		}
		job, err := delivery.NewJob(ctx, "build", &delivery.JobArgs{
			Stage: dev, Source: code, Directory: pulumi.String("app"), Command: pulumi.String("make"),
		})
		if err != nil {
			return err
		}
		pool := job.Outputs.ApplyT(func(outputs map[string]string) string {
			// Record that the apply ran and with which value.
			_ = os.WriteFile(os.Getenv("U1_APPLY_MARKER"), []byte("ran"), 0o600)
			return outputs["pool"]
		}).(pulumi.StringOutput)
		// A resource of another provider that uses an output of the job.
		return ctx.RegisterResource("marker:index:File", "consumer", pulumi.Map{"value": pool}, &markerFile{})
	})
}

// stacksCase is a Stacks resource with a waiting entry and a resource of another provider that uses the output
// of the entry "api". In the case "group" the group has a second entry, which waits too.
func stacksCase(ctx *pulumi.Context, kase string, code *delivery.GitHubSource, dev *delivery.Stage) error {
	entry := func(stack string) delivery.StacksEntryInput {
		return delivery.StacksEntryArgs{Stack: pulumi.String(stack), Source: code, Directory: pulumi.String("app")}
	}
	entries := delivery.StacksEntryMap{"api": entry("acme/shop-api/dev")}
	if kase == "group" {
		entries["worker"] = entry("acme/shop-worker/dev")
	}
	stacks, err := delivery.NewStacks(ctx, "svc", &delivery.StacksArgs{Stage: dev, Stacks: entries})
	if err != nil {
		return err
	}
	pool := stacks.Outputs.ApplyT(func(outputs map[string]map[string]any) string {
		_ = os.WriteFile(os.Getenv("U1_APPLY_MARKER"), []byte("ran"), 0o600)
		pool, _ := outputs["api"]["pool"].(string)
		return pool
	}).(pulumi.StringOutput)
	return ctx.RegisterResource("marker:index:File", "consumer", pulumi.Map{"value": pool}, &markerFile{})
}

// artifactCase is an Artifact with a known value and a resource of another provider that uses its value.
func artifactCase(ctx *pulumi.Context, dev *delivery.Stage) error {
	artifact, err := delivery.NewArtifact(ctx, "endpoint", &delivery.ArtifactArgs{
		Stage: dev, Value: pulumi.ToSecret(pulumi.String("https://example.com")),
	})
	if err != nil {
		return err
	}
	value := artifact.Value.ApplyT(func(v any) string {
		_ = os.WriteFile(os.Getenv("U1_APPLY_MARKER"), []byte("ran"), 0o600)
		s, _ := v.(string)
		return s
	}).(pulumi.StringOutput)
	return ctx.RegisterResource("marker:index:File", "consumer", pulumi.Map{"value": value}, &markerFile{})
}
