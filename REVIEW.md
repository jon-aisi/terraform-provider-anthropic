# Review map

Branch `aisi/wif-subset`, based on upstream `v1.43.2` (`f1686a6`). Line
numbers are as of this commit; `rg` patterns are given so they can be
re-derived after a rebase. `vendor/` is excluded from every count.

## What remains

Go, excluding `vendor/`: 17,070 lines (7,264 non-test, 9,806 test); 245 test
functions, of which 19 are acceptance tests (`TestAcc*`, run only with
`TF_ACC=1` and live credentials). Upstream `main` had 30,440 lines and 353
test functions.

| Package | Non-test | Test | Role |
|---|---:|---:|---|
| `main.go` | 40 | 0 | Serves the provider at `registry.terraform.io/ippontech/anthropic`. |
| `internal/provider` | 643 | 1,450 | Schema, credential resolution, client construction. `provider.go` (upstream, edited); `federation.go`, `base_url.go` and `httpclient.go` (fork). |
| `internal/providerdata` | 34 | 0 | Struct handed to every resource: `AdminClient` (Admin API key) and `OAuthClient` (bearer). |
| `internal/admin` | 338 | 861 | Hand-rolled HTTP client for `/v1/organizations/workspaces*` with retries. Used only by `workspaces`. |
| `internal/errors` | 105 | 323 | Nil-client guards that turn a missing credential into a diagnostic; `Detail` caps the response body an SDK error prints. |
| `internal/tfvalue` | 32 | 28 | `""`/zero-time to null helpers. |
| `internal/services/federation` | 3,565 | 4,507 | `anthropic_federation_issuer`, `_rule`, `_rule_workspace` resources; `federation_issuer(s)`, `federation_rule(s)`, `federation_rule_workspaces` data sources. SDK client. |
| `internal/services/serviceaccounts` | 1,311 | 2,113 | `anthropic_service_account`, `_service_account_workspace` resources; `service_account(s)`, `service_account_workspaces` data sources. SDK client. |
| `internal/services/workspaces` | 946 | 524 | `anthropic_workspace` resource; `workspace`, `workspaces` data sources. Admin client. `workspacetest.go` is test scaffolding compiled into the package (auth/transport review finding 7); it moves to a `_test.go` file on the resources branch, `aisi/fix-resources`, not here. |
| `internal/acctest` | 47 | 0 | Acceptance-test provider factory and env pre-checks. `TerraformTestsWorkspaceID` is upstream's own workspace ID (see flags). |
| `internal/admintest` | 23 | 0 | Builds an `admin.Client` against an httptest server. |
| `internal/wifprobetest` | 167 | 0 | Harness for the opt-in read-after-write staleness probe (`TestAccWIFStalenessProbe`, live writes; needs `TF_ACC=1`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_WIF_STALENESS_PROBE=1`). |
| `tools` | 13 | 0 | Separate module: `tfplugindocs` for `go generate`. Not vendored. |

Non-Go: 17 doc pages under `docs/` (generated from `templates/`), 19 example
modules under `examples/`, 17 Terraform native tests under `tests/` (15 offline
via `mock_provider` or plan-only with dummy credentials; the two workspace data
source tests read the live API and are excluded from CI), `hack/trim-upstream.sh`.

## Network calls

Outside tests, `net/http` is imported by `internal/admin/admin_client.go`,
`internal/provider/httpclient.go` (the one client every request leaves
through) and `internal/provider/provider.go` (which hands it to both API
clients). Everything else goes through the Anthropic Go SDK. All requests
target the single origin resolved by `internal/provider/base_url.go`
(`base_url` attribute, else `ANTHROPIC_BASE_URL`, else
`https://api.anthropic.com`; https only, no query, fragment or user info;
trailing slash trimmed). Any other origin is announced by the `Non-default API
Destination` warning, naming the host and whether the argument or the
environment set it.

### HTTP client (`internal/provider/httpclient.go`)

`newHTTPClient` builds the `*http.Client` the admin client, the SDK client and
the SDK's federation token exchange all use:

- `http.DefaultTransport` cloned (proxy from the environment, dial timeouts,
  HTTP/2), with `ResponseHeaderTimeout` 30 s and TLS 1.2 minimum;
  `http.Client.Timeout` 60 s.
- `CheckRedirect` returns `http.ErrUseLastResponse`: no redirect is followed.
  net/http would otherwise follow up to ten, https to http included, forward
  `x-api-key` and `anthropic-beta` to any host (only `Authorization`, `Cookie`
  and `Proxy-*` are dropped on a cross-host hop, and kept for a subdomain) and
  replay the body on a 307/308, which for the exchange is the identity token.
  The 3xx surfaces as an error naming the destination: `admin.APIError`
  `refused to follow the redirect to "<Location>"` (not retried), the SDK
  middleware `refuseRedirect` (`METHOD "URL": refused to follow the N redirect
  to "<Location>"`), or an `OAuthTokenError` with the 3xx status from the
  exchange.
- Tests: `httpClientSettings` lets a test trust the httptest certificate and
  shorten the timeouts, so the production client is what runs;
  `TestHTTPClientRefusesRedirects` sends a 307 to a second TLS server through
  all three paths and requires it to see nothing
  (`TestStockClientFollowsTheRedirect` is the control),
  `TestHTTPClientTimesOutOnAStalledServer` covers both clients stalling before
  and after the status line, `TestNewHTTPClientProductionSettings` pins the
  values.

### Admin client (`internal/admin/admin_client.go`)

- URL: `c.BaseURL + path` (`DoRequest`, line 119). Paths are literals in
  `internal/services/workspaces` plus an encoded query string
  (`workspaces_data_source.go:170`).
- Headers: `x-api-key: <admin_api_key>`, `anthropic-version: 2023-06-01`,
  `content-type: application/json` when there is a body (lines 135-138).
- Transport: the shared client above, passed to `admin.NewClient`; the
  package has no default of its own.
- Retries: up to 2 with exponential backoff and jitter. Idempotent methods
  retry on connection error, 408, 409, 429, 5xx; POST retries only on 429; a
  3xx is never retried. A
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

The same `newSDKClient` sets `option.WithHTTPClient` to the shared client,
`option.WithMiddleware(refuseRedirect)` and `option.WithMaxRetries(0)`. The
SDK would otherwise retry any request, POST included, on a connection error,
408, 409, 429 or 5xx; every WIF create is a POST, and a replay after a
committed write leaves an untracked issuer, rule or service account behind.
One attempt per request, pinned by `TestSDKClientDoesNotRetry`.

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
  The exchange runs through the shared HTTP client (`option.WithHTTPClient`
  precedes the federation option), so a 3xx at the token endpoint fails the
  exchange rather than replaying the assertion elsewhere. The endpoint is
  always `scheme://host/v1/oauth/token`: a path in `base_url` is dropped by
  the SDK and `option.FederationOptions` has no field for it, so Configure
  warns (`Base URL Path Ignored By The Token Exchange`).
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
| `admin_api_key`, `auth_token` | `provider.go` `Configure` (140-141) | `req.Config.Get`, else `os.Getenv(ANTHROPIC_ADMIN_API_KEY / ANTHROPIC_AUTH_TOKEN)`. Unknown config values count as unset. |
| `identity_token`, `identity_token_file`, `federation_rule_id`, `organization_id`, `service_account_id`, `workspace_id` | `federation.go` `resolveFederation` | Same rule, env names `ANTHROPIC_IDENTITY_TOKEN[_FILE]`, `ANTHROPIC_FEDERATION_RULE_ID`, `ANTHROPIC_ORGANIZATION_ID`, `ANTHROPIC_SERVICE_ACCOUNT_ID`, `ANTHROPIC_WORKSPACE_ID`. `validate` reads the file and rejects a read error or an empty file. |
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
  from config, naming the env var otherwise), `Invalid Base URL`, and three
  warnings: `Workload Identity Federation Settings Ignored`, `Non-default API
  Destination` (any base URL but the production one, naming the host and
  whether the argument or `ANTHROPIC_BASE_URL` set it) and `Base URL Path
  Ignored By The Token Exchange` (federation with a path in the base URL).
- Each resource and data source `Configure` runs a guard from
  `internal/errors`: `Missing OAuth Token` (names `auth_token`, federation and
  says an Admin API key is not accepted) or `Missing Admin API Key`. This is
  how a configuration with only `admin_api_key` and federation resources
  fails, at plan time, before any request.
- API failures become `resp.Diagnostics.AddError(<summary>, "...: " + err)`
  in the services. For the SDK, `anthropic.Error.Error()` is `METHOD "URL":
  STATUS text (Request-ID: ...) <raw JSON response body>`; `errors.Detail(err)`
  returns the same text with the body capped at 512 bytes
  (`admin.MaxErrorBodyBytes`, `admin.TruncateBody`) and is what those sites
  should pass instead of `err`; adopting it changes `internal/services` and
  belongs to the resources branch. For the admin client, `APIError` is
  `API error (STATUS type): message`, where `message` is the API's
  `error.message`, or the raw body when it is not JSON, either capped at 512
  bytes when the error is built; a 3xx reads `refused to follow the redirect
  to "<Location>"`. Until the services call `errors.Detail`, an SDK error's
  body still reaches Terraform output whole. Federation exchange failures
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
`go-plugin`, `grpc`, `protobuf`, `yamux`. Three of them are pinned above
upstream because `govulncheck` flagged upstream's versions as reachable from
this code (GO-2026-6443, -6348, -6061 in `grpc`; GO-2026-5970 in `x/text`;
GO-2026-5026 in `x/net`): `google.golang.org/grpc` v1.79.3 -> v1.83.2,
`golang.org/x/net` v0.52.0 -> v0.56.0, `golang.org/x/text` v0.36.0 -> v0.41.0,
with `x/crypto`, `x/mod`, `x/sync`, `x/sys`, `x/tools` and `genproto/googleapis/rpc`
following. `govulncheck ./...` reports no vulnerabilities at this commit; the
CI job fails on any new one. `go mod verify` is clean and CI re-vendors and
diffs on every run. The `tools` module
(`terraform-plugin-docs` v0.25.0) is not vendored; the docs job downloads it
through `proxy.golang.org`, verified against `tools/go.sum`. `govulncheck`
cannot analyse it (its only file is build-tagged and imports a `main`
package), so its dependency tree is not vulnerability-scanned in CI.

## Diff vs upstream

`git diff --stat upstream/main...HEAD -- . ':!vendor'`:
289 files changed, 2,051 insertions, 23,382 deletions. The insertions are
`internal/provider/{federation,base_url}.go` and their tests (~800 lines),
the CI and release changes (~300), `hack/trim-upstream.sh`, this file,
`FORK.md`, `go.sum` for the bumped modules, and the docs regenerated from the
templates.

Commits, in order: trim; federation auth (and `api_key` removal); `base_url`;
vendoring and CI; review docs; vendored files the inherited `.gitignore`
dropped; dependency bumps past govulncheck findings; then the transport fixes
listed under Findings addressed.

## Flags for the human review

Things in upstream, the SDK or this fork worth a deliberate look. None is a
known defect.

1. **Response bodies in diagnostics.** Capped at 512 bytes in the admin
   client's `APIError`; `errors.Detail` applies the same cap to SDK errors
   but the services still pass `err` directly (see Errors), so that half
   lands with the resources branch.
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
4. **Single-use `jti`.** Every Terraform command is a new provider process
   and a new exchange, so with `check_jti` on the issuer `plan` then `apply`
   on one token file fails at the apply. `docs/index.md` says so, shows the
   CI job refreshing the token before each command (the recommendation) and
   names `check_jti = false` on the bootstrap issuer as the alternative. An
   inline `identity_token` has no refresh path at all.
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

## Findings addressed

From the auth and transport review of `a691c88` (findings by its numbering),
on branch `aisi/fix-transport`:

| Finding | Commit | Change |
|---|---|---|
| 1 redirects followed, 4 no SDK timeout | `bc342f5` | One `http.Client` for the admin client, the SDK and the token exchange: redirects refused, 60 s timeout, 30 s response-header timeout, TLS 1.2 minimum. |
| 2 retried creates | `d211322` | `option.WithMaxRetries(0)` on the SDK client. |
| 3 `jti` re-exchange | `eb84e05` | Documented; the CI example refreshes the token before each Terraform command. |
| 5 destination set silently | `3332ab5` | `Non-default API Destination` warning. |
| 6 raw bodies in diagnostics | `c7fb7f2` | 512-byte cap in `admin.APIError`; `errors.Detail` for SDK errors, for the services to adopt. |
| 7 test scaffolding in the binary | none here | `workspacetest.go` moves on `aisi/fix-resources`. |
| 8 info items | `0ab8cec` | Token file read at Configure; warning for a base URL path with federation; `TF_LOG_SDK_PROTO_DATA_DIR` note. |
