#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 4 ]]; then
  echo "usage: $0 <package> <version> <archive> <source-directory>" >&2
  exit 1
fi

package_name=$1
version=$2
archive=$3
source_directory=$4

if [[ ! -f "$archive" ]]; then
  echo "npm package archive does not exist: ${archive}" >&2
  exit 1
fi

expected_integrity="sha512-$(openssl dgst -sha512 -binary "$archive" | openssl base64 -A)"
registry_integrity() {
  npm view "${package_name}@${version}" dist.integrity --fetch-timeout=5000 --fetch-retries=0 2>/dev/null || true
}
published_integrity=$(registry_integrity)

if [[ -n "$published_integrity" ]]; then
  if [[ "$published_integrity" != "$expected_integrity" ]]; then
    echo "immutable npm package conflict for ${package_name}@${version}" >&2
    exit 1
  fi
  echo "npm package ${package_name}@${version} already has the expected integrity"
else
  npm publish --access public "$archive"
fi

for attempt in {1..120}; do
  published_integrity=$(registry_integrity)
  if [[ "$published_integrity" == "$expected_integrity" ]]; then
    break
  fi
  if [[ -n "$published_integrity" ]]; then
    echo "immutable npm package conflict for ${package_name}@${version}" >&2
    exit 1
  fi
  if [[ "$attempt" -lt 120 ]]; then
    sleep 5
  fi
done

if [[ "$published_integrity" != "$expected_integrity" ]]; then
  echo "npm package ${package_name}@${version} metadata is still unavailable after 120 checks; retry promotion with the same retained candidate" >&2
  exit 1
fi

git -C "$source_directory" config user.name "CI automation"
git -C "$source_directory" config user.email "ci@rstream.io"
git -C "$source_directory" add rstream-cli/package.json rstream-cli/package-lock.json
if ! git -C "$source_directory" diff --cached --quiet; then
  git -C "$source_directory" commit -m "update rstream-cli version to ${version}"
  git -C "$source_directory" push
fi
