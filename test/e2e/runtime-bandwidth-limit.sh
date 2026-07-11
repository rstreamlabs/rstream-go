#!/usr/bin/env bash
# Validate per-tunnel bandwidth limits against a live EE engine.
#
# Configure every plan used by the test engine with the same 1 Mbps upstream
# and downstream limit before starting it. The defaults below transfer 256 kB
# in each direction and expect the 100 ms token-bucket burst.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
BIN="${BIN:-$ROOT/out/test}"
MIN_DURATION="${RSTREAM_BANDWIDTH_MIN_DURATION:-1500ms}"
MAX_DURATION="${RSTREAM_BANDWIDTH_MAX_DURATION:-3250ms}"
PAYLOAD_SIZE="${RSTREAM_BANDWIDTH_PAYLOAD_SIZE:-1000}"
ITERATIONS="${RSTREAM_BANDWIDTH_ITERATIONS:-256}"
SERVER_PID=
SERVER_LOG=
PASS=0

cleanup() {
  if [ -n "${SERVER_PID:-}" ]; then
    kill "$SERVER_PID" 2>/dev/null || true
    for _ in $(seq 1 50); do
      kill -0 "$SERVER_PID" 2>/dev/null || break
      sleep 0.02
    done
    kill -9 "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
    SERVER_PID=
  fi
  if [ -n "${SERVER_LOG:-}" ]; then
    rm -f "$SERVER_LOG"
  fi
}
trap cleanup EXIT

for executable in "$BIN/bandwidth/server" "$BIN/bandwidth/client"; do
  if [ ! -x "$executable" ]; then
    printf 'ERROR missing %s (run: make test-bins)\n' "$executable" >&2
    exit 2
  fi
done
if [ -z "${RSTREAM_CONTEXT:-}" ] && [ -z "${RSTREAM_ENGINE:-}" ]; then
  printf 'ERROR RSTREAM_CONTEXT or RSTREAM_ENGINE is required\n' >&2
  exit 2
fi

run_case() {
  local label="$1" type="$2" owner_transport="$3" dialer_transport="$4" expected_path="$5" guaranteed="$6"
  local name="bandwidth-${label}-$$"
  cleanup
  SERVER_LOG=$(mktemp)
  local server_args=(--type "$type" --name "$name")
  if [ "$guaranteed" = true ]; then
    server_args+=(--datagram-guaranteed-delivery)
  fi
  RSTREAM_TUNNEL_TRANSPORT="$owner_transport" "$BIN/bandwidth/server" "${server_args[@]}" >"$SERVER_LOG" 2>&1 &
  SERVER_PID=$!
  local ready=false
  for _ in $(seq 1 100); do
    if grep -q '^READY ' "$SERVER_LOG"; then
      ready=true
      break
    fi
    if ! kill -0 "$SERVER_PID" 2>/dev/null; then
      cat "$SERVER_LOG" >&2
      return 1
    fi
    sleep 0.1
  done
  if [ "$ready" != true ]; then
    cat "$SERVER_LOG" >&2
    printf 'FAIL %-32s server readiness timeout\n' "$label" >&2
    return 1
  fi
  local client_args=(
    --type "$type"
    --name "$name"
    --payload-size "$PAYLOAD_SIZE"
    --iterations "$ITERATIONS"
    --min-duration "$MIN_DURATION"
    --max-duration "$MAX_DURATION"
  )
  if [ -n "$expected_path" ]; then
    client_args+=(--expect-tunnel-packet-path "$expected_path")
  fi
  RSTREAM_TUNNEL_TRANSPORT="$dialer_transport" "$BIN/bandwidth/client" "${client_args[@]}"
  printf 'PASS %-32s owner=%s dialer=%s\n' "$label" "$owner_transport" "$dialer_transport"
  PASS=$((PASS + 1))
}

run_case bytestream-tls bytestream tls tls '' false
run_case bytestream-quic bytestream quic quic '' false
run_case datagram-tls datagram tls tls stream false
run_case datagram-owner-quic datagram quic tls stream false
run_case datagram-both-quic datagram quic quic quic-datagram false
run_case datagram-guaranteed-quic datagram quic quic stream true

cleanup
printf '\nBandwidth limit E2E: %d/%d passed\n' "$PASS" 6
