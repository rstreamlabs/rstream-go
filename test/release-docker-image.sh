#!/usr/bin/env bash

set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
publisher="${repo_root}/.github/scripts/publish-docker-image.sh"
workflow="${repo_root}/.github/workflows/promote-release.yml"
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT

docker_job=$(sed -n '/^  publish-docker:/,/^  publish-homebrew:/p' "$workflow")
grep -F './.promotion-policy/.github/scripts/publish-docker-image.sh' \
  <<<"$docker_job" >/dev/null
if grep -E '^[[:space:]]+\./\.github/scripts/publish-docker-image\.sh' \
  <<<"$docker_job" >/dev/null; then
  echo "Docker promotion must not execute publication policy from the release tag" >&2
  exit 1
fi

manifest='{"schemaVersion":2}'
manifest_digest=$(printf '%s' "$manifest" | shasum -a 256 | awk '{print $1}')
image=docker.io/rstream/rstream
version=1.29.3

mkdir -p "$work_dir/bin" "$work_dir/layout"
printf '%s\n' '#!/usr/bin/env bash' >"$work_dir/bin/skopeo"
# The generated helper must expand these variables when it is executed.
# shellcheck disable=SC2016
printf '%s\n' \
  'set -euo pipefail' \
  'printf "%s\n" "$*" >>"$SKOPEO_CALLS"' \
  'if [[ $1 == inspect && $2 == --raw ]]; then' \
  '  printf "%s" "$SKOPEO_MANIFEST"' \
  '  exit 0' \
  'fi' \
  'exit 0' >>"$work_dir/bin/skopeo"
chmod +x "$work_dir/bin/skopeo"

write_archive() {
  local version_digest=$1
  local latest_digest=$2
  local archive=$3
  jq -n \
    --arg version_image "${image}:${version}" \
    --arg latest_image "${image}:latest" \
    --arg version "$version" \
    --arg version_digest "$version_digest" \
    --arg latest_digest "$latest_digest" \
    '{
      schemaVersion: 2,
      manifests: [
        {
          mediaType: "application/vnd.oci.image.index.v1+json",
          digest: $version_digest,
          size: 19,
          annotations: {
            "io.containerd.image.name": $version_image,
            "org.opencontainers.image.ref.name": $version
          }
        },
        {
          mediaType: "application/vnd.oci.image.index.v1+json",
          digest: $latest_digest,
          size: 19,
          annotations: {
            "io.containerd.image.name": $latest_image,
            "org.opencontainers.image.ref.name": "latest"
          }
        }
      ]
    }' >"$work_dir/layout/index.json"
  tar -cf "$archive" -C "$work_dir/layout" index.json
}

archive="$work_dir/candidate.oci.tar"
digest="sha256:${manifest_digest}"
write_archive "$digest" "$digest" "$archive"
SKOPEO_CALLS="$work_dir/skopeo.calls" \
SKOPEO_MANIFEST="$manifest" \
PATH="$work_dir/bin:$PATH" \
  "$publisher" "$archive" "$image" "$version"
grep -F "inspect --raw oci-archive:${archive}:${version}" \
  "$work_dir/skopeo.calls" >/dev/null

write_archive "$digest" \
  'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' \
  "$archive"
if SKOPEO_CALLS="$work_dir/skopeo.calls" \
  SKOPEO_MANIFEST="$manifest" \
  PATH="$work_dir/bin:$PATH" \
  "$publisher" "$archive" "$image" "$version" >/dev/null 2>&1; then
  echo "publisher accepted divergent version and latest archive references" >&2
  exit 1
fi

jq 'del(.manifests[0])' "$work_dir/layout/index.json" >"$work_dir/layout/index.next.json"
mv "$work_dir/layout/index.next.json" "$work_dir/layout/index.json"
tar -cf "$archive" -C "$work_dir/layout" index.json
if SKOPEO_CALLS="$work_dir/skopeo.calls" \
  SKOPEO_MANIFEST="$manifest" \
  PATH="$work_dir/bin:$PATH" \
  "$publisher" "$archive" "$image" "$version" >/dev/null 2>&1; then
  echo "publisher accepted an archive without the immutable version reference" >&2
  exit 1
fi
