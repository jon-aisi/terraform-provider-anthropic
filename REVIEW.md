# Review map

Branch `aisi/wif-subset`, based on upstream `v1.43.2` (`f1686a6`). Line
numbers are as of this commit; `rg` patterns are given so they can be
re-derived after a rebase. `vendor/` is excluded from every count.

## What remains

Go, excluding `vendor/`: 16,231 lines (7,071 non-test, 9,160 test); 233 test
functions, of which 19 are acceptance tests (`TestAcc*`, run only with
`TF_ACC=1` and live credentials). Upstream `main` had 30,440 lines and 353
test functions.

| Package | Non-test | Test | Role |
|---|---:|---:|---|
| `main.go` | 40 | 0 | Serves the provider at `registry.terraform.io/ippontech/anthropic`. |
| `internal/provider` | 514 | 979 | Schema, credential resolution, client construction. `provider.go` (upstream, edited), `federation.go` and `base_url.go` (fork). |
| `internal/providerdata` | 34 | 0 | Struct handed to every resource: `AdminClient` (Admin API key) and `OAuthClient` (bearer). |
| `internal/admin` | 305 | 782 | Hand-rolled HTTP client for `/v1/organizations/workspaces*` with retries. Used only by `workspaces`. |
| `internal/errors` | 74 | 227 | Nil-client guards that turn a missing credential into a diagnostic. |
| `internal/tfvalue` | 32 | 28 | `""`/zero-time to null helpers. |
| `internal/services/federation` | 3,565 | 4,507 | `anthropic_federation_issuer`, `_rule`, `_rule_workspace` resources; `federation_issuer(s)`, `federation_rule(s)`, `federation_rule_workspaces` data sources. SDK client. |
| `internal/services/serviceaccounts` | 1,311 | 2,113 | `anthropic_service_account`, `_service_account_workspace` resources; `service_account(s)`, `service_account_workspaces` data sources. SDK client. |
| `internal/services/workspaces` | 946 | 524 | `anthropic_workspace` resource; `workspace`, `workspaces` data sources. Admin client. `workspacetest.go` is test scaffolding compiled into the package. |
| `internal/acctest` | 47 | 0 | Acceptance-test provider factory and env pre-checks. `TerraformTestsWorkspaceID` is upstream's own workspace ID (see flags). |
| `internal/admintest` | 23 | 0 | Builds an `admin.Client` against an httptest server. |
| `internal/wifprobetest` | 167 | 0 | Harness for the opt-in read-after-write staleness probe (`TestAccWIFStalenessProbe`, live writes; needs `TF_ACC=1`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_WIF_STALENESS_PROBE=1`). |
| `tools` | 13 | 0 | Separate module: `tfplugindocs` for `go generate`. Not vendored. |

Non-Go: 17 doc pages under `docs/` (generated from `templates/`), 19 example
modules under `examples/`, 17 Terraform native tests under `tests/` (15 offline
via `mock_provider` or plan-only with dummy credentials; the two workspace data
source tests read the live API and are excluded from CI), `hack/trim-upstream.sh`.

## Network calls

Only two files outside tests import `net/http`: `internal/admin/admin_client.go`
and `internal/provider/provider.go` (the `httpClient` test hook). Everything
else goes through the Anthropic Go SDK. All requests target the single origin
resolved by `internal/provider/base_url.go` (`base_url` attribute, else
`ANTHROPIC_BASE_URL`, else `https://api.anthropic.com`; https only, no query,
fragment or user info; trailing slash trimmed).

### Admin client (`internal/admin/admin_client.go`)

- URL: `c.BaseURL + path` (`DoRequest`, line 104). Paths are literals in
  `internal/services/workspaces` plus an encoded query string
  (`workspaces_data_source.go:170`).
- Headers: `x-api-key: <admin_api_key>`, `anthropic-version: 2023-06-01`,
  `content-type: application/json` when there is a body (lines 111-115).
- Transport: `http.Client{Timeout: 60s}`; the provider swaps in its test hook
  client when set.
- Retries: up to 2 with exponential backoff and jitter. Idempotent methods
  retry on connection error, 408, 409, 429, 5xx; POST retries only on 429. A
  server `x-should-retry: true/false` header overrides both rules;
  `retry-after-ms` / `retry-after` are honoured up to 60 s (`shouldRetry`,
  `retryDelay`, `parseRetryAfter`).
- Call sites: `rg -n 'DoRequest\(' internal/services --glob '!*_test.go'`:
  `workspace_resource.go` POST create (223), GET read (252), POST update
  (308), POST archive (337); `workspace_data_source.go` GET (114);
  `workspaces_data_source.go` GET list with pagination (171).

### SDK client, static bearer (`auth_token`)

`option.WithAuthToken` sets `Authorization: Bearer <auth_token>` on every
request. `option.WithoutEnvironmentDefaults()` disables the SDK's own
credential chain (`ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, profiles under
`ANTHROPIC_CONFIG_DIR`, env-var federation): the client carries exactly one
credential (`provider.go` `newSDKClient`; pinned by
`TestConfigureOAuthClientCarriesOnlyTheBearer` and
`TestConfigureIgnoresTheAmbientProfile`).

### SDK client, federation (`identity_token_file` and friends)

`internal/provider/federation.go` `requestOption` returns
`option.WithFederationTokenProvider`, implemented in the vendored SDK at
`vendor/github.com/anthropics/anthropic-sdk-go/internal/auth/{workload,middleware,cache}.go`:

- Exchange: `POST <base_url>/v1/oauth/token`, JSON body `grant_type`
  (`urn:ietf:params:oauth:grant-type:jwt-bearer`), `assertion` (the identity
  token), `federation_rule_id`, `organization_id`, optional
  `service_account_id`, `workspace_id`. Headers `Content-Type:
  application/json`, `User-Agent: anthropic-sdk-go/<version>
  (oidc-federation)`, `anthropic-beta: oauth-2025-04-20,oidc-federation-2026-04-01`.
  No credential header. The identity token is read by the provider function
  on every exchange (`option.IdentityTokenFile` re-reads the file; an inline
  `identity_token` is returned unchanged). Assertions over 16 KiB and
  responses over 1 MiB are rejected. The SDK refuses non-https token
  endpoints except loopback; the provider refuses all non-https before that.
- Response: `access_token`, optional `token_type` (must be Bearer),
  `expires_in`. Cached in memory per client; refreshed in the background when
  under 120 s remain (5 s backoff between failed attempts), synchronously
  under 30 s or when expired.
- API requests: `Authorization: Bearer <access_token>` and
  `anthropic-beta: oauth-2025-04-20` appended. Only for requests to the
  client's base URL origin. On a 401 the cache is invalidated and the request
  re-exchanged and replayed once when the body is replayable.
- Tests: `internal/provider/federation_test.go` drives the whole path against
  an httptest TLS server (`TestFederationExchangesIdentityTokenForBearer`,
  `TestFederationRereadsTheTokenFileOnEveryExchange`, ...).

### SDK call sites (`rg -n 'client\.Beta\.Organization' internal/services --glob '!*_test.go'`)

All under `/v1/organizations/...` via the SDK's typed beta services; the SDK
adds `anthropic-version` and the beta header each endpoint needs.

| File | Calls |
|---|---|
| `federation/federation_issuer_resource.go` | `Federation.Issuers.New` (397), `Get` (420), `Update` (485), `Archive` (511) |
| `federation/federation_issuer_data_source.go` | `Issuers.Get` (218) |
| `federation/federation_issuers_data_source.go` | `Issuers.ListAutoPaging` (243) |
| `federation/federation_rule_resource.go` | `Rules.New` (448), `Get` (471, 530), `Update` (643), `Archive` (670) |
| `federation/federation_rule_data_source.go` | `Rules.Get` (237) |
| `federation/federation_rules_data_source.go` | `Rules.ListAutoPaging` (275) |
| `federation/federation_rule_workspace_resource.go` | `Rules.Workspaces.Add` (148), `Remove` (236), `ListAutoPaging` (310) |
| `federation/federation_rule_workspaces_data_source.go` | `Rules.Workspaces.ListAutoPaging` (135) |
| `serviceaccounts/service_account_resource.go` | `ServiceAccounts.New` (191), `Get` (214, 271), `Update` (316), `Archive` (341) |
| `serviceaccounts/service_account_data_source.go` | `ServiceAccounts.Get` (130) |
| `serviceaccounts/service_accounts_data_source.go` | `ServiceAccounts.ListAutoPaging` (161) |
| `serviceaccounts/service_account_workspace_resource.go` | `ServiceAccounts.Workspaces.Add` (248), `Remove` (257), `ListAutoPaging` (276) |
| `serviceaccounts/service_account_workspaces_data_source.go` | `ServiceAccounts.Workspaces.ListAutoPaging` (126) |

The second `Get` in `federation_rule_resource.go` (530) and
`service_account_resource.go` (271), and the list in
`federation_rule_workspace_resource.go` (310), are read-after-write
consistency waits: bounded polling with `time.After(interval)` (lines 555,
296, 299) that logs a warning and gives up at the timeout.

### Anything else

- `internal/wifprobetest/probe.go:158` builds an SDK client from
  `ANTHROPIC_AUTH_TOKEN` directly. Test harness only; not reachable from the
  provider binary.
- Tests use `httptest.NewTLSServer` throughout; no test contacts the network
  except the `TestAcc*` functions.
- The SDK's `warnOnce` (`internal/auth/logging.go`) writes to the standard
  `log` package (provider stderr) once per category: an unreplayable 401
  retry, or a failed refresh including the `OAuthTokenError` text.

## Credentials

### Read

| What | Where | How |
|---|---|---|
| `admin_api_key`, `auth_token` | `provider.go` `Configure` (139-140) | `req.Config.Get`, else `os.Getenv(ANTHROPIC_ADMIN_API_KEY / ANTHROPIC_AUTH_TOKEN)`. Unknown config values count as unset. |
| `identity_token`, `identity_token_file`, `federation_rule_id`, `organization_id`, `service_account_id`, `workspace_id` | `federation.go` `resolveFederation` | Same rule, env names `ANTHROPIC_IDENTITY_TOKEN[_FILE]`, `ANTHROPIC_FEDERATION_RULE_ID`, `ANTHROPIC_ORGANIZATION_ID`, `ANTHROPIC_SERVICE_ACCOUNT_ID`, `ANTHROPIC_WORKSPACE_ID`. `validate` does `os.Stat` on the file. |
| Identity token contents | SDK `auth.IdentityTokenFile.GetIdentityToken` | `os.ReadFile` on every exchange, trimmed, empty is an error. |
| `base_url` | `base_url.go` | Config, else `ANTHROPIC_BASE_URL`. Not a credential, but the destination of every credential. |

Precedence: `auth_token` over federation (warning when both are set);
federation over nothing. `admin_api_key` is independent and never substitutes
for the bearer (`TestConfigureAdminKeyAloneBuildsOnlyTheAdminClient`).
Partial federation configuration is an error, not a fall-through
(`TestFederationRejectsPartialConfiguration`).

### Stored

In memory only, for the life of the provider process: `admin.Client.ApiKey`
(string field), the SDK client's `Authorization` header option, the SDK
`TokenCache` (access token and expiry), the SDK's identity-token provider
closure (file path, or the inline token). Nothing is written to disk.

### Logged

The provider never logs a credential. `rg -n 'tflog\.' internal --glob '!*_test.go'`
gives five `tflog.Warn` calls (`federation_rule_resource.go:537,545`,
`federation_rule_workspace_resource.go:168`, `service_account_resource.go:278,286`)
whose fields are object IDs, a timeout and `err.Error()`. The SDK `warnOnce`
lines above carry error text, never a token. Not verified: what
`TF_LOG=TRACE` makes the framework and go-plugin log about the provider
configuration; Terraform redacts Sensitive values in plan output, not in trace
logs.

### Written to state

Provider configuration is never in state. No resource or data source
attribute is marked Sensitive because none holds secret material: the issuer
`jwks` block carries public keys (`jwks.keys`, `jwks.url`,
`jwks.discovery_base`), rules carry claim matchers and IDs, service accounts
carry names and roles, workspaces carry names and data-residency settings. No
resource creates an API key.

### Sensitive attributes

`admin_api_key`, `auth_token`, `identity_token`: Sensitive. `identity_token_file`
(a path), the four IDs and `base_url`: not Sensitive. Pinned by
`TestSchemaMarksCredentialsSensitive`, which also fails when an attribute is
added without a decision.

## Errors

- `Configure` returns diagnostics: `Missing Credentials`, `Invalid Workload
  Identity Federation Configuration` (attributed to the attribute when it came
  from config, naming the env var otherwise), `Invalid Base URL`, and the
  `Workload Identity Federation Settings Ignored` warning.
- Each resource and data source `Configure` runs a guard from
  `internal/errors`: `Missing OAuth Token` (names `auth_token`, federation and
  says an Admin API key is not accepted) or `Missing Admin API Key`. This is
  how a configuration with only `admin_api_key` and federation resources
  fails, at plan time, before any request.
- API failures become `resp.Diagnostics.AddError(<summary>, err.Error())`.
  For the SDK, `anthropic.Error.Error()` is `METHOD "URL": STATUS text
  (Request-ID: ...) <raw JSON response body>`; for the admin client,
  `APIError` is `API error (STATUS type): message`, where `message` is the
  API's `error.message`, or the raw body when it is not JSON. Response bodies
  therefore reach Terraform output and CI logs. Federation exchange failures
  surface as `failed to get credentials token: oauth token request failed
  (status N); request id ...; <error>: <error_description>` with the body
  redacted by the SDK, plus a hint on 401.
- A 404 on read removes the object from state (six sites:
  `rg -n 'StatusCode == 404|IsNotFound\(' internal/services --glob '!*_test.go'`).

## Dependencies (`go.mod`, direct)

| Module | Version | Why |
|---|---|---|
| `github.com/anthropics/anthropic-sdk-go` | v1.67.0 | Typed clients for the WIF beta endpoints (`Beta.Organization.Federation.*`, `.ServiceAccounts.*`) and the federation token exchange. 44 files. |
| `github.com/hashicorp/terraform-plugin-framework` | v1.19.0 | Provider framework. |
| `github.com/hashicorp/terraform-plugin-framework-jsontypes` | v0.2.0 | `jsontypes.Normalized` for JSON-valued attributes (`jwks.keys`, rule claim matchers). 5 files. |
| `github.com/hashicorp/terraform-plugin-framework-validators` | v0.19.0 | String and int64 validators in schemas. 4 files. |
| `github.com/hashicorp/terraform-plugin-go` | v0.31.0 | `tfprotov6` for the acceptance-test provider factory; `tftypes` to build raw config in provider unit tests. 8 files. |
| `github.com/hashicorp/terraform-plugin-log` | v0.10.0 | `tflog.Warn` at the five sites above. 3 files. |
| `github.com/hashicorp/terraform-plugin-testing` | v1.16.0 | Acceptance tests only (`TestAcc*`), but a direct requirement, so vendored with its transitive tree (`hc-install`, `terraform-exec`, `terraform-json`, `terraform-plugin-sdk/v2`). |

Indirect requirements are listed in `go.mod`; the framework transport brings
`go-plugin`, `grpc`, `protobuf`, `yamux`. `go mod verify` is clean and CI
re-vendors and diffs on every run. The `tools` module
(`terraform-plugin-docs` v0.25.0) is not vendored; the docs job downloads it
through `proxy.golang.org`, verified against `tools/go.sum`.

## Diff vs upstream

`git diff --stat upstream/main...HEAD -- . ':!vendor'`:
282 files changed, 1,574 insertions, 23,328 deletions. Of the insertions,
`vendor/` aside: `internal/provider/{federation,base_url}.go` and their tests
(~800 lines), the CI and release changes (~250), `hack/trim-upstream.sh`,
this file, `FORK.md`, and the docs regenerated from the templates.

Commits, in order: trim; federation auth (and `api_key` removal); `base_url`;
vendoring and CI; review docs.

## Flags for the human review

Things in upstream, the SDK or this fork worth a deliberate look. None is a
known defect.

1. **Response bodies in diagnostics.** Both error types echo the API response
   body (see Errors). Harmless for Anthropic's error envelopes; anything
   sitting at `base_url` could put arbitrary text into Terraform output.
2. **Server-driven POST replay.** `admin.shouldRetry` replays a create when
   the server answers `x-should-retry: true`, whatever the status. A hostile
   or buggy endpoint could induce duplicate workspaces. Only the admin client;
   the SDK has the same rule for its own calls.
3. **SDK credential chain.** `anthropic.NewClient` would otherwise read
   `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, profiles under
   `~/.config/anthropic` and the federation env vars, and a profile can set a
   base URL and a workspace header. Disabled with
   `option.WithoutEnvironmentDefaults()` and pinned by tests; any new client
   construction must keep it.
4. **`identity_token` inline and single-use `jti`.** Documented limitation:
   the provider can re-read a file but cannot fetch a new identity token, so
   with `check_jti` on the issuer a re-exchange fails once the access token
   expires. Set the bootstrap rule's `token_lifetime_seconds` to cover the
   run.
5. **Provider address unchanged.** `main.go` serves
   `registry.terraform.io/ippontech/anthropic` and examples pin that source.
   Publishing under another namespace or mirror needs that string changed,
   together with `examples/**/versions.tf` and `tests/versions.tf`.
6. **Upstream workspace ID.** `acctest.TerraformTestsWorkspaceID` is Ippon's
   test workspace; the two live workspace data source tests reference it and
   will fail against another organisation.
7. **Consistency waits.** Three bounded polling loops after writes (Network
   calls). Check the timeouts and intervals are acceptable for CI
   (`rg -n 'interval|timeout' internal/services/*/*_resource.go`).
8. **Third-party actions.** All SHA-pinned; creators outside GitHub/HashiCorp:
   `step-security/harden-runner` (egress policy; reports to step-security),
   `crazy-max/ghaction-import-gpg` (holds the GPG key on release),
   `golangci/golangci-lint-action`, `golang/govulncheck-action`,
   `rhysd/actionlint`, `semgrep/semgrep-action` (contacts
   `fail-open.prod.semgrep.dev`), `bridgecrewio/checkov-action` (pypi,
   bridgecrew.cloud), `anchore/sbom-action`, `goreleaser/goreleaser-action`.
   `hashicorp/setup-terraform` reaches `checkpoint-api.hashicorp.com`.
   The CodeQL and release jobs run harden-runner in `audit` rather than
   `block` mode.
9. **`terraform-plugin-testing` in the production dependency graph.** A
   direct requirement because Go has no test-only scope, so its transitive
   tree is vendored and scanned even though only `TestAcc*` uses it. The
   largest vendored modules are otherwise `golang.org/x/sys` (8.9 MB), the
   framework (5.5 MB) and the SDK (4.3 MB); `vendor/` is 42 MB in total.
10. **Beta headers.** The WIF endpoints and the token exchange are behind
    `anthropic-beta` values set by the SDK
    (`oauth-2025-04-20`, `oidc-federation-2026-04-01`); a server-side change
    to those would surface as 4xx errors, not silently.
11. **Trace logging not verified.** See Credentials / Logged.
