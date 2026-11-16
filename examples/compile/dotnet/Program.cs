// Copyright 2026, Pulumi Corporation. All rights reserved.

// Checks that a .NET program builds against the generated SDK. It is never run.
using System.Collections.Generic;
using Pulumi;
using Pulumi.Delivery;
using Pulumi.Delivery.Delivery;
using Pulumi.Delivery.Delivery.Inputs;

public static class Compile
{
    public static void Program()
    {
        var code = new GitHubSource("code", new GitHubSourceArgs
        {
            Repository = "acme/shop",
            Branch = "main",
            Trigger = new SourceTriggerArgs { Paths = { "src/**" } },
        });
        // A source that is part of the release and never starts a run.
        new GitHubSource("docs", new GitHubSourceArgs { Repository = "acme/docs", Branch = "main" });
        var platform = new PipelineSource("platform", new PipelineSourceArgs
        {
            Pipeline = "platform-delivery/main",
            Stage = "prod",
            Trigger = true,
        });
        var pipeline = new Pipeline("shop", new PipelineArgs
        {
            Program = new ProgramSourceArgs { Source = code, Directory = "delivery" },
        });
        // manual returns a plain result in .NET: the rule is built as RuleArgs.
        _ = Manual.InvokeAsync(new ManualArgs { Name = "dashboards", Description = "Check the dashboards first." });
        var prod = new Stage("prod", new StageArgs
        {
            Pipeline = pipeline,
            When = new RuleArgs { Kind = "manual", Name = "dashboards", Description = "Check the dashboards first." },
        });
        var release = pipeline.ReleaseNumber;
        var commit = code.Commit;
        // A structured, secret value.
        var value = Output.CreateSecret(new Dictionary<string, object> { ["host"] = "example.com", ["ports"] = new[] { 443 } });
        new Artifact("clusterEndpoint", new ArtifactArgs { Stage = prod, Value = value.Apply(v => (object)v) });
    }
}
