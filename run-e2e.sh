#!/usr/bin/env bash
# End-to-end test runner.
#
# Usage:
#   export RSTREAM_CONTEXT=prod   # context with tunnel creation rights
#   export BIN=out/test           # directory containing built test binaries
#   export RSTREAM_E2E_OWNER_CONTEXT=edge-owner-eu # optional distinct owner
#   export RSTREAM_E2E_NAME_PREFIX=e2e-manual # optional deterministic names
#   bash run-e2e.sh [--quic|--auto]
#
# The baseline is pinned to TLS for deterministic packet-path assertions.
# --quic: run with strict QUIC transport.
# --auto: exercise QUIC preference through automatic transport selection.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
. "$ROOT/test/e2e/runtime_common.sh"
BIN="${BIN:-out/test}"
RUN_ID=$(printf '%s' "$(date +%s)-$$-$RANDOM-$RANDOM" | cksum | awk '{printf "%08x", $1}')
NAME_PREFIX="${RSTREAM_E2E_NAME_PREFIX:-e2e-$RUN_ID}"
STREAM_PREFIX="$NAME_PREFIX-stream"
DATAGRAM_PREFIX="$NAME_PREFIX-datagram"
PASS=0
FAIL=0
SERVER_PID=
SERVER_ADDR=
OWNER_ENV=()
REQUIRED_BINS=(
  stream/server
  stream/client
  datagram/server
  datagram/client
  http/server
  http/client
  websocket/server
  websocket/client
  webtransport/server
  webtransport/client
)

TUNNEL_TRANSPORT=tls
TUNNEL_PACKET_PATH=stream
for arg in "$@"; do
  if [ "$arg" = "--quic" ]; then
    TUNNEL_TRANSPORT=quic
    TUNNEL_PACKET_PATH=quic-datagram
  fi
  if [ "$arg" = "--auto" ]; then
    TUNNEL_TRANSPORT=auto
    TUNNEL_PACKET_PATH=quic-datagram
  fi
done
export RSTREAM_TUNNEL_TRANSPORT="$TUNNEL_TRANSPORT"

preflight() {
  local missing=0
  local rel
  if [ -z "${RSTREAM_CONTEXT:-}" ] && [ -z "${RSTREAM_ENGINE:-}" ]; then
    printf "ERROR RSTREAM_CONTEXT or RSTREAM_ENGINE must be set before running e2e tests\n" >&2
    missing=1
  fi
  if [ -n "${RSTREAM_E2E_OWNER_ENGINE:-}" ] && [ -z "${RSTREAM_E2E_OWNER_AUTHENTICATION_TOKEN:-}" ]; then
    printf "ERROR RSTREAM_E2E_OWNER_AUTHENTICATION_TOKEN is required with RSTREAM_E2E_OWNER_ENGINE\n" >&2
    missing=1
  fi
  if [ -z "${RSTREAM_E2E_OWNER_ENGINE:-}" ] && [ -n "${RSTREAM_E2E_OWNER_AUTHENTICATION_TOKEN:-}" ]; then
    printf "ERROR RSTREAM_E2E_OWNER_ENGINE is required with RSTREAM_E2E_OWNER_AUTHENTICATION_TOKEN\n" >&2
    missing=1
  fi
  for rel in "${REQUIRED_BINS[@]}"; do
    if [ ! -x "$BIN/$rel" ]; then
      printf "ERROR missing executable %s (run: make test-bins)\n" "$BIN/$rel" >&2
      missing=1
    fi
  done
  [ "$missing" -eq 0 ] || exit 2
}

owner_exec() {
  exec env "${OWNER_ENV[@]}" "$@"
}

stop_server() {
  local pid="${SERVER_PID:-}"
  local i=0
  [ -n "$pid" ] || return 0
  SERVER_PID=
  kill "$pid" 2>/dev/null || true
  while kill -0 "$pid" 2>/dev/null && [ "$i" -lt 50 ]; do
    sleep 0.1
    i=$((i + 1))
  done
  if kill -0 "$pid" 2>/dev/null; then
    kill -9 "$pid" 2>/dev/null || true
  fi
  wait "$pid" 2>/dev/null || true
}

cleanup() {
  stop_server
}
trap cleanup EXIT

log_pass() {
  printf "PASS %-35s\n" "$1"
  PASS=$((PASS + 1))
}
log_fail() {
  printf "FAIL %-35s  %s\n" "$1" "$2"
  FAIL=$((FAIL + 1))
}

preflight
if [ -n "${RSTREAM_E2E_OWNER_ENGINE:-}" ]; then
  OWNER_ENV+=("RSTREAM_ENGINE=$RSTREAM_E2E_OWNER_ENGINE")
  OWNER_ENV+=("RSTREAM_AUTHENTICATION_TOKEN=$RSTREAM_E2E_OWNER_AUTHENTICATION_TOKEN")
fi
if [ -n "${RSTREAM_E2E_OWNER_CONTEXT:-}" ]; then
  OWNER_ENV+=("RSTREAM_CONTEXT=$RSTREAM_E2E_OWNER_CONTEXT")
fi

# start_server <label> <suite> [flags…]
# Starts the server in background, waits for "READY <addr>", sets SERVER_ADDR.
start_server() {
  local label="$1" suite="$2"
  shift 2
  local tmpout
  local exe="$BIN/$suite"
  [ -d "$exe" ] && exe="$exe/server"
  tmpout=$(mktemp)
  owner_exec "$exe" "$@" >"$tmpout" 2>&1 &
  SERVER_PID=$!
  local i=0
  while [ $i -lt 20 ]; do
    if grep -q "^READY" "$tmpout" 2>/dev/null; then
      SERVER_ADDR=$(rewrite_downstream_address "$(grep "^READY" "$tmpout" | head -1 | awk '{print $2}')")
      rm -f "$tmpout"
      return 0
    fi
    if ! kill -0 "$SERVER_PID" 2>/dev/null; then
      log_fail "$label" "server exited early: $(tail -1 "$tmpout" 2>/dev/null)"
      rm -f "$tmpout"
      SERVER_PID=
      return 1
    fi
    sleep 1
    i=$((i + 1))
  done
  log_fail "$label" "server did not become ready (timeout)"
  rm -f "$tmpout"
  stop_server
  return 1
}

# run_client <label> <suite> [flags…]
run_client() {
  local label="$1" suite="$2"
  shift 2
  local out
  local exe="$BIN/$suite"
  [ -d "$exe" ] && exe="$exe/client"
  if out=$("$exe" "$@" 2>&1); then
    log_pass "$label"
  else
    log_fail "$label" "client exited non-zero"
    printf '%s\n' "$out" | sed 's/^/  /'
  fi
}

run_client_expect_fail() {
  local label="$1" suite="$2"
  shift 2
  local out
  local exe="$BIN/$suite"
  [ -d "$exe" ] && exe="$exe/client"
  if out=$("$exe" "$@" 2>&1); then
    log_fail "$label" "client succeeded unexpectedly"
  else
    log_pass "$label"
  fi
}

echo "=== stream ==="
if start_server "stream/plain" stream/server --variant plain --name "$STREAM_PREFIX-plain"; then
	run_client "stream/plain" stream/client --variant plain --tunnel "$STREAM_PREFIX"
  stop_server
fi

if start_server "stream/tls" stream/server --variant tls --name "$STREAM_PREFIX-tls"; then
	run_client "stream/tls" stream/client --variant tls --tunnel "$STREAM_PREFIX"
  stop_server
fi

if start_server "stream/tls-published" stream/server --variant tls --publish --tls-alpn rstream-stream-echo --name "$STREAM_PREFIX-tls-published"; then
  run_client "stream/tls-published" stream/client --variant tls --addr "$SERVER_ADDR" --tls-alpn rstream-stream-echo
  stop_server
fi

if start_server "stream/tls-published-alpn-reject" stream/server --variant tls --publish --tls-alpn rstream-stream-echo --name "$STREAM_PREFIX-tls-alpn-reject"; then
  run_client_expect_fail "stream/tls-published-alpn-reject" stream/client --variant tls --addr "$SERVER_ADDR" --tls-alpn rstream-stream-wrong
  stop_server
fi

if start_server "stream/tls-published-passthrough" stream/server --variant tls --publish --tls-mode passthrough --name "$STREAM_PREFIX-tls-passthrough"; then
  run_client "stream/tls-published-passthrough" stream/client --variant tls --addr "$SERVER_ADDR"
  stop_server
fi

if start_server "stream/tls-published-upstream-tls" stream/server --variant tls --publish --tls-alpn rstream-stream-echo --upstream-tls --name "$STREAM_PREFIX-tls-upstream"; then
  run_client "stream/tls-published-upstream-tls" stream/client --variant tls --addr "$SERVER_ADDR" --tls-alpn rstream-stream-echo
  stop_server
fi

if start_server "stream/tls-published-upstream-tls-alpn-reject" stream/server --variant tls --publish --tls-alpn rstream-stream-echo --upstream-tls --name "$STREAM_PREFIX-tls-upstream-reject"; then
  run_client_expect_fail "stream/tls-published-upstream-tls-alpn-reject" stream/client --variant tls --addr "$SERVER_ADDR" --tls-alpn rstream-stream-wrong
  stop_server
fi

echo "=== datagram ==="
if start_server "datagram/dtls" datagram/server --variant dtls --name "$DATAGRAM_PREFIX-dtls"; then
	run_client "datagram/dtls" datagram/client --variant dtls --tunnel "$DATAGRAM_PREFIX" --expect-tunnel-packet-path "$TUNNEL_PACKET_PATH"
  stop_server
fi

if start_server "datagram/quic" datagram/server --variant quic --name "$DATAGRAM_PREFIX-quic"; then
	run_client "datagram/quic" datagram/client --variant quic --tunnel "$DATAGRAM_PREFIX" --expect-tunnel-packet-path "$TUNNEL_PACKET_PATH"
  stop_server
fi

if start_server "datagram/sctp" datagram/server --variant sctp --name "$DATAGRAM_PREFIX-sctp"; then
	run_client "datagram/sctp" datagram/client --variant sctp --tunnel "$DATAGRAM_PREFIX" --expect-tunnel-packet-path "$TUNNEL_PACKET_PATH"
  stop_server
fi

if start_server "datagram/dtls-guaranteed-delivery" datagram/server --variant dtls --name "$DATAGRAM_PREFIX-guaranteed-dtls" --datagram-guaranteed-delivery; then
	run_client "datagram/dtls-guaranteed-delivery" datagram/client --variant dtls --tunnel "$DATAGRAM_PREFIX-guaranteed" --expect-tunnel-packet-path stream
  stop_server
fi

if start_server "datagram/dtls-published" datagram/server --variant dtls --publish --tls-alpn rstream-dtls-echo --name "$DATAGRAM_PREFIX-dtls-published"; then
  run_client "datagram/dtls-published" datagram/client --variant dtls --addr "$SERVER_ADDR" --tls-alpn rstream-dtls-echo
  stop_server
fi

if start_server "datagram/dtls-published-alpn-reject" datagram/server --variant dtls --publish --tls-alpn rstream-dtls-echo --name "$DATAGRAM_PREFIX-dtls-alpn-reject"; then
  run_client_expect_fail "datagram/dtls-published-alpn-reject" datagram/client --variant dtls --addr "$SERVER_ADDR" --tls-alpn rstream-dtls-wrong
  stop_server
fi

if start_server "datagram/dtls-published-upstream-tls" datagram/server --variant dtls --publish --tls-alpn rstream-dtls-echo --upstream-tls --name "$DATAGRAM_PREFIX-dtls-upstream"; then
  run_client "datagram/dtls-published-upstream-tls" datagram/client --variant dtls --addr "$SERVER_ADDR" --tls-alpn rstream-dtls-echo
  stop_server
fi

if start_server "datagram/dtls-published-upstream-tls-alpn-reject" datagram/server --variant dtls --publish --tls-alpn rstream-dtls-echo --upstream-tls --name "$DATAGRAM_PREFIX-dtls-upstream-reject"; then
  run_client_expect_fail "datagram/dtls-published-upstream-tls-alpn-reject" datagram/client --variant dtls --addr "$SERVER_ADDR" --tls-alpn rstream-dtls-wrong
  stop_server
fi

if start_server "datagram/quic-published" datagram/server --variant quic --publish --tls-alpn rstream-quic-echo --name "$DATAGRAM_PREFIX-quic-published"; then
  run_client "datagram/quic-published" datagram/client --variant quic --addr "$SERVER_ADDR" --tls-alpn rstream-quic-echo
  stop_server
fi

if start_server "datagram/quic-published-alpn-reject" datagram/server --variant quic --publish --tls-alpn rstream-quic-echo --name "$DATAGRAM_PREFIX-quic-alpn-reject"; then
  run_client_expect_fail "datagram/quic-published-alpn-reject" datagram/client --variant quic --addr "$SERVER_ADDR" --tls-alpn rstream-quic-wrong
  stop_server
fi

if start_server "datagram/sctp-published" datagram/server --variant sctp --publish --tls-alpn rstream-sctp-echo --name "$DATAGRAM_PREFIX-sctp-published"; then
  run_client "datagram/sctp-published" datagram/client --variant sctp --addr "$SERVER_ADDR" --tls-alpn rstream-sctp-echo
  stop_server
fi

if start_server "datagram/sctp-published-alpn-reject" datagram/server --variant sctp --publish --tls-alpn rstream-sctp-echo --name "$DATAGRAM_PREFIX-sctp-alpn-reject"; then
  run_client_expect_fail "datagram/sctp-published-alpn-reject" datagram/client --variant sctp --addr "$SERVER_ADDR" --tls-alpn rstream-sctp-wrong
  stop_server
fi

if start_server "datagram/sctp-published-upstream-tls" datagram/server --variant sctp --publish --tls-alpn rstream-sctp-echo --upstream-tls --name "$DATAGRAM_PREFIX-sctp-upstream"; then
  run_client "datagram/sctp-published-upstream-tls" datagram/client --variant sctp --addr "$SERVER_ADDR" --tls-alpn rstream-sctp-echo
  stop_server
fi

echo "=== http ==="
if start_server "http/h1" http/server --upstream h1 --name "$NAME_PREFIX-http-h1"; then
	run_client "http/h1" http/client --upstream h1 --tunnel "$NAME_PREFIX-http-h1" --sse
  stop_server
fi

if start_server "http/h2c" http/server --upstream h2c --name "$NAME_PREFIX-http-h2c"; then
	run_client "http/h2c" http/client --upstream h2c --tunnel "$NAME_PREFIX-http-h2c" --sse
  stop_server
fi

if start_server "http/h3" http/server --upstream h3 --name "$NAME_PREFIX-http-h3"; then
	run_client "http/h3" http/client --upstream h3 --tunnel "$NAME_PREFIX-http-h3" --sse
  stop_server
fi

echo "=== websocket ==="
for up in h1 h2c h3; do
	if start_server "websocket/${up}" websocket/server --upstream "$up" --name "$NAME_PREFIX-ws-$up"; then
    for down in h1 h2 h3; do
      run_client "websocket/${up}→${down}" websocket/client \
			--downstream "$down" --tunnel "$NAME_PREFIX-ws-$up"
    done
    stop_server
  fi
done

echo "=== webtransport ==="
if start_server "webtransport/all" webtransport/server --name "$NAME_PREFIX-wt-private"; then
	run_client "webtransport/all" webtransport/client --case all --tunnel "$NAME_PREFIX-wt-private"
  stop_server
fi

if start_server "webtransport/published-http" webtransport/server --publish --published-protocol http --name "$NAME_PREFIX-wt-published"; then
	run_client "webtransport/published-http" webtransport/client --publish --case all --tunnel "$NAME_PREFIX-wt-published"
  stop_server
fi

echo ""
echo "Results: ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -eq 0 ]
