terraform {
  required_providers {
    delivery = {
      source = "pulumi/delivery/delivery"
    }
  }
}

resource "delivery_git_hub_source" "code" {
  repository = "acme/shop"
  branch     = "main"
  trigger = {
    paths = ["src/**"]
  }
}

resource "delivery_pipeline" "shop" {
}

resource "delivery_stage" "dev" {
  pipeline = delivery_pipeline.shop
}

resource "delivery_stage" "prod" {
  pipeline = delivery_pipeline.shop
  from     = [delivery_stage.dev]
  when = {
    kind = "approval"
  }
}

resource "delivery_job" "test" {
  stage     = delivery_stage.dev
  source    = delivery_git_hub_source.code
  directory = "app"
  command   = "npm test"
}

resource "delivery_stacks" "services" {
  stage = delivery_stage.dev
  stacks = {
    api = {
      stack     = "shop-api/dev"
      source    = delivery_git_hub_source.code
      directory = "api"
    }
  }
}

output "release" {
  value = delivery_pipeline.shop.release_number
}
