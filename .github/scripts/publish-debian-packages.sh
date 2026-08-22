#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 5 ]]; then
  echo "usage: $0 <package> <channel> <version> <aptly-url> <digest-credentials>" >&2
  exit 1
fi

package_name=$1
channel=$2
version=$3
aptly_url=${4%/}
digest_credentials=$5
release_root="out/cmd/${package_name}/${channel}/${version}"
repository="linux-${channel}"
upload_directory="${package_name}-${channel}-${version}"
retry_flags=(--retry 5 --retry-all-errors --retry-delay 2 --connect-timeout 30)

mapfile -d '' packages < <(
  find "$release_root" -mindepth 3 -maxdepth 3 -type f -name "${package_name}-${version}-*.deb" -print0 \
    | LC_ALL=C sort -z
)

if ((${#packages[@]} == 0)); then
  echo "no Debian packages found below ${release_root}" >&2
  exit 1
fi

repository_packages() {
  curl --fail --silent --show-error "${retry_flags[@]}" --digest --user "$digest_credentials" \
    "${aptly_url}/api/repos/${repository}/packages?format=details"
}

existing=$(repository_packages)
missing_packages=()
for package in "${packages[@]}"; do
  deb_name=$(dpkg-deb --field "$package" Package)
  deb_version=$(dpkg-deb --field "$package" Version)
  deb_arch=$(dpkg-deb --field "$package" Architecture)
  checksum=$(shasum -a 256 "$package" | awk '{print $1}')
  if [[ "$deb_name" != "$package_name" || "$deb_version" != "$version" ]]; then
    echo "unexpected Debian metadata in ${package}" >&2
    exit 1
  fi
  matches=$(jq --arg name "$deb_name" --arg version "$deb_version" --arg arch "$deb_arch" \
    '[.[] | select(.Package == $name and .Version == $version and .Architecture == $arch)]' \
    <<<"$existing")
  match_count=$(jq 'length' <<<"$matches")
  if [[ "$match_count" == 0 ]]; then
    missing_packages+=("$package")
    continue
  fi
  if [[ "$match_count" != 1 ]]; then
    echo "repository returned ${match_count} Debian packages for ${deb_name} ${deb_version} ${deb_arch}" >&2
    exit 1
  fi
  published_checksum=$(jq -r '.[0].SHA256 | gsub("^ +"; "")' <<<"$matches")
  if [[ "$published_checksum" != "$checksum" ]]; then
    echo "immutable Debian package conflict for ${deb_name} ${deb_version} ${deb_arch}" >&2
    exit 1
  fi
done

if ((${#missing_packages[@]} == 0)); then
  echo "All Debian packages already exist in ${repository}"
  exit 0
fi

upload_args=()
for package in "${missing_packages[@]}"; do
  upload_args+=(--form "file=@${package}")
done

curl --fail --silent --show-error "${retry_flags[@]}" --digest --user "$digest_credentials" \
  --request POST "${upload_args[@]}" \
  "${aptly_url}/api/files/${upload_directory}"
curl --fail --silent --show-error "${retry_flags[@]}" --digest --user "$digest_credentials" \
  --request POST \
  "${aptly_url}/api/repos/${repository}/file/${upload_directory}"
curl --fail --silent --show-error "${retry_flags[@]}" --digest --user "$digest_credentials" \
  --header "Content-Type: application/json" \
  --request PUT \
  --data '{"ForceOverwrite": true}' \
  "${aptly_url}/api/publish/filesystem:public-repo:linux/linux"
curl --fail --silent --show-error "${retry_flags[@]}" --digest --user "$digest_credentials" \
  --request DELETE \
  "${aptly_url}/api/files/${upload_directory}"

published=$(repository_packages)
for package in "${packages[@]}"; do
  deb_name=$(dpkg-deb --field "$package" Package)
  deb_version=$(dpkg-deb --field "$package" Version)
  deb_arch=$(dpkg-deb --field "$package" Architecture)
  checksum=$(shasum -a 256 "$package" | awk '{print $1}')
  published_checksum=$(jq -r --arg name "$deb_name" --arg version "$deb_version" --arg arch "$deb_arch" \
    '[.[] | select(.Package == $name and .Version == $version and .Architecture == $arch)] | if length == 1 then .[0].SHA256 | gsub("^ +"; "") else "" end' \
    <<<"$published")
  if [[ "$published_checksum" != "$checksum" ]]; then
    echo "Debian package did not become visible: ${deb_name} ${deb_version} ${deb_arch}" >&2
    exit 1
  fi
done
