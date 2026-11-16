#!/usr/bin/env bash
set -euo pipefail

repository="${RELEASE_REPOSITORY:-}"
if [[ ! "$repository" =~ ^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9_.-]+$ ]]; then
  printf 'Configure the release source repository secret as owner/name before running this workflow.
' >&2
  exit 1
fi
# Mask the coordinate before checkout also includes it in URLs and git output.
printf '::add-mask::%s
' "$repository"
