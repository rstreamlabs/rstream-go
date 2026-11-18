#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 <version>" >&2
  exit 1
fi

version=$1
source_commit=$(git rev-parse --verify HEAD)
source_root=out/fips/linux
destination="out/cmd/rstream/stable/${version}/fips"
work_directory=$(mktemp -d)

cleanup() {
  rm -rf "$work_directory"
}
trap cleanup EXIT HUP INT TERM

if [[ ! "$version" =~ ^[0-9]+(\.[0-9]+){2}(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$ ]]; then
  echo "invalid release version: ${version}" >&2
  exit 1
fi
if [[ ! "$source_commit" =~ ^[0-9a-f]{40}$ ]]; then
  echo "invalid source commit: ${source_commit}" >&2
  exit 1
fi

mkdir -p "$destination"
for arch in x86_64 arm64; do
  binary="${source_root}/${arch}/rstream"
  go_arch=$arch
  if [[ "$arch" == x86_64 ]]; then
    go_arch=amd64
  fi
  archive_name="rstream-fips-${version}-linux-${arch}.tar.gz"
  package_name=${archive_name%.tar.gz}
  package_root="${work_directory}/${package_name}"
  metadata="${package_root}/BUILD-INFO.txt"

  if [[ ! -x "$binary" || -L "$binary" ]]; then
    echo "FIPS binary is missing, non-executable, or a symlink: ${binary}" >&2
    exit 1
  fi

  mkdir -p "$package_root"
  go version -m "$binary" >"$metadata"
  grep -Eq ': go1\.27([.]|$)' "$metadata"
  grep -Fq $'\tbuild\t-tags=rstream_fips,fips140v1.0' "$metadata"
  grep -Fq $'\tbuild\tDefaultGODEBUG=fips140=on' "$metadata"
  grep -Fq $'\tbuild\tCGO_ENABLED=0' "$metadata"
  grep -Fq $'\tbuild\tGOFIPS140=v1.0.0-c2097c7c' "$metadata"
  grep -Fq $'\tbuild\tGOOS=linux' "$metadata"
  grep -Fq $'\tbuild\tGOARCH='"${go_arch}" "$metadata"
  grep -Fq $'\tdep\tgithub.com/quic-go/quic-go\tv0.60.0\t' "$metadata"
  grep -Fq $'\tdep\tgithub.com/quic-go/webtransport-go\tv0.11.1\t' "$metadata"

  {
    printf '\nrelease-version\t%s\n' "$version"
    printf 'source-commit\t%s\n' "$source_commit"
    printf 'profile\trstream_fips\n'
    printf 'claim\tFIPS 140-3 Inside — Go Cryptographic Module, Certificate #5247 (Overall Security Level 1)\n'
  } >>"$metadata"
  install -m 0755 "$binary" "${package_root}/rstream"
  install -m 0644 LICENSE "${package_root}/LICENSE"
  install -m 0644 docs/010-fips-140-3-profile.md \
    "${package_root}/FIPS-140-3-PROFILE.md"
  (
    cd "$package_root"
    shasum -a 256 BUILD-INFO.txt FIPS-140-3-PROFILE.md LICENSE rstream \
      >SHA256SUMS
  )
  tar -czf "${destination}/${archive_name}" -C "$work_directory" "$package_name"
  (
    cd "$destination"
    shasum -a 256 "$archive_name" >"${archive_name}.sha256"
  )
done
