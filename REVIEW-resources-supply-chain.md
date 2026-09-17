# Review: WIF resources, tests, CI and supply chain

Scope: `aisi/wif-subset` @ `a691c88`. `internal/services/*` and `internal/admin` are byte-identical to upstream v1.43.2 (`git diff f1686a6...HEAD`: 0 insertions there). Locally: build, vet, unit tests pass; `go mod vendor` reproduces `vendor/`.

## Findings

### High

1. **Release never verifies `vendor/`.** `.goreleaser.yml:8` runs `go mod verify`, but on a fresh runner the module cache is empty and it prints "all modules verified" (reproduced: `GOMODCACHE=$(mktemp -d) go mod verify`). The real check, `go mod vendor && git diff` (`provider.yml:53-61`), runs on push/PR only; `goreleaser-release.yml:3-6` fires on any `v*` tag, which can point at any commit. Fix: add the vendor step to the release job; tag ruleset restricting `v*`; require a green `Provider` run on the tagged SHA.

### Medium

2. **Out-of-band archive is invisible.** `federation_rule_resource.go:464-490`, `federation_issuer_resource.go:413-439`, `service_account_resource.go:207-233` keep the resource and store `archived_at` as Computed: `plan` shows no changes for a rule revoked in the Console; the next edit fails 400. Do not copy `workspace_resource.go:268-272` (RemoveResource): Terraform would re-create, so re-grant, a revoked rule. Fix: warning diagnostic in Read when `archived_at` is set.

3. **Rule Update always resends `workspace_id`** (`federation_rule_resource.go:609-611`). API: "Rejected with 400 if the rule is enabled for more than one workspace", so a rule with `anthropic_federation_rule_workspace` enablements cannot be updated at all. Fix: send only on change; verify live; add an httptest on the update body (none exists).

4. **Trust-root changes are in-place.** `federation_issuer_resource.go:133-172`: `issuer_url` and `jwks` change with a `~` line and every rule under the issuer trusts the new IdP/keys at once. RequiresReplace is impractical (archive refused while rules exist; names unique). Fix: `ModifyPlan` warning; document `prevent_destroy` for the bootstrap issuer; review `~ issuer_url|jwks|target|oauth_scope|applies_to_all_workspaces` as access changes.

5. **Rebase can smuggle code past the trim.** `hack/trim-upstream.sh` deletes by allowlist under `internal/services`, docs, examples, tests and two named workflows, and keeps anything else upstream adds: `main.go`; new `internal/<pkg>` (only `retry` removed, line 44); new files inside kept packages (an `init()` runs in the provider); `go.mod` (vendored at next sync); `.goreleaser.yml` `before.hooks` (runs on the release runner with the GPG key); `GNUmakefile`, `mise.toml`, `tools/`; new workflows. FORK.md's checklist omits `main.go`, `tools/`, `GNUmakefile`, `mise.toml`, `hack/`. Fix: fail the script on unexpected entries in `internal/`, the root and `.github/workflows/`; extend the checklist.

6. **Release job in harden-runner `audit`** (`goreleaser-release.yml:23-26`) while holding `GPG_PRIVATE_KEY` and `contents: write`. Fix: switch to `block` from the audit log, or sign in a separate minimal job.

7. **Provider address unchanged** (`main.go`: `registry.terraform.io/ippontech/anthropic`). Without the mirror config a consumer silently installs Ippon's full-surface provider. Fix: change the address, or ship `.terraform.lock.hcl` with the fork's `h1:` hashes and no `direct {}` fallback.

8. **Acceptance tests write to whatever org `ANTHROPIC_AUTH_TOKEN` names.** `acctest.go:42-47` checks presence only; `make testacc` runs all 19 `TestAcc*`. `federation_rule_workspace_resource_test.go:85-115` creates a live `workspace:developer` rule bound to an arbitrary real workspace whose issuer trusts the RFC 7517 A.1 key (`federation_rule_resource_test.go:36-42`; private half is published). Fixed names, no sweepers, two tests hardcode Ippon's workspace (`acctest.go:26`). Fix: require an org-ID env matching a dedicated test org; randomise names; add sweepers.

### Low

9. `applies_to_all_workspaces` false→true leaves the legacy `workspace_id` server-side; Read restores it, config is null, permanent diff (`federation_rule_resource.go:811-819`). Fix: send `workspace_id: null` when cleared.
10. `service_account_workspace` with `create_before_destroy`: Create upserts the role, Delete then removes the membership (`service_account_workspace_resource.go:199-218`). Fix: in-place role Update via Add (documented upsert).
11. Rule Update replaces `match` wholesale (`federation_rule_resource.go:585-593`); unmodelled server-side match fields are dropped. Keep SDK/schema in lockstep.
12. `golangci-lint-action` has no `version:`, no `.golangci.yml`, no binary checksum. Lint-only. Fix: `version: v2.13.2` plus a config.
13. `tools/go.sum` pins `x/text v0.36.0`, `x/crypto v0.45.0`, below the main module's govulncheck bumps; not scanned.

### Info

14. `workspace_ids`, `issuer_name`, `updated_*`, `archived_*` lack `UseStateForUnknown`: plan noise on every update.
15. `attributes = {}` and `match.claims = {}` pass the validators (`federation_rule_resource.go:334-347`) and fail server-side.
16. SA `organization_role` developer→admin is a `~` line; allowed from a user token, refused from a workload token.

## What is sound

Bounds and defaults match the reference: `token_lifetime_seconds` 60–86400/3600, `max_jwt_lifetime_seconds` 1–176400/3600, `check_jti` true, names `^[a-z0-9-]+$` 1–255. Immutability matches: `issuer_id`, SA `name` and both membership keys are RequiresReplace; nothing else is. Destroy archives where the API archives and removes where it removes; rule-before-issuer/SA ordering is enforced by the API (400) and by Terraform's graph. `applies_to_all_workspaces` is Computed+Default(false), so reverting sends an explicit `false`. SA Update sends `organization_role` only on change. `match.condition` and `claims` are opaque pass-through. Import and Read cover every attribute; `jwks.keys` keeps wire bytes. The Remove argument inversion is isolated and path-tested. No Sensitive data in state, no credential logging. Tests cover mapping, param builders, validators, 404 handling, archive paths, pagination and await loops; untested: Update bodies, Read of an archived object. CI: `permissions: {}`, `contents: read`, `persist-credentials: false`, all actions SHA-pinned, `pull_request` not `pull_request_target`, no secrets on PR jobs, SBOM and provenance attested.

## Verdict

Trustworthy for a production org, with one qualification: the resource layer is Ippon's v1.43.2 verbatim and each rebase re-imports whatever Ippon merges, so the trim-and-review step is the control, not the code. The code does what the API documents, every access-widening operation is plan-visible, and the API independently blocks `org:admin` escalation from any bearer. Change first: (1) release-job vendor check and tag protection, since the binary will hold `org:admin`; (2) an org guard on acceptance tests; (3) the archived-drift warning, the `workspace_id` resend, and a `ModifyPlan` warning on issuer trust changes. Then the rebase guard (5) and the provider address (7).
