#!/usr/bin/env bash
# See LICENSE file in the project root for license information.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
. "$ROOT/test/e2e/runtime_common.sh"
RSTREAM_BIN=$(resolve_rstream_cli "$ROOT")
PYTHON="${PYTHON:-python3}"
TIMEOUT_SECONDS="${RSTREAM_RUNTIME_TIMEOUT:-60}"
TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/rstream-go-runtime-tcp-policy.XXXXXX")
UNRESERVED_PORT="${RSTREAM_RUNTIME_UNRESERVED_TCP_PORT:-}"
TEMP_RESERVED_PORT=
PASS=0
FAIL=0

cleanup() {
  if [ -n "$TEMP_RESERVED_PORT" ]; then
    "$RSTREAM_BIN" project tcp-address release "$TEMP_RESERVED_PORT" \
      --api-url "$RSTREAM_RUNTIME_API_URL" \
      --project-id "$RSTREAM_RUNTIME_PROJECT_ID" --output json >/dev/null 2>&1 || true
  fi
  if [ -f "$TMP_DIR/settings.json" ]; then
    "$PYTHON" "$TMP_DIR/api.py" put-settings "$TMP_DIR/settings.json" >/dev/null 2>&1 || true
  fi
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

pass() {
  printf "PASS %-48s\n" "$1"
  PASS=$((PASS + 1))
}

fail() {
  printf "FAIL %-48s %s\n" "$1" "$2" >&2
  FAIL=$((FAIL + 1))
}

require_executable "$RSTREAM_BIN"
require_control_plane_api_url
require_control_plane_token
if [ -z "${RSTREAM_RUNTIME_PROJECT_ID:-}" ]; then
  printf "ERROR set RSTREAM_RUNTIME_PROJECT_ID to the Pro or Enterprise project used by this test\n" >&2
  exit 2
fi
if [ -z "${RSTREAM_CONTEXT:-}" ] && [ -z "${RSTREAM_ENGINE:-}" ]; then
  printf "ERROR RSTREAM_CONTEXT or RSTREAM_ENGINE is required\n" >&2
  exit 2
fi
require_runtime_project_engine_match "$RSTREAM_BIN"
export RSTREAM_AUTHENTICATION_TOKEN="$RSTREAM_RUNTIME_CONTROL_TOKEN"
export RSTREAM_API_URL="$RSTREAM_RUNTIME_API_URL"

if [ -z "$UNRESERVED_PORT" ]; then
  reservation=$(
    "$RSTREAM_BIN" project tcp-address reserve \
      --api-url "$RSTREAM_RUNTIME_API_URL" \
      --project-id "$RSTREAM_RUNTIME_PROJECT_ID" --output json
  )
  TEMP_RESERVED_PORT=$(
    "$PYTHON" -c 'import json, sys; print(json.load(sys.stdin)["port"])' \
      <<<"$reservation"
  )
  UNRESERVED_PORT="$TEMP_RESERVED_PORT"
  "$RSTREAM_BIN" project tcp-address release "$TEMP_RESERVED_PORT" \
    --api-url "$RSTREAM_RUNTIME_API_URL" \
    --project-id "$RSTREAM_RUNTIME_PROJECT_ID" --output json >/dev/null
  TEMP_RESERVED_PORT=
fi

cat >"$TMP_DIR/api.py" <<'PY'
import json
import os
import sys
import urllib.request

api_url = os.environ["RSTREAM_RUNTIME_API_URL"].rstrip("/")
token = os.environ["RSTREAM_RUNTIME_CONTROL_TOKEN"]
project_id = os.environ["RSTREAM_RUNTIME_PROJECT_ID"]

def control_plane_headers():
    raw = os.environ.get("RSTREAM_CONTROL_PLANE_HEADERS", "").strip()
    if not raw:
        return {}
    try:
        values = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise RuntimeError("RSTREAM_CONTROL_PLANE_HEADERS must contain a JSON object") from exc
    if not isinstance(values, dict):
        raise RuntimeError("RSTREAM_CONTROL_PLANE_HEADERS must contain a JSON object")
    for name, value in values.items():
        if not isinstance(name, str) or not isinstance(value, str):
            raise RuntimeError("RSTREAM_CONTROL_PLANE_HEADERS keys and values must be strings")
        if name.lower() == "authorization":
            raise RuntimeError("RSTREAM_CONTROL_PLANE_HEADERS cannot override authorization")
    return values

def request(method, body=None):
    data = None if body is None else json.dumps(body).encode()
    headers = control_plane_headers()
    headers.update({
        "authorization": "Bearer " + token,
        "content-type": "application/json",
    })
    req = urllib.request.Request(
        f"{api_url}/api/projects/tunnels/{project_id}/settings",
        data=data,
        method=method,
        headers=headers,
    )
    with urllib.request.urlopen(req, timeout=20) as response:
        payload = response.read().decode()
        return json.loads(payload) if payload else None

command = sys.argv[1]
if command == "get-settings":
    print(json.dumps(request("GET")))
elif command == "patch-settings":
    print(json.dumps(request("PATCH", json.loads(sys.argv[2]))))
elif command == "put-settings":
    with open(sys.argv[2], encoding="utf-8") as stream:
        request("PUT", json.load(stream))
else:
    raise SystemExit(f"unknown command: {command}")
PY

set_settings() {
  "$PYTHON" "$TMP_DIR/api.py" patch-settings "$1" >/dev/null
}

expect_rejected() {
  local label=$1 expected=$2
  shift 2
  local log="$TMP_DIR/${label// /-}.log"
  "$RSTREAM_BIN" forward 127.0.0.1:9 --tcp --no-retry --output text "$@" >"$log" 2>&1 &
  local pid=$! deadline=$((SECONDS + TIMEOUT_SECONDS)) status=0
  while kill -0 "$pid" 2>/dev/null && [ "$SECONDS" -lt "$deadline" ]; do
    sleep 0.2
  done
  if kill -0 "$pid" 2>/dev/null; then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
    fail "$label" "tunnel was accepted"
    return
  fi
  set +e
  wait "$pid"
  status=$?
  set -e
  if [ "$status" -eq 0 ]; then
    fail "$label" "command unexpectedly succeeded"
  elif ! grep -Fq "$expected" "$log"; then
    fail "$label" "expected error not found: $expected"
    sed -n '1,80p' "$log" >&2
  else
    pass "$label"
  fi
}

"$PYTHON" "$TMP_DIR/api.py" get-settings >"$TMP_DIR/settings.json"
set_settings '{"publishedTcpEnabled":false,"publicAccessPolicy":"allowed"}'
expect_rejected "published TCP explicitly disabled" "Project security policy forbids published TCP tunnels."
set_settings '{"publishedTcpEnabled":true,"publicAccessPolicy":"forbidden"}'
expect_rejected "forbidden public access policy" "Project public access policy forbids published tunnels."
set_settings '{"publishedTcpEnabled":true,"publicAccessPolicy":"auth-required"}'
expect_rejected "auth-required incompatibility" "Project public access policy requires edge authentication and is incompatible with published TCP tunnels."
set_settings '{"publishedTcpEnabled":true,"publicAccessPolicy":"allowed"}'
expect_rejected "invalid reserved TCP port" "published TCP port must be between 1 and 65535" --tcp-port 65536
expect_rejected "unreserved TCP port" "Reserved TCP address is not available for this project." --tcp-port "$UNRESERVED_PORT"

printf "\nResults: %d passed, %d failed\n" "$PASS" "$FAIL"
if [ "$FAIL" -ne 0 ]; then
  exit 1
fi
