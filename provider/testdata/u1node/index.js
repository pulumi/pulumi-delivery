// Copyright 2026, Pulumi Corporation. All rights reserved.

// Node.js program of the U1 test (u1_test.go): a Job that waits, and a resource of another provider that uses an
// output of the Job. The components are declared like the generated SDK declares them (sdk/nodejs): remote
// components whose outputs are inputs with an undefined value.
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

const code = new GitHubSource("code", { repository: "acme/shop", branch: "main" });
const pipeline = new Pipeline("shop");
const dev = new Stage("dev", { pipeline });
const job = new Job("build", { stage: dev, source: code, directory: "app", command: "make" });

const pool = job.outputs.apply((outputs) => {
    // Record that the apply ran.
    fs.writeFileSync(process.env.U1_APPLY_MARKER, "ran");
    return outputs.pool;
});
// A resource of another provider that uses an output of the job.
new pulumi.CustomResource("marker:index:File", "consumer", { value: pool });
