#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 4 ]]; then
  echo "usage: $0 <package-directory> <version> <registry-url> <destination>" >&2
  exit 1
fi

package_directory=$1
version=$2
registry_url=${3%/}
destination=$4

if [[ ! "$version" =~ ^[0-9]+(\.[0-9]+){2}(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$ ]]; then
  echo "invalid npm package version: ${version}" >&2
  exit 1
fi

configured_registry=$(cd "$package_directory" && npm pkg get '@rstreamlabs/installer.registry' | tr -d '"')
if [[ "$configured_registry" != "$registry_url" ]]; then
  echo "unexpected rstream package registry: ${configured_registry}" >&2
  exit 1
fi

mkdir -p "$destination"
destination=$(cd "$destination" && pwd)
(
  cd "$package_directory"
  npm version "$version" --no-git-tag-version --ignore-scripts
  npm ci --ignore-scripts
  npm run prepack
  npm pack --ignore-scripts --pack-destination "$destination"
)
git -C "$package_directory" rev-parse HEAD >"${destination}/source-commit"
