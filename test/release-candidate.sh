#!/usr/bin/env bash

set -euo pipefail

version=0.0.0-test
release_root="out/cmd/rstream/stable/${version}"
archive="release-candidate-${version}.tar.gz"

cleanup() {
  rm -rf out/cmd/rstream/stable "$archive" "${archive}.sha256"
}
trap cleanup EXIT HUP INT TERM

mkdir -p "${release_root}/linux/arm64/release/bin"
printf 'test binary\n' >"${release_root}/linux/arm64/release/bin/rstream"

./.github/scripts/create-release-candidate.sh rstream stable "$version" "$archive"
rm -rf "$release_root"
./.github/scripts/restore-release-candidate.sh rstream stable "$version" "$archive"

if [[ "$(<"${release_root}/linux/arm64/release/bin/rstream")" != "test binary" ]]; then
  echo "restored release candidate content differs" >&2
  exit 1
fi

printf 'Release candidate round trip succeeded.\n'
