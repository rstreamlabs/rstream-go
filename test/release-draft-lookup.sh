#!/usr/bin/env bash
set -euo pipefail
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
selector="${repo_root}/.github/scripts/select-release-by-tag.sh"
pages='[{"id":11,"tag_name":"v1.29.2","draft":true}]
[{"id":12,"tag_name":"v1.29.3","draft":true}]'
selected=$(printf '%s\n' "$pages" | "$selector" v1.29.3)
jq --exit-status '.id == 12 and .draft == true' <<<"$selected" >/dev/null
if printf '%s\n' "$pages" | "$selector" v9.9.9 >/dev/null 2>&1; then
  printf 'A missing draft release tag must fail closed.\n' >&2
  exit 1
fi
duplicate_pages='[{"id":12,"tag_name":"v1.29.3","draft":true}]
[{"id":13,"tag_name":"v1.29.3","draft":true}]'
if printf '%s\n' "$duplicate_pages" | "$selector" v1.29.3 >/dev/null 2>&1; then
  printf 'Duplicate release tags must fail closed.\n' >&2
  exit 1
fi
workflow="${repo_root}/.github/workflows/promote-release.yml"
grep -F 'releases?per_page=100' "$workflow" >/dev/null
prepare_job=$(sed -n '/^  prepare:/,/^  publish-package-api:/p' "$workflow")
grep -F 'actions: read' <<<"$prepare_job" >/dev/null
grep -F 'contents: write' <<<"$prepare_job" >/dev/null
# Promotion helpers must be loaded from the trusted default branch. A release
# candidate predating this helper cannot provide it itself.
# shellcheck disable=SC2016
grep -F 'ref: ${{ github.event.repository.default_branch }}' "$workflow" >/dev/null
grep -F 'uses: ./.promotion-policy/.github/actions/restore-release-candidate' "$workflow" >/dev/null
# shellcheck disable=SC2016
grep -F '${{ github.action_path }}/../../scripts/restore-release-candidate.sh' \
  "${repo_root}/.github/actions/restore-release-candidate/action.yml" >/dev/null
# These are literal workflow expressions, not shell expansions in this test.
# shellcheck disable=SC2016
grep -F 'select-release-by-tag.sh "$RELEASE_TAG"' "$workflow" >/dev/null
# shellcheck disable=SC2016
if grep -F 'release=$(gh api "repos/${GITHUB_REPOSITORY}/releases/tags/${RELEASE_TAG}")' "$workflow" >/dev/null; then
  printf 'Draft release lookup must not use the published-only tag endpoint.\n' >&2
  exit 1
fi
