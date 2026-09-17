---
page_title: "Installing the AISI build"
subcategory: ""
description: |-
  Install this fork from a filesystem mirror synced from S3, pin it with a committed lock file, and keep the public registry out of the resolution path.
---

# Installing the AISI build

This provider is served at `terraform.aisi.org.uk/aisi/anthropic`. The hostname is a mirror path, not a registry: nothing is published to the public Terraform Registry under it, and it differs from upstream's `registry.terraform.io/ippontech/anthropic` on purpose. A configuration that names this source cannot be satisfied by Ippon's full-surface provider, and one that names Ippon's cannot pick this build up.

## 1. The mirror directory

A release (see `RELEASE.md`) is a GitHub release with one zip per platform, `terraform-provider-anthropic_<version>_<os>_<arch>.zip`, a `*_SHA256SUMS` file and a build provenance attestation. Terraform's packed filesystem mirror layout is those zips under a directory named after the provider address:

```text
<mirror>/terraform.aisi.org.uk/aisi/anthropic/terraform-provider-anthropic_1.43.2-aisi.1_linux_amd64.zip
<mirror>/terraform.aisi.org.uk/aisi/anthropic/terraform-provider-anthropic_1.43.2-aisi.1_darwin_arm64.zip
```

Publish a release by verifying the assets and copying the zips to the S3 prefix that backs the mirror:

```shell
version=1.43.2-aisi.1
repo=<owner>/terraform-provider-anthropic
gh release download "v${version}" --repo "${repo}" --dir dist
(cd dist && sha256sum -c "terraform-provider-anthropic_${version}_SHA256SUMS")
for zip in dist/*.zip; do gh attestation verify "${zip}" --repo "${repo}"; done
aws s3 sync dist/ "s3://<bucket>/terraform-mirror/terraform.aisi.org.uk/aisi/anthropic/" --exclude '*' --include '*.zip'
```

Consumers (a laptop, a CI job) sync the bucket to a local directory before `terraform init`:

```shell
aws s3 sync "s3://<bucket>/terraform-mirror/" "${HOME}/.terraform.d/aisi-mirror/"
```

## 2. CLI configuration

Put this in `~/.terraformrc`, or in a file named by `TF_CLI_CONFIG_FILE` in CI:

```hcl
provider_installation {
  filesystem_mirror {
    path    = "/home/<user>/.terraform.d/aisi-mirror"
    include = ["terraform.aisi.org.uk/*/*"]
  }
  direct {
    exclude = ["terraform.aisi.org.uk/*/*"]
  }
}
```

The `include` and `exclude` pair sends this address to the mirror and every other provider to its registry as usual. Without the `exclude`, a version missing from the mirror falls through to a registry lookup against a host that does not serve one; the result is still an error, but a slower one with a misleading message.

## 3. Configuration and lock file

```hcl
terraform {
  required_providers {
    anthropic = {
      source  = "terraform.aisi.org.uk/aisi/anthropic"
      version = "1.43.2-aisi.1"
    }
  }
}
```

Pin the exact version. The fork's versions carry a prerelease suffix (`-aisi.N`), and Terraform matches a prerelease version only against an exact constraint, never against `~>` or `>=`.

Run `terraform init` and commit `.terraform.lock.hcl`. From a filesystem mirror the lock file records an `h1:` hash for the platform `init` ran on; add the other platforms your team and CI use so every machine verifies the same artefacts:

```shell
terraform providers lock \
  -fs-mirror="${HOME}/.terraform.d/aisi-mirror" \
  -platform=linux_amd64 -platform=darwin_arm64 -platform=darwin_amd64
```

The committed hashes are the check that the zip a machine installs is the one that was reviewed: a mirror entry replaced by a different build fails `terraform init` with a checksum mismatch.

## 4. Development builds

`make install` builds the provider into `$GOBIN` and `make .dev.tfrc` writes a `dev_overrides` block for this address (see `CONTRIBUTING.md`). `dev_overrides` bypasses the mirror and the lock file; use it on a branch, never in CI.
