#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
expected_source="repository: \${{ secrets.RELEASE_NPM_REPOSITORY }}"
expected_token="token: \${{ secrets.GIT_TOKEN }}"
checkout_block=$(awk '/repository: \$\{\{ secrets.RELEASE_NPM_REPOSITORY/ { print; for (line=0; line<4 && getline; line++) print }' .github/workflows/cross-compile.yml)
[[ "$checkout_block" == *"$expected_source"* && "$checkout_block" == *"$expected_token"* ]]
[[ "$checkout_block" == *'persist-credentials: false'* ]]
grep -Fq 'run: ./.github/scripts/verify-release-approval.sh' .github/workflows/promote-release.yml

for workflow in .github/workflows/cross-compile.yml .github/workflows/promote-release.yml; do
  grep -Fq 'run: ./.github/scripts/require-release-repository.sh' "$workflow"
done

# Empty or malformed configuration must fail before checkout can fall back to this repository.
for repository in '' 'missing-owner' 'https://example.test/owner/repository' $'owner/repository\n::warning::injected'; do
  if output=$(RELEASE_REPOSITORY="$repository" .github/scripts/require-release-repository.sh 2>&1); then
    printf 'An invalid release repository was accepted.
' >&2
    exit 1
  fi
  [[ -z "$repository" || "$output" != *"$repository"* ]]
done
output=$(RELEASE_REPOSITORY='example-org/release-source' .github/scripts/require-release-repository.sh)
[[ "$output" == '::add-mask::example-org/release-source' ]]
printf 'Release source configuration and promotion access checks passed.
'
