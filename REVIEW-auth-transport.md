# Auth and transport review: `aisi/wif-subset` at `a691c88`

Scope: provider configuration to the network. Every claim was read in code. `go test ./internal/provider/ ./internal/admin/ ./internal/errors/` passes (vendored).

## Findings

**1. Medium. Redirects are followed with credentials and the assertion body.**
`internal/admin/admin_client.go:89` (`http.Client{Timeout: 60s}`) and the SDK client (`requestconfig.go:176`, `http.DefaultClient`) set no `CheckRedirect`. Go follows up to 10 redirects, https to http included, and on a cross-host hop strips only `Authorization`, `Cookie`, `Proxy-*` (stdlib `client.go` ~823) and keeps them for subdomains of the base host; `x-api-key` and `anthropic-beta` are copied to any host, and a 307/308 replays the body. The admin client sets `GetBody` for exactly that (`admin_client.go:146-151`); the token exchange (`workload.go:92`) gets it automatically, so the OIDC `assertion` follows a redirect anywhere. Precondition: a 3xx from the origin or something in front of it; unlikely for `api.anthropic.com`, less so for a non-default `base_url`. Fix: `CheckRedirect: func(...) error { return http.ErrUseLastResponse }` on both clients, passed via `option.WithHTTPClient` before the credential option (`provider.go:219-221`).

**2. Medium. The SDK retries non-idempotent creates.**
`requestconfig.go:248-273`: every method retries on connection error, 408, 409, 429, 5xx, or `x-should-retry: true`; `MaxRetries` is 2 (`:173`; `provider.go:223-227` leaves it). `Issuers.New`, `Rules.New`, `ServiceAccounts.New`, `Workspaces.Add` are POSTs. A 5xx or dropped connection after the write commits produces an untracked second issuer, rule or service account; an untracked federation issuer is an untracked trust anchor into the organisation. The admin client limits POST retry to 429 on purpose (`admin_client.go:211-233`); the SDK path, which owns every WIF object, does not. Fix: `option.WithMaxRetries(0)` on the SDK client and retry only reads in the provider, or list by name before create.

**3. Medium. Single-use `jti` fails before expiry does.**
Each `Configure` builds a fresh client and `TokenCache` (`provider.go:174,180`; `middleware.go:130-147`), so every graph walk (plan, then apply) re-exchanges whatever the file holds (`federation.go:170-177`). With `check_jti` on the issuer and a token file written once per job, the second exchange fails. Mid-apply behaviour: over 120 s left, cached; 30-120 s, background re-exchange with the stale token served (`cache.go:112-129`); under 30 s, synchronous re-exchange; a failure becomes the resource error before any request is sent (`middleware.go:47-49`), so auth failure cannot half-apply a write. A 401 gets one forced re-exchange and one replay (`middleware.go:58-95`). Fix: document `check_jti = false` for the bootstrap issuer and a rule `token_lifetime_seconds` longer than the run, or refresh the file between walks.

**4. Low. `WithoutEnvironmentDefaults` drops the SDK timeout.**
`client.go:211-212` replaces `DefaultClientOptions`, whose transport carries a 10-minute `ResponseHeaderTimeout` (`default_http_client.go:14-27`), with the base URL alone. The SDK client is therefore `http.DefaultClient`: no `Timeout`, no header timeout, no `RequestTimeout`. A stalled connection blocks the apply until interrupted; a background refresh (`cache.go:211`) never returns. Fix: the same `WithHTTPClient` as finding 1, with `Timeout` set.

**5. Low. Destination set by environment alone, silently.**
`base_url.go:30`: `ANTHROPIC_BASE_URL` sends every credential to any https host with nothing in the configuration and no diagnostic. Whoever controls the job environment already holds the identity token; the marginal exposure is the static `auth_token` and `admin_api_key`. Fix: `AddWarning` naming the host whenever the resolved base URL is not the default.

**6. Low. Raw response bodies reach diagnostics.**
`apierror.go:73` appends the raw body; `admin_client.go:190-193` falls back to it when the body is not an error envelope. Headers never appear, nothing calls `DumpRequest`, and the exchange error is filtered to `error`/`error_description` (`error.go:54-63`, `oautherr.go:26-46`). Matters only with a non-Anthropic origin. Fix: truncate in `newAPIError` and wrap SDK errors the same way.

**7. Info. Test scaffolding in the release binary.**
`internal/services/workspaces/workspacetest.go` imports `testing`, `net/http/httptest` and `internal/admintest` from a non-`_test.go` file; `go list -deps .` confirms they link. Rename it. Also linked, benign: `os/exec` (go-plugin, every provider), `embed` (protobuf), `standard-webhooks`, `jsonschema`, `yaml` (SDK, unused). `terraform-plugin-testing`, `hc-install` and `go-checkpoint` are not linked.

**8. Info.** `middleware.go:45` exchanges at `scheme://host/v1/oauth/token`, dropping any path prefix in `base_url`. `federation.go:156` says "not readable" but `os.Stat` checks existence only. `TF_LOG_SDK_PROTO_DATA_DIR` (tf6server `server.go:604`) writes raw config including Sensitive values to disk; never set it in CI.

## What is sound

- One origin: `base_url`, else `ANTHROPIC_BASE_URL`, else `https://api.anthropic.com` (`base_url.go:27-61`); https only, no query, fragment or userinfo; applied to both clients (`provider.go:159,223`); seven rejection cases pinned. The only other reachable host is an `HTTPS_PROXY` if set (CONNECT, SNI only).
- `WithoutEnvironmentDefaults` honoured at `client.go:211`: skips `ANTHROPIC_API_KEY`, `AUTH_TOKEN`, `PROFILE`, profile files, env federation, `ANTHROPIC_BASE_URL` and `ANTHROPIC_CUSTOM_HEADERS` (header injection from env). No other env read on the request path.
- Per-exchange file re-read is implemented: `option.IdentityTokenFile` calls `identity.go:20` `os.ReadFile` on every exchange; `TestFederationRereadsTheTokenFileOnEveryExchange` drives it.
- The exchange carries no credential header, bounds the assertion at 16 KiB and the response at 1 MiB, and accepts only `Bearer` (`workload.go:66-156`); the bearer is attached only to same-origin requests (`middleware.go:42`).
- Credentials are read once (`provider.go:139-141`, `federation.go:66-82`), held in memory only, never logged (the five `tflog.Warn` sites carry IDs and error text) and never in state. `admin_api_key`, `auth_token`, `identity_token` are Sensitive, pinned with an exhaustiveness check (`provider_test.go:313`). No resource attribute holds secret material.
- No `replace` directives, no git dependencies, `go mod verify` clean, TLS at Go defaults.

## Verdict

Before running against a production organisation: (1) refuse redirects and set timeouts on both clients through one production `*http.Client`; (2) stop the SDK retrying POST creates; (3) settle the `jti` question with `check_jti = false` on the bootstrap issuer or a token refresh between walks, and document it. Findings 5-7 belong in the same pass. Nothing found sends the credential anywhere but the configured origin under normal operation.
