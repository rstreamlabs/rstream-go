#!/usr/bin/env bash
# See LICENSE file in the project root for license information.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
. "$ROOT/test/e2e/runtime_common.sh"
BIN="${BIN:-$ROOT/out/test}"
RSTREAM_BIN=$(resolve_rstream_cli "$ROOT")
PYTHON="${PYTHON:-python3}"
TIMEOUT_SECONDS="${RSTREAM_RUNTIME_TIMEOUT:-60}"
NAME_PREFIX="${RSTREAM_RUNTIME_NAME_PREFIX:-runtime-proxy-$$}"
ENGINE="${RSTREAM_RUNTIME_PROXY_ENGINE:-${RSTREAM_ENGINE:-}}"
TOKEN="${RSTREAM_AUTHENTICATION_TOKEN:-}"
MASQUE_CERT="${RSTREAM_RUNTIME_MASQUE_PROXY_CERT_FILE:-}"
MASQUE_KEY="${RSTREAM_RUNTIME_MASQUE_PROXY_KEY_FILE:-}"
MASQUE_HOST="${RSTREAM_RUNTIME_MASQUE_PROXY_HOST:-masque.c.localhost.rstream.io}"
TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/rstream-go-proxy-runtime.XXXXXX")
PASS=0
FAIL=0
PIDS=()
STARTED_PID=""
STARTED_VALUE=""

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
require_executable "$BIN/stream/client"
require_executable "$BIN/proxy/server"

if [ -z "$ENGINE" ]; then
  printf "ERROR set RSTREAM_RUNTIME_PROXY_ENGINE or RSTREAM_ENGINE\n" >&2
  exit 2
fi
if [ -z "$TOKEN" ]; then
  printf "ERROR set RSTREAM_AUTHENTICATION_TOKEN\n" >&2
  exit 2
fi

wait_ready() {
  local pid=$1 log=$2 label=$3
  local deadline=$((SECONDS + TIMEOUT_SECONDS))
  local ready
  while [ "$SECONDS" -lt "$deadline" ]; do
    if ready=$(ready_value_from_log "$log") && [ -n "$ready" ]; then
      printf '%s\n' "$ready"
      return 0
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      printf "FAIL %-36s process exited early\n" "$label" >&2
      tail -40 "$log" >&2 || true
      return 1
    fi
    sleep 0.2
  done
  printf "FAIL %-36s process did not become ready\n" "$label" >&2
  tail -40 "$log" >&2 || true
  return 1
}

start_proxy() {
  local label=$1 mode=$2
  local log="$TMP_DIR/proxy-$label.log"
  case "$mode" in
  http | socks5)
    "$BIN/proxy/server" --mode "$mode" >"$log" 2>&1 &
    ;;
  masque)
    if [ -z "$MASQUE_CERT" ] || [ -z "$MASQUE_KEY" ]; then
      printf "ERROR MASQUE proxy case requires RSTREAM_RUNTIME_MASQUE_PROXY_CERT_FILE and RSTREAM_RUNTIME_MASQUE_PROXY_KEY_FILE\n" >&2
      exit 2
    fi
    "$BIN/proxy/server" --mode masque --public-host "$MASQUE_HOST" --cert "$MASQUE_CERT" --key "$MASQUE_KEY" >"$log" 2>&1 &
    ;;
  *)
    printf "ERROR unknown proxy mode: %s\n" "$mode" >&2
    exit 2
    ;;
  esac
  local pid=$!
  PIDS+=("$pid")
  STARTED_PID=$pid
  if ! STARTED_VALUE=$(wait_ready "$pid" "$log" "$label"); then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
    STARTED_VALUE=
    return 1
  fi
}

start_upstream() {
  local label=$1
  local log="$TMP_DIR/upstream-$label.log"
  "$PYTHON" "$ROOT/test/e2e/runtime_harness.py" serve tcp >"$log" 2>&1 &
  local pid=$!
  PIDS+=("$pid")
  STARTED_PID=$pid
  if ! STARTED_VALUE=$(wait_ready "$pid" "$log" "$label"); then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
    STARTED_VALUE=
    return 1
  fi
}

write_config() {
  local path=$1 use_quic=$2 proxy_key=$3 proxy_url=$4
  local proxy_tls=""
  if [ "$use_quic" = "true" ] && [ "$proxy_key" = "http" ] && [ -n "$MASQUE_CERT" ]; then
    proxy_tls="
        tls:
          caFile: \"$MASQUE_CERT\""
  fi
  cat >"$path" <<EOF
version: 1
defaults:
  context:
    name: proxy-e2e
contexts:
  - name: proxy-e2e
    engine: "$ENGINE"
    auth:
      token:
        storage:
          kind: inline
          value: "$TOKEN"
    transport:
      useQuic: $use_quic
      proxy:
        $proxy_key: "$proxy_url"
$proxy_tls
EOF
}

extract_forward_ready() {
  "$PYTHON" - "$1" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    for line in stream:
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        if event.get("status") == "online":
            sys.exit(0)
sys.exit(1)
PY
}

print_case_logs() {
  local label=$1 component
  for component in proxy upstream forward; do
    printf "\n%s log:\n" "$component" >&2
    tail -40 "$TMP_DIR/$component-$label.log" >&2 || true
  done
}

start_forward() {
  local label=$1 config_path=$2 upstream=$3 tunnel_prefix=$4
  local log="$TMP_DIR/forward-$label.log"
  RSTREAM_CONFIG="$config_path" RSTREAM_CONTEXT=proxy-e2e \
    "$RSTREAM_BIN" forward "$upstream" --output json --no-retry \
    --bytestream --no-publish --name "$tunnel_prefix-plain" >"$log" 2>&1 &
  local pid=$!
  PIDS+=("$pid")
  local deadline=$((SECONDS + TIMEOUT_SECONDS))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if extract_forward_ready "$log"; then
      STARTED_PID=$pid
      return 0
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      printf "FAIL %-36s forward exited early\n" "$label" >&2
      tail -40 "$log" >&2 || true
      return 1
    fi
    sleep 0.2
  done
  printf "FAIL %-36s forward did not become ready\n" "$label" >&2
  tail -40 "$log" >&2 || true
  return 1
}

run_case() {
  local label=$1 proxy_mode=$2 use_quic=$3 proxy_key=$4
  local proxy_url proxy_pid upstream upstream_pid config_path tunnel_prefix forward_pid
  start_proxy "$label" "$proxy_mode" || return 1
  proxy_url=$STARTED_VALUE
  proxy_pid=$STARTED_PID
  start_upstream "$label" || return 1
  upstream=$STARTED_VALUE
  upstream_pid=$STARTED_PID
  config_path="$TMP_DIR/config-$label.yaml"
  tunnel_prefix="$NAME_PREFIX-$label"
  write_config "$config_path" "$use_quic" "$proxy_key" "$proxy_url"
  if ! start_forward "$label" "$config_path" "$upstream" "$tunnel_prefix"; then
    printf "FAIL %-36s\n" "$label" >&2
    FAIL=$((FAIL + 1))
    return
  fi
  forward_pid=$STARTED_PID
  if RSTREAM_CONFIG="$config_path" RSTREAM_CONTEXT=proxy-e2e "$BIN/stream/client" --variant plain --tunnel "$tunnel_prefix" &&
    { [ "$proxy_mode" != "masque" ] || sleep 35; } &&
    RSTREAM_CONFIG="$config_path" RSTREAM_CONTEXT=proxy-e2e "$BIN/stream/client" --variant plain --tunnel "$tunnel_prefix"; then
    printf "PASS %-36s\n" "$label"
    PASS=$((PASS + 1))
  else
    printf "FAIL %-36s\n" "$label" >&2
    print_case_logs "$label"
    FAIL=$((FAIL + 1))
  fi
  kill "$forward_pid" 2>/dev/null || true
  wait "$forward_pid" 2>/dev/null || true
  kill "$upstream_pid" "$proxy_pid" 2>/dev/null || true
  wait "$upstream_pid" "$proxy_pid" 2>/dev/null || true
}

run_case "tls-http-proxy" http false http
run_case "tls-socks5-proxy" socks5 false socks5
run_case "quic-masque-proxy" masque true http
run_case "quic-socks5-proxy" socks5 true socks5

printf "\nResults: %d passed, %d failed\n" "$PASS" "$FAIL"
if [ "$FAIL" -ne 0 ]; then
  exit 1
fi
