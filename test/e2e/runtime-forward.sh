#!/usr/bin/env bash
# See LICENSE file in the project root for license information.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
BIN="${BIN:-$ROOT/out/test}"
RSTREAM_BIN="${RSTREAM_BIN:-$ROOT/out/cmd/rstream/dev/main/macos/arm64/release/bin/rstream}"
PYTHON="${PYTHON:-python3}"
TIMEOUT_SECONDS="${RSTREAM_RUNTIME_TIMEOUT:-60}"
NAME_PREFIX="${RSTREAM_RUNTIME_NAME_PREFIX:-runtime-forward-$$}"
TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/rstream-go-runtime.XXXXXX")
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

if [ "${1:-}" = "--quic-transport" ]; then
  export RSTREAM_QUIC_TRANSPORT=1
  shift
fi

require_executable() {
  if [ ! -x "$1" ]; then
    printf "ERROR missing executable: %s\n" "$1" >&2
    exit 2
  fi
}

require_executable "$RSTREAM_BIN"
require_executable "$BIN/stream/client"
require_executable "$BIN/http/client"
require_executable "$BIN/datagram/client"

make_cert() {
  openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
    -subj "/CN=localhost" \
    -keyout "$TMP_DIR/upstream.key" \
    -out "$TMP_DIR/upstream.crt" >/dev/null 2>&1
}

wait_ready() {
  local pid=$1 log=$2 label=$3
  local deadline=$((SECONDS + TIMEOUT_SECONDS))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if grep -q "^READY " "$log" 2>/dev/null; then
      awk '/^READY / {print $2; exit}' "$log"
      return 0
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      printf "FAIL %-42s upstream exited early\n" "$label" >&2
      tail -20 "$log" >&2 || true
      return 1
    fi
    sleep 0.2
  done
  printf "FAIL %-42s upstream did not become ready\n" "$label" >&2
  tail -20 "$log" >&2 || true
  return 1
}

start_upstream() {
  local label=$1 mode=$2
  local log="$TMP_DIR/upstream-$label.log"
  case "$mode" in
    tcp|http|udp)
      "$PYTHON" "$ROOT/test/e2e/runtime_harness.py" serve "$mode" >"$log" 2>&1 &
      ;;
    tls)
      "$PYTHON" "$ROOT/test/e2e/runtime_harness.py" serve tls \
        --cert "$TMP_DIR/upstream.crt" \
        --key "$TMP_DIR/upstream.key" >"$log" 2>&1 &
      ;;
    *)
      printf "ERROR unknown upstream mode: %s\n" "$mode" >&2
      exit 2
      ;;
  esac
  local pid=$!
  PIDS+=("$pid")
  UPSTREAM_ADDR=$(wait_ready "$pid" "$log" "$label")
}

extract_forwarding() {
  "$PYTHON" - "$1" "$2" <<'PY'
import json
import sys

path = sys.argv[1]
require_forwarding = sys.argv[2] == "1"
with open(path, encoding="utf-8") as stream:
    for line in stream:
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        if event.get("status") == "online":
            forwarding = event.get("forwarding", "")
            if require_forwarding and not forwarding:
                continue
            print(forwarding)
            sys.exit(0)
sys.exit(1)
PY
}

start_forward() {
  local label=$1 target=$2 need_forwarding=$3
  shift 3
  FORWARD_LOG="$TMP_DIR/forward-$label.log"
  : >"$FORWARD_LOG"
  "$RSTREAM_BIN" forward "$target" --output json --no-retry "$@" >"$FORWARD_LOG" 2>&1 &
  FORWARD_PID=$!
  PIDS+=("$FORWARD_PID")

  local deadline=$((SECONDS + TIMEOUT_SECONDS))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if FORWARDING=$(extract_forwarding "$FORWARD_LOG" "$need_forwarding"); then
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

case_private_plain() {
  local upstream
  local rc=0
  start_upstream "private-plain" tcp
  upstream=$UPSTREAM_ADDR
  local tunnel_prefix="$NAME_PREFIX-private"
  start_forward "private-plain" "$upstream" 0 \
    --bytestream --no-publish --name "$tunnel_prefix-plain"
  "$BIN/stream/client" --variant plain --tunnel "$tunnel_prefix" || rc=$?
  stop_pid "$FORWARD_PID"
  return "$rc"
}

case_tls_terminated() {
  local upstream
  local rc=0
  start_upstream "tls-terminated" tcp
  upstream=$UPSTREAM_ADDR
  start_forward "tls-terminated" "$upstream" 1 \
    --bytestream --publish --tls --tls-mode terminated \
    --tls-alpn rstream-runtime-stream --name "$NAME_PREFIX-tls-terminated"
  "$BIN/stream/client" --variant tls --addr "$FORWARDING" --tls-alpn rstream-runtime-stream || rc=$?
  stop_pid "$FORWARD_PID"
  return "$rc"
}

case_tls_upstream_tls() {
  local upstream
  local rc=0
  start_upstream "tls-upstream-tls" tls
  upstream=$UPSTREAM_ADDR
  start_forward "tls-upstream-tls" "$upstream" 1 \
    --bytestream --publish --tls --tls-mode terminated --upstream-tls \
    --tls-alpn rstream-runtime-stream --name "$NAME_PREFIX-tls-upstream"
  "$BIN/stream/client" --variant tls --addr "$FORWARDING" --tls-alpn rstream-runtime-stream || rc=$?
  stop_pid "$FORWARD_PID"
  return "$rc"
}

case_tls_passthrough() {
  local upstream
  local rc=0
  start_upstream "tls-passthrough" tls
  upstream=$UPSTREAM_ADDR
  start_forward "tls-passthrough" "$upstream" 1 \
    --bytestream --publish --tls --tls-mode passthrough \
    --name "$NAME_PREFIX-tls-passthrough"
  "$BIN/stream/client" --variant tls --addr "$FORWARDING" || rc=$?
  stop_pid "$FORWARD_PID"
  return "$rc"
}

case_http_h1() {
  local upstream forwarding_hostport
  local rc=0
  start_upstream "http-h1" http
  upstream=$UPSTREAM_ADDR
  start_forward "http-h1" "$upstream" 1 \
    --bytestream --publish --http --name "$NAME_PREFIX-http-h1"
  forwarding_hostport=$("$PYTHON" "$ROOT/test/e2e/runtime_harness.py" hostport --addr "$FORWARDING")
  "$BIN/http/client" --upstream h1 --addr "$forwarding_hostport" || rc=$?
  stop_pid "$FORWARD_PID"
  return "$rc"
}

case_dtls() {
  local upstream
  local rc=0
  start_upstream "dtls" udp
  upstream=$UPSTREAM_ADDR
  start_forward "dtls" "$upstream" 1 \
    --datagram --publish --dtls --tls-alpn rstream-runtime-dtls \
    --name "$NAME_PREFIX-dtls"
  "$BIN/datagram/client" --variant dtls --addr "$FORWARDING" --tls-alpn rstream-runtime-dtls || rc=$?
  stop_pid "$FORWARD_PID"
  return "$rc"
}

make_cert
run_case "forward/private bytestream plain" case_private_plain
run_case "forward/tls terminated" case_tls_terminated
run_case "forward/tls upstream tls" case_tls_upstream_tls
run_case "forward/tls passthrough" case_tls_passthrough
run_case "forward/http h1" case_http_h1
run_case "forward/dtls" case_dtls

printf "\nResults: %d passed, %d failed\n" "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
