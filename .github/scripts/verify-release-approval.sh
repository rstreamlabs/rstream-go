#!/usr/bin/env bash
# The environment approval is scoped to this repository, source, workflow run
# and set of immutable artifact IDs and digests.
set -euo pipefail

: "${GITHUB_REPOSITORY:?Missing repository}"
: "${CANDIDATE_SHA:?Missing candidate commit}"
: "${CANDIDATE_RUN_ID:?Missing candidate workflow run}"
: "${CANDIDATE_PREFIX:?Missing artifact prefix}"
: "${APPROVED_RELEASE_CANDIDATE:?Missing candidate approval}"
[[ "$CANDIDATE_SHA" =~ ^[a-f0-9]{40}$ && "$CANDIDATE_RUN_ID" =~ ^[1-9][0-9]*$ ]]

artifacts=$(gh api --paginate --slurp \
  "repos/$GITHUB_REPOSITORY/actions/runs/$CANDIDATE_RUN_ID/artifacts?per_page=100" |
  jq -er --arg prefix "$CANDIDATE_PREFIX" --arg sha "$CANDIDATE_SHA" --argjson run "$CANDIDATE_RUN_ID" '
    [.[].artifacts[] | select(.name | startswith($prefix))] | sort_by(.name) |
    if length > 0 and (map(.name) | unique | length) == length and
      all(.[]; .expired == false and .workflow_run.id == $run and
        .workflow_run.head_sha == $sha and (.id | type == "number") and
        (.name | test("^[a-zA-Z0-9._-]+$")) and (.digest | test("^sha256:[a-f0-9]{64}$")))
    then .[] | "\(.name) \(.id) \(.digest)"
    else error("Missing, expired, duplicate or mismatched release artifacts") end')
actual=$(printf '%s\n' "$GITHUB_REPOSITORY" "$CANDIDATE_SHA" "$CANDIDATE_RUN_ID" "$artifacts" | shasum -a 256)
if [[ "${actual%% *}" != "$APPROVED_RELEASE_CANDIDATE" ]]; then
  printf '%s\n' 'The approval does not match this candidate and its archives.' >&2
  exit 1
fi
