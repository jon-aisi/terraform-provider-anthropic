---
page_title: "Provider: Anthropic"
description: |-
  Use the Anthropic Terraform provider to interact with Anthropic APIs.
---

# Anthropic Provider

The Anthropic provider is used to interact with [Anthropic](https://anthropic.com) APIs.
This build manages the [Workload Identity Federation](https://platform.claude.com/docs/en/manage-claude/workload-identity-federation) subset of the Admin API: federation issuers, service accounts, federation rules, their workspace bindings, and workspaces.

## Authentication

The federation resources and data sources (`anthropic_federation_*`, `anthropic_service_account*`) call the [Workload Identity Federation admin endpoints](https://platform.claude.com/docs/en/manage-claude/wif-admin-api). Those endpoints accept only an OAuth bearer token carrying the `org:admin` scope. **An Admin API key is not accepted there**, for reads or for writes: a configuration that declares federation resources with only `admin_api_key` fails at plan time with `Missing OAuth Token`.

The provider obtains that bearer in one of two ways. Configure exactly one; when both are present `auth_token` wins and a warning is emitted.

### Workload identity federation (`identity_token_file`, `federation_rule_id`, `organization_id`)

The provider exchanges the workload's own OIDC identity token for an `org:admin` access token itself, with the [RFC 7523 jwt-bearer grant](https://platform.claude.com/docs/en/manage-claude/workload-identity-federation#authenticate-from-your-workload) at `<base URL>/v1/oauth/token`, so CI stores no Anthropic credential at all. The access token is cached and re-exchanged before it expires; `identity_token_file` is re-read on every exchange so a rotated token is picked up.

| Argument | Environment variable | |
|---|---|---|
| `identity_token_file` | `ANTHROPIC_IDENTITY_TOKEN_FILE` | Path to the OIDC JWT. Preferred over `identity_token`. |
| `identity_token` | `ANTHROPIC_IDENTITY_TOKEN` | The JWT inline (Sensitive). A JWT with a single-use `jti` can be exchanged only once, so the first re-exchange fails. |
| `federation_rule_id` | `ANTHROPIC_FEDERATION_RULE_ID` | Required. The `fdrl_...` rule granting `org:admin` (see the guide). |
| `organization_id` | `ANTHROPIC_ORGANIZATION_ID` | Required. The organization UUID. |
| `service_account_id` | `ANTHROPIC_SERVICE_ACCOUNT_ID` | Optional expected-target check (`svac_...`). |
| `workspace_id` | `ANTHROPIC_WORKSPACE_ID` | Only when the rule is enabled for several workspaces (`wrkspc_...` or `default`). |

These are the variables the Anthropic SDKs read for their own federation auto-discovery, so a workload configured for the SDK configures the provider too. None of the IDs is a secret. On GitHub Actions (with `permissions: id-token: write` on the job):

```yaml
- name: Request the GitHub OIDC token
  run: |
    curl -sS -H "Authorization: Bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" \
      "$ACTIONS_ID_TOKEN_REQUEST_URL&audience=https://api.anthropic.com" \
      | jq -r .value > /tmp/anthropic-identity-token

- run: terraform plan
  env:
    ANTHROPIC_IDENTITY_TOKEN_FILE: /tmp/anthropic-identity-token
    ANTHROPIC_FEDERATION_RULE_ID: fdrl_...
    ANTHROPIC_ORGANIZATION_ID: 00000000-0000-0000-0000-000000000000
    ANTHROPIC_SERVICE_ACCOUNT_ID: svac_...
```

Which rule to point at, and why creating it is a one-time Console step, is covered in the [Workload Identity Federation guide](guides/workload_identity_federation).

~> **Note**: GitHub and most OIDC issuers put a single-use `jti` in the token, and the file holds one token. The provider can re-exchange it only if the issuer does not enforce `check_jti`; otherwise the access token must outlive the Terraform run. Set the rule's `token_lifetime_seconds` accordingly, or mint a fresh identity token into the file before each Terraform command.

### Static bearer token (`auth_token` / `ANTHROPIC_AUTH_TOKEN`)

For interactive use. Obtain a token with the `ant` CLI:

```bash
ant auth login --profile admin --scope "org:admin"
export ANTHROPIC_AUTH_TOKEN="$(ant auth print-credentials --profile admin --access-token)"
```

**Provider argument**:

```hcl
provider "anthropic" {
  auth_token = "sk-ant-oat01-..."
}
```

~> **Warning**: These tokens are short-lived. A long `terraform apply` can outlive the token and start failing with `401`. Mint a fresh token immediately before the run; the provider does not refresh a static token.

### Admin API key (`admin_api_key` / `ANTHROPIC_ADMIN_API_KEY`)

Required only for `anthropic_workspace` and the `anthropic_workspace` / `anthropic_workspaces` data sources, which are plain Admin API calls. Generate one in the [Anthropic Console → Admin API Keys](https://platform.claude.com/settings/admin-keys).

```bash
export ANTHROPIC_ADMIN_API_KEY="sk-ant-admin03-..."
```

### Base URL (`base_url` / `ANTHROPIC_BASE_URL`)

Every request, including the federation token exchange, goes to `https://api.anthropic.com`. `base_url` exists so tests can point the provider at a local server; it must be an `https://` origin with no query string or fragment, and both the Admin API client and the SDK client use it. Any other value produces a `Non-default API Destination` warning naming the host, whether it came from the argument or from `ANTHROPIC_BASE_URL`. Do not set it in production configurations.

A path prefix (`https://proxy.example/anthropic`) is applied to API requests but not to the token exchange, which the SDK always posts to `<scheme>://<host>/v1/oauth/token`; the provider warns when federation is configured with such a base URL. Redirects are never followed: a `3xx` from the origin fails the request with the destination named, so no credential or identity token travels to a second host. Every request is bounded by a 60-second timeout, 30 seconds of it for the response headers.

Profiles under `~/.config/anthropic` are ignored: each client is built from the credential resolved above and nothing else, so a profile left active by `ant auth login` can never redirect a request to another base URL or scope it to another workspace behind your back.

~> **Warning**: Never hardcode API keys in your Terraform configuration files.
Use environment variables or a secrets manager instead.

### Logging

The provider logs no credential at any level. Terraform's `TF_LOG_SDK_PROTO_DATA_DIR` is different: it makes the plugin framework write every raw protocol message, the provider configuration with its `Sensitive` values included, to files in that directory. Never set it in CI or anywhere the directory outlives the run.

## Example Usage

```hcl
terraform {
  required_version = ">= 1.0"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

provider "anthropic" {}
```

<!-- schema generated by tfplugindocs -->
## Schema

### Optional

- `admin_api_key` (String, Sensitive) The Anthropic Admin API key. Used only by the `anthropic_workspace` resource and data sources; the Workload Identity Federation endpoints reject it. Can also be set via the ANTHROPIC_ADMIN_API_KEY environment variable.
- `auth_token` (String, Sensitive) An org:admin OAuth bearer token (`sk-ant-oat01-...`) for the Workload Identity Federation admin endpoints. Takes precedence over workload identity federation when both are configured. Can also be set via the ANTHROPIC_AUTH_TOKEN environment variable.
- `base_url` (String) Origin of the Anthropic API, used by every request including the federation token exchange. Defaults to `https://api.anthropic.com`; https only. Override it only to point tests at a local server; any other value is reported as a warning naming the host. Can also be set via the ANTHROPIC_BASE_URL environment variable.
- `federation_rule_id` (String) The federation rule (`fdrl_...`) that governs the exchange. Required with an identity token. Can also be set via the ANTHROPIC_FEDERATION_RULE_ID environment variable.
- `identity_token` (String, Sensitive) An OIDC identity token (JWT) exchanged for an org:admin access token through workload identity federation. Prefer `identity_token_file`: the token is re-exchanged when the access token expires, and a JWT carrying a single-use `jti` is accepted only once. Can also be set via the ANTHROPIC_IDENTITY_TOKEN environment variable.
- `identity_token_file` (String) Path to a file holding the OIDC identity token. Re-read before every exchange, so a rotated token is picked up. Mutually exclusive with `identity_token`. Can also be set via the ANTHROPIC_IDENTITY_TOKEN_FILE environment variable.
- `organization_id` (String) The organization UUID the federation rule belongs to. Required with an identity token. Can also be set via the ANTHROPIC_ORGANIZATION_ID environment variable.
- `service_account_id` (String) The service account (`svac_...`) the rule targets; an expected-target check for rules with `target_type = SERVICE_ACCOUNT`. Can also be set via the ANTHROPIC_SERVICE_ACCOUNT_ID environment variable.
- `workspace_id` (String) The workspace (`wrkspc_...`, or `default`) the minted token is scoped to. Required when the rule is enabled for more than one workspace. Can also be set via the ANTHROPIC_WORKSPACE_ID environment variable.
