#!/usr/bin/env bash
# Deletes everything outside the Workload Identity Federation subset, then
# checks the rest of the tree against hack/upstream-manifest.txt.
#
# The deletions are allowlist-driven so that a service package, doc page,
# example or test that upstream adds later is removed on the next sync without
# editing this file. The manifest check is what catches everything the
# allowlists do not reach: a new package or non-test file under internal/, a
# new root entry or workflow, a new .goreleaser.yml before hook (runs on the
# release runner next to the GPG key), or a change to main.go, GNUmakefile,
# mise.toml, tools/ or hack/. The script exits non-zero listing them; review
# each one (FORK.md), then rerun with --update-manifest and commit the
# manifest.
#
# Run from the repository root after rebasing onto upstream/main, then review
# `git status` and rebuild. See FORK.md.
set -euo pipefail
export LC_ALL=C

cd "$(git rev-parse --show-toplevel)"

manifest=hack/upstream-manifest.txt
update_manifest=0
for arg in "$@"; do
  case "$arg" in
    --update-manifest) update_manifest=1 ;;
    -h|--help) sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

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
keep_guides=(install workload_identity_federation)

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

# Rebase guard: everything the allowlists above do not reach.
emit_manifest() {
  # Top-level entries (files and directories).
  git ls-files | cut -d/ -f1 | sort -u | sed 's/^/root /'
  # Every directory under internal/: a new package is a new init() candidate.
  git ls-files internal | xargs -n1 dirname | sort -u | sed 's/^/dir /'
  # Every non-test Go file under internal/: these link into the binary.
  git ls-files internal | grep '\.go$' | grep -v '_test\.go$' | sort | sed 's/^/go /'
  git ls-files .github/workflows | sort | sed 's/^/workflow /'
  # .goreleaser.yml before hooks, verbatim.
  awk '/^before:/ {f=1; print "hooks " $0; next} f && /^[^[:space:]#]/ {f=0} f {print "hooks " $0}' .goreleaser.yml
  # Files whose content matters, not just their presence.
  local f
  for f in main.go GNUmakefile mise.toml $(git ls-files tools hack | grep -vx "$manifest" | sort); do
    [[ -e $f ]] || continue
    printf 'sha256 %s %s\n' "$(sha256sum "$f" | cut -d' ' -f1)" "$f"
  done
}

current=$(mktemp)
trap 'rm -f "$current"' EXIT
emit_manifest > "$current"

if (( update_manifest )); then
  {
    echo "# Expected tree after hack/trim-upstream.sh; the script fails on any difference."
    echo "# Regenerate with 'bash hack/trim-upstream.sh --update-manifest' once every"
    echo "# reported entry has been reviewed (FORK.md, sync checklist)."
    cat "$current"
  } > "$manifest"
  echo "manifest written: $manifest"
elif [[ ! -f $manifest ]]; then
  echo "trim: $manifest is missing; review the tree, then run: bash $0 --update-manifest" >&2
  exit 1
elif ! delta=$(diff <(grep -v '^#' "$manifest") "$current"); then
  echo "trim: the tree differs from $manifest:" >&2
  echo "$delta" | sed -n 's/^< /  in manifest, not in tree: /p; s/^> /  in tree, not in manifest: /p' >&2
  echo "Review each entry (FORK.md, sync checklist). If every one is intended, run: bash $0 --update-manifest" >&2
  exit 1
fi

echo "trim complete; now fix registrations in internal/provider/provider.go and rebuild"
