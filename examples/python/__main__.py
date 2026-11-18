# Copyright 2026, Pulumi Corporation. All rights reserved.

import pulumi
import pulumi_delivery as delivery

code = delivery.GitHubSource(
    "code",
    repository="acme/shop",
    branch="main",
    trigger=delivery.SourceTriggerArgs(paths=["src/**"]),
)
# A source that is part of the release and never starts a run.
docs = delivery.GitHubSource("docs", repository="acme/docs", branch="main")
platform = delivery.PipelineSource("platform", pipeline="platform-delivery/main", stage="prod", trigger=True)

pipeline = delivery.Pipeline("shop")
dev = delivery.Stage("dev", pipeline=pipeline)
prod = delivery.Stage(
    "prod",
    pipeline=pipeline,
    from_=[dev],
    # Plain rule values: Python rejects the result of approval_output and the other rule functions as an input.
    when=delivery.RuleArgs(kind="all", rules=[
        delivery.RuleArgs(kind="approval", approvers=["your-username"]),
        delivery.RuleArgs(kind="manual", description="Check the dashboards before prod."),
    ]),
)
delivery.Job("test", stage=dev, source=code, directory="app", command="npm test")
services = delivery.Stacks(
    "services",
    stage=dev,
    stacks={
        "api": delivery.StacksEntryArgs(
            stack="shop-api/dev",
            source=code,
            directory="api",
            config={"clusterEndpoint": platform.artifacts["clusterEndpoint"]},
        ),
    },
)

pulumi.export("release", pipeline.release_number)
pulumi.export("commit", code.commit)
