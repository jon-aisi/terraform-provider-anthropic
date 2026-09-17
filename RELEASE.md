# Releasing

A release is a signed git tag pushed to the fork. There is no release bot.

```bash
git tag -s v1.43.2 -m "v1.43.2"
git push origin v1.43.2
```

Tags are plain semver, `vX.Y.Z`. The provider address
(`terraform.aisi.org.uk/aisi/anthropic`, see `docs/guides/install.md`) is
what tells this build apart from upstream's, so the version carries no fork
suffix; a prerelease suffix would also stop `~>` and `>=` constraints from
matching, since Terraform selects a prerelease only by exact version.

The tag triggers `.github/workflows/goreleaser-release.yml`, which re-vendors
and diffs `vendor/` against `go.mod`/`go.sum`, builds and tests the tagged
tree, then builds the provider for every platform in `.goreleaser.yml`,
writes one SPDX SBOM per archive, publishes a GitHub release, and attaches a
SLSA build provenance attestation to every artifact. Verify an artifact with:

```bash
gh attestation verify terraform-provider-anthropic_<version>_linux_amd64.zip --repo jon-aisi/terraform-provider-anthropic
```

## Tag protection (required before the first release from the AISI org repo)

The release workflow runs on any `v*` tag, and a tag can point at any commit,
including one that never went through a pull request or a green `Provider`
run. The workflow's own vendor/build/test step catches a tree that does not
match its lockfile; it does not decide who may cut a release. That control is
a repository ruleset, which must be configured in the AISI organisation repo
(rulesets on a personal fork cannot be created through the API on this plan,
and are not carried across by a fork or transfer):

`Settings > Rules > Rulesets > New ruleset > New tag ruleset`

| Setting | Value |
|---------|-------|
| Enforcement status | Active |
| Target tags | Include by pattern `v*` |
| Bypass list | The release maintainers only (a team, not "repository admins") |
| Restrict creations / updates / deletions | All three enabled |
| Require status checks to pass | `lint`, `test`, `govulncheck` (the `Provider` workflow jobs), `goreleaser-check`, `actionlint`, `poutine` |
| Require signed commits | Enabled |

With this in place a `v*` tag can only be created by a bypass-list member,
only on a commit whose `Provider` and `Security` runs are green, and cannot
be moved after the release is published. Confirm the ruleset exists before
publishing the first release: the provider binary it produces will hold
`org:admin` on every organisation that installs it.

## GPG signing (optional)

The Terraform Registry requires the `*_SHA256SUMS` file to be GPG-signed.
Signing happens only when the `GPG_PRIVATE_KEY` repository secret is set
(`GPG_PASSPHRASE` alongside it if the key has one); without it the release is
built with `--skip=sign` and is installable through a filesystem or network
mirror, not through the public Registry.

Generate a key:

```bash
gpg --full-generate-key   # RSA 4096, no expiry
gpg --list-secret-keys --keyid-format LONG
```

Repository secrets (`Settings > Secrets and variables > Actions`):

| Secret | Value |
|--------|-------|
| `GPG_PRIVATE_KEY` | `gpg --armor --export-secret-keys YOUR_KEY_ID` |
| `GPG_PASSPHRASE` | The key's passphrase |

For the Registry, also add `gpg --armor --export YOUR_KEY_ID` under
`registry.terraform.io > Settings > GPG Keys`.

No workflow that runs on pull requests reads a secret.
