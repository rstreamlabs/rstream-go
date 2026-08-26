#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 3 ]]; then
  echo "usage: $0 <oci-archive> <image> <version>" >&2
  exit 1
fi

archive=$1
image=$2
version=$3
version_source_image="${image}:${version}"
latest_source_image="${image}:latest"
version_source_ref=$version
latest_source_ref=latest
version_ref="docker://${image}:${version}"
latest_ref="docker://${image}:latest"

if [[ ! -f "$archive" ]]; then
  echo "OCI archive does not exist: ${archive}" >&2
  exit 1
fi

if ! archive_index=$(tar -xOf "$archive" index.json); then
  echo "OCI archive does not contain a readable root index.json: ${archive}" >&2
  exit 1
fi
if ! jq --exit-status '.schemaVersion == 2 and (.manifests | type == "array")' \
  <<<"$archive_index" >/dev/null; then
  echo "OCI archive contains an invalid root index.json: ${archive}" >&2
  exit 1
fi

version_match=$(jq --arg image "$version_source_image" --arg ref "$version_source_ref" \
  '[.manifests[] | select(
    .annotations["io.containerd.image.name"] == $image and
    .annotations["org.opencontainers.image.ref.name"] == $ref
  )]' \
  <<<"$archive_index")
latest_match=$(jq --arg image "$latest_source_image" --arg ref "$latest_source_ref" \
  '[.manifests[] | select(
    .annotations["io.containerd.image.name"] == $image and
    .annotations["org.opencontainers.image.ref.name"] == $ref
  )]' \
  <<<"$archive_index")
if [[ $(jq 'length' <<<"$version_match") -ne 1 ]]; then
  echo "OCI archive must contain exactly one ${version_source_image} reference" >&2
  exit 1
fi
if [[ $(jq 'length' <<<"$latest_match") -ne 1 ]]; then
  echo "OCI archive must contain exactly one ${latest_source_image} reference" >&2
  exit 1
fi
version_archive_digest=$(jq -r '.[0].digest' <<<"$version_match")
latest_archive_digest=$(jq -r '.[0].digest' <<<"$latest_match")
if [[ ! "$version_archive_digest" =~ ^sha256:[0-9a-f]{64}$ ]] ||
  [[ "$version_archive_digest" != "$latest_archive_digest" ]]; then
  echo "OCI archive version and latest references do not identify one valid immutable image" >&2
  exit 1
fi

source_ref="oci-archive:${archive}:${version_source_ref}"
source_digest=$(skopeo inspect --raw "$source_ref" | shasum -a 256 | awk '{print $1}')
if [[ "sha256:${source_digest}" != "$version_archive_digest" ]]; then
  echo "selected OCI manifest does not match the archive index digest" >&2
  exit 1
fi

if remote_manifest=$(skopeo inspect --raw "$version_ref" 2>/dev/null); then
  remote_digest=$(printf '%s' "$remote_manifest" | shasum -a 256 | awk '{print $1}')
  if [[ "$remote_digest" != "$source_digest" ]]; then
    echo "immutable Docker tag conflict for ${image}:${version}" >&2
    echo "expected ${source_digest}, found ${remote_digest}" >&2
    exit 1
  fi
  echo "Docker image ${image}:${version} already has the expected digest"
else
  skopeo copy --all --preserve-digests "$source_ref" "$version_ref"
fi

published_digest=$(skopeo inspect --raw "$version_ref" | shasum -a 256 | awk '{print $1}')
if [[ "$published_digest" != "$source_digest" ]]; then
  echo "published Docker digest mismatch for ${image}:${version}" >&2
  exit 1
fi

if latest_manifest=$(skopeo inspect --raw "$latest_ref" 2>/dev/null); then
  latest_digest=$(printf '%s' "$latest_manifest" | shasum -a 256 | awk '{print $1}')
else
  latest_digest=
fi
if [[ "$latest_digest" != "$published_digest" ]]; then
  skopeo copy --all --preserve-digests "$version_ref" "$latest_ref"
  latest_digest=$(skopeo inspect --raw "$latest_ref" | shasum -a 256 | awk '{print $1}')
fi
if [[ "$latest_digest" != "$published_digest" ]]; then
  echo "Docker latest tag does not match ${image}:${version}" >&2
  exit 1
fi
