#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 5 ]]; then
  echo "usage: $0 <package> <channel> <version> <source> <api-key>" >&2
  exit 1
fi

package_name=$1
channel=$2
version=$3
source="${4%/}/"
api_key=$5
package="out/cmd/${package_name}/${channel}/${version}/windows/${package_name}.${version}.nupkg"
package_url="${source}Packages(Id='${package_name}',Version='${version}')"
download_url="${source}${package_name}/${version}"
retry_flags=(--retry 5 --retry-all-errors --retry-delay 2 --connect-timeout 30)

if [[ ! -f "$package" ]]; then
  echo "NuGet package does not exist: ${package}" >&2
  exit 1
fi

expected_checksum=$(shasum -a 256 "$package" | awk '{print $1}')

verify_download() {
  local actual_checksum

  actual_checksum="$(
    curl --fail --silent --show-error --location "${retry_flags[@]}" "$download_url" \
      | shasum -a 256 \
      | awk '{print $1}'
  )"
  if [[ "$actual_checksum" != "$expected_checksum" ]]; then
    echo "published NuGet checksum mismatch for ${package_name} ${version}" >&2
    exit 1
  fi
}

if curl --fail --silent --show-error --location "${retry_flags[@]}" "$package_url" >/dev/null 2>&1; then
  verify_download
  echo "NuGet package ${package_name} ${version} already has the expected checksum"
  exit 0
fi

nuget push "$package" "$api_key" \
  -Source "$source" \
  -NonInteractive \
  -SkipDuplicate \
  -Verbosity detailed

for _ in {1..12}; do
  if curl --fail --silent --show-error --location "${retry_flags[@]}" "$package_url" >/dev/null 2>&1; then
    verify_download
    exit 0
  fi
  sleep 5
done

echo "NuGet package did not become visible: ${package_name} ${version}" >&2
exit 1
