#!/usr/bin/env bash
# Deletes everything outside the Workload Identity Federation subset.
#
# Allowlist-driven so that a service package, doc page, example or test that
# upstream adds later is removed on the next sync without editing this file.
# Run from the repository root after rebasing onto upstream/main, then review
# `git status` and rebuild. See FORK.md.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

keep_services=(federation serviceaccounts workspaces)
keep_resources=(
  federation_issuer federation_rule federation_rule_workspace
  service_account service_account_workspace workspace
)
keep_data_sources=(
  federation_issuer federation_issuers federation_rule federation_rules
  federation_rule_workspaces service_account service_accounts
  service_account_workspaces workspace workspaces
)
keep_guides=(workload_identity_federation)

in_list() {
  local needle=$1; shift
  local x
  for x in "$@"; do [[ "$x" == "$needle" ]] && return 0; done
  return 1
}

# Service packages.
for dir in internal/services/*/; do
  name=$(basename "$dir")
  in_list "$name" "${keep_services[@]}" || git rm -rq "$dir"
done

# Non-WIF files inside the kept workspaces package.
git rm -q --ignore-unmatch \
  internal/services/workspaces/workspace_member*.go \
  internal/services/workspaces/workspace_members_data_source*.go \
  internal/services/workspaces/workspace_rate_limits_data_source*.go

# Shared packages only the removed services used.
git rm -rq --ignore-unmatch internal/retry

# Docs, templates and examples, by resource / data source name.
for kind in resources data-sources; do
  if [[ $kind == resources ]]; then keep=("${keep_resources[@]}"); else keep=("${keep_data_sources[@]}"); fi
  for f in docs/$kind/*.md templates/$kind/*.md.tmpl; do
    [[ -e $f ]] || continue
    name=$(basename "$f"); name=${name%%.*}
    in_list "$name" "${keep[@]}" || git rm -q "$f"
  done
  for dir in examples/$kind/*/; do
    [[ -d $dir ]] || continue
    name=$(basename "$dir")
    in_list "$name" "${keep[@]}" || git rm -rq "$dir"
  done
done
for f in docs/guides/*.md templates/guides/*.md.tmpl; do
  [[ -e $f ]] || continue
  name=$(basename "$f"); name=${name%%.*}
  in_list "$name" "${keep_guides[@]}" || git rm -q "$f"
done

# Terraform native tests: keep the provider test and those named after a kept
# resource or data source.
for f in tests/*.tftest.hcl; do
  name=$(basename "$f" .tftest.hcl)
  case "$name" in
    provider) continue ;;
    *_data_source) base=${name%_data_source}; in_list "$base" "${keep_data_sources[@]}" && continue ;;
    *_plan) base=${name%_plan}; in_list "$base" "${keep_resources[@]}" && continue ;;
    *) in_list "$name" "${keep_resources[@]}" && continue ;;
  esac
  git rm -q "$f"
done

# Release automation and agent configuration that the fork does not use.
git rm -rq --ignore-unmatch \
  .claude CLAUDE.md renovate.json .github/renovate.md .releaserc \
  .github/workflows/semantic-release.yml .github/workflows/testacc.yml

echo "trim complete; now fix registrations in internal/provider/provider.go and rebuild"
