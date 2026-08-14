#!/usr/bin/env bash
# See LICENSE file in the project root for license information.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
REPO_PARENT="$(cd "${ROOT}/.." && pwd)"
JS_ROOT="${RSTREAM_JS_REPO:-${REPO_PARENT}/rstream-js}"
CPP_ROOT="${RSTREAM_CPP_REPO:-${REPO_PARENT}/rstream-cpp}"
TIMEOUT_SECONDS="${RSTREAM_WEBTTY_RUNTIME_TIMEOUT_SECONDS:-30}"
TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/rstream-webtty-runtime.XXXXXX")
KEEP_RUNTIME="${RSTREAM_KEEP_RUNTIME:-0}"
PIDS=()
PASS=0
FAIL=0

cleanup() {
  local pid
  for pid in "${PIDS[@]:-}"; do
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  done
  if [ "$KEEP_RUNTIME" = "1" ]; then
    printf "kept runtime directory: %s\n" "$TMP_DIR" >&2
  else
    rm -rf "$TMP_DIR"
  fi
}
trap cleanup EXIT

log_pass() {
  printf "PASS %-48s\n" "$1"
  PASS=$((PASS + 1))
}

log_fail() {
  printf "FAIL %-48s %s\n" "$1" "$2" >&2
  FAIL=$((FAIL + 1))
}

run_case() {
  local label=$1
  shift
  local log="$TMP_DIR/case-${label//[^A-Za-z0-9_.-]/_}.log"
  if "$@" >"$log" 2>&1; then
    log_pass "$label"
  else
    log_fail "$label" "$(tail -20 "$log" | tr '\n' ' ')"
  fi
}

run_case_expect_fail() {
  local label=$1
  local expected=$2
  shift 2
  local log="$TMP_DIR/case-${label//[^A-Za-z0-9_.-]/_}.log"
  if "$@" >"$log" 2>&1; then
    log_fail "$label" "command succeeded unexpectedly"
  elif grep -q "$expected" "$log"; then
    log_pass "$label"
  else
    log_fail "$label" "$(tail -20 "$log" | tr '\n' ' ')"
  fi
}

reserve_port() {
  python3 - <<'PY'
import socket
sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
sock.bind(("127.0.0.1", 0))
print(sock.getsockname()[1])
sock.close()
PY
}

wait_tcp() {
  local address=$1
  local deadline=$((SECONDS + TIMEOUT_SECONDS))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if python3 - "$address" <<'PY' >/dev/null 2>&1; then
import socket
import sys
host, port = sys.argv[1].rsplit(":", 1)
with socket.create_connection((host, int(port)), timeout=0.5):
    pass
PY
      return 0
    fi
    sleep 0.2
  done
  return 1
}

known_server_from_identity() {
  python3 - "$1" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as stream:
    identity = json.load(stream)
if identity.get("endpoint_identity"):
    print(identity["endpoint_identity"])
elif identity.get("encryption_key_id"):
    print(":".join([
        identity["encryption_key_id"],
        identity["encryption_public_key"],
        identity["signing_key_id"],
        identity["signing_public_key"],
    ]))
else:
    print(f"{identity['key_id']}:{identity['public_key']}")
PY
}

authorized_client_from_identity() {
  python3 - "$1" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as stream:
    identity = json.load(stream)
print(f"{identity['signing_key_id']}:{identity['signing_public_key']}")
PY
}

build_rstream() {
  if [ -n "${RSTREAM_BIN:-}" ]; then
    printf "%s\n" "$RSTREAM_BIN"
    return
  fi
  local bin="$TMP_DIR/rstream"
  (cd "$ROOT" && go build -o "$bin" ./cmd/rstream)
  printf "%s\n" "$bin"
}

make_cert() {
  openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
    -subj "/CN=localhost" \
    -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" \
    -addext "basicConstraints=critical,CA:TRUE" \
    -addext "keyUsage=critical,keyCertSign,digitalSignature,keyEncipherment" \
    -addext "extendedKeyUsage=serverAuth" \
    -keyout "$TMP_DIR/webtty.key" \
    -out "$TMP_DIR/webtty.crt" >/dev/null 2>&1
}

start_go_server() {
  local label=$1
  shift
  local log="$TMP_DIR/server-${label//[^A-Za-z0-9_.-]/_}.log"
  "$RSTREAM" webtty server "$@" >"$log" 2>&1 &
  PIDS+=("$!")
}

start_go_server_with_home() {
  local label=$1
  local home=$2
  shift 2
  local log="$TMP_DIR/server-${label//[^A-Za-z0-9_.-]/_}.log"
  HOME="$home" "$RSTREAM" webtty server "$@" >"$log" 2>&1 &
  PIDS+=("$!")
}

go_exec_text() {
  local expected=$1
  shift
  local out
  out=$("$RSTREAM" webtty exec "$@" 2>&1)
  printf "%s\n" "$out"
  printf "%s" "$out" | grep -q "$expected"
}

go_exec_text_with_home() {
  local home=$1
  local expected=$2
  shift 2
  local out
  out=$(HOME="$home" "$RSTREAM" webtty exec "$@" 2>&1)
  printf "%s\n" "$out"
  printf "%s" "$out" | grep -q "$expected"
}

go_client_stdin() {
  local expected=$1
  shift
  local out
  out=$(printf "payload\n" | "$RSTREAM" webtty client "$@" 2>&1)
  printf "%s\n" "$out"
  printf "%s" "$out" | grep -q "$expected"
}

start_cpp_server() {
  local label=$1
  shift
  local log="$TMP_DIR/cpp-server-${label//[^A-Za-z0-9_.-]/_}.log"
  "$CPP_SERVER" "$@" >"$log" 2>&1 &
  PIDS+=("$!")
}

start_cpp_server_with_home() {
  local label=$1
  local home=$2
  shift 2
  local log="$TMP_DIR/cpp-server-${label//[^A-Za-z0-9_.-]/_}.log"
  HOME="$home" "$CPP_SERVER" "$@" >"$log" 2>&1 &
  PIDS+=("$!")
}

cpp_client_text() {
  local expected=$1
  shift
  local out
  out=$("$CPP_CLIENT" "$@" 2>&1)
  printf "%s\n" "$out"
  printf "%s" "$out" | grep -q "$expected"
}

cpp_client_text_with_home() {
  local home=$1
  local expected=$2
  shift 2
  local out
  out=$(HOME="$home" "$CPP_CLIENT" "$@" 2>&1)
  printf "%s\n" "$out"
  printf "%s" "$out" | grep -q "$expected"
}

go_client_remote_exit_before_eof() {
  local label=$1
  local expected_size=$2
  shift 2
  local fifo="$TMP_DIR/${label//[^A-Za-z0-9_.-]/_}.fifo"
  local output="$TMP_DIR/${label//[^A-Za-z0-9_.-]/_}.out"
  mkfifo "$fifo"
  { dd if="$EARLY_EXIT_PAYLOAD" bs="$expected_size" count=1 status=none; sleep 5; } >"$fifo" &
  local producer=$!
  PIDS+=("$producer")
  local started
  started=$(python3 -c 'import time; print(time.monotonic())')
  local rc=0
  "$RSTREAM" webtty exec "$@" -- /usr/bin/env python3 -c "import sys; print(len(sys.stdin.buffer.read($expected_size)))" <"$fifo" >"$output" 2>&1 || rc=$?
  local finished
  finished=$(python3 -c 'import time; print(time.monotonic())')
  kill "$producer" 2>/dev/null || true
  wait "$producer" 2>/dev/null || true
  if [ "$rc" -ne 0 ]; then
    cat "$output"
    return "$rc"
  fi
  python3 - "$started" "$finished" <<'PY'
import sys
elapsed = float(sys.argv[2]) - float(sys.argv[1])
if elapsed >= 4:
    raise SystemExit(f"client waited {elapsed:.3f}s for local stdin EOF after remote exit")
PY
  grep -q "$expected_size" "$output"
}

cpp_client_remote_exit_before_eof() {
  local label=$1
  local expected_size=$2
  shift 2
  local fifo="$TMP_DIR/${label//[^A-Za-z0-9_.-]/_}.fifo"
  local output="$TMP_DIR/${label//[^A-Za-z0-9_.-]/_}.out"
  mkfifo "$fifo"
  { dd if="$EARLY_EXIT_PAYLOAD" bs="$expected_size" count=1 status=none; sleep 5; } >"$fifo" &
  local producer=$!
  PIDS+=("$producer")
  local started
  started=$(python3 -c 'import time; print(time.monotonic())')
  local rc=0
  "$CPP_CLIENT" "$@" -i -T -- /usr/bin/env python3 -c "import sys; print(len(sys.stdin.buffer.read($expected_size)))" <"$fifo" >"$output" 2>&1 || rc=$?
  local finished
  finished=$(python3 -c 'import time; print(time.monotonic())')
  kill "$producer" 2>/dev/null || true
  wait "$producer" 2>/dev/null || true
  if [ "$rc" -ne 0 ]; then
    cat "$output"
    return "$rc"
  fi
  python3 - "$started" "$finished" <<'PY'
import sys
elapsed = float(sys.argv[2]) - float(sys.argv[1])
if elapsed >= 4:
    raise SystemExit(f"client waited {elapsed:.3f}s for local stdin EOF after remote exit")
PY
  grep -q "$expected_size" "$output"
}

go_client_archive() {
  local out
  if ! out=$(tar -cf - -C "$TMP_DIR" archive-payload | "$RSTREAM" webtty exec "$@" -- /usr/bin/env python3 -c "$ARCHIVE_READER" 2>&1); then
    printf "%s\n" "$out"
    return 1
  fi
  printf "%s" "$out" | grep -q "$ARCHIVE_DIGEST"
}

cpp_client_archive() {
  local out
  if ! out=$(tar -cf - -C "$TMP_DIR" archive-payload | "$CPP_CLIENT" "$@" -i -T -- /usr/bin/env python3 -c "$ARCHIVE_READER" 2>&1); then
    printf "%s\n" "$out"
    return 1
  fi
  printf "%s" "$out" | grep -q "$ARCHIVE_DIGEST"
}

find_cpp_binary() {
  local name=$1
  local root
  local found
  for root in "$CPP_ROOT"/build-*/release/bin "$CPP_ROOT/build/codex-webtty-check" "$CPP_ROOT"/build-*/build "$CPP_ROOT/build" "$CPP_ROOT"/out/build/* "$CPP_ROOT"; do
    if [ ! -e "$root" ]; then
      continue
    fi
    while IFS= read -r found; do
      if "$found" --help >/dev/null 2>&1; then
        printf "%s\n" "$found"
        return 0
      fi
      printf "Ignoring unusable C++ runtime binary: %s\n" "$found" >&2
    done < <(find "$root" -path "*/$name" -type f -perm -111 2>/dev/null | sort)
  done
  return 1
}

RSTREAM=$(build_rstream)
if [ ! -x "$RSTREAM" ]; then
  printf "ERROR missing rstream CLI: %s\n" "$RSTREAM" >&2
  exit 2
fi
runtime_client_identity="$TMP_DIR/runtime-client.identity.json"
"$RSTREAM" webtty identity create --identity-file "$runtime_client_identity" -o json >/dev/null
runtime_authorized_client=$(authorized_client_from_identity "$runtime_client_identity")
runtime_denied_client_identity="$TMP_DIR/runtime-denied-client.identity.json"
"$RSTREAM" webtty identity create --identity-file "$runtime_denied_client_identity" -o json >/dev/null

echo "=== go cli direct ==="
ws_port=$(reserve_port)
ws_addr="127.0.0.1:$ws_port"
start_go_server "go-ws" --listen "$ws_addr" --allow-unauthenticated
wait_tcp "$ws_addr"
run_case "go/ws/spawn/plaintext" go_exec_text "go-ws" --url "ws://$ws_addr" -- /bin/sh -c "printf go-ws"
run_case "go/ws/interactive-stdin" go_client_stdin "payload" --url "ws://$ws_addr" -i -T -- /bin/sh -c "read line; printf %s \"\$line\""

login_port=$(reserve_port)
login_addr="127.0.0.1:$login_port"
start_go_server "go-ws-login" --listen "$login_addr" --allow-unauthenticated --execution-mode login --login-user "$(id -un)"
wait_tcp "$login_addr"
run_case "go/ws/login-current-user" go_exec_text "go-login" --url "ws://$login_addr" -- /bin/sh -c "printf go-login"

e2e_port=$(reserve_port)
e2e_addr="127.0.0.1:$e2e_port"
e2e_identity="$TMP_DIR/go-server.identity.json"
start_go_server "go-ws-e2e" --listen "$e2e_addr" --allow-unauthenticated --e2e --identity-file "$e2e_identity" --authorized-client-key "$runtime_authorized_client"
wait_tcp "$e2e_addr"
known_server=$(known_server_from_identity "$e2e_identity")
run_case "go/ws/e2e" go_exec_text "go-e2e" --url "ws://$e2e_addr" --known-server-key "$known_server" --identity-file "$runtime_client_identity" -- /bin/sh -c "printf go-e2e"
run_case_expect_fail "go/ws/e2e-requires-known-server" "known server endpoint identity" "$RSTREAM" webtty exec --url "ws://$e2e_addr" -- /bin/sh -c "printf no"

dynamic_home_server="$TMP_DIR/dynamic-server-home"
dynamic_home_client="$TMP_DIR/dynamic-client-home"
mkdir -p "$dynamic_home_server" "$dynamic_home_client"
dynamic_server_identity="$TMP_DIR/dynamic-server.identity.json"
dynamic_client_identity="$TMP_DIR/dynamic-client.identity.json"
HOME="$dynamic_home_server" "$RSTREAM" webtty identity create --name shell -o json >"$dynamic_server_identity"
HOME="$dynamic_home_client" "$RSTREAM" webtty identity create --name operator-workstation -o json >"$dynamic_client_identity"
dynamic_known_server=$(known_server_from_identity "$dynamic_server_identity")
dynamic_authorized_client=$(known_server_from_identity "$dynamic_client_identity")
HOME="$dynamic_home_client" "$RSTREAM" webtty known-server add shell --key "$dynamic_known_server" >/dev/null
dynamic_e2e_port=$(reserve_port)
dynamic_e2e_addr="127.0.0.1:$dynamic_e2e_port"
start_go_server_with_home "go-ws-e2e-dynamic-authz" "$dynamic_home_server" --listen "$dynamic_e2e_addr" --allow-unauthenticated --e2e --identity shell
wait_tcp "$dynamic_e2e_addr"
run_case_expect_fail "go/ws/e2e-dynamic-unauthorized" "signing key is not authorized" env HOME="$dynamic_home_client" "$RSTREAM" webtty exec --url "ws://$dynamic_e2e_addr" --known-server-key "$dynamic_known_server" --identity operator-workstation -- /bin/sh -c "printf no"
HOME="$dynamic_home_server" "$RSTREAM" webtty authorized-client add operator-workstation --identity shell --key "$dynamic_authorized_client" >/dev/null
run_case "go/ws/e2e-dynamic-authorized" go_exec_text_with_home "$dynamic_home_client" "go-dynamic-e2e" --url "ws://$dynamic_e2e_addr" --known-server-key "$dynamic_known_server" --identity operator-workstation -- /bin/sh -c "printf go-dynamic-e2e"
HOME="$dynamic_home_client" "$RSTREAM" webtty known-server set-identity shell --identity operator-workstation >/dev/null
run_case "go/ws/e2e-known-server-client-identity" go_exec_text_with_home "$dynamic_home_client" "go-known-server-identity" --url "ws://$dynamic_e2e_addr" --known-server shell -- /bin/sh -c "printf go-known-server-identity"
HOME="$dynamic_home_server" "$RSTREAM" webtty authorized-client remove operator-workstation --identity shell >/dev/null
run_case_expect_fail "go/ws/e2e-dynamic-removed" "signing key is not authorized" env HOME="$dynamic_home_client" "$RSTREAM" webtty exec --url "ws://$dynamic_e2e_addr" --known-server-key "$dynamic_known_server" --identity operator-workstation -- /bin/sh -c "printf no"

plain_port=$(reserve_port)
plain_addr="127.0.0.1:$plain_port"
start_go_server "go-plain" --listen "$plain_addr" --transport plain --allow-unauthenticated
wait_tcp "$plain_addr"
run_case "go/plain/spawn/plaintext" go_exec_text "go-plain" --transport plain --url "$plain_addr" -- /bin/sh -c "printf go-plain"

plain_e2e_port=$(reserve_port)
plain_e2e_addr="127.0.0.1:$plain_e2e_port"
plain_e2e_identity="$TMP_DIR/go-plain.identity.json"
start_go_server "go-plain-e2e" --listen "$plain_e2e_addr" --transport plain --allow-unauthenticated --e2e --identity-file "$plain_e2e_identity" --authorized-client-key "$runtime_authorized_client"
wait_tcp "$plain_e2e_addr"
plain_known_server=$(known_server_from_identity "$plain_e2e_identity")
run_case "go/plain/e2e" go_exec_text "go-plain-e2e" --transport plain --url "$plain_e2e_addr" --known-server-key "$plain_known_server" --identity-file "$runtime_client_identity" -- /bin/sh -c "printf go-plain-e2e"

if command -v openssl >/dev/null 2>&1; then
  make_cert

  plain_tls_port=$(reserve_port)
  plain_tls_addr="127.0.0.1:$plain_tls_port"
  start_go_server "go-plain-tls" --listen "$plain_tls_addr" --transport plain --allow-unauthenticated --tls-cert-file "$TMP_DIR/webtty.crt" --tls-key-file "$TMP_DIR/webtty.key"
  wait_tcp "$plain_tls_addr"
  run_case "go/plain-tls/spawn/plaintext" go_exec_text "go-plain-tls" --transport plain --url "tls://$plain_tls_addr" --tls-ca-file "$TMP_DIR/webtty.crt" -- /bin/sh -c "printf go-plain-tls"

  plain_tls_e2e_port=$(reserve_port)
  plain_tls_e2e_addr="127.0.0.1:$plain_tls_e2e_port"
  plain_tls_e2e_identity="$TMP_DIR/go-plain-tls.identity.json"
  start_go_server "go-plain-tls-e2e" --listen "$plain_tls_e2e_addr" --transport plain --allow-unauthenticated --tls-cert-file "$TMP_DIR/webtty.crt" --tls-key-file "$TMP_DIR/webtty.key" --e2e --identity-file "$plain_tls_e2e_identity" --authorized-client-key "$runtime_authorized_client"
  wait_tcp "$plain_tls_e2e_addr"
  plain_tls_known_server=$(known_server_from_identity "$plain_tls_e2e_identity")
  run_case "go/plain-tls/e2e" go_exec_text "go-plain-tls-e2e" --transport plain --url "tls://$plain_tls_e2e_addr" --tls-ca-file "$TMP_DIR/webtty.crt" --known-server-key "$plain_tls_known_server" --identity-file "$runtime_client_identity" -- /bin/sh -c "printf go-plain-tls-e2e"

  wt_port=$(reserve_port)
  wt_addr="127.0.0.1:$wt_port"
  start_go_server "go-webtransport" --listen "$wt_addr" --transport webtransport --allow-unauthenticated --tls-cert-file "$TMP_DIR/webtty.crt" --tls-key-file "$TMP_DIR/webtty.key"
  sleep 0.5
  run_case "go/webtransport/spawn/plaintext" go_exec_text "go-webtransport" --transport webtransport --url "https://$wt_addr/" --tls-insecure-skip-verify -- /bin/sh -c "printf go-webtransport"

  wt_e2e_port=$(reserve_port)
  wt_e2e_addr="127.0.0.1:$wt_e2e_port"
  wt_e2e_identity="$TMP_DIR/go-webtransport.identity.json"
  start_go_server "go-webtransport-e2e" --listen "$wt_e2e_addr" --transport webtransport --allow-unauthenticated --tls-cert-file "$TMP_DIR/webtty.crt" --tls-key-file "$TMP_DIR/webtty.key" --e2e --identity-file "$wt_e2e_identity" --authorized-client-key "$runtime_authorized_client"
  sleep 0.5
  wt_known_server=$(known_server_from_identity "$wt_e2e_identity")
  run_case "go/webtransport/e2e" go_exec_text "go-webtransport-e2e" --transport webtransport --url "https://$wt_e2e_addr/" --tls-insecure-skip-verify --known-server-key "$wt_known_server" --identity-file "$runtime_client_identity" -- /bin/sh -c "printf go-webtransport-e2e"
else
  printf "SKIP %-48s openssl not available\n" "go/plain-tls+webtransport"
fi

go_cfg_port=$(reserve_port)
go_cfg_addr="127.0.0.1:$go_cfg_port"
go_cfg_identity="$TMP_DIR/go-config.identity.json"
go_cfg="$TMP_DIR/go-webtty.yaml"
cat >"$go_cfg" <<EOF
version: 1
server:
  listen: $go_cfg_addr
  transport: websocket
  allowUnauthenticated: true
e2e:
  enabled: true
  identityFile: $go_cfg_identity
  authorizedClientKeys:
    - $runtime_authorized_client
EOF
start_go_server "go-config-e2e" --webtty-config "$go_cfg"
wait_tcp "$go_cfg_addr"
go_cfg_known_server=$(known_server_from_identity "$go_cfg_identity")
run_case "go/config/ws/e2e" go_exec_text "go-config-e2e" --url "ws://$go_cfg_addr" --known-server-key "$go_cfg_known_server" --identity-file "$runtime_client_identity" -- /bin/sh -c "printf go-config-e2e"
run_case_expect_fail "go/registered/requires-os-policy" "login execution mode requires --login-user" "$RSTREAM" webtty server --server-id runtime-missing
run_case_expect_fail "go/registered/missing-enrollment" "no such file or directory" env RSTREAM_ENGINE=127.0.0.1:1 RSTREAM_AUTHENTICATION_TOKEN=token "$RSTREAM" webtty server --server-id runtime-missing --execution-mode spawn

echo "=== js client against go server ==="
if [ -f "$JS_ROOT/packages/webtty/package.json" ]; then
  js_port=$(reserve_port)
  js_addr="127.0.0.1:$js_port"
  js_fs="$TMP_DIR/js-fs"
  mkdir -p "$js_fs"
  start_go_server "js-ws" --listen "$js_addr" --allow-unauthenticated --fs-root "$js_fs"
  wait_tcp "$js_addr"
  run_case "js/ws/runtime" env WEBTTY_RUNTIME_E2E_URL="ws://$js_addr" npm --prefix "$JS_ROOT/packages/webtty" run test:runtime

  js_e2e_port=$(reserve_port)
  js_e2e_addr="127.0.0.1:$js_e2e_port"
  js_e2e_identity="$TMP_DIR/js-go.identity.json"
  start_go_server "js-ws-e2e" --listen "$js_e2e_addr" --allow-unauthenticated --e2e --identity-file "$js_e2e_identity" --authorized-client-key "$runtime_authorized_client"
  wait_tcp "$js_e2e_addr"
  js_known_server=$(known_server_from_identity "$js_e2e_identity")
  run_case "js/ws/e2e-runtime" env WEBTTY_RUNTIME_E2E_URL="ws://$js_e2e_addr" WEBTTY_RUNTIME_E2E_RECIPIENT="$js_known_server" WEBTTY_RUNTIME_E2E_SERVER_IDENTITY="$js_known_server" WEBTTY_RUNTIME_E2E_CLIENT_IDENTITY_FILE="$runtime_client_identity" WEBTTY_RUNTIME_E2E_KEY_CONTEXT="runtime/js-ws" npm --prefix "$JS_ROOT/packages/webtty" run test:runtime
  mkdir -p "$TMP_DIR/js-home/.rstream/webtty"
  python3 - "$js_e2e_identity" "$TMP_DIR/js-home/.rstream/webtty/known_servers.json" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as stream:
    identity = json.load(stream)
payload = {
    "version": 1,
    "crypto_suite": "webtty-e2e-x25519-hpke-aes-256-gcm-v1",
    "known_servers": [{
        "name": "go-for-js",
        "key_id": identity["encryption_key_id"],
        "public_key": identity["encryption_public_key"],
        "signing_key_id": identity["signing_key_id"],
        "signing_public_key": identity["signing_public_key"],
    }],
}
with open(sys.argv[2], "w", encoding="utf-8") as stream:
    json.dump(payload, stream)
PY
  run_case "js/ws/e2e-default-trust" env HOME="$TMP_DIR/js-home" WEBTTY_RUNTIME_E2E_URL="ws://$js_e2e_addr" WEBTTY_RUNTIME_E2E_SERVER_IDENTITY="$js_known_server" WEBTTY_RUNTIME_E2E_CLIENT_IDENTITY_FILE="$runtime_client_identity" WEBTTY_RUNTIME_E2E_LOCAL_TRUST=1 WEBTTY_RUNTIME_E2E_KEY_CONTEXT="runtime/js-local-trust" npm --prefix "$JS_ROOT/packages/webtty" run test:runtime
  chrome_bin="${RSTREAM_WEBTTY_CHROME_BIN:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"
  if [ -x "$chrome_bin" ] && command -v openssl >/dev/null 2>&1; then
    run_case "js/browser/webtransport/runtime" env RSTREAM_BIN="$RSTREAM" RSTREAM_JS_REPO="$JS_ROOT" RSTREAM_WEBTTY_CHROME_BIN="$chrome_bin" node "$ROOT/test/e2e/webtty-browser-webtransport.mjs"
  else
    printf "SKIP %-48s Chrome or openssl not available\n" "js/browser/webtransport/runtime"
  fi
else
  printf "SKIP %-48s JS repo not found at %s\n" "js/runtime" "$JS_ROOT"
fi

echo "=== c++ cli interop ==="
CPP_SERVER="${RSTREAM_CPP_WEBTTY_SERVER_BIN:-$(find_cpp_binary rstream-webtty-server || true)}"
CPP_CLIENT="${RSTREAM_CPP_WEBTTY_CLIENT_BIN:-$(find_cpp_binary rstream-webtty-client || true)}"
if [ -x "${CPP_SERVER:-}" ] && [ -x "${CPP_CLIENT:-}" ]; then
  EARLY_EXIT_SIZE=1048576
  EARLY_EXIT_PAYLOAD="$TMP_DIR/early-exit-payload"
  dd if=/dev/urandom of="$EARLY_EXIT_PAYLOAD" bs="$EARLY_EXIT_SIZE" count=1 status=none
  ARCHIVE_PAYLOAD="$TMP_DIR/archive-payload"
  dd if=/dev/urandom of="$ARCHIVE_PAYLOAD" bs=1048576 count=16 status=none
  ARCHIVE_DIGEST=$(python3 -c 'import hashlib,sys; print(hashlib.sha256(open(sys.argv[1], "rb").read()).hexdigest())' "$ARCHIVE_PAYLOAD")
  ARCHIVE_READER="import hashlib,sys,tarfile; archive=tarfile.open(fileobj=sys.stdin.buffer,mode='r|'); member=next(item for item in archive if item.name=='archive-payload'); stream=archive.extractfile(member); print(hashlib.sha256(stream.read()).hexdigest())"
  cpp_ws_port=$(reserve_port)
  cpp_ws_addr="127.0.0.1:$cpp_ws_port"
  start_cpp_server "cpp-ws" --uri="$cpp_ws_addr" --transport=websocket --allow-unauthenticated
  wait_tcp "$cpp_ws_addr"
  run_case "go-client/cpp-server/ws" go_exec_text "cpp-server-ws" --url "ws://$cpp_ws_addr" -- /bin/sh -c "printf cpp-server-ws"
  run_case "cpp-client/cpp-server/ws" cpp_client_text "cpp-client-ws" --uri="$cpp_ws_addr" --transport=websocket -I -T -- /bin/sh -c "printf cpp-client-ws"
  run_case "go-client/cpp-server/ws/remote-exit-before-eof" go_client_remote_exit_before_eof "go-cpp-ws-early-exit" "$EARLY_EXIT_SIZE" --url "ws://$cpp_ws_addr"
  run_case "go-client/cpp-server/ws/binary-archive" go_client_archive --url "ws://$cpp_ws_addr"
  run_case "cpp-client/cpp-server/ws/binary-archive" cpp_client_archive --uri="$cpp_ws_addr" --transport=websocket

  cpp_login_port=$(reserve_port)
  cpp_login_addr="127.0.0.1:$cpp_login_port"
  start_cpp_server "cpp-ws-login" --uri="$cpp_login_addr" --transport=websocket --allow-unauthenticated --execution-mode=login --login-user="$(id -un)"
  wait_tcp "$cpp_login_addr"
  run_case "go-client/cpp-server/ws/login-current-user" go_exec_text "cpp-server-login" --url "ws://$cpp_login_addr" -- /bin/sh -c "printf cpp-server-login"

  run_case_expect_fail "cpp-server/login-requires-os-policy" "login execution mode requires --login-user" "$CPP_SERVER" --uri=127.0.0.1:0 --transport=websocket --allow-unauthenticated --execution-mode=login

  go_cpp_ws_port=$(reserve_port)
  go_cpp_ws_addr="127.0.0.1:$go_cpp_ws_port"
  start_go_server "go-for-cpp-ws" --listen "$go_cpp_ws_addr" --allow-unauthenticated
  wait_tcp "$go_cpp_ws_addr"
  run_case "cpp-client/go-server/ws" cpp_client_text "go-server-ws" --uri="$go_cpp_ws_addr" --transport=websocket -I -T -- /bin/sh -c "printf go-server-ws"
  run_case "cpp-client/go-server/ws/remote-exit-before-eof" cpp_client_remote_exit_before_eof "cpp-go-ws-early-exit" "$EARLY_EXIT_SIZE" --uri="$go_cpp_ws_addr" --transport=websocket
  run_case "go-client/go-server/ws/binary-archive" go_client_archive --url "ws://$go_cpp_ws_addr"
  run_case "cpp-client/go-server/ws/binary-archive" cpp_client_archive --uri="$go_cpp_ws_addr" --transport=websocket

  cpp_plain_port=$(reserve_port)
  cpp_plain_addr="127.0.0.1:$cpp_plain_port"
  start_cpp_server "cpp-plain" --uri="$cpp_plain_addr" --transport=plain --allow-unauthenticated
  wait_tcp "$cpp_plain_addr"
  run_case "go-client/cpp-server/plain" go_exec_text "cpp-server-plain" --transport plain --url "$cpp_plain_addr" -- /bin/sh -c "printf cpp-server-plain"
  run_case "cpp-client/cpp-server/plain" cpp_client_text "cpp-client-plain" --uri="$cpp_plain_addr" --transport=plain -I -T -- /bin/sh -c "printf cpp-client-plain"
  run_case "go-client/cpp-server/plain/remote-exit-before-eof" go_client_remote_exit_before_eof "go-cpp-plain-early-exit" "$EARLY_EXIT_SIZE" --transport plain --url "$cpp_plain_addr"

  go_cpp_plain_port=$(reserve_port)
  go_cpp_plain_addr="127.0.0.1:$go_cpp_plain_port"
  start_go_server "go-for-cpp-plain" --listen "$go_cpp_plain_addr" --transport plain --allow-unauthenticated
  wait_tcp "$go_cpp_plain_addr"
  run_case "cpp-client/go-server/plain" cpp_client_text "go-server-plain" --uri="$go_cpp_plain_addr" --transport=plain -I -T -- /bin/sh -c "printf go-server-plain"
  run_case "cpp-client/go-server/plain/remote-exit-before-eof" cpp_client_remote_exit_before_eof "cpp-go-plain-early-exit" "$EARLY_EXIT_SIZE" --uri="$go_cpp_plain_addr" --transport=plain

  cpp_e2e_port=$(reserve_port)
  cpp_e2e_addr="127.0.0.1:$cpp_e2e_port"
  cpp_e2e_identity="$TMP_DIR/cpp-server.identity.json"
  start_cpp_server "cpp-ws-e2e" --uri="$cpp_e2e_addr" --transport=websocket --allow-unauthenticated --e2e --identity-file="$cpp_e2e_identity" --authorized-client-key="$runtime_authorized_client"
  wait_tcp "$cpp_e2e_addr"
  cpp_known_server=$(known_server_from_identity "$cpp_e2e_identity")
  run_case "go-client/cpp-server/ws/e2e-authenticated" go_exec_text "cpp-server-e2e" --url "ws://$cpp_e2e_addr" --known-server-key "$cpp_known_server" --identity-file "$runtime_client_identity" -- /bin/sh -c "printf cpp-server-e2e"
  run_case "go-client/cpp-server/ws/e2e/remote-exit-before-eof" go_client_remote_exit_before_eof "go-cpp-e2e-early-exit" "$EARLY_EXIT_SIZE" --url "ws://$cpp_e2e_addr" --known-server-key "$cpp_known_server" --identity-file "$runtime_client_identity"
  run_case "cpp-client/cpp-server/ws/e2e-authenticated" cpp_client_text "cpp-client-e2e" --uri="$cpp_e2e_addr" --transport=websocket --known-server-key="$cpp_known_server" --identity-file="$runtime_client_identity" -I -T -- /bin/sh -c "printf cpp-client-e2e"
  run_case_expect_fail "cpp-server/e2e-rejects-unauthorized-client" "WebTTY client signing key is not authorized" "$RSTREAM" webtty exec --url "ws://$cpp_e2e_addr" --known-server-key "$cpp_known_server" --identity-file "$runtime_denied_client_identity" -- /bin/sh -c "printf no"

  cpp_dynamic_home="$TMP_DIR/cpp-dynamic-server-home"
  mkdir -p "$cpp_dynamic_home"
  cpp_dynamic_identity_json="$TMP_DIR/cpp-dynamic-server.identity.json"
  HOME="$cpp_dynamic_home" "$RSTREAM" webtty identity create --name shell -o json >"$cpp_dynamic_identity_json"
  cpp_dynamic_known_server=$(known_server_from_identity "$cpp_dynamic_identity_json")
  cpp_dynamic_port=$(reserve_port)
  cpp_dynamic_addr="127.0.0.1:$cpp_dynamic_port"
  start_cpp_server_with_home "cpp-ws-e2e-dynamic-authz" "$cpp_dynamic_home" --uri="$cpp_dynamic_addr" --transport=websocket --allow-unauthenticated --e2e --identity=shell
  wait_tcp "$cpp_dynamic_addr"
  run_case_expect_fail "cpp-server/e2e-dynamic-unauthorized" "WebTTY client signing key is not authorized" "$RSTREAM" webtty exec --url "ws://$cpp_dynamic_addr" --known-server-key "$cpp_dynamic_known_server" --identity-file "$runtime_client_identity" -- /bin/sh -c "printf no"
  HOME="$cpp_dynamic_home" "$RSTREAM" webtty authorized-client add runtime --identity shell --key "$runtime_authorized_client" >/dev/null
  run_case "go-client/cpp-server/ws/e2e-dynamic-authorized" go_exec_text "cpp-dynamic-e2e" --url "ws://$cpp_dynamic_addr" --known-server-key "$cpp_dynamic_known_server" --identity-file "$runtime_client_identity" -- /bin/sh -c "printf cpp-dynamic-e2e"

  cpp_env_port=$(reserve_port)
  cpp_env_addr="127.0.0.1:$cpp_env_port"
  cpp_env_identity="$TMP_DIR/cpp-env.identity.json"
  cpp_env_log="$TMP_DIR/cpp-server-env-e2e.log"
  RSTREAM_WEBTTY_IDENTITY_FILE="$cpp_env_identity" RSTREAM_WEBTTY_AUTHORIZED_CLIENT_KEYS="$runtime_authorized_client" "$CPP_SERVER" --uri="$cpp_env_addr" --transport=websocket --allow-unauthenticated --e2e >"$cpp_env_log" 2>&1 &
  PIDS+=("$!")
  wait_tcp "$cpp_env_addr"
  cpp_env_known_server=$(known_server_from_identity "$cpp_env_identity")
  run_case "go-client/cpp-server/ws/e2e-env-identity" go_exec_text "cpp-server-env-e2e" --url "ws://$cpp_env_addr" --known-server-key "$cpp_env_known_server" --identity-file "$runtime_client_identity" -- /bin/sh -c "printf cpp-server-env-e2e"

  go_cpp_e2e_port=$(reserve_port)
  go_cpp_e2e_addr="127.0.0.1:$go_cpp_e2e_port"
  go_cpp_e2e_identity="$TMP_DIR/go-for-cpp.identity.json"
  start_go_server "go-for-cpp-e2e" --listen "$go_cpp_e2e_addr" --allow-unauthenticated --e2e --identity-file "$go_cpp_e2e_identity" --authorized-client-key "$runtime_authorized_client"
  wait_tcp "$go_cpp_e2e_addr"
  go_cpp_known_server=$(known_server_from_identity "$go_cpp_e2e_identity")
  go_cpp_wrong_known_server=$(known_server_from_identity "$runtime_denied_client_identity")
  mkdir -p "$TMP_DIR/cpp-empty-home"
  run_case_expect_fail "cpp-client/go-server/ws/e2e-requires-known-server" "E2E client mode requires" env HOME="$TMP_DIR/cpp-empty-home" "$CPP_CLIENT" --uri="$go_cpp_e2e_addr" --transport=websocket --e2e -I -T -- /bin/sh -c "printf no"
  run_case_expect_fail "cpp-client/go-server/ws/e2e-rejects-wrong-server-key" "WebTTY server endpoint identity does not match" "$CPP_CLIENT" --uri="$go_cpp_e2e_addr" --transport=websocket --known-server-key="$go_cpp_wrong_known_server" --identity-file="$runtime_client_identity" -I -T -- /bin/sh -c "printf no"
  run_case_expect_fail "cpp-client/go-server/ws/e2e-rejects-unauthorized-client" "WebTTY client signing key is not authorized" "$CPP_CLIENT" --uri="$go_cpp_e2e_addr" --transport=websocket --known-server-key="$go_cpp_known_server" --identity-file="$runtime_denied_client_identity" -I -T -- /bin/sh -c "printf no"
  run_case "cpp-client/go-server/ws/e2e-authenticated" cpp_client_text "go-server-e2e" --uri="$go_cpp_e2e_addr" --transport=websocket --known-server-key="$go_cpp_known_server" --identity-file="$runtime_client_identity" -I -T -- /bin/sh -c "printf go-server-e2e"
  run_case "cpp-client/go-server/ws/e2e/remote-exit-before-eof" cpp_client_remote_exit_before_eof "cpp-go-e2e-early-exit" "$EARLY_EXIT_SIZE" --uri="$go_cpp_e2e_addr" --transport=websocket --known-server-key="$go_cpp_known_server" --identity-file="$runtime_client_identity"
  cpp_client_credential="$TMP_DIR/cpp-client-credential.json"
  printf '{"type":"test.workspace.credential","v":1}\n' >"$cpp_client_credential"
  run_case "cpp-client/go-server/ws/e2e-client-credential" cpp_client_text "go-server-e2e-credential" --uri="$go_cpp_e2e_addr" --transport=websocket --known-server-key="$go_cpp_known_server" --identity-file="$runtime_client_identity" --client-credential-file="$cpp_client_credential" -I -T -- /bin/sh -c "printf go-server-e2e-credential"
  mkdir -p "$TMP_DIR/cpp-home/.rstream/webtty"
  python3 - "$go_cpp_e2e_identity" "$TMP_DIR/cpp-home/.rstream/webtty/known_servers.json" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as stream:
    identity = json.load(stream)
payload = {
    "version": 1,
    "crypto_suite": "webtty-e2e-x25519-hpke-aes-256-gcm-v1",
    "known_servers": [{
        "name": "go-for-cpp",
        "key_id": identity["encryption_key_id"],
        "public_key": identity["encryption_public_key"],
        "signing_key_id": identity["signing_key_id"],
        "signing_public_key": identity["signing_public_key"],
    }],
}
with open(sys.argv[2], "w", encoding="utf-8") as stream:
    json.dump(payload, stream)
PY
  run_case "cpp-client/go-server/ws/e2e-default-trust" cpp_client_text_with_home "$TMP_DIR/cpp-home" "go-server-e2e-default" --uri="$go_cpp_e2e_addr" --transport=websocket --e2e --identity-file="$runtime_client_identity" -I -T -- /bin/sh -c "printf go-server-e2e-default"
  mkdir -p "$TMP_DIR/cpp-home-associated/.rstream/webtty/identities" "$TMP_DIR/cpp-home-associated/.rstream/webtty"
  cp "$runtime_client_identity" "$TMP_DIR/cpp-home-associated/.rstream/webtty/identities/runtime-client.identity.json"
  python3 - "$go_cpp_e2e_identity" "$TMP_DIR/cpp-home-associated/.rstream/webtty/known_servers.json" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as stream:
    identity = json.load(stream)
payload = {
    "version": 1,
    "crypto_suite": "webtty-e2e-x25519-hpke-aes-256-gcm-v1",
    "known_servers": [{
        "name": "go-for-cpp",
        "key_id": identity["encryption_key_id"],
        "public_key": identity["encryption_public_key"],
        "signing_key_id": identity["signing_key_id"],
        "signing_public_key": identity["signing_public_key"],
        "client_identity": "runtime-client",
    }],
}
with open(sys.argv[2], "w", encoding="utf-8") as stream:
    json.dump(payload, stream)
PY
  run_case "cpp-client/go-server/ws/e2e-known-server-client-identity" cpp_client_text_with_home "$TMP_DIR/cpp-home-associated" "go-server-e2e-associated" --uri="$go_cpp_e2e_addr" --transport=websocket --e2e --known-server=go-for-cpp -I -T -- /bin/sh -c "printf go-server-e2e-associated"

  cpp_cfg_port=$(reserve_port)
  cpp_cfg_addr="127.0.0.1:$cpp_cfg_port"
  cpp_cfg_identity="$TMP_DIR/cpp-config.identity.json"
  cpp_cfg_authorized="$TMP_DIR/cpp-config-authorized-clients.json"
  "$RSTREAM" webtty authorized-client add runtime --authorized-clients-file "$cpp_cfg_authorized" --key "$runtime_authorized_client" >/dev/null
  cpp_cfg="$TMP_DIR/cpp-webtty.yaml"
  cat >"$cpp_cfg" <<EOF
version: 1
server:
  listen: $cpp_cfg_addr
  transport: websocket
  allowUnauthenticated: true
e2e:
  enabled: true
  identityFile: $cpp_cfg_identity
  authorizedClientsFile: $cpp_cfg_authorized
EOF
  start_cpp_server "cpp-config-e2e" --webtty-config="$cpp_cfg"
  wait_tcp "$cpp_cfg_addr"
  cpp_cfg_known_server=$(known_server_from_identity "$cpp_cfg_identity")
  run_case "cpp/config/ws/e2e" go_exec_text "cpp-config-e2e" --url "ws://$cpp_cfg_addr" --known-server-key "$cpp_cfg_known_server" --identity-file "$runtime_client_identity" -- /bin/sh -c "printf cpp-config-e2e"
else
  printf "SKIP %-48s C++ WebTTY binaries not found\n" "c++/interop"
fi

echo "=== summary ==="
printf "PASS %d\nFAIL %d\n" "$PASS" "$FAIL"
if [ "$FAIL" -ne 0 ]; then
  exit 1
fi
