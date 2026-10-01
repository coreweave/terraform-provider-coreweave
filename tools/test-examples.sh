#!/usr/bin/env bash

set -euo pipefail

log() {
  echo "[test-examples] $*" >&2
}

fail() {
  log "ERROR: $*"
  exit 1
}

if ! command -v terraform >/dev/null 2>&1; then
  fail "terraform is required but was not found in PATH"
fi

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(git -C "$script_dir" rev-parse --show-toplevel)"

# Explicit registry of independently copyable documentation examples.
example_dirs=(
  "examples/resources/coreweave_object_storage_bucket_policy"
  "examples/data-sources/coreweave_object_storage_bucket_policy_document"
)

export TF_IN_AUTOMATION=1

validate_example() {
  local example_rel="$1"
  local example_dir="$repo_root/$example_rel"
  local tmpdir
  local tf_files

  if [[ ! -d "$example_dir" ]]; then
    log "FAIL $example_rel: directory does not exist"
    return 1
  fi

  tf_files=("$example_dir"/*.tf)
  if [[ ! -e "${tf_files[0]}" ]]; then
    log "FAIL $example_rel: no .tf files found"
    return 1
  fi

  tmpdir="$(mktemp -d)"
  trap 'rm -rf "$tmpdir"' RETURN

  log "Validating $example_rel"

  cp "$example_dir"/*.tf "$tmpdir/"

  terraform -chdir="$tmpdir" fmt -check -diff
  terraform -chdir="$tmpdir" init -backend=false -input=false
  terraform -chdir="$tmpdir" validate

  log "PASS $example_rel"
}

failed=0

for example_rel in "${example_dirs[@]}"; do
  if ! validate_example "$example_rel"; then
    log "FAIL $example_rel"
    failed=1
  fi
done

if [[ "$failed" -ne 0 ]]; then
  fail "one or more documentation examples failed validation"
fi

log "all documentation examples passed validation"
