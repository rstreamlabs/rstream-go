#!/usr/bin/env bash

set -euo pipefail

version=0.0.0-test
release_root="out/cmd/rstream/stable/${version}"
candidate_root="out/test-release-candidate"
archive="${candidate_root}/release-candidate-${version}.tar.gz"

cleanup() {
  rm -rf out/cmd/rstream/stable "$candidate_root"
}
trap cleanup EXIT HUP INT TERM

mkdir -p "${release_root}/linux/arm64/release/bin"
mkdir -p "$candidate_root"
printf 'test binary\n' >"${release_root}/linux/arm64/release/bin/rstream"

./.github/scripts/create-release-candidate.sh rstream stable "$version" "$archive"
candidate_checksum=$(<"${archive}.sha256")
rm -rf "$release_root"
./.github/scripts/restore-release-candidate.sh rstream stable "$version" "$archive"

if [[ "$(<"${release_root}/linux/arm64/release/bin/rstream")" != "test binary" ]]; then
  echo "restored release candidate content differs" >&2
  exit 1
fi

candidate_digest=${candidate_checksum%% *}
printf '%s  %s\n' "$candidate_digest" different-archive.tar.gz >"${archive}.sha256"
if ./.github/scripts/restore-release-candidate.sh rstream stable "$version" "$archive"; then
  echo "restore accepted a checksum for a different archive" >&2
  exit 1
fi

printf '%s\n%s\n' "$candidate_checksum" "$candidate_checksum" >"${archive}.sha256"
if ./.github/scripts/restore-release-candidate.sh rstream stable "$version" "$archive"; then
  echo "restore accepted multiple checksum records" >&2
  exit 1
fi

printf 'Release candidate round trip succeeded.\n'
