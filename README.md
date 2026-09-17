# Terraform Provider Anthropic (WIF subset)

A fork of [ippontech/terraform-provider-anthropic](https://github.com/ippontech/terraform-provider-anthropic)
trimmed to the Workload Identity Federation subset of the Anthropic Admin API:
federation issuers, service accounts, service account workspace memberships,
federation rules, rule workspaces and workspaces. The provider authenticates
either with a static `org:admin` bearer or by exchanging the workload's own
OIDC identity token itself, so CI stores no Anthropic credential.

- [`docs/index.md`](docs/index.md): configuration and authentication.
- [`docs/guides/workload_identity_federation.md`](docs/guides/workload_identity_federation.md): bootstrap and end-to-end setup.
- [`FORK.md`](FORK.md): why the fork exists, what was removed, how to sync with upstream.
- [`REVIEW.md`](REVIEW.md): review map (packages, network calls, credentials, dependencies).
- [`RELEASE.md`](RELEASE.md): tagging, signing, provenance.

## CI

[![Provider](https://github.com/jon-aisi/terraform-provider-anthropic/actions/workflows/provider.yml/badge.svg?branch=aisi%2Fwif-subset)](https://github.com/jon-aisi/terraform-provider-anthropic/actions/workflows/provider.yml)
[![Security](https://github.com/jon-aisi/terraform-provider-anthropic/actions/workflows/security.yml/badge.svg?branch=aisi%2Fwif-subset)](https://github.com/jon-aisi/terraform-provider-anthropic/actions/workflows/security.yml)
[![CodeQL](https://github.com/jon-aisi/terraform-provider-anthropic/actions/workflows/codeql.yml/badge.svg?branch=aisi%2Fwif-subset)](https://github.com/jon-aisi/terraform-provider-anthropic/actions/workflows/codeql.yml)
[![GoReleaser Check](https://github.com/jon-aisi/terraform-provider-anthropic/actions/workflows/goreleaser-check.yml/badge.svg?branch=aisi%2Fwif-subset)](https://github.com/jon-aisi/terraform-provider-anthropic/actions/workflows/goreleaser-check.yml)

## Building

```bash
go build ./... && go vet ./... && go test ./...
make install        # go install; then point Terraform at it with dev_overrides, see CONTRIBUTING.md
```

Dependencies are vendored; `go build` uses `vendor/` without network access.

## Licence

MPL-2.0, as upstream. See [`LICENSE`](LICENSE). Upstream authors: Ippon
Technologies (Timothée Aufort).
