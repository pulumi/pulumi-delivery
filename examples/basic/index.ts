import * as delivery from "@pulumi/delivery";

const code = new delivery.GitHubSource("code", {
    repository: "acme/shop",
    branch: "main",
    trigger: {paths: ["src/**", "delivery/**"]},
});
// A stage of another pipeline in the same organization. Its artifacts are known once a run reads them.
const platform = new delivery.PipelineSource("platform", {
    pipeline: "platform-delivery/main",
    stage: "prod",
    trigger: true,
});
const pipeline = new delivery.Pipeline("shop");
const dev = new delivery.Stage("dev", {pipeline});
const prod = new delivery.Stage("prod", {
    pipeline,
    from: [dev],
    when: delivery.allOfOutput({rules: [
        delivery.approvalOutput({approvers: ["your-username"]}),
        delivery.manualOutput({description: "Check the dashboards before prod."}),
    ]}),
});
new delivery.Job("test", {stage: dev, source: code, directory: "app", command: "npm test"});
const smoke = new delivery.Job("smoke", {stage: prod, source: code, directory: "app", command: "./scripts/smoke.sh"});
// Pipelines that read the prod stage with a PipelineSource see this artifact as artifacts["shopEndpoint"].
new delivery.Artifact("shopEndpoint", {stage: prod, value: smoke.outputs.apply(outputs => outputs["endpoint"])});
const services = new delivery.Stacks("services", {
    stage: dev,
    stacks: {
        api: {
            stack: "shop-api/dev",
            source: code,
            directory: "api",
            config: {clusterEndpoint: platform.artifacts["clusterEndpoint"]},
        },
        worker: {stack: "shop-worker/dev", source: code, directory: "worker", timeout: 1800},
    },
});
export const release = pipeline.releaseNumber;
export const commit = code.commit;
export const apiOutputs = services.outputs.apply(outputs => outputs["api"]);
