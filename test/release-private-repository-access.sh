#!/usr/bin/env bash

set -euo pipefail

workflow=.github/workflows/cross-compile.yml
expected_token="token: \${{ secrets.GIT_TOKEN }}"
checkout_block="$(awk '
  /repository: rstreamlabs\/npm/ {
    print
    for (line = 0; line < 4 && getline; line++) {
      print
    }
  }
' "$workflow")"
if [[ "$checkout_block" != *"$expected_token"* ]]; then
  printf 'The stable release checkout of rstreamlabs/npm must use GIT_TOKEN.\n' >&2
  exit 1
fi
if [[ "$checkout_block" != *'persist-credentials: false'* ]]; then
  printf 'The stable release checkout must not persist its credential.\n' >&2
  exit 1
fi
if ! grep -Fq 'run: ./.github/scripts/verify-release-approval.sh' .github/workflows/promote-release.yml; then
  printf 'The promotion must verify the approved candidate.\n' >&2
  exit 1
fi
printf 'Release packaging and promotion access checks passed.\n'
