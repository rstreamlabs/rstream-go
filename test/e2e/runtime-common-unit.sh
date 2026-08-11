#!/usr/bin/env bash
# See LICENSE file in the project root for license information.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
. "$ROOT/test/e2e/runtime_common.sh"
PASS=0
TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/rstream-runtime-common.XXXXXX")
trap 'rm -rf "$TMP_DIR"' EXIT

expect_equal() {
  local label=$1 actual=$2 expected=$3
  if [ "$actual" != "$expected" ]; then
    printf 'FAIL %s: got %q, want %q\n' "$label" "$actual" "$expected" >&2
    exit 1
  fi
  PASS=$((PASS + 1))
}

unset RSTREAM_CONTROL_PLANE_HEADERS
control_plane_curl_headers
expect_equal "empty control-plane headers" "${#CONTROL_PLANE_CURL_ARGS[@]}" 0
RSTREAM_CONTROL_PLANE_HEADERS='{"x-test-header":"test value"}' control_plane_curl_headers
expect_equal "control-plane header argument count" "${#CONTROL_PLANE_CURL_ARGS[@]}" 2
expect_equal "control-plane header flag" "${CONTROL_PLANE_CURL_ARGS[0]}" -H
expect_equal "control-plane header value" "${CONTROL_PLANE_CURL_ARGS[1]}" "x-test-header: test value"
if RSTREAM_CONTROL_PLANE_HEADERS='{"authorization":"forbidden"}' control_plane_curl_headers 2>/dev/null; then
  printf 'FAIL authorization header override was accepted\n' >&2
  exit 1
fi
PASS=$((PASS + 1))
if RSTREAM_CONTROL_PLANE_HEADERS='not-json' control_plane_curl_headers 2>/dev/null; then
  printf 'FAIL malformed control-plane headers were accepted\n' >&2
  exit 1
fi
PASS=$((PASS + 1))
mkdir -p "$TMP_DIR/out/cmd/rstream/a" "$TMP_DIR/out/cmd/rstream/b"
printf '#!/bin/sh\nexit 126\n' >"$TMP_DIR/out/cmd/rstream/a/rstream"
printf '%s\n' '#!/bin/sh' "[ \"\$1\" = --help ]" >"$TMP_DIR/out/cmd/rstream/b/rstream"
chmod +x "$TMP_DIR/out/cmd/rstream/a/rstream" "$TMP_DIR/out/cmd/rstream/b/rstream"
expect_equal \
  "skip unusable rstream binary" \
  "$(RSTREAM_BIN='' find_rstream_cli "$TMP_DIR")" \
  "$TMP_DIR/out/cmd/rstream/b/rstream"
RSTREAM_E2E_DOWNSTREAM_HOST=203.0.113.10 expect_equal \
  "downstream host rewrite" \
  "$(RSTREAM_E2E_DOWNSTREAM_HOST=203.0.113.10 rewrite_downstream_address 'https://edge.example.test:443/path?q=1')" \
  "https://203.0.113.10:443/path?q=1"
RSTREAM_E2E_DOWNSTREAM_PORT_MAP=443:8443 expect_equal \
  "downstream port rewrite" \
  "$(RSTREAM_E2E_DOWNSTREAM_PORT_MAP=443:8443 rewrite_downstream_address 'edge.example.test:443')" \
  "edge.example.test:8443"
printf 'Runtime common unit tests: %d passed\n' "$PASS"
