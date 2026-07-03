#!/usr/bin/env bash
# See LICENSE file in the project root for license information.

find_rstream_cli() {
  local root=$1
  local candidate

  if [ -n "${RSTREAM_BIN:-}" ]; then
    printf "%s\n" "$RSTREAM_BIN"
    return
  fi

  while IFS= read -r candidate; do
    if [ -x "$candidate" ]; then
      printf "%s\n" "$candidate"
      return
    fi
  done < <(find "$root/out/cmd/rstream" -type f -name rstream 2>/dev/null | sort)

  if command -v rstream >/dev/null 2>&1; then
    command -v rstream
  fi
}

resolve_rstream_cli() {
  local root=$1
  local binary

  binary=$(find_rstream_cli "$root" || true)
  if [ -z "$binary" ] || [ ! -x "$binary" ]; then
    printf "ERROR missing rstream binary; set RSTREAM_BIN or build the CLI first\n" >&2
    exit 2
  fi
  printf "%s\n" "$binary"
}

require_executable() {
  if [ ! -x "$1" ]; then
    printf "ERROR missing executable: %s\n" "$1" >&2
    exit 2
  fi
}

require_control_plane_api_url() {
  if [ -z "${RSTREAM_RUNTIME_API_URL:-}" ]; then
    printf "ERROR set RSTREAM_RUNTIME_API_URL to the Control plane API URL for this test\n" >&2
    printf "This runtime suite is not engine-only; it creates or verifies Control plane resources.\n" >&2
    exit 2
  fi
}

require_control_plane_token() {
  if [ -z "${RSTREAM_RUNTIME_CONTROL_TOKEN:-}" ]; then
    printf "ERROR set RSTREAM_RUNTIME_CONTROL_TOKEN to a PAT with the permissions required by this test\n" >&2
    printf "Do not rely on the engine context token for Control plane setup checks.\n" >&2
    exit 2
  fi
}
