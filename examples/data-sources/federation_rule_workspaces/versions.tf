terraform {
  required_version = "~> 1.0"

  required_providers {
    anthropic = {
      source  = "terraform.aisi.org.uk/aisi/anthropic"
      version = "~> 1.0"
    }
  }
}
