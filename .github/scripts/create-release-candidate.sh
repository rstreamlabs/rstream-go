#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 4 ]]; then
  echo "usage: $0 <package> <channel> <version> <archive>" >&2
  exit 1
fi

package_name=$1
channel=$2
version=$3
archive=$4
release_root="out/cmd/${package_name}/${channel}/${version}"
manifest="${release_root}/release-manifest.sha256"

if [[ ! "$version" =~ ^[0-9]+(\.[0-9]+){2}(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$ ]]; then
  echo "invalid release version: ${version}" >&2
  exit 1
fi

if [[ ! -d "$release_root" ]]; then
  echo "release root does not exist: ${release_root}" >&2
  exit 1
fi

(
  cd "$release_root"
  find . -type f ! -name release-manifest.sha256 -print0 \
    | LC_ALL=C sort -z \
    | xargs -0 shasum -a 256
) >"$manifest"

tar -czf "$archive" -C "$(dirname "$release_root")" "$version"
shasum -a 256 "$archive" >"${archive}.sha256"
