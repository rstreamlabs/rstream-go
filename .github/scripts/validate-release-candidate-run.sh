#!/usr/bin/env bash
set -euo pipefail

if (($# != 2)); then
  printf 'usage: %s <repository> <allowed-actor>\n' "$0" >&2
  exit 2
fi

repository=$1
allowed_actor=$2
if [[ -z "$repository" || -z "$allowed_actor" ]]; then
  printf 'repository and allowed actor must be non-empty\n' >&2
  exit 2
fi

jq --exit-status \
  --arg repository "$repository" \
  --arg allowed_actor "$allowed_actor" '
    if type == "object" and
       .name == "build release candidates" and
       .path == ".github/workflows/cross-compile.yml" and
       .event == "push" and
       .status == "completed" and
       .conclusion == "success" and
       .actor.login == $allowed_actor and
       .head_repository.full_name == $repository and
       (.head_branch | type == "string" and test("^v[0-9]+(\\.[0-9]+){2}$")) and
       (.head_sha | type == "string" and test("^[0-9a-f]{40}$"))
    then .
    else error("workflow run is not an approved release candidate")
    end
  '
