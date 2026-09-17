# About this fork

This repository is a fork of
[ippontech/terraform-provider-anthropic](https://github.com/ippontech/terraform-provider-anthropic)
reduced to the Workload Identity Federation (WIF) subset of the Anthropic Admin
API, for use by AISI to manage federation configuration from CI without a
stored Anthropic credential. Upstream is MPL-2.0; so is this fork (`LICENSE`).

## Why

- The upstream provider covers the whole Anthropic API surface (inference,
  managed agents, skills, vaults, API keys, members, ...). We need six resources
  and ten data sources, and a reviewable amount of code behind them.
- Upstream authenticates the WIF endpoints with a static `org:admin` bearer
  that CI has to mint in a preceding step. This fork performs the RFC 7523
  jwt-bearer exchange itself from the workload's OIDC token (provider
  attributes `identity_token_file`, `federation_rule_id`, `organization_id`,
  ...), so no token is stored or passed between steps.
- Supply chain: dependencies are vendored and verified, CI is SHA-pinned and
  needs no secrets on pull requests, releases carry an SBOM and a build
  provenance attestation.

`REVIEW.md` maps what remains for a reviewer.

## What was removed

- Service packages: agents, apikeys, environments, messages, models,
  organizations, skills, vaults; the workspace member and workspace rate
  limit resources/data sources inside the kept `workspaces` package; the
  `retry` package (only skills used it). With their docs, templates, examples
  and Terraform native tests.
- The `api_key` provider attribute and the standard-API SDK client: no
  remaining resource uses them.
- Renovate configuration, the semantic-release workflow and config, the
  acceptance-test workflow (needs organisation secrets), and the Claude Code
  agent files (`.claude/`, `CLAUDE.md`).

`hack/trim-upstream.sh` encodes the deletions as an allowlist.

## What was added

- `internal/provider/federation.go`: resolution and validation of the
  federation attributes, and the SDK option that performs the exchange.
- `internal/provider/base_url.go`: one validated `base_url` for both clients.
- Tests for both, `vendor/`, the CI/release changes, `hack/trim-upstream.sh`,
  `FORK.md`, `REVIEW.md`.
- Indirect dependency bumps past `govulncheck` findings (`grpc`, `x/net`,
  `x/text` and their `x/*` cascade; see `REVIEW.md`). Upstream's `go.mod`
  will conflict on these lines at every rebase until upstream catches up:
  keep the higher version, then `go mod tidy && go mod vendor`.

Edits to files shared with upstream are kept to registration lines and the
credential surface: `internal/provider/provider.go`,
`internal/providerdata/providerdata.go`, `internal/errors/{api_key,auth_token}.go`,
`internal/acctest/acctest.go`, the provider docs template and the WIF guide,
`CONTRIBUTING.md`, `RELEASE.md`, `README.md`, `.goreleaser.yml`, and the
workflows.

## Branches

- `main`: mirror of `upstream/main`. Never commit to it.
- `aisi/wif-subset`: the fork. Rebased onto `main` on every sync.

## Syncing with upstream

```bash
git fetch upstream
git checkout main && git rebase upstream/main && git push origin main
git checkout aisi/wif-subset && git rebase main
```

Expect conflicts of two kinds:

1. Modify/delete on files the trim removed (upstream changed a file we
   deleted). Resolve by deleting: `bash hack/trim-upstream.sh` re-applies the
   allowlist and stages the removals, including any service package upstream
   added since. Then `git add -A && git rebase --continue`.
2. Textual conflicts in the shared files listed above, most often the
   registration lists in `internal/provider/provider.go` and the docs
   templates. Keep our side; add nothing that references a removed package.

## Checklist for each sync

- [ ] `git log main@{1}..main` reviewed: read every upstream change to
      `internal/admin`, `internal/provider`, `internal/errors`,
      `internal/services/{federation,serviceaccounts,workspaces}`, `go.mod`,
      `.goreleaser.yml` and `.github/`. New network calls, new dependencies
      and new credential handling go into `REVIEW.md`.
- [ ] `bash hack/trim-upstream.sh` run; `internal/services` holds only
      `federation`, `serviceaccounts`, `workspaces`.
- [ ] `go mod tidy && go mod vendor && go mod verify`; `vendor/` committed, and
      `git ls-files --others --ignored --exclude-standard vendor` prints nothing
      (upstream's `.gitignore` patterns match at any depth).
- [ ] `go build ./... && go vet ./... && go test ./... && golangci-lint run`.
- [ ] `cd tools && go generate ./...` (needs `terraform`); `docs/` committed.
- [ ] `make install .dev.tfrc && TF_CLI_CONFIG_FILE=$PWD/.dev.tfrc terraform -chdir=tests test` for the offline tests (see `provider.yml` for the filter list).
- [ ] `REVIEW.md` LOC table and diff summary refreshed.
- [ ] Push `main` and `aisi/wif-subset` to `origin` only. Never open a pull
      request against upstream from this fork.
