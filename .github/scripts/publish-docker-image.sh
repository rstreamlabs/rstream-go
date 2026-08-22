#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 3 ]]; then
  echo "usage: $0 <oci-archive> <image> <version>" >&2
  exit 1
fi

archive=$1
image=$2
version=$3
source_ref="oci-archive:${archive}"
version_ref="docker://${image}:${version}"
latest_ref="docker://${image}:latest"

if [[ ! -f "$archive" ]]; then
  echo "OCI archive does not exist: ${archive}" >&2
  exit 1
fi

source_digest=$(skopeo inspect --raw "$source_ref" | shasum -a 256 | awk '{print $1}')

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
