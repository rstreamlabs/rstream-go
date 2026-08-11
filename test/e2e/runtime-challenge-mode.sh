#!/usr/bin/env bash
# See LICENSE file in the project root for license information.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
. "$ROOT/test/e2e/runtime_common.sh"
BIN="${BIN:-$ROOT/out/test}"
RSTREAM_BIN=$(resolve_rstream_cli "$ROOT")
PYTHON="${PYTHON:-python3}"
TIMEOUT_SECONDS="${RSTREAM_RUNTIME_TIMEOUT:-60}"
NAME_PREFIX="${RSTREAM_RUNTIME_NAME_PREFIX:-runtime-challenge-$$}"
require_control_plane_api_url
API_URL="${RSTREAM_RUNTIME_API_URL}"
TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/rstream-go-challenge.XXXXXX")
PASS=0
FAIL=0
PIDS=()
UPSTREAM_ADDR=
FORWARD_PID=
FORWARDING=
FORWARD_LOG=

cleanup() {
  local pid
  for pid in "${PIDS[@]:-}"; do
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  done
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

require_executable "$RSTREAM_BIN"
require_executable "$BIN/http/client"

wait_ready() {
  local pid=$1 log=$2 label=$3
  local deadline=$((SECONDS + TIMEOUT_SECONDS))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if grep -q "^READY " "$log" 2>/dev/null; then
      awk '/^READY / {print $2; exit}' "$log"
      return 0
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      printf "FAIL %-42s process exited early\n" "$label" >&2
      tail -20 "$log" >&2 || true
      return 1
    fi
    sleep 0.2
  done
  printf "FAIL %-42s process did not become ready\n" "$label" >&2
  tail -20 "$log" >&2 || true
  return 1
}

extract_forwarding() {
  "$PYTHON" - "$1" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    for line in stream:
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        if event.get("status") == "online" and event.get("forwarding"):
            print(event["forwarding"])
            sys.exit(0)
sys.exit(1)
PY
}

start_upstream() {
  local label=$1
  local log="$TMP_DIR/upstream-$label.log"
  "$PYTHON" "$ROOT/test/e2e/runtime_harness.py" serve http >"$log" 2>&1 &
  local pid=$!
  PIDS+=("$pid")
  UPSTREAM_ADDR=$(wait_ready "$pid" "$log" "$label")
}

start_challenge_forward() {
  local label=$1
  FORWARD_LOG="$TMP_DIR/forward-$label.log"
  : >"$FORWARD_LOG"
  "$RSTREAM_BIN" forward "$UPSTREAM_ADDR" --output json --no-retry \
    --bytestream --publish --http --challenge-mode \
    --name "$NAME_PREFIX-$label" >"$FORWARD_LOG" 2>&1 &
  FORWARD_PID=$!
  PIDS+=("$FORWARD_PID")

  local deadline=$((SECONDS + TIMEOUT_SECONDS))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if FORWARDING=$(extract_forwarding "$FORWARD_LOG"); then
      return 0
    fi
    if ! kill -0 "$FORWARD_PID" 2>/dev/null; then
      printf "FAIL %-42s forward exited early\n" "$label" >&2
      tail -40 "$FORWARD_LOG" >&2 || true
      return 1
    fi
    if grep -Eiq "tunnel creation failed|connection failed|failed to create tunnel" "$FORWARD_LOG"; then
      printf "FAIL %-42s forward reported an error\n" "$label" >&2
      tail -40 "$FORWARD_LOG" >&2 || true
      return 1
    fi
    sleep 0.2
  done
  printf "FAIL %-42s forward did not become ready\n" "$label" >&2
  tail -40 "$FORWARD_LOG" >&2 || true
  return 1
}

stop_pid() {
  local pid=$1
  kill "$pid" 2>/dev/null || true
  wait "$pid" 2>/dev/null || true
}

run_case() {
  local label=$1
  shift
  if "$@"; then
    printf "PASS %-42s\n" "$label"
    PASS=$((PASS + 1))
  else
    printf "FAIL %-42s\n" "$label" >&2
    FAIL=$((FAIL + 1))
  fi
}

case_challenge_api_route_exists() {
  local headers status
  headers="$TMP_DIR/challenge-api-headers.txt"
  control_plane_curl_headers
  status=$(curl -sS -D "$headers" -o "$TMP_DIR/challenge-api-body.txt" -w "%{http_code}" \
    -X POST "$API_URL/api/rstream/challenge/requests" \
    -H "Authorization: Bearer invalid" \
    -H "Content-Type: application/json" \
    "${CONTROL_PLANE_CURL_ARGS[@]}" \
    --data '{"returnUrl":"https://app.example.com/api/challenge/callback","destination":[{"key":"targetUrl","value":"https://app.example.com/"},{"key":"tunnelHost","value":"app.example.com"}]}' || true)
  if [ "$status" != "401" ]; then
    printf "expected challenge API route to exist and reject bad auth with 401, got %s\n" "$status" >&2
    sed -n '1,20p' "$TMP_DIR/challenge-api-body.txt" >&2 || true
    return 1
  fi
  if ! grep -Fqi 'content-type: application/json' "$headers" || ! grep -Fq '"error":"Unauthorized."' "$TMP_DIR/challenge-api-body.txt"; then
    printf "expected challenge API application rejection, got an upstream or malformed 401\n" >&2
    sed -n '1,20p' "$headers" >&2 || true
    sed -n '1,20p' "$TMP_DIR/challenge-api-body.txt" >&2 || true
    return 1
  fi
}

case_http_challenge_h2_redirects() {
  local headers status expected
  headers="$TMP_DIR/challenge-h2-headers.txt"
  expected="${API_URL%/}/rstream/challenge?request="
  start_upstream "challenge-h2"
  start_challenge_forward "challenge-h2"
  status=$(curl -sk --http2 -D "$headers" -o /dev/null -w "%{http_code}" "$FORWARDING/ping" || true)
  stop_pid "$FORWARD_PID"
  if [ "$status" != "302" ]; then
    printf "expected challenge redirect status 302, got %s\n" "$status" >&2
    cat "$headers" >&2 || true
    return 1
  fi
  if ! grep -Fqi "location: $expected" "$headers"; then
    printf "expected Location to contain %s\n" "$expected" >&2
    cat "$headers" >&2 || true
    return 1
  fi
}

case_http_challenge_h3_redirects() {
  local expected status=0
  expected="${API_URL%/}/rstream/challenge?request="
  start_upstream "challenge-h3"
  start_challenge_forward "challenge-h3"
  "$BIN/http/client" \
    --h3-redirect-url "$FORWARDING/ping" \
    --location-contains "$expected" || status=$?
  stop_pid "$FORWARD_PID"
  return "$status"
}

run_case "challenge/api route" case_challenge_api_route_exists
run_case "challenge/http h2 redirect" case_http_challenge_h2_redirects
run_case "challenge/http h3 redirect" case_http_challenge_h3_redirects

printf "\nResults: %d passed, %d failed\n" "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
