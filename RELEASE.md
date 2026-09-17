# Releasing

A release is a signed git tag pushed to the fork. There is no release bot.

```bash
git tag -s v1.43.2-aisi.1 -m "v1.43.2-aisi.1"
git push origin v1.43.2-aisi.1
```

The tag triggers `.github/workflows/goreleaser-release.yml`, which builds the
provider for every platform in `.goreleaser.yml`, writes one SPDX SBOM per
archive, publishes a GitHub release, and attaches a SLSA build provenance
attestation to every artifact. Verify an artifact with:

```bash
gh attestation verify terraform-provider-anthropic_<version>_linux_amd64.zip --repo jon-aisi/terraform-provider-anthropic
```

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
