#!/usr/bin/env bash
# End-to-end test runner.
#
# Usage:
#   export RSTREAM_CONTEXT=prod   # context with tunnel creation rights
#   export BIN=out/test           # directory containing built test binaries
#   bash run-e2e.sh [--quic]
#
# --quic: run the suite using QUIC tunnel transport (sets RSTREAM_QUIC_TRANSPORT=1).
set -euo pipefail

BIN="${BIN:-out/test}"
PASS=0
FAIL=0
SERVER_PID=
SERVER_ADDR=
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

QUIC=0
for arg in "$@"; do
  [ "$arg" = "--quic" ] && QUIC=1
done
[ "$QUIC" = "1" ] && export RSTREAM_QUIC_TRANSPORT=1

preflight() {
  local missing=0
  local rel
  if [ -z "${RSTREAM_CONTEXT:-}" ] && [ -z "${RSTREAM_ENGINE:-}" ]; then
    printf "ERROR RSTREAM_CONTEXT or RSTREAM_ENGINE must be set before running e2e tests\n" >&2
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

cleanup() {
  [ -n "${SERVER_PID:-}" ] && kill "$SERVER_PID" 2>/dev/null || true
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

# start_server <label> <suite> [flags…]
# Starts the server in background, waits for "READY <addr>", sets SERVER_ADDR.
start_server() {
  local label="$1" suite="$2"
  shift 2
  local tmpout
  local exe="$BIN/$suite"
  [ -d "$exe" ] && exe="$exe/server"
  tmpout=$(mktemp)
  "$exe" "$@" >"$tmpout" 2>&1 &
  SERVER_PID=$!
  local i=0
  while [ $i -lt 20 ]; do
    if grep -q "^READY" "$tmpout" 2>/dev/null; then
      SERVER_ADDR=$(grep "^READY" "$tmpout" | head -1 | awk '{print $2}')
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
  kill "$SERVER_PID" 2>/dev/null
  SERVER_PID=
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
    log_fail "$label" "$(echo "$out" | tail -3 | tr '\n' ' ')"
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

stop_server() {
  [ -n "${SERVER_PID:-}" ] && {
    kill "$SERVER_PID" 2>/dev/null
    wait "$SERVER_PID" 2>/dev/null || true
  }
  SERVER_PID=
}

echo "=== stream ==="
if start_server "stream/plain" stream/server --variant plain; then
  run_client "stream/plain" stream/client --variant plain
  stop_server
fi

if start_server "stream/tls" stream/server --variant tls; then
  run_client "stream/tls" stream/client --variant tls
  stop_server
fi

if start_server "stream/tls-published" stream/server --variant tls --publish --tls-alpn rstream-stream-echo; then
  run_client "stream/tls-published" stream/client --variant tls --addr "$SERVER_ADDR" --tls-alpn rstream-stream-echo
  stop_server
fi

if start_server "stream/tls-published-alpn-reject" stream/server --variant tls --publish --tls-alpn rstream-stream-echo; then
  run_client_expect_fail "stream/tls-published-alpn-reject" stream/client --variant tls --addr "$SERVER_ADDR" --tls-alpn rstream-stream-wrong
  stop_server
fi

if start_server "stream/tls-published-passthrough" stream/server --variant tls --publish --tls-mode passthrough; then
  run_client "stream/tls-published-passthrough" stream/client --variant tls --addr "$SERVER_ADDR"
  stop_server
fi

if start_server "stream/tls-published-upstream-tls" stream/server --variant tls --publish --tls-alpn rstream-stream-echo --upstream-tls; then
  run_client "stream/tls-published-upstream-tls" stream/client --variant tls --addr "$SERVER_ADDR" --tls-alpn rstream-stream-echo
  stop_server
fi

if start_server "stream/tls-published-upstream-tls-alpn-reject" stream/server --variant tls --publish --tls-alpn rstream-stream-echo --upstream-tls; then
  run_client_expect_fail "stream/tls-published-upstream-tls-alpn-reject" stream/client --variant tls --addr "$SERVER_ADDR" --tls-alpn rstream-stream-wrong
  stop_server
fi

echo "=== datagram ==="
if start_server "datagram/dtls" datagram/server --variant dtls; then
  run_client "datagram/dtls" datagram/client --variant dtls
  stop_server
fi

if start_server "datagram/quic" datagram/server --variant quic; then
  run_client "datagram/quic" datagram/client --variant quic
  stop_server
fi

if start_server "datagram/dtls-published" datagram/server --variant dtls --publish --tls-alpn rstream-dtls-echo; then
  run_client "datagram/dtls-published" datagram/client --variant dtls --addr "$SERVER_ADDR" --tls-alpn rstream-dtls-echo
  stop_server
fi

if start_server "datagram/dtls-published-alpn-reject" datagram/server --variant dtls --publish --tls-alpn rstream-dtls-echo; then
  run_client_expect_fail "datagram/dtls-published-alpn-reject" datagram/client --variant dtls --addr "$SERVER_ADDR" --tls-alpn rstream-dtls-wrong
  stop_server
fi

if start_server "datagram/dtls-published-upstream-tls" datagram/server --variant dtls --publish --tls-alpn rstream-dtls-echo --upstream-tls; then
  run_client "datagram/dtls-published-upstream-tls" datagram/client --variant dtls --addr "$SERVER_ADDR" --tls-alpn rstream-dtls-echo
  stop_server
fi

if start_server "datagram/dtls-published-upstream-tls-alpn-reject" datagram/server --variant dtls --publish --tls-alpn rstream-dtls-echo --upstream-tls; then
  run_client_expect_fail "datagram/dtls-published-upstream-tls-alpn-reject" datagram/client --variant dtls --addr "$SERVER_ADDR" --tls-alpn rstream-dtls-wrong
  stop_server
fi

if start_server "datagram/quic-published" datagram/server --variant quic --publish --tls-alpn rstream-quic-echo; then
  run_client "datagram/quic-published" datagram/client --variant quic --addr "$SERVER_ADDR" --tls-alpn rstream-quic-echo
  stop_server
fi

if start_server "datagram/quic-published-alpn-reject" datagram/server --variant quic --publish --tls-alpn rstream-quic-echo; then
  run_client_expect_fail "datagram/quic-published-alpn-reject" datagram/client --variant quic --addr "$SERVER_ADDR" --tls-alpn rstream-quic-wrong
  stop_server
fi

echo "=== http ==="
if start_server "http/h1" http/server --upstream h1; then
  run_client "http/h1" http/client --upstream h1 --tunnel "http-matrix-h1"
  stop_server
fi

if start_server "http/h2c" http/server --upstream h2c; then
  run_client "http/h2c" http/client --upstream h2c --tunnel "http-matrix-h2c"
  stop_server
fi

if start_server "http/h3" http/server --upstream h3; then
  run_client "http/h3" http/client --upstream h3 --tunnel "http-matrix-h3"
  stop_server
fi

echo "=== websocket ==="
for up in h1 h2c h3; do
  if start_server "websocket/${up}" websocket/server --upstream "$up"; then
    for down in h1 h2 h3; do
      run_client "websocket/${up}→${down}" websocket/client \
        --downstream "$down" --tunnel "ws-matrix-${up}"
    done
    stop_server
  fi
done

echo "=== webtransport ==="
if start_server "webtransport/all" webtransport/server; then
  run_client "webtransport/all" webtransport/client --case all
  stop_server
fi

echo ""
echo "Results: ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -eq 0 ]
