#!/usr/bin/env bash
# See LICENSE file in the project root for license information.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
. "$ROOT/test/e2e/runtime_common.sh"
BIN="${BIN:-$ROOT/out/test}"
RSTREAM_BIN=$(resolve_rstream_cli "$ROOT")
PYTHON="${PYTHON:-python3}"
TIMEOUT_SECONDS="${RSTREAM_RUNTIME_TIMEOUT:-60}"
CASE_FILTER="${RSTREAM_RUNTIME_CASE_FILTER:-}"
TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/rstream-go-runtime.XXXXXX")
RUN_ID=$(printf '%s' "${TMP_DIR##*.}" | tr '[:upper:]' '[:lower:]')
NAME_PREFIX="${RSTREAM_RUNTIME_NAME_PREFIX:-runtime-forward-$RUN_ID}"
PASS=0
FAIL=0
PIDS=()
UPSTREAM_ADDR=
FORWARD_PID=
FORWARDING=
FORWARD_LOG=
FORWARD_ROUTING_ARGS=()
OWNER_ENV=()

case "${RSTREAM_E2E_ALLOW_CROSS_REGION_ROUTING:-}" in
  1|true)
    FORWARD_ROUTING_ARGS+=(--allow-cross-region-routing)
    ;;
  0|false)
    FORWARD_ROUTING_ARGS+=(--allow-cross-region-routing=false)
    ;;
  "")
    ;;
  *)
    printf "ERROR RSTREAM_E2E_ALLOW_CROSS_REGION_ROUTING must be a boolean\n" >&2
    exit 2
    ;;
esac
if [ -n "${RSTREAM_E2E_OWNER_ENGINE:-}" ] && [ -z "${RSTREAM_E2E_OWNER_AUTHENTICATION_TOKEN:-}" ]; then
  printf "ERROR RSTREAM_E2E_OWNER_AUTHENTICATION_TOKEN is required with RSTREAM_E2E_OWNER_ENGINE\n" >&2
  exit 2
fi
if [ -z "${RSTREAM_E2E_OWNER_ENGINE:-}" ] && [ -n "${RSTREAM_E2E_OWNER_AUTHENTICATION_TOKEN:-}" ]; then
  printf "ERROR RSTREAM_E2E_OWNER_ENGINE is required with RSTREAM_E2E_OWNER_AUTHENTICATION_TOKEN\n" >&2
  exit 2
fi
if [ -n "${RSTREAM_E2E_OWNER_STABLE_DOMAIN_ENGINE:-}" ] && [ -z "${RSTREAM_E2E_OWNER_ENGINE:-}" ]; then
  printf "ERROR RSTREAM_E2E_OWNER_ENGINE is required with RSTREAM_E2E_OWNER_STABLE_DOMAIN_ENGINE\n" >&2
  exit 2
fi
if [ -n "${RSTREAM_E2E_OWNER_ENGINE:-}" ]; then
  OWNER_ENV+=("RSTREAM_ENGINE=$RSTREAM_E2E_OWNER_ENGINE")
  OWNER_ENV+=("RSTREAM_AUTHENTICATION_TOKEN=$RSTREAM_E2E_OWNER_AUTHENTICATION_TOKEN")
fi
if [ -n "${RSTREAM_E2E_OWNER_CONTEXT:-}" ]; then
  OWNER_ENV+=("RSTREAM_CONTEXT=$RSTREAM_E2E_OWNER_CONTEXT")
fi

owner_exec() {
  exec env "${OWNER_ENV[@]}" "$@"
}

cleanup() {
  local pid
  for pid in "${PIDS[@]:-}"; do
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  done
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

case "${1:-}" in
  --quic-transport)
    export RSTREAM_TUNNEL_TRANSPORT=quic
    shift
    ;;
  --auto-transport)
    export RSTREAM_TUNNEL_TRANSPORT=auto
    shift
    ;;
  *)
    export RSTREAM_TUNNEL_TRANSPORT=tls
    ;;
esac

require_executable "$RSTREAM_BIN"
require_executable "$BIN/stream/client"
require_executable "$BIN/http/client"
require_executable "$BIN/datagram/client"
require_executable "$BIN/masque/client"
require_executable "$BIN/connect/client"
if [ "${RSTREAM_E2E_SKIP_PUBLISHED_TCP:-0}" != "1" ]; then
  require_executable "$ROOT/out/examples/tcp-ssh-client"
  require_executable "$ROOT/out/examples/tcp-ssh-server"
fi

make_cert() {
  openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
    -subj "/CN=localhost" \
    -keyout "$TMP_DIR/upstream.key" \
    -out "$TMP_DIR/upstream.crt" >/dev/null 2>&1
}

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

wait_ssh_ready() {
  local pid=$1 log=$2
  local deadline=$((SECONDS + TIMEOUT_SECONDS))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if grep -q "^SSH address: " "$log" 2>/dev/null && grep -q "^SSH host key fingerprint: " "$log" 2>/dev/null; then
      return 0
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      printf "FAIL %-42s server exited early\n" "forward/published tcp ssh" >&2
      tail -20 "$log" >&2 || true
      return 1
    fi
    sleep 0.2
  done
  printf "FAIL %-42s server did not become ready\n" "forward/published tcp ssh" >&2
  tail -20 "$log" >&2 || true
  return 1
}

start_upstream() {
  local label=$1 mode=$2
  local log="$TMP_DIR/upstream-$label.log"
  case "$mode" in
  tcp | tcp-eof-reply | http | udp)
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
  if ! UPSTREAM_ADDR=$(wait_ready "$pid" "$log" "$label"); then
    stop_pid "$pid"
    UPSTREAM_ADDR=
    return 1
  fi
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

generated_owner_stable_domain() {
  local endpoint=${RSTREAM_E2E_OWNER_STABLE_DOMAIN_ENGINE:-}
  local host=${endpoint#*://}
  host=${host%%/*}
  host=${host%%:*}
  local project_endpoint=${host%%.*}
  local cluster_domain=${host#*.}
  if [ -z "$project_endpoint" ] || [ "$cluster_domain" = "$host" ]; then
    printf "ERROR invalid RSTREAM_E2E_OWNER_STABLE_DOMAIN_ENGINE: %s\n" "$endpoint" >&2
    return 1
  fi
  local slug
  slug=$(printf '%s' "$NAME_PREFIX-$1" | shasum -a 256 | awk '{print substr($1, 1, 8)}')
  printf 'r%s-%s.t.%s\n' "$slug" "$project_endpoint" "$cluster_domain"
}

start_forward() {
  local label=$1 target=$2 need_forwarding=$3
  shift 3
  local arg has_host=0 published=0 tcp=0
  for arg in "$@"; do
    case "$arg" in
    --host | --host=*) has_host=1 ;;
    --publish) published=1 ;;
    --tcp) tcp=1 ;;
    esac
  done
  if [ -n "${RSTREAM_E2E_OWNER_STABLE_DOMAIN_ENGINE:-}" ] && [ "$published" -eq 1 ] && [ "$tcp" -eq 0 ] && [ "$has_host" -eq 0 ]; then
    set -- "$@" --host "$(generated_owner_stable_domain "$label")"
  fi
  FORWARD_LOG="$TMP_DIR/forward-$label.log"
  : >"$FORWARD_LOG"
  owner_exec "$RSTREAM_BIN" forward "$target" --output json --no-retry "${FORWARD_ROUTING_ARGS[@]}" "$@" >"$FORWARD_LOG" 2>&1 &
  FORWARD_PID=$!
  PIDS+=("$FORWARD_PID")

  local deadline=$((SECONDS + TIMEOUT_SECONDS))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if FORWARDING=$(extract_forwarding "$FORWARD_LOG" "$need_forwarding"); then
      FORWARDING=$(rewrite_downstream_address "$FORWARDING")
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
  if [ -n "$CASE_FILTER" ] && [[ "$label" != *"$CASE_FILTER"* ]]; then
    return
  fi
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
  start_upstream "private-plain" tcp || return 1
  upstream=$UPSTREAM_ADDR
  local tunnel_prefix="$NAME_PREFIX-private"
  start_forward "private-plain" "$upstream" 0 \
    --bytestream --no-publish --name "$tunnel_prefix-plain" || return 1
  "$BIN/stream/client" --variant plain --tunnel "$tunnel_prefix" || rc=$?
  stop_pid "$FORWARD_PID"
  return "$rc"
}

case_tls_terminated() {
  local upstream
  local rc=0
  start_upstream "tls-terminated" tcp || return 1
  upstream=$UPSTREAM_ADDR
  start_forward "tls-terminated" "$upstream" 1 \
    --bytestream --publish --tls --tls-mode terminated \
    --tls-alpn rstream-runtime-stream --name "$NAME_PREFIX-tls-terminated" || return 1
  "$BIN/stream/client" --variant tls --addr "$FORWARDING" --tls-alpn rstream-runtime-stream || rc=$?
  stop_pid "$FORWARD_PID"
  return "$rc"
}

case_tls_upstream_tls() {
  local upstream
  local rc=0
  start_upstream "tls-upstream-tls" tls || return 1
  upstream=$UPSTREAM_ADDR
  start_forward "tls-upstream-tls" "$upstream" 1 \
    --bytestream --publish --tls --tls-mode terminated --upstream-tls \
    --tls-alpn rstream-runtime-stream --name "$NAME_PREFIX-tls-upstream" || return 1
  "$BIN/stream/client" --variant tls --addr "$FORWARDING" --tls-alpn rstream-runtime-stream || rc=$?
  stop_pid "$FORWARD_PID"
  return "$rc"
}

case_tls_passthrough() {
  local upstream
  local rc=0
  start_upstream "tls-passthrough" tls || return 1
  upstream=$UPSTREAM_ADDR
  start_forward "tls-passthrough" "$upstream" 1 \
    --bytestream --publish --tls --tls-mode passthrough \
    --name "$NAME_PREFIX-tls-passthrough" || return 1
  "$BIN/stream/client" --variant tls --addr "$FORWARDING" || rc=$?
  stop_pid "$FORWARD_PID"
  return "$rc"
}

case_published_tcp() {
  local rc=0
  start_upstream "published-tcp" tcp || return 1
  start_forward "published-tcp" "$UPSTREAM_ADDR" 1 \
    --tcp --name "$NAME_PREFIX-published-tcp" || return 1
  "$BIN/stream/client" --variant plain --addr "$FORWARDING" || rc=$?
  stop_pid "$FORWARD_PID"
  return "$rc"
}

case_published_tcp_half_close() {
  local rc=0
  start_upstream "published-tcp-half-close" tcp-eof-reply || return 1
  start_forward "published-tcp-half-close" "$UPSTREAM_ADDR" 1 \
    --tcp --name "$NAME_PREFIX-published-tcp-half-close" || return 1
  "$PYTHON" "$ROOT/test/e2e/runtime_harness.py" check tcp-half-close \
    --addr "$FORWARDING" || rc=$?
  stop_pid "$FORWARD_PID"
  return "$rc"
}

case_published_tcp_ssh() {
  local server_pid server_log address fingerprint output
  local password="runtime-tcp-ssh-password"
  local rc=0
  server_log="$TMP_DIR/published-tcp-ssh-server.log"
  owner_exec env RSTREAM_SSH_PASSWORD="$password" "$ROOT/out/examples/tcp-ssh-server" \
    -name "$NAME_PREFIX-published-tcp-ssh" >"$server_log" 2>&1 &
  server_pid=$!
  PIDS+=("$server_pid")
  if ! wait_ssh_ready "$server_pid" "$server_log"; then
    stop_pid "$server_pid"
    return 1
  fi
  address=$(awk -F': ' '/^SSH address: / {print $2; exit}' "$server_log")
  fingerprint=$(awk -F': ' '/^SSH host key fingerprint: / {print $2; exit}' "$server_log")
  if RSTREAM_SSH_PASSWORD="wrong-password" "$ROOT/out/examples/tcp-ssh-client" \
    -address "$address" -fingerprint "$fingerprint" >"$TMP_DIR/published-tcp-ssh-wrong-password.log" 2>&1; then
    printf "SSH client accepted an invalid password\n" >&2
    rc=1
  fi
  if RSTREAM_SSH_PASSWORD="$password" "$ROOT/out/examples/tcp-ssh-client" \
    -address "$address" -fingerprint "SHA256:invalid" >"$TMP_DIR/published-tcp-ssh-wrong-key.log" 2>&1; then
    printf "SSH client accepted an invalid host key fingerprint\n" >&2
    rc=1
  fi
  if ! output=$(RSTREAM_SSH_PASSWORD="$password" "$ROOT/out/examples/tcp-ssh-client" \
    -address "$address" -fingerprint "$fingerprint"); then
    rc=1
  elif [ "$output" != "SSH over an rstream TCP tunnel" ]; then
    printf "Unexpected SSH response: %s\n" "$output" >&2
    rc=1
  fi
  stop_pid "$server_pid"
  return "$rc"
}

case_http_h1() {
  local upstream forwarding_hostport
  local rc=0
  start_upstream "http-h1" http || return 1
  upstream=$UPSTREAM_ADDR
  start_forward "http-h1" "$upstream" 1 \
    --bytestream --publish --http --name "$NAME_PREFIX-http-h1" || return 1
  forwarding_hostport=$("$PYTHON" "$ROOT/test/e2e/runtime_harness.py" hostport --addr "$FORWARDING")
  "$BIN/http/client" --upstream h1 --addr "$forwarding_hostport" || rc=$?
  stop_pid "$FORWARD_PID"
  return "$rc"
}

case_http_h2_reused_connection_routes() {
  local first_forward_pid first_forwarding second_forward_pid second_forwarding trigger checker_pid checker_log
  local rc=0
  trigger="$TMP_DIR/h2-reuse-trigger"
  checker_log="$TMP_DIR/h2-reuse-checker.log"
  start_upstream "http-h2-reuse-first" http || return 1
  start_forward "http-h2-reuse-first" "$UPSTREAM_ADDR" 1 \
    --bytestream --publish --http --name "$NAME_PREFIX-http-h2-reuse-first" || return 1
  first_forward_pid=$FORWARD_PID
  first_forwarding=$FORWARDING
  if ! start_upstream "http-h2-reuse-second" http; then
    stop_pid "$first_forward_pid"
    return 1
  fi
  if ! start_forward "http-h2-reuse-second" "$UPSTREAM_ADDR" 1 \
    --bytestream --publish --http --name "$NAME_PREFIX-http-h2-reuse-second"; then
    stop_pid "$first_forward_pid"
    return 1
  fi
  second_forward_pid=$FORWARD_PID
  second_forwarding=$FORWARDING
  "$PYTHON" "$ROOT/test/e2e/runtime_harness.py" check h2-reuse-routes \
    --first "$first_forwarding/ping" \
    --second "$second_forwarding/ping" \
    --trigger "$trigger" >"$checker_log" 2>&1 &
  checker_pid=$!
  PIDS+=("$checker_pid")
  if ! wait_ready "$checker_pid" "$checker_log" "http-h2-reuse-checker" >/dev/null; then
    stop_pid "$first_forward_pid"
    stop_pid "$second_forward_pid"
    return 1
  fi
  stop_pid "$first_forward_pid"
  sleep 1
  : >"$trigger"
  if ! wait "$checker_pid"; then
    cat "$checker_log" >&2 || true
    rc=1
  fi
  stop_pid "$second_forward_pid"
  return "$rc"
}

case_http_h2_subpath_preserves_request_path() {
  local rc=0
  start_upstream "http-h2-subpath" http || return 1
  start_forward "http-h2-subpath" "$UPSTREAM_ADDR" 1 \
    --bytestream --publish --http --name "$NAME_PREFIX-http-h2-subpath" || return 1
  "$PYTHON" "$ROOT/test/e2e/runtime_harness.py" check h2-subpath-response \
    --url "$FORWARDING/directory/" || rc=$?
  stop_pid "$FORWARD_PID"
  return "$rc"
}

case_http_h3_subpath_preserves_request_path() {
  local rc=0
  start_upstream "http-h3-subpath" http || return 1
  start_forward "http-h3-subpath" "$UPSTREAM_ADDR" 1 \
    --bytestream --publish --http --name "$NAME_PREFIX-http-h3-subpath" || return 1
  "$BIN/http/client" --h3-url "$FORWARDING/directory/" || rc=$?
  stop_pid "$FORWARD_PID"
  return "$rc"
}

case_http_h3_reused_connection_routes() {
  local first_forward_pid first_forwarding second_forward_pid second_forwarding
  local rc=0
  start_upstream "http-h3-reuse-first" http || return 1
  start_forward "http-h3-reuse-first" "$UPSTREAM_ADDR" 1 \
    --bytestream --publish --http --name "$NAME_PREFIX-http-h3-reuse-first" || return 1
  first_forward_pid=$FORWARD_PID
  first_forwarding=$FORWARDING
  if ! start_upstream "http-h3-reuse-second" http; then
    stop_pid "$first_forward_pid"
    return 1
  fi
  if ! start_forward "http-h3-reuse-second" "$UPSTREAM_ADDR" 1 \
    --bytestream --publish --http --name "$NAME_PREFIX-http-h3-reuse-second"; then
    stop_pid "$first_forward_pid"
    return 1
  fi
  second_forward_pid=$FORWARD_PID
  second_forwarding=$FORWARDING
  "$BIN/http/client" \
    --h3-reuse-first "$first_forwarding/ping" \
    --h3-reuse-second "$second_forwarding/ping" || rc=$?
  stop_pid "$first_forward_pid"
  stop_pid "$second_forward_pid"
  return "$rc"
}

case_dtls() {
  local upstream
  local rc=0
  start_upstream "dtls" udp || return 1
  upstream=$UPSTREAM_ADDR
  start_forward "dtls" "$upstream" 1 \
    --datagram --publish --dtls --tls-alpn rstream-runtime-dtls \
    --name "$NAME_PREFIX-dtls" || return 1
  "$BIN/datagram/client" --variant dtls --addr "$FORWARDING" --tls-alpn rstream-runtime-dtls || rc=$?
  stop_pid "$FORWARD_PID"
  return "$rc"
}

case_quic() {
  local server_pid server_log forwarding
  local rc=0
  server_log="$TMP_DIR/quic-server.log"
  owner_exec "$BIN/datagram/server" --variant quic --publish \
    --tls-alpn rstream-runtime-quic \
    --name "$NAME_PREFIX-quic" >"$server_log" 2>&1 &
  server_pid=$!
  PIDS+=("$server_pid")
  if ! forwarding=$(wait_ready "$server_pid" "$server_log" "quic"); then
    stop_pid "$server_pid"
    return 1
  fi
  forwarding=$(rewrite_downstream_address "$forwarding")
  "$BIN/datagram/client" --variant quic \
    --addr "$forwarding" \
    --tls-alpn rstream-runtime-quic || rc=$?
  stop_pid "$server_pid"
  return "$rc"
}

case_connect_udp() {
  local server_pid server_log forwarding upstream
  local rc=0
  start_upstream "connect-udp-target" udp || return 1
  upstream=$UPSTREAM_ADDR
  server_log="$TMP_DIR/connect-udp-server.log"
  owner_exec "$BIN/masque/server" --variant connect-udp \
    --name "$NAME_PREFIX-connect-udp" \
    --public-port "${RSTREAM_E2E_DOWNSTREAM_PORT:-}" >"$server_log" 2>&1 &
  server_pid=$!
  PIDS+=("$server_pid")
  if ! forwarding=$(wait_ready "$server_pid" "$server_log" "connect-udp"); then
    stop_pid "$server_pid"
    return 1
  fi
  forwarding=$(rewrite_downstream_address "$forwarding")
  "$BIN/masque/client" --variant connect-udp \
    --addr "$forwarding" \
    --target "$upstream" || rc=$?
  stop_pid "$server_pid"
  return "$rc"
}

case_connect_ip() {
  local server_pid server_log forwarding
  local rc=0
  server_log="$TMP_DIR/connect-ip-server.log"
  owner_exec "$BIN/masque/server" --variant connect-ip \
    --name "$NAME_PREFIX-connect-ip" \
    --public-port "${RSTREAM_E2E_DOWNSTREAM_PORT:-}" >"$server_log" 2>&1 &
  server_pid=$!
  PIDS+=("$server_pid")
  if ! forwarding=$(wait_ready "$server_pid" "$server_log" "connect-ip"); then
    stop_pid "$server_pid"
    return 1
  fi
  forwarding=$(rewrite_downstream_address "$forwarding")
  "$BIN/masque/client" --variant connect-ip \
    --addr "$forwarding" || rc=$?
  stop_pid "$server_pid"
  return "$rc"
}

case_plain_connect() {
  local upstream=$1
  local downstream=$2
  local target server_pid server_log forwarding
  local rc=0
  start_upstream "connect-$upstream-$downstream-target" tcp || return 1
  target=$UPSTREAM_ADDR
  server_log="$TMP_DIR/connect-$upstream-$downstream-server.log"
  owner_exec "$BIN/connect/server" --upstream "$upstream" \
    --name "$NAME_PREFIX-connect-$upstream-$downstream" >"$server_log" 2>&1 &
  server_pid=$!
  PIDS+=("$server_pid")
  if ! forwarding=$(wait_ready "$server_pid" "$server_log" "connect-$upstream-$downstream"); then
    stop_pid "$server_pid"
    return 1
  fi
  forwarding=$(rewrite_downstream_address "$forwarding")
  "$BIN/connect/client" --downstream "$downstream" \
    --addr "$forwarding" \
    --target "$target" || rc=$?
  stop_pid "$server_pid"
  return "$rc"
}

make_cert
run_case "forward/private bytestream plain" case_private_plain
run_case "forward/tls terminated" case_tls_terminated
run_case "forward/tls upstream tls" case_tls_upstream_tls
run_case "forward/tls passthrough" case_tls_passthrough
if [ "${RSTREAM_E2E_SKIP_PUBLISHED_TCP:-0}" != "1" ]; then
  run_case "forward/published tcp" case_published_tcp
  run_case "forward/published tcp half-close" case_published_tcp_half_close
  run_case "forward/published tcp ssh" case_published_tcp_ssh
fi
run_case "forward/http h1" case_http_h1
run_case "forward/http h2 reused connection" case_http_h2_reused_connection_routes
run_case "forward/http h2 subpath" case_http_h2_subpath_preserves_request_path
run_case "forward/http h3 subpath" case_http_h3_subpath_preserves_request_path
run_case "forward/http h3 reused connection" case_http_h3_reused_connection_routes
for upstream in h1 h2c h3; do
  for downstream in h1 h2 h3; do
    run_case "forward/http connect $upstream->$downstream" case_plain_connect "$upstream" "$downstream"
  done
done
run_case "forward/dtls" case_dtls
run_case "forward/quic" case_quic
run_case "forward/connect udp" case_connect_udp
run_case "forward/connect ip" case_connect_ip

printf "\nResults: %d passed, %d failed\n" "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
