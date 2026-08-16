#!/usr/bin/env bash

set -euo pipefail

dry_run="$({
  make -n rstream-linux-arm64-deploy-pkg \
    VERSION=0.0.0-test \
    RSTREAM_TOKEN=test \
    RSTREAM_URL=https://packages.example.invalid
} 2>&1)"

expect_count() {
  local expected="$1"
  local value="$2"
  local count
  count="$(grep -oF -- "$value" <<<"$dry_run" | wc -l | tr -d ' ')"
  if [[ "$count" != "$expected" ]]; then
    printf 'Expected %s occurrences of %q, found %s.\n' "$expected" "$value" "$count" >&2
    exit 1
  fi
}

expect_count 2 "--retry 5"
expect_count 2 "--retry-all-errors"
expect_count 2 "--retry-delay 2"
expect_count 2 "--connect-timeout 30"

printf 'Package metadata and artifact uploads both use the release retry policy.\n'
