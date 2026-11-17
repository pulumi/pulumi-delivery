// Copyright 2026, Pulumi Corporation. All rights reserved.

// Node.js program of the local run tests (local_test.go), the same cases as testdata/localgo. The components are
// declared like the generated SDK declares them: remote components whose outputs are inputs with an undefined value.
const fs = require("fs");
const pulumi = require("@pulumi/pulumi");

function remote(type, outputs) {
    return class extends pulumi.ComponentResource {
        constructor(name, args, opts) {
            const inputs = { ...args };
            for (const output of outputs) {
                inputs[output] = undefined;
            }
            super(type, name, inputs, opts, true);
        }
    };
}

const GitHubSource = remote("delivery:index:GitHubSource", ["commit"]);
const Pipeline = remote("delivery:index:Pipeline", []);
const Stage = remote("delivery:index:Stage", []);
const Job = remote("delivery:index:Job", ["outputs"]);
const Stacks = remote("delivery:index:Stacks", ["outputs"]);
const env = (name, fallback) => process.env[name] || fallback;

const code = new GitHubSource("code", { repository: "acme/shop", branch: "main" });
const lib = new GitHubSource("lib", { repository: "acme/lib", branch: "main" });
const pipeline = new Pipeline("shop");
const dev = new Stage("dev", { pipeline });

if (process.env.LOCAL_CASE === "stacks") {
    const entry = (stack, command) => ({ stack, source: code, directory: "app", environment: { CMD: command } });
    const group = new Stacks("svc", {
        stage: dev,
        stacks: { x: entry("acme/svc-x/dev", env("LOCAL_X_CMD", "x1")), y: entry("acme/svc-y/dev", env("LOCAL_Y_CMD", "y1")) },
    });
    const seen = group.outputs.apply((outputs) => {
        const out = JSON.stringify(outputs);
        fs.writeFileSync(process.env.LOCAL_OBSERVED, out);
        return out;
    });
    new pulumi.CustomResource("marker:index:File", "consumer", { value: seen });
} else {
    const second = process.env.LOCAL_TWO_STAGES ? new Stage("prod", { pipeline, from: [dev] }) : dev;
    const a = new Job("a", { stage: dev, source: code, directory: "app", command: env("LOCAL_A_CMD", "a1") });
    const bArgs = { stage: second, source: lib, directory: "app", command: env("LOCAL_B_CMD", "b1") };
    const opts = {};
    if (process.env.LOCAL_B_MODE === "output") {
        bArgs.environment = { UP: a.outputs.apply((o) => o.exec) };
    } else if (process.env.LOCAL_B_MODE === "depends") {
        opts.dependsOn = [a];
    }
    const b = new Job("b", bArgs, opts);
    const seen = pulumi.all([a.outputs, b.outputs]).apply(([ao, bo]) => {
        const out = JSON.stringify({ a: ao, b: bo });
        fs.writeFileSync(process.env.LOCAL_OBSERVED, out);
        return out;
    });
    new pulumi.CustomResource("marker:index:File", "consumer", { value: seen });
}
