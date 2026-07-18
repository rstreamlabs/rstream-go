#!/usr/bin/env bash
# See LICENSE file in the project root for license information.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
. "$ROOT/test/e2e/runtime_common.sh"
BIN="${BIN:-$ROOT/out/test}"
RSTREAM_BIN=$(resolve_rstream_cli "$ROOT")
PYTHON="${PYTHON:-python3}"
TIMEOUT_SECONDS="${RSTREAM_RUNTIME_TIMEOUT:-60}"
TEST_ACTIVE_RELEASE=false
TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/rstream-go-runtime-tcp.XXXXXX")
ADDRESS_ID=
TCP_PORT=
CONCURRENT_PORTS=
FORWARD_PID=
UPSTREAM_PID=

cleanup() {
  if [ -n "$FORWARD_PID" ]; then
    kill "$FORWARD_PID" 2>/dev/null || true
    wait "$FORWARD_PID" 2>/dev/null || true
  fi
  if [ -n "$UPSTREAM_PID" ]; then
    kill "$UPSTREAM_PID" 2>/dev/null || true
    wait "$UPSTREAM_PID" 2>/dev/null || true
  fi
  if [ -n "$TCP_PORT" ]; then
    "$RSTREAM_BIN" project tcp-address release "$TCP_PORT" \
      --project-id "$RSTREAM_RUNTIME_PROJECT_ID" --output json >/dev/null 2>&1 || true
  fi
  for port in $CONCURRENT_PORTS; do
    "$RSTREAM_BIN" project tcp-address release "$port" \
      --project-id "$RSTREAM_RUNTIME_PROJECT_ID" --output json >/dev/null 2>&1 || true
  done
  for reservation_file in "$TMP_DIR"/concurrent-*.json; do
    if [ ! -s "$reservation_file" ]; then
      continue
    fi
    port=$("$PYTHON" -c 'import json, sys; print(json.load(open(sys.argv[1], encoding="utf-8")).get("port", ""))' "$reservation_file" 2>/dev/null || true)
    if [ -n "$port" ]; then
      "$RSTREAM_BIN" project tcp-address release "$port" \
        --project-id "$RSTREAM_RUNTIME_PROJECT_ID" --output json >/dev/null 2>&1 || true
    fi
  done
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

case "${1:-}" in
  "") ;;
  --release-active) TEST_ACTIVE_RELEASE=true ;;
  *)
    printf "usage: %s [--release-active]\n" "$0" >&2
    exit 2
    ;;
esac

require_executable "$RSTREAM_BIN"
require_executable "$BIN/stream/client"
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
export RSTREAM_API_URL="$RSTREAM_RUNTIME_API_URL"
export RSTREAM_AUTHENTICATION_TOKEN="$RSTREAM_RUNTIME_CONTROL_TOKEN"

RESERVE_PIDS=
for index in 1 2; do
  "$RSTREAM_BIN" project tcp-address reserve \
    --project-id "$RSTREAM_RUNTIME_PROJECT_ID" --output json >"$TMP_DIR/concurrent-$index.json" &
  RESERVE_PIDS="$RESERVE_PIDS $!"
done
for pid in $RESERVE_PIDS; do
  if ! wait "$pid"; then
    cat "$TMP_DIR"/concurrent-*.json >&2
    exit 1
  fi
done
read -r CONCURRENT_ID_1 CONCURRENT_PORT_1 CONCURRENT_ID_2 CONCURRENT_PORT_2 < <("$PYTHON" -c '
import json
import sys

values = [json.load(open(path, encoding="utf-8")) for path in sys.argv[1:]]
print(values[0]["id"], values[0]["port"], values[1]["id"], values[1]["port"])
' "$TMP_DIR/concurrent-1.json" "$TMP_DIR/concurrent-2.json")
if [ -z "$CONCURRENT_ID_1" ] || [ -z "$CONCURRENT_ID_2" ] || [ "$CONCURRENT_ID_1" = "$CONCURRENT_ID_2" ] || [ "$CONCURRENT_PORT_1" = "$CONCURRENT_PORT_2" ]; then
  printf "FAIL concurrent TCP reservations are not unique\n" >&2
  exit 1
fi
CONCURRENT_PORTS="$CONCURRENT_PORT_1 $CONCURRENT_PORT_2"
for port in $CONCURRENT_PORTS; do
  "$RSTREAM_BIN" project tcp-address release "$port" \
    --project-id "$RSTREAM_RUNTIME_PROJECT_ID" --output json >/dev/null
done
CONCURRENT_PORTS=
rm -f "$TMP_DIR"/concurrent-*.json
printf "PASS concurrent TCP address allocation\n"

RESERVATION_JSON=$("$RSTREAM_BIN" project tcp-address reserve \
  --project-id "$RSTREAM_RUNTIME_PROJECT_ID" --output json)
read -r ADDRESS_ID TCP_PORT < <("$PYTHON" -c '
import json
import sys

value = json.load(sys.stdin)
print(value["id"], value["port"])
' <<<"$RESERVATION_JSON")
if [ -z "$ADDRESS_ID" ] || [ -z "$TCP_PORT" ]; then
  printf "FAIL reserved TCP address response is incomplete\n" >&2
  exit 1
fi

UPSTREAM_LOG="$TMP_DIR/upstream.log"
"$PYTHON" "$ROOT/test/e2e/runtime_harness.py" serve tcp >"$UPSTREAM_LOG" 2>&1 &
UPSTREAM_PID=$!
for _ in $(seq 1 $((TIMEOUT_SECONDS * 5))); do
  if grep -q "^READY " "$UPSTREAM_LOG"; then
    UPSTREAM_ADDR=$(awk '/^READY / {print $2; exit}' "$UPSTREAM_LOG")
    break
  fi
  if ! kill -0 "$UPSTREAM_PID" 2>/dev/null; then
    cat "$UPSTREAM_LOG" >&2
    exit 1
  fi
  sleep 0.2
done
if [ -z "${UPSTREAM_ADDR:-}" ]; then
  printf "FAIL TCP upstream readiness timeout\n" >&2
  exit 1
fi

FORWARD_LOG="$TMP_DIR/forward.log"
"$RSTREAM_BIN" forward "$UPSTREAM_ADDR" --tcp --tcp-port "$TCP_PORT" \
  --name "runtime-reserved-tcp-$$" --output json --no-retry >"$FORWARD_LOG" 2>&1 &
FORWARD_PID=$!
for _ in $(seq 1 $((TIMEOUT_SECONDS * 5))); do
  if FORWARDING=$("$PYTHON" -c '
import json
import sys

for line in open(sys.argv[1], encoding="utf-8"):
    try:
        event = json.loads(line)
    except json.JSONDecodeError:
        continue
    if event.get("status") == "online" and event.get("forwarding"):
        print(event["forwarding"])
        break
' "$FORWARD_LOG") && [ -n "$FORWARDING" ]; then
    break
  fi
  if ! kill -0 "$FORWARD_PID" 2>/dev/null; then
    cat "$FORWARD_LOG" >&2
    exit 1
  fi
  sleep 0.2
done
if [ -z "${FORWARDING:-}" ]; then
  printf "FAIL reserved TCP tunnel readiness timeout\n" >&2
  cat "$FORWARD_LOG" >&2
  exit 1
fi
CANONICAL_FORWARDING=$("$PYTHON" "$ROOT/test/e2e/runtime_harness.py" hostport --addr "$FORWARDING")
if [[ "$CANONICAL_FORWARDING" != *":$TCP_PORT" ]]; then
  printf "FAIL forwarding address %s does not use reserved port %s\n" "$FORWARDING" "$TCP_PORT" >&2
  exit 1
fi
"$BIN/stream/client" --variant plain --addr "$CANONICAL_FORWARDING"
if [ "$TEST_ACTIVE_RELEASE" = true ]; then
  "$RSTREAM_BIN" project tcp-address release "$TCP_PORT" \
    --project-id "$RSTREAM_RUNTIME_PROJECT_ID" --output json >/dev/null
  ADDRESS_ID=
  TCP_PORT=
  deadline=$((SECONDS + TIMEOUT_SECONDS + 10))
  while kill -0 "$FORWARD_PID" 2>/dev/null && [ "$SECONDS" -lt "$deadline" ]; do
    "$BIN/stream/client" --variant plain --addr "$CANONICAL_FORWARDING" >/dev/null 2>&1 || true
    sleep 2
  done
  if kill -0 "$FORWARD_PID" 2>/dev/null; then
    printf "FAIL released TCP address did not revoke its active tunnel\n" >&2
    exit 1
  fi
  wait "$FORWARD_PID" 2>/dev/null || true
  FORWARD_PID=
  if "$BIN/stream/client" --variant plain --addr "$CANONICAL_FORWARDING" >/dev/null 2>&1; then
    printf "FAIL released TCP address still accepts connections\n" >&2
    exit 1
  fi
  printf "PASS published TCP active address revocation\n"
  exit 0
fi
kill "$FORWARD_PID" 2>/dev/null || true
wait "$FORWARD_PID" 2>/dev/null || true
FORWARD_PID=
"$RSTREAM_BIN" project tcp-address release "$TCP_PORT" \
  --project-id "$RSTREAM_RUNTIME_PROJECT_ID" --output json >/dev/null
ADDRESS_ID=
TCP_PORT=
printf "PASS published TCP reserved address lifecycle\n"
