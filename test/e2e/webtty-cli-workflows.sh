#!/usr/bin/env bash
# See LICENSE file in the project root for license information.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
TIMEOUT_SECONDS="${RSTREAM_WEBTTY_RUNTIME_TIMEOUT_SECONDS:-30}"
TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/rstream-webtty-cli.XXXXXX")
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
  chmod -R u+w "$TMP_DIR" 2>/dev/null || true
  if [ "$KEEP_RUNTIME" = "1" ]; then
    printf "kept runtime directory: %s\n" "$TMP_DIR" >&2
  else
    rm -rf "$TMP_DIR"
  fi
}
trap cleanup EXIT

log_pass() {
  printf "PASS %-52s\n" "$1"
  PASS=$((PASS + 1))
}

log_fail() {
  printf "FAIL %-52s %s\n" "$1" "$2" >&2
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

json_field() {
  python3 - "$1" "$2" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as stream:
    payload = json.load(stream)
print(payload[sys.argv[2]])
PY
}

assert_mode() {
  python3 - "$1" "$2" <<'PY'
import os
import stat
import sys
mode = stat.S_IMODE(os.stat(sys.argv[1]).st_mode)
expected = int(sys.argv[2], 8)
if mode != expected:
    raise SystemExit(f"{sys.argv[1]} mode {mode:o}, expected {expected:o}")
PY
}

assert_same_path() {
  python3 - "$1" "$2" <<'PY'
import os
import sys
left = os.path.realpath(sys.argv[1])
right = os.path.realpath(sys.argv[2])
if left != right:
    raise SystemExit(f"{left} != {right}")
PY
}

assert_json_trusted_count() {
  python3 - "$1" "$2" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as stream:
    payload = json.load(stream)
count = len(payload.get("known_servers", []))
expected = int(sys.argv[2])
if count != expected:
    raise SystemExit(f"known server count {count}, expected {expected}")
PY
}

assert_json_authorized_count() {
  python3 - "$1" "$2" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as stream:
    payload = json.load(stream)
count = len(payload.get("authorized_clients", []))
expected = int(sys.argv[2])
if count != expected:
    raise SystemExit(f"authorized client count {count}, expected {expected}")
PY
}

assert_json_known_server_client_identity() {
  python3 - "$1" "$2" "$3" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as stream:
    payload = json.load(stream)
for entry in payload.get("known_servers", []):
    if entry.get("name") == sys.argv[2]:
        value = entry.get("client_identity", "")
        if value != sys.argv[3]:
            raise SystemExit(f"client_identity={value!r}, expected {sys.argv[3]!r}")
        raise SystemExit(0)
raise SystemExit(f"known server {sys.argv[2]!r} not found")
PY
}

assert_json_authorized_name_prefix() {
  python3 - "$1" "$2" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as stream:
    payload = json.load(stream)
prefix = sys.argv[2]
for entry in payload.get("authorized_clients", []):
    if str(entry.get("name", "")).startswith(prefix):
        raise SystemExit(0)
raise SystemExit(f"no authorized client starts with {prefix!r}")
PY
}

assert_json_field_equals() {
  python3 - "$1" "$2" "$3" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as stream:
    payload = json.load(stream)
value = str(payload.get(sys.argv[2], ""))
if value != sys.argv[3]:
    raise SystemExit(f"{sys.argv[2]}={value!r}, expected {sys.argv[3]!r}")
PY
}

assert_contains() {
  local pattern=$1
  local file=$2
  grep -q -e "$pattern" "$file"
}

assert_not_contains() {
  local pattern=$1
  local file=$2
  ! grep -q -e "$pattern" "$file"
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

start_server() {
  local label=$1
  shift
  local log="$TMP_DIR/server-${label//[^A-Za-z0-9_.-]/_}.log"
  "$RSTREAM" webtty server "$@" >"$log" 2>&1 &
  PIDS+=("$!")
}

start_workspace_device_api() {
  local port=$1
  local log="$TMP_DIR/workspace-device-api.log"
  python3 - "$port" >"$log" 2>&1 <<'PY' &
import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/api/workspaces":
            self.send_response(200)
            self.send_header("content-type", "application/json")
            self.end_headers()
            payload = {
                "workspaces": [
                    {
                        "id": "workspace-runtime",
                        "type": "organization",
                        "name": "Runtime ACME",
                        "membership": {"role": "owner", "status": "active"},
                        "enterprise": {
                            "status": "active",
                            "projectCreationMode": "enterprise_only",
                            "workspaceKeyMode": "enabled",
                        },
                    }
                ]
            }
            self.wfile.write(json.dumps(payload).encode())
            return
        if self.path == "/api/projects/tunnels/resolve/runtime-project":
            self.send_response(200)
            self.send_header("content-type", "application/json")
            self.end_headers()
            payload = {
                "id": "project-runtime",
                "workspaceId": "workspace-runtime",
                "endpoint": "runtime-project",
                "routing": "regional",
            }
            self.wfile.write(json.dumps(payload).encode())
            return
        self.send_error(404)
    def do_POST(self):
        if self.path == "/api/workspaces/workspace-runtime/enterprise/devices/lookup":
            length = int(self.headers.get("content-length", "0"))
            json.loads(self.rfile.read(length))
            self.send_response(200)
            self.send_header("content-type", "application/json")
            self.end_headers()
            payload = {
                "devices": [
                    {
                        "id": "device-service-runtime",
                        "kind": "service",
                        "status": "active",
                        "publicEncryptionKey": "unused",
                        "fingerprint": "sha256:runtime",
                        "createdAt": "2026-06-08T10:00:00.000Z",
                    }
                ],
                "deviceEnvelopes": [
                    {
                        "id": "envelope-runtime",
                        "keysetId": "keyset-runtime",
                        "recipientKind": "device",
                        "recipientId": "device-service-runtime",
                        "ciphertext": "ciphertext",
                        "crypto": {"suite": "p256-hkdf-sha256-aes-256-gcm"},
                        "createdAt": "2026-06-08T10:00:00.000Z",
                    }
                ],
            }
            self.wfile.write(json.dumps(payload).encode())
            return
        if self.path != "/api/workspaces/workspace-runtime/enterprise/devices":
            self.send_error(404)
            return
        length = int(self.headers.get("content-length", "0"))
        body = json.loads(self.rfile.read(length))
        if body.get("kind") != "service" or body.get("label") != "Audit exporter":
            self.send_error(400)
            return
        self.send_response(201)
        self.send_header("content-type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps({"deviceKeyId": "device-service-runtime", "status": "pending"}).encode())
    def log_message(self, *_args):
        pass

ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), Handler).serve_forever()
PY
  PIDS+=("$!")
}

exec_text() {
  local expected=$1
  shift
  local out
  out=$("$RSTREAM" webtty exec "$@" 2>&1)
  printf "%s" "$out" | grep -q "$expected"
}

exec_pipe_text() {
  local expected=$1
  local input=$2
  shift 2
  local out
  out=$(printf "%s" "$input" | "$RSTREAM" webtty exec --output text "$@" 2>&1)
  printf "%s" "$out" | grep -q "$expected"
}

exec_pipe_json() {
  local expected=$1
  local input=$2
  shift 2
  local out
  if ! out=$(printf "%s" "$input" | "$RSTREAM" webtty exec --output json "$@" 2>&1); then
    printf "%s" "$out"
    return 1
  fi
  python3 -c 'import json, sys; assert json.loads(sys.stdin.read())["stdout"] == sys.argv[1]' "$expected" <<<"$out"
}

exec_empty_pipe_json() {
  local out
  if ! out=$(printf "%s" "" | "$RSTREAM" webtty exec --output json "$@" 2>&1); then
    printf "%s" "$out"
    return 1
  fi
  python3 -c 'import json, sys; payload = json.loads(sys.stdin.read()); assert payload["exit_code"] == 0 and payload["stdout"] == "" and payload["stderr"] == ""' <<<"$out"
}

exec_large_pipe_json() {
  local expected_size=$1
  shift
  local out
  if ! out=$(python3 -c 'import sys; sys.stdout.buffer.write(b"x" * int(sys.argv[1]))' "$expected_size" | "$RSTREAM" webtty exec --output json "$@" 2>&1); then
    printf "%s" "$out"
    return 1
  fi
  python3 -c 'import json, sys; payload = json.loads(sys.stdin.read()); assert payload["exit_code"] == 0 and int(payload["stdout"].strip()) == int(sys.argv[1]) and payload["stderr"] == ""' "$expected_size" <<<"$out"
}

exec_binary_pipe_json() {
  local expected_size=$1
  shift
  local expected_digest
  local out
  expected_digest=$(python3 -c 'import hashlib, sys; payload = bytes(range(256)) * (int(sys.argv[1]) // 256); print(hashlib.sha256(payload).hexdigest())' "$expected_size")
  if ! out=$(python3 -c 'import sys; sys.stdout.buffer.write(bytes(range(256)) * (int(sys.argv[1]) // 256))' "$expected_size" | "$RSTREAM" webtty exec --output json "$@" 2>&1); then
    printf "%s" "$out"
    return 1
  fi
  python3 -c 'import json, sys; payload = json.loads(sys.stdin.read()); assert payload["exit_code"] == 0 and payload["stdout"].strip() == sys.argv[1] and payload["stderr"] == ""' "$expected_digest" <<<"$out"
}

exec_remote_exit_with_open_stdin() {
  local fifo="$TMP_DIR/open-stdin.fifo"
  local stdout_file="$TMP_DIR/open-stdin-stdout.json"
  local stderr_file="$TMP_DIR/open-stdin-stderr.txt"
  local pid
  local hold_fd
  local deadline
  mkfifo "$fifo"
  exec {hold_fd}<>"$fifo"
  "$RSTREAM" webtty exec --output json "$@" <"$fifo" >"$stdout_file" 2>"$stderr_file" &
  pid=$!
  deadline=$((SECONDS + 5))
  while kill -0 "$pid" 2>/dev/null && [ "$SECONDS" -lt "$deadline" ]; do
    sleep 0.05
  done
  if kill -0 "$pid" 2>/dev/null; then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
    exec {hold_fd}>&-
    cat "$stderr_file"
    return 1
  fi
  if ! wait "$pid"; then
    exec {hold_fd}>&-
    cat "$stderr_file"
    return 1
  fi
  exec {hold_fd}>&-
  python3 -c 'import json, sys; payload = json.load(open(sys.argv[1], encoding="utf-8")); assert payload["exit_code"] == 0 and payload["stdout"] == "remote-exit" and payload["stderr"] == ""' "$stdout_file"
}

exec_runtime_config_json() {
  local expected_workdir=$1
  shift
  local out
  if ! out=$("$RSTREAM" webtty exec --output json "$@" 2>&1); then
    printf "%s" "$out"
    return 1
  fi
  python3 -c 'import json, os, sys; payload = json.loads(sys.stdin.read()); env, workdir = payload["stdout"].splitlines(); assert payload["exit_code"] == 0 and env == "runtime-flags" and os.path.realpath(workdir) == os.path.realpath(sys.argv[1]) and payload["stderr"] == ""' "$expected_workdir" <<<"$out"
}

exec_pipe_json_with_exit() {
  local expected_stdout=$1
  local expected_stderr=$2
  local expected_exit=$3
  local input=$4
  shift 4
  local stdout_file="$TMP_DIR/nonzero-stdout.json"
  local stderr_file="$TMP_DIR/nonzero-stderr.txt"
  local status
  set +e
  printf "%s" "$input" | "$RSTREAM" webtty exec --output json "$@" >"$stdout_file" 2>"$stderr_file"
  status=$?
  set -e
  if [ "$status" -ne "$expected_exit" ]; then
    cat "$stderr_file"
    return 1
  fi
  python3 - "$stdout_file" "$expected_stdout" "$expected_stderr" "$expected_exit" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as stream:
    payload = json.load(stream)
assert payload["stdout"] == sys.argv[2]
assert payload["stderr"] == sys.argv[3]
assert payload["exit_code"] == int(sys.argv[4])
PY
}

RSTREAM=$(build_rstream)
HOME="$TMP_DIR/home"
export HOME
unset RSTREAM_API_URL RSTREAM_AUTHENTICATION_TOKEN RSTREAM_CONFIG RSTREAM_CONTEXT
mkdir -p "$HOME"

echo "=== local identity and trust store ==="
identity_json="$TMP_DIR/identity.json"
"$RSTREAM" webtty identity create --name prod-shell --output json >"$identity_json"
identity_path=$(json_field "$identity_json" identity)
endpoint_identity=$(json_field "$identity_json" endpoint_identity)
encryption_key_id=$(json_field "$identity_json" encryption_key_id)
encryption_public_key=$(json_field "$identity_json" encryption_public_key)
signing_key_id=$(json_field "$identity_json" signing_key_id)
fingerprint=$(json_field "$identity_json" signing_fingerprint)
expected_identity="$HOME/.rstream/webtty/identities/prod-shell.identity.json"
run_case "identity create writes default 0600 file" assert_same_path "$identity_path" "$expected_identity"
run_case "identity file is private" assert_mode "$identity_path" 600
run_case "identity output includes fingerprint" test -n "$fingerprint"

show_json="$TMP_DIR/identity-show.json"
"$RSTREAM" webtty identity show --name prod-shell --output json >"$show_json"
run_case "identity show returns same encryption key id" test "$(json_field "$show_json" encryption_key_id)" = "$encryption_key_id"
run_case "identity show returns same encryption public key" test "$(json_field "$show_json" encryption_public_key)" = "$encryption_public_key"
run_case "identity show returns same signing key id" test "$(json_field "$show_json" signing_key_id)" = "$signing_key_id"
endpoint_only="$TMP_DIR/identity-endpoint.txt"
"$RSTREAM" webtty identity show --name prod-shell --endpoint-identity >"$endpoint_only"
run_case "identity show prints endpoint identity only" test "$(tr -d '\n' <"$endpoint_only")" = "$endpoint_identity"
created_endpoint_only="$TMP_DIR/identity-create-endpoint.txt"
"$RSTREAM" webtty identity create --name copyable-shell --endpoint-identity >"$created_endpoint_only"
run_case "identity create can print endpoint identity only" test "$(wc -c <"$created_endpoint_only" | tr -d ' ')" -gt 80
run_case "identity create endpoint output has no labels" assert_not_contains "Endpoint identity:" "$created_endpoint_only"

client_identity_json="$TMP_DIR/operator-identity.json"
"$RSTREAM" webtty identity create --name operator --output json >"$client_identity_json"
client_signing_key_id=$(json_field "$client_identity_json" signing_key_id)
client_signing_public_key=$(json_field "$client_identity_json" signing_public_key)
client_endpoint_identity=$(json_field "$client_identity_json" endpoint_identity)
authorized_client_key="$client_signing_key_id:$client_signing_public_key"
run_case "client identity output includes signing key" test -n "$authorized_client_key"

trusted_json="$TMP_DIR/trusted-add.json"
"$RSTREAM" webtty known-server add prod-shell --key "$endpoint_identity" --client-identity operator --output json >"$trusted_json"
trusted_path=$(json_field "$trusted_json" known_servers)
expected_trusted="$HOME/.rstream/webtty/known_servers.json"
run_case "known-server add writes default 0600 file" assert_same_path "$trusted_path" "$expected_trusted"
run_case "known-server file is private" assert_mode "$trusted_path" 600
"$RSTREAM" webtty known-server list --output json >"$TMP_DIR/trusted-list.json"
run_case "known-server list contains one entry" assert_json_trusted_count "$TMP_DIR/trusted-list.json" 1
run_case "known-server add can associate client identity" assert_json_known_server_client_identity "$TMP_DIR/trusted-list.json" prod-shell operator
"$RSTREAM" webtty known-server remove prod-shell --output json >/dev/null
"$RSTREAM" webtty known-server list --output json >"$TMP_DIR/trusted-empty.json"
run_case "known-server remove empties file" assert_json_trusted_count "$TMP_DIR/trusted-empty.json" 0

authorized_json="$TMP_DIR/authorized-add.json"
"$RSTREAM" webtty authorized-client add operator --identity prod-shell --key "$client_endpoint_identity" --output json >"$authorized_json"
authorized_path=$(json_field "$authorized_json" authorized_clients)
expected_authorized="$HOME/.rstream/webtty/authorized_clients/prod-shell.json"
run_case "authorized-client add writes default 0600 file" assert_same_path "$authorized_path" "$expected_authorized"
run_case "authorized-client file is private" assert_mode "$authorized_path" 600
"$RSTREAM" webtty authorized-client list --identity prod-shell --output json >"$TMP_DIR/authorized-list.json"
run_case "authorized-client list contains one entry" assert_json_authorized_count "$TMP_DIR/authorized-list.json" 1
"$RSTREAM" webtty authorized-client remove operator --identity prod-shell --output json >/dev/null
"$RSTREAM" webtty authorized-client list --identity prod-shell --output json >"$TMP_DIR/authorized-empty.json"
run_case "authorized-client remove empties file" assert_json_authorized_count "$TMP_DIR/authorized-empty.json" 0
derived_authorized_json="$TMP_DIR/authorized-derived.json"
"$RSTREAM" webtty authorized-client add --identity prod-shell --key "$client_endpoint_identity" --output json >"$derived_authorized_json"
"$RSTREAM" webtty authorized-client list --identity prod-shell --output json >"$TMP_DIR/authorized-derived-list.json"
run_case "authorized-client add can derive a readable name" assert_json_authorized_name_prefix "$TMP_DIR/authorized-derived-list.json" client-
"$RSTREAM" webtty authorized-client remove "$(json_field "$derived_authorized_json" name)" --identity prod-shell --output json >/dev/null
server_id_authorized_json="$TMP_DIR/authorized-server-id-derived.json"
"$RSTREAM" webtty authorized-client add --server-id registered-shell --key "$client_endpoint_identity" --output json >"$server_id_authorized_json"
server_id_authorized_path=$(json_field "$server_id_authorized_json" authorized_clients)
expected_server_id_authorized="$HOME/.rstream/webtty/authorized_clients/registered-shell.json"
run_case "authorized-client add --server-id writes default file" assert_same_path "$server_id_authorized_path" "$expected_server_id_authorized"
"$RSTREAM" webtty authorized-client list --server-id registered-shell --output json >"$TMP_DIR/authorized-server-id-list.json"
run_case "authorized-client add --server-id accepts endpoint identity" assert_json_authorized_name_prefix "$TMP_DIR/authorized-server-id-list.json" client-

echo "=== inferred E2E through default files ==="
"$RSTREAM" webtty known-server add prod-shell --key "$endpoint_identity" --output json >/dev/null
"$RSTREAM" webtty known-server set-identity prod-shell --identity operator --output json >/dev/null
"$RSTREAM" webtty known-server list --output json >"$TMP_DIR/trusted-associated-list.json"
run_case "known-server set-identity updates existing entry" assert_json_known_server_client_identity "$TMP_DIR/trusted-associated-list.json" prod-shell operator
"$RSTREAM" webtty authorized-client add operator --identity prod-shell --key "$client_endpoint_identity" --output json >/dev/null
port=$(reserve_port)
addr="127.0.0.1:$port"
start_server "named-identity-e2e" --listen "$addr" --allow-unauthenticated --identity prod-shell
wait_tcp "$addr"
run_case "server identity implies E2E" exec_text "named-e2e" --url "ws://$addr" --identity operator --e2e -- /bin/sh -c "printf named-e2e"
run_case "direct known key infers E2E" exec_text "named-e2e-auto" --url "ws://$addr" --identity operator --known-server-key "$endpoint_identity" -- /bin/sh -c "printf named-e2e-auto"
run_case "non-interactive text exec forwards piped stdin and EOF" exec_pipe_text "piped-text" "piped-text" --url "ws://$addr" --identity operator --e2e -- cat
run_case "non-interactive JSON exec forwards piped stdin and EOF" exec_pipe_json "piped-json" "piped-json" --url "ws://$addr" --identity operator --e2e -- cat
run_case "empty pipe sends EOF without waiting" exec_empty_pipe_json --url "ws://$addr" --identity operator --e2e -- cat
run_case "large pipe is not truncated" exec_large_pipe_json 2097152 --url "ws://$addr" --identity operator --e2e -- wc -c
run_case "binary pipe preserves every byte" exec_binary_pipe_json 1048576 --url "ws://$addr" --identity operator --e2e -- python3 -c 'import hashlib, sys; print(hashlib.sha256(sys.stdin.buffer.read()).hexdigest())'
run_case "remote exit does not wait for open stdin" exec_remote_exit_with_open_stdin --url "ws://$addr" --identity operator --e2e -- /bin/sh -c 'printf remote-exit'
runtime_workdir="$TMP_DIR/runtime-workdir"
mkdir -p "$runtime_workdir"
# shellcheck disable=SC2016
run_case "exec applies env workdir and no-TTY mode" exec_runtime_config_json "$runtime_workdir" --url "ws://$addr" --identity operator --e2e --no-tty --env RSTREAM_RUNTIME_FLAG=runtime-flags --workdir "$runtime_workdir" -- /bin/sh -c 'test ! -t 0; printf "%s\n%s" "$RSTREAM_RUNTIME_FLAG" "$PWD"'
run_case "exec allocates a PTY on request" exec_text "runtime-tty" --url "ws://$addr" --identity operator --e2e --tty -- /bin/sh -c 'test -t 0 && test -t 1 && printf runtime-tty'
run_case "JSON preserves streams and remote exit status" exec_pipe_json_with_exit "input" "failure" 17 "input" --url "ws://$addr" --identity operator --e2e -- /bin/sh -c 'cat; printf failure >&2; exit 17'
untrusted_home="$TMP_DIR/untrusted-home"
mkdir -p "$untrusted_home"
run_case_expect_fail "E2E client fails closed without trust store" "known server endpoint identity" env HOME="$untrusted_home" "$RSTREAM" webtty exec --url "ws://$addr" -- /bin/sh -c "printf no"

echo "=== bearer authentication ==="
auth_token_file="$TMP_DIR/webtty-token"
printf "runtime-secret\n" >"$auth_token_file"
auth_port=$(reserve_port)
auth_addr="127.0.0.1:$auth_port"
start_server "bearer-auth" --listen "$auth_addr" --auth-token-file "$auth_token_file"
wait_tcp "$auth_addr"
run_case_expect_fail "server rejects a missing bearer token" "status 401" "$RSTREAM" webtty exec --url "ws://$auth_addr" -- /bin/sh -c "printf no"
run_case "server accepts the configured bearer token" exec_text "bearer-authenticated" --url "ws://$auth_addr" --auth-token-file "$auth_token_file" -- /bin/sh -c "printf bearer-authenticated"

echo "=== env runtime config ==="
env_identity_json="$TMP_DIR/env-identity.json"
"$RSTREAM" webtty identity create --name env-shell --output json >"$env_identity_json"
env_endpoint_identity=$(json_field "$env_identity_json" endpoint_identity)
"$RSTREAM" webtty known-server add env-shell --key "$env_endpoint_identity" --output json >/dev/null
env_port=$(reserve_port)
env_addr="127.0.0.1:$env_port"
env_config="$TMP_DIR/env-webtty.yaml"
cat >"$env_config" <<EOF
version: 1
server:
  listen: $env_addr
  allowUnauthenticated: true
e2e:
  identity: env-shell
  authorizedClientKeys:
    - $authorized_client_key
EOF
env_config_log="$TMP_DIR/server-env-config-e2e.log"
RSTREAM_WEBTTY_CONFIG="$env_config" "$RSTREAM" webtty server >"$env_config_log" 2>&1 &
PIDS+=("$!")
wait_tcp "$env_addr"
run_case "RSTREAM_WEBTTY_CONFIG applies named identity" exec_text "env-config-e2e" --url "ws://$env_addr" --identity operator --known-server-key "$env_endpoint_identity" --e2e -- /bin/sh -c "printf env-config-e2e"

echo "=== workspace device enrollment ==="
device_api_port=$(reserve_port)
start_workspace_device_api "$device_api_port"
wait_tcp "127.0.0.1:$device_api_port"
workspace_runtime_config="$TMP_DIR/workspace-runtime-config.yaml"
cat >"$workspace_runtime_config" <<EOF
version: 1
defaults:
  context:
    name: runtime
contexts:
  - name: runtime
    apiUrl: http://127.0.0.1:$device_api_port
    projectEndpoint: runtime-project
    auth:
      token:
        storage:
          kind: inline
          value: token
EOF
workspace_list_output="$TMP_DIR/workspaces.txt"
RSTREAM_CONFIG="$workspace_runtime_config" "$RSTREAM" workspace list >"$workspace_list_output"
run_case "workspace list exposes workspace IDs" assert_contains "workspace-runtime" "$workspace_list_output"
run_case "workspace list exposes protection state" assert_contains "enabled" "$workspace_list_output"
service_device_json="$TMP_DIR/service-device.json"
RSTREAM_API_URL="http://127.0.0.1:$device_api_port" RSTREAM_AUTHENTICATION_TOKEN="token" "$RSTREAM" workspace device enroll --workspace workspace-runtime --kind service --label "Audit exporter" --output json >"$service_device_json"
service_device_path=$(json_field "$service_device_json" device_file)
service_webtty_identity=$(json_field "$service_device_json" webtty_identity)
service_device_id=$(json_field "$service_device_json" device_id)
expected_service_webtty_identity="$HOME/.rstream/workspaces/workspace-runtime/webtty/identities/$service_device_id.identity.json"
run_case "workspace service device enrolls through control plane" assert_json_field_equals "$service_device_json" kind service
run_case "workspace service device file is private" assert_mode "$service_device_path" 600
run_case "workspace service device file stores kind" assert_json_field_equals "$service_device_path" kind service
run_case "workspace service WebTTY identity stays under workspace" assert_same_path "$service_webtty_identity" "$expected_service_webtty_identity"
run_case "workspace service WebTTY identity is private" assert_mode "$service_webtty_identity" 600
inferred_device_json="$TMP_DIR/service-device-inferred.json"
RSTREAM_CONFIG="$workspace_runtime_config" "$RSTREAM" workspace device enroll --kind service --label "Audit exporter" --output json >"$inferred_device_json"
inferred_device_path=$(json_field "$inferred_device_json" device_file)
inferred_webtty_identity=$(json_field "$inferred_device_json" webtty_identity)
inferred_device_id=$(json_field "$inferred_device_json" device_id)
expected_inferred_webtty_identity="$HOME/.rstream/workspaces/workspace-runtime/webtty/identities/$inferred_device_id.identity.json"
run_case "workspace device enroll infers active project workspace" assert_json_field_equals "$inferred_device_json" workspace_source active_project
inferred_status_output="$TMP_DIR/service-device-status.txt"
RSTREAM_CONFIG="$workspace_runtime_config" "$RSTREAM" workspace device status >"$inferred_status_output"
run_case "workspace device status infers active project workspace" assert_contains "from active project runtime-project" "$inferred_status_output"
run_case "workspace device status refreshes trust state" assert_contains "device-service-runtime service active" "$inferred_status_output"
run_case "inferred workspace device file is private" assert_mode "$inferred_device_path" 600
run_case "inferred workspace WebTTY identity stays under workspace" assert_same_path "$inferred_webtty_identity" "$expected_inferred_webtty_identity"
run_case "inferred workspace WebTTY identity is private" assert_mode "$inferred_webtty_identity" 600

echo "=== security validation ==="
conflict_config="$TMP_DIR/conflict-webtty.yaml"
mkdir -p "$TMP_DIR/fs-root"
cat >"$conflict_config" <<EOF
version: 1
server:
  listen: 127.0.0.1:1
  allowUnauthenticated: true
filesystem:
  root: $TMP_DIR/fs-root
e2e:
  enabled: true
EOF
run_case_expect_fail "E2E refuses WebDAV filesystem sidecar from config" "filesystem sidecar" "$RSTREAM" webtty server --webtty-config "$conflict_config"

echo "=== public CLI surface ==="
help_output="$TMP_DIR/help.txt"
{
  "$RSTREAM" webtty --help
  "$RSTREAM" webtty server --help
  "$RSTREAM" webtty client --help
  "$RSTREAM" webtty exec --help
  "$RSTREAM" webtty server create --help
  "$RSTREAM" webtty server list --help
  "$RSTREAM" webtty server show --help
  "$RSTREAM" webtty server delete --help
  "$RSTREAM" webtty list --help
  "$RSTREAM" webtty sessions list --help
} >"$help_output"
run_case "webtty help has no deprecated protocol flag" assert_not_contains "--protocol" "$help_output"
run_case "webtty help has no e2e-policy flag" assert_not_contains "e2e-policy" "$help_output"
run_case "webtty help has no server-binding flag" assert_not_contains "server-binding" "$help_output"
run_case "webtty help has no heartbeat toggle" assert_not_contains "no-heartbeat" "$help_output"
run_case "webtty list help has no aliases" assert_not_contains "Aliases:" "$help_output"
run_case "webtty help separates connection commands" assert_contains "Connection Commands:" "$help_output"
run_case "webtty help separates server commands" assert_contains "Server Commands:" "$help_output"
run_case "webtty help separates managed session commands" assert_contains "Managed Session Commands:" "$help_output"
run_case "webtty server help shows tunnel workflow" assert_contains "rstream webtty server -v --rstream --name shell" "$help_output"
run_case "webtty server help shows registered workflow" assert_contains "rstream webtty server -v --server-id server_id" "$help_output"
run_case "webtty server help shows config workflow" assert_contains "rstream webtty server -v --webtty-config /etc/rstream/webtty/prod-shell.yaml" "$help_output"
run_case "webtty server help exposes registered create" assert_contains "Create a registered WebTTY server" "$help_output"
run_case "webtty server help exposes registered list" assert_contains "List registered WebTTY servers" "$help_output"
run_case "webtty server help exposes registered show" assert_contains "Show a registered WebTTY server" "$help_output"
run_case "webtty server help exposes registered delete" assert_contains "Delete a registered WebTTY server" "$help_output"
sessions_help_output="$TMP_DIR/sessions-help.txt"
"$RSTREAM" webtty sessions --help >"$sessions_help_output"
run_case "webtty sessions help separates primary commands" assert_contains "Session Commands:" "$sessions_help_output"
run_case "webtty sessions help separates advanced commands" assert_contains "Advanced Commands:" "$sessions_help_output"

echo "=== summary ==="
printf "PASS %d\nFAIL %d\n" "$PASS" "$FAIL"
if [ "$FAIL" -ne 0 ]; then
  exit 1
fi
