// Copyright 2026, Pulumi Corporation. All rights reserved.

// Program of the local run tests (local_test.go). The environment of the test chooses the case: "jobs" has the jobs a
// and b, in one stage or in two, and "stacks" has a group of two entries. The consumer resource of another provider
// writes the outputs that it saw to the file in LOCAL_OBSERVED.
package main

import (
	"encoding/json"
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
		lib, err := delivery.NewGitHubSource(ctx, "lib", &delivery.GitHubSourceArgs{
			Repository: pulumi.String("acme/lib"), Branch: pulumi.String("main"),
		})
		if err != nil {
			return err
		}
		pipeline, err := delivery.NewPipeline(ctx, "shop", nil)
		if err != nil {
			return err
		}
		dev, err := delivery.NewStage(ctx, "dev", &delivery.StageArgs{Pipeline: pipeline})
		if err != nil {
			return err
		}
		if os.Getenv("LOCAL_CASE") == "stacks" {
			return stacks(ctx, code, dev)
		}
		second := dev
		if os.Getenv("LOCAL_TWO_STAGES") != "" {
			second, err = delivery.NewStage(ctx, "prod", &delivery.StageArgs{Pipeline: pipeline, From: delivery.StageArray{dev}})
			if err != nil {
				return err
			}
		}
		a, err := delivery.NewJob(ctx, "a", &delivery.JobArgs{
			Stage: dev, Source: code, Directory: pulumi.String("app"), Command: pulumi.String(env("LOCAL_A_CMD", "a1")),
		})
		if err != nil {
			return err
		}
		bArgs := &delivery.JobArgs{Stage: second, Source: lib, Directory: pulumi.String("app"), Command: pulumi.String(env("LOCAL_B_CMD", "b1"))}
		var opts []pulumi.ResourceOption
		switch os.Getenv("LOCAL_B_MODE") {
		case "output":
			bArgs.Environment = pulumi.StringMap{"UP": a.Outputs.MapIndex(pulumi.String("exec"))}
		case "depends":
			opts = append(opts, pulumi.DependsOn([]pulumi.Resource{a}))
		}
		b, err := delivery.NewJob(ctx, "b", bArgs, opts...)
		if err != nil {
			return err
		}
		seen := pulumi.All(a.Outputs, b.Outputs).ApplyT(func(v []any) string {
			out, _ := json.Marshal(map[string]any{"a": v[0], "b": v[1]})
			_ = os.WriteFile(os.Getenv("LOCAL_OBSERVED"), out, 0o600)
			return string(out)
		}).(pulumi.StringOutput)
		return ctx.RegisterResource("marker:index:File", "consumer", pulumi.Map{"value": seen}, &markerFile{})
	})
}

func stacks(ctx *pulumi.Context, code *delivery.GitHubSource, dev *delivery.Stage) error {
	entry := func(stack, command string) delivery.StacksEntryInput {
		return delivery.StacksEntryArgs{
			Stack: pulumi.String(stack), Source: code, Directory: pulumi.String("app"),
			Environment: pulumi.StringMap{"CMD": pulumi.String(command)},
		}
	}
	group, err := delivery.NewStacks(ctx, "svc", &delivery.StacksArgs{Stage: dev, Stacks: delivery.StacksEntryMap{
		"x": entry("acme/svc-x/dev", env("LOCAL_X_CMD", "x1")),
		"y": entry("acme/svc-y/dev", env("LOCAL_Y_CMD", "y1")),
	}})
	if err != nil {
		return err
	}
	seen := group.Outputs.ApplyT(func(outputs map[string]map[string]any) string {
		out, _ := json.Marshal(outputs)
		_ = os.WriteFile(os.Getenv("LOCAL_OBSERVED"), out, 0o600)
		return string(out)
	}).(pulumi.StringOutput)
	return ctx.RegisterResource("marker:index:File", "consumer", pulumi.Map{"value": seen}, &markerFile{})
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
