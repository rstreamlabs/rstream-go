#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 5 ]]; then
  echo "usage: $0 <package> <channel> <version> <base-url> <token>" >&2
  exit 1
fi

package_name=$1
channel=$2
version=$3
base_url=${4%/}
token=$5
release_root="out/cmd/${package_name}/${channel}/${version}"
retry_flags=(--retry 5 --retry-all-errors --retry-delay 2 --connect-timeout 30)

query_package() {
  local os=$1
  local arch=$2
  local filename=$3

  curl --fail --silent --show-error "${retry_flags[@]}" --get \
    --data-urlencode "name=${package_name}" \
    --data-urlencode "version=${version}" \
    --data-urlencode "channel=${channel}" \
    --data-urlencode "os=${os}" \
    --data-urlencode "arch=${arch}" \
    --data-urlencode "filename=${filename}" \
    "${base_url}/api/packages"
}

verify_download() {
  local package_id=$1
  local expected_checksum=$2
  local actual_checksum

  if ! actual_checksum="$(
    curl --fail --silent --show-error --location "${retry_flags[@]}" \
      "${base_url}/api/packages/${package_id}/download" \
      | shasum -a 256 \
      | awk '{print $1}'
  )"; then
    return 1
  fi
  if [[ "$actual_checksum" != "$expected_checksum" ]]; then
    echo "published package checksum mismatch for ${package_id}" >&2
    return 2
  fi
}

upload_archive() {
  local expected_id=$1
  local headers response_body signed_url upload_status

  headers=$(mktemp)
  response_body=$(mktemp)
  if ! curl --fail --silent --show-error "${retry_flags[@]}" \
    --header "Authorization: Bearer ${token}" \
    --dump-header "$headers" \
    --output "$response_body" \
    --request PUT \
    --get \
    --data-urlencode "name=${package_name}" \
    --data-urlencode "version=${version}" \
    --data-urlencode "channel=${channel}" \
    --data-urlencode "os=${os}" \
    --data-urlencode "arch=${arch}" \
    --data-urlencode "filename=${filename}" \
    --data-urlencode "checksum=${checksum}" \
    --data-urlencode "storageType=s3" \
    "${base_url}/api/packages"; then
    rm -f "$headers" "$response_body"
    return 1
  fi
  package_id=$(awk 'tolower($1) == "x-package-id:" { gsub("\\r", "", $2); print $2 }' "$headers")
  signed_url=$(awk 'tolower($1) == "location:" { gsub("\\r", "", $2); print $2 }' "$headers")
  rm -f "$headers"
  if [[ -z "$package_id" || -z "$signed_url" ||
    ( -n "$expected_id" && "$package_id" != "$expected_id" ) ]]; then
    echo "package API returned an unexpected upload target for ${filename}" >&2
    rm -f "$response_body"
    return 1
  fi
  # A failed download can mean an interrupted upload, denied access, or an
  # outage. Only create a missing object; never overwrite one based on that
  # ambiguous failure. A concurrent upload must still pass download verification.
  if ! upload_status=$(curl --fail --silent --show-error "${retry_flags[@]}" \
    --header "Content-Type: application/octet-stream" \
    --header "If-None-Match: *" \
    --output "$response_body" \
    --write-out '%{http_code}' \
    --upload-file "$archive" \
    --request PUT \
    "$signed_url"); then
    if [[ "$upload_status" != 412 ]]; then
      rm -f "$response_body"
      return 1
    fi
  fi
  rm -f "$response_body"
}

mapfile -d '' archives < <(
  find "$release_root" -mindepth 3 -maxdepth 3 -type f \
    \( -name "${package_name}-${version}-*.tar.gz" -o -name "${package_name}-${version}-*.zip" \) \
    -print0 \
    | LC_ALL=C sort -z
)

if ((${#archives[@]} == 0)); then
  echo "no release archives found below ${release_root}" >&2
  exit 1
fi

for archive in "${archives[@]}"; do
  relative=${archive#"${release_root}/"}
  os=${relative%%/*}
  remainder=${relative#*/}
  arch=${remainder%%/*}
  filename=${archive##*/}
  checksum=$(shasum -a 256 "$archive" | awk '{print $1}')
  response=$(query_package "$os" "$arch" "$filename")
  total=$(jq -r '.total' <<<"$response")

  if [[ "$total" == 1 ]]; then
    package_id=$(jq -r '.packages[0].id' <<<"$response")
    published_checksum=$(jq -r '.packages[0].checksum' <<<"$response")
    if [[ "$published_checksum" != "$checksum" ]]; then
      echo "immutable package conflict for ${filename}" >&2
      echo "expected ${checksum}, found ${published_checksum}" >&2
      exit 1
    fi
    echo "Package ${filename} already exists with the expected checksum"
    if verify_download "$package_id" "$checksum"; then
      :
    else
      download_status=$?
      if [[ "$download_status" == 2 ]]; then
        exit 1
      fi
      upload_archive "$package_id"
    fi
  elif [[ "$total" == 0 ]]; then
    upload_archive ""
  else
    echo "package query returned ${total} records for ${filename}" >&2
    exit 1
  fi

  for _ in {1..12}; do
    response=$(query_package "$os" "$arch" "$filename")
    if [[ "$(jq -r '.total' <<<"$response")" == 1 ]]; then
      break
    fi
    sleep 5
  done
  if [[ "$(jq -r '.total' <<<"$response")" != 1 ]]; then
    echo "published package did not become visible: ${filename}" >&2
    exit 1
  fi
  package_id=$(jq -r '.packages[0].id' <<<"$response")
  published_checksum=$(jq -r '.packages[0].checksum' <<<"$response")
  if [[ "$published_checksum" != "$checksum" ]]; then
    echo "published package metadata checksum mismatch for ${filename}" >&2
    exit 1
  fi
  verify_download "$package_id" "$checksum"
  printf 'name:%s\nid:%s\nversion:%s\nchannel:%s\nos:%s\narch:%s\nfilename:%s\nchecksum:%s\n' \
    "$package_name" "$package_id" "$version" "$channel" "$os" "$arch" "$filename" "$checksum" \
    >"${archive}.info"
done
