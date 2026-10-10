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
for marker in \
  'name: Checkout trusted WebTTY certification policy' \
  'uses: ./.webtty-policy/.github/actions/verify-webtty-release'; do
  certification_block="$(awk -v marker="$marker" '
    index($0, marker) { capture = 1; next }
    capture && /^      - / { exit }
    capture { print }
  ' .github/workflows/promote-release.yml)"
  if [[ "$certification_block" != *"$expected_token"* ]]; then
    printf 'Release certification must use the configured GIT_TOKEN: %s\n' "$marker" >&2
    exit 1
  fi
done
printf 'Private release repository checkout is authenticated without persisting credentials.\n'
