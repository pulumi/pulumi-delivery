terraform {
  required_providers {
    delivery = {
      source  = "pulumi/delivery"
      version = "0.0.1-alpha.3"
    }
  }
}

resource "delivery_github_source" "code" {
  repository = "acme/shop"
  branch     = "main"
  trigger = {
    paths = ["src/**", "delivery/**"]
  }
}

# A stage of another pipeline in the same organization. Its artifacts are known once a run reads them.
resource "delivery_pipeline_source" "platform" {
  pipeline = "acme/platform-delivery/main"
  stage    = "prod"
  trigger  = true
}

resource "delivery_pipeline" "shop" {
  program = {
    source    = delivery_github_source.code
    directory = "delivery"
  }
}

resource "delivery_stage" "dev" {
  pipeline = delivery_pipeline.shop
}

resource "delivery_stage" "prod" {
  pipeline = delivery_pipeline.shop
  from     = [delivery_stage.dev]

  # Entry rules are objects. Each kind (approval, manual, http, all, any) takes the arguments of
  # the delivery function of the same name.
  when = {
    kind = "all"
    rules = [
      { kind = "approval", approvers = ["your-username"] },
      { kind = "manual", description = "Check the dashboards before prod." },
    ]
  }
}

resource "delivery_job" "test" {
  stage     = delivery_stage.dev
  source    = delivery_github_source.code
  directory = "app"
  command   = "npm test"
}

resource "delivery_job" "smoke" {
  stage     = delivery_stage.prod
  source    = delivery_github_source.code
  directory = "app"
  command   = "./scripts/smoke.sh"
}

# Pipelines that read the prod stage with a PipelineSource see this artifact as artifacts["shopEndpoint"].
resource "delivery_artifact" "shopEndpoint" {
  stage = delivery_stage.prod
  value = delivery_job.smoke.outputs["endpoint"]
}

resource "delivery_stacks" "services" {
  stage = delivery_stage.dev
  stacks = {
    api = {
      stack     = "acme/shop-api/dev"
      source    = delivery_github_source.code
      directory = "api"
      config = {
        clusterEndpoint = delivery_pipeline_source.platform.artifacts["clusterEndpoint"]
      }
    }
    worker = {
      stack     = "acme/shop-worker/dev"
      source    = delivery_github_source.code
      directory = "worker"
      timeout   = 1800
    }
  }
}

output "release" {
  value = delivery_pipeline.shop.release_number
}

output "commit" {
  value = delivery_github_source.code.commit
}

output "apiOutputs" {
  value = delivery_stacks.services.outputs["api"]
}
