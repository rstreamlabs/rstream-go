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
release_parent="out/cmd/${package_name}/${channel}"
release_root="${release_parent}/${version}"

if [[ ! -f "$archive" || ! -f "${archive}.sha256" ]]; then
  echo "release candidate archive or checksum is missing" >&2
  exit 1
fi

checksum_line_count=$(awk 'END { print NR }' "${archive}.sha256")
checksum_line=$(<"${archive}.sha256")
if [[ "$checksum_line_count" -ne 1 || ! "$checksum_line" =~ ^([[:xdigit:]]{64})[[:space:]]+\*?(.+)$ ]]; then
  echo "release candidate checksum has an invalid format" >&2
  exit 1
fi

expected_checksum=$(printf '%s' "${BASH_REMATCH[1]}" | tr '[:upper:]' '[:lower:]')
recorded_archive=${BASH_REMATCH[2]}
if [[ "$(basename "$recorded_archive")" != "$(basename "$archive")" ]]; then
  echo "release candidate checksum names a different archive" >&2
  exit 1
fi

actual_checksum=$(shasum -a 256 "$archive" | awk '{ print $1 }')
if [[ "$actual_checksum" != "$expected_checksum" ]]; then
  echo "release candidate checksum mismatch" >&2
  exit 1
fi

if tar -tzf "$archive" | awk -v prefix="${version}/" '
  $0 != prefix && index($0, prefix) != 1 { invalid = 1 }
  /(^|\/)\.\.($|\/)/ { invalid = 1 }
  END { exit invalid }
'; then
  :
else
  echo "release candidate contains an invalid path" >&2
  exit 1
fi

if [[ -e "$release_root" ]]; then
  echo "release root already exists: ${release_root}" >&2
  exit 1
fi

mkdir -p "$release_parent"
tar -xzf "$archive" -C "$release_parent"

if [[ ! -f "${release_root}/release-manifest.sha256" ]]; then
  echo "release manifest is missing" >&2
  exit 1
fi

(
  cd "$release_root"
  shasum -a 256 --check release-manifest.sha256
)
