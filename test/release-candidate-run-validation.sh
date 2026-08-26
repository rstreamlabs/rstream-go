#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
validator="${repo_root}/.github/scripts/validate-release-candidate-run.sh"
repository=rstreamlabs/rstream-go
allowed_actor=release-operator
candidate='{
  "name": "build release candidates",
  "path": ".github/workflows/cross-compile.yml",
  "event": "push",
  "status": "completed",
  "conclusion": "success",
  "actor": {"login": "release-operator"},
  "head_repository": {"full_name": "rstreamlabs/rstream-go"},
  "head_branch": "v1.29.3",
  "head_sha": "0123456789abcdef0123456789abcdef01234567"
}'

printf '%s\n' "$candidate" |
  "$validator" "$repository" "$allowed_actor" |
  jq --exit-status '.head_branch == "v1.29.3"' >/dev/null

assert_rejected() {
  local filter=$1
  if jq "$filter" <<<"$candidate" |
    "$validator" "$repository" "$allowed_actor" >/dev/null 2>&1; then
    printf 'candidate mutation was unexpectedly accepted: %s\n' "$filter" >&2
    exit 1
  fi
}

assert_rejected '.path = ".github/workflows/tests.yml"'
assert_rejected '.event = "workflow_dispatch"'
assert_rejected '.status = "in_progress"'
assert_rejected '.conclusion = "failure"'
assert_rejected '.actor.login = "untrusted"'
assert_rejected '.head_repository.full_name = "fork/rstream-go"'
assert_rejected '.head_branch = "main"'
assert_rejected '.head_sha = "not-a-sha"'

workflow="${repo_root}/.github/workflows/promote-release.yml"
grep -F 'workflow_dispatch:' "$workflow" >/dev/null
grep -F 'candidate_run_id:' "$workflow" >/dev/null
# These are literal workflow expressions, not shell expansions in this test.
# shellcheck disable=SC2016
grep -F 'actions/runs/${CANDIDATE_RUN_ID}' "$workflow" >/dev/null
grep -F 'validate-release-candidate-run.sh' "$workflow" >/dev/null
# shellcheck disable=SC2016
grep -F 'run-id: ${{ steps.release.outputs.run-id }}' "$workflow" >/dev/null
