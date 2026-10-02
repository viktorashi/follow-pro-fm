terraform {
  required_version = ">= 1.8.0"

  required_providers {
    fly = {
      source  = "ampbase-io/fly"
      version = "~> 0.3.0"
    }
  }
}
