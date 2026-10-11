#!/usr/bin/env bash
set -euo pipefail
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
test_directory=$(mktemp -d)
trap 'rm -rf "$test_directory"' EXIT
mkdir "$test_directory/bin"
printf 'retained npm candidate\n' >"$test_directory/package.tgz"
NPM_TEST_INTEGRITY="sha512-$(openssl dgst -sha512 -binary "$test_directory/package.tgz" | openssl base64 -A)"
export NPM_TEST_INTEGRITY
export NPM_TEST_DIRECTORY="$test_directory"
cat >"$test_directory/bin/npm" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" == publish ]]; then
  printf 'publish\n' >>"$NPM_TEST_DIRECTORY/published"
  exit 0
fi
[[ "$1" == view ]]
count=$(cat "$NPM_TEST_DIRECTORY/views")
count=$((count + 1))
printf '%s\n' "$count" >"$NPM_TEST_DIRECTORY/views"
case "$NPM_TEST_SCENARIO" in
  existing) printf '%s\n' "$NPM_TEST_INTEGRITY" ;;
  delayed)
    if [[ "$count" -gt 20 ]]; then
      printf '%s\n' "$NPM_TEST_INTEGRITY"
    else
      exit 1
    fi
    ;;
  conflict) printf 'sha512-conflicting-bytes\n' ;;
  published-conflict)
    if [[ "$count" -gt 1 ]]; then
      printf 'sha512-conflicting-bytes\n'
    else
      exit 1
    fi
    ;;
  unavailable) exit 1 ;;
  *) exit 2 ;;
esac
STUB
cat >"$test_directory/bin/git" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$NPM_TEST_DIRECTORY/git-calls"
STUB
cat >"$test_directory/bin/sleep" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
[[ "$1" == 5 ]]
printf 'sleep\n' >>"$NPM_TEST_DIRECTORY/sleeps"
STUB
chmod +x "$test_directory/bin/"*
export PATH="$test_directory/bin:$PATH"
for scenario in existing delayed conflict published-conflict unavailable; do
  export NPM_TEST_SCENARIO="$scenario"
  printf '0\n' >"$test_directory/views"
  rm -f "$test_directory/published" "$test_directory/git-calls" "$test_directory/sleeps"
  result=0
  "$repo_root/.github/scripts/publish-npm-package.sh" rstream 1.2.3 \
    "$test_directory/package.tgz" "$test_directory" >"$test_directory/output" 2>&1 || result=$?
  case "$scenario" in
    existing)
      [[ "$result" -eq 0 && ! -f "$test_directory/published" && -s "$test_directory/git-calls" ]]
      ;;
    delayed)
      [[ "$result" -eq 0 && -s "$test_directory/git-calls" ]]
      [[ $(wc -l <"$test_directory/published") -eq 1 ]]
      [[ $(wc -l <"$test_directory/sleeps") -gt 12 ]]
      ;;
    conflict|published-conflict)
      [[ "$result" -ne 0 && ! -f "$test_directory/git-calls" && ! -f "$test_directory/sleeps" ]]
      grep -F 'immutable npm package conflict' "$test_directory/output" >/dev/null
      if [[ "$scenario" == conflict ]]; then
        [[ ! -f "$test_directory/published" ]]
      else
        [[ $(wc -l <"$test_directory/published") -eq 1 ]]
      fi
      ;;
    unavailable)
      [[ "$result" -ne 0 && ! -f "$test_directory/git-calls" ]]
      [[ $(wc -l <"$test_directory/published") -eq 1 ]]
      [[ $(wc -l <"$test_directory/sleeps") -eq 119 ]]
      grep -F 'metadata is still unavailable' "$test_directory/output" >/dev/null
      grep -F 'same retained candidate' "$test_directory/output" >/dev/null
      ;;
  esac
  printf 'npm publication: %s passed\n' "$scenario"
done
