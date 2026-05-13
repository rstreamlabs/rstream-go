#!/usr/bin/env bash
# See LICENSE file in the project root for license information.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
. "$ROOT/test/e2e/runtime_common.sh"
PYTHON="${PYTHON:-python3}"
RSTREAM_BIN=$(resolve_rstream_cli "$ROOT")
API_URL="${RSTREAM_RUNTIME_API_URL:-http://localhost:3000}"
CONTROL_TOKEN="${RSTREAM_RUNTIME_CONTROL_TOKEN:-${RSTREAM_AUTHENTICATION_TOKEN:-}}"
TIMEOUT_SECONDS="${RSTREAM_RUNTIME_TIMEOUT:-60}"
NAME_PREFIX="${RSTREAM_RUNTIME_NAME_PREFIX:-runtime-mtls-$$}"
TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/rstream-go-mtls-runtime.XXXXXX")
PASS=0
FAIL=0
PIDS=()
CREDENTIAL_IDS=()
FORWARD_PID=
FORWARDING=
FORWARD_LOG=
UPSTREAM_ADDR=

cleanup() {
  local pid
  for pid in "${PIDS[@]:-}"; do
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  done
  if [ "${#CREDENTIAL_IDS[@]}" -gt 0 ]; then
    RSTREAM_RUNTIME_API_URL="$API_URL" RSTREAM_RUNTIME_CONTROL_TOKEN="$CONTROL_TOKEN" \
      "$PYTHON" "$TMP_DIR/api.py" delete-credentials "${CREDENTIAL_IDS[@]}" >/dev/null 2>&1 || true
  fi
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

fail() {
  printf "FAIL %-48s %s\n" "$1" "$2" >&2
  FAIL=$((FAIL + 1))
}

pass() {
  printf "PASS %-48s\n" "$1"
  PASS=$((PASS + 1))
}

require_executable "$RSTREAM_BIN"
if [ -z "$CONTROL_TOKEN" ]; then
  printf "ERROR set RSTREAM_RUNTIME_CONTROL_TOKEN to a PAT with credential, token, and project read permissions\n" >&2
  exit 2
fi

cat >"$TMP_DIR/api.py" <<'PY'
import json
import os
import sys
import urllib.error
import urllib.request

api_url = os.environ["RSTREAM_RUNTIME_API_URL"].rstrip("/")
token = os.environ["RSTREAM_RUNTIME_CONTROL_TOKEN"]

def request(method, path, body=None, expect=(200,)):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(
        api_url + path,
        data=data,
        method=method,
        headers={
            "authorization": "Bearer " + token,
            "content-type": "application/json",
        },
    )
    try:
        with urllib.request.urlopen(req, timeout=20) as resp:
            payload = resp.read().decode()
            if resp.status not in expect:
                raise RuntimeError(f"{method} {path} returned {resp.status}: {payload}")
            return json.loads(payload) if payload else None
    except urllib.error.HTTPError as exc:
        payload = exc.read().decode()
        if exc.code in expect:
            return json.loads(payload) if payload else None
        raise RuntimeError(f"{method} {path} returned {exc.code}: {payload}") from exc

def project_selection():
    projects = request("GET", "/api/projects/tunnels?pageSize=100")["projects"]
    pro_endpoint = os.environ.get("RSTREAM_RUNTIME_PRO_PROJECT_ENDPOINT", "").strip()
    basic_endpoint = os.environ.get("RSTREAM_RUNTIME_BASIC_PROJECT_ENDPOINT", "").strip()
    pro = next((p for p in projects if p["endpoint"] == pro_endpoint), None) if pro_endpoint else next((p for p in projects if p["plan"] == "pro"), None)
    basic = next((p for p in projects if p["endpoint"] == basic_endpoint), None) if basic_endpoint else next((p for p in projects if p["plan"] == "basic"), None)
    if not pro:
        raise RuntimeError("no Pro project found; set RSTREAM_RUNTIME_PRO_PROJECT_ENDPOINT")
    if not basic:
        raise RuntimeError("no Basic project found; set RSTREAM_RUNTIME_BASIC_PROJECT_ENDPOINT")
    return pro, basic

def create_mtls(name, cert_path, project_id, permissions):
    with open(cert_path, encoding="utf-8") as stream:
        cert = stream.read()
    return request("POST", "/api/credentials", {
        "type": "mtls",
        "name": name,
        "certificatePem": cert,
        "permissionPolicy": {
            "control": {"mode": "none", "permissions": []},
            "engine": {
                "mode": "select" if permissions else "none",
                "permissions": permissions,
            },
        },
        "tunnelsGrants": {"projects": [project_id]},
    })

def create_token(permissions, grants):
    return request("POST", "/api/tokens", {
        "permissions": permissions,
        "tunnelsGrants": grants,
    })["token"]

def setup():
    created_ids = []
    pro, basic = project_selection()
    try:
        name_prefix = os.environ["RSTREAM_RUNTIME_NAME_PREFIX"]
        cert = os.environ["RSTREAM_RUNTIME_CERT"]
        denied_cert = os.environ["RSTREAM_RUNTIME_DENIED_CERT"]
        allowed_name = os.environ["RSTREAM_RUNTIME_ALLOWED_NAME"]
        full_permissions = [
            "tunnels.tunnels.create-delete",
            "tunnels.streams.create-delete",
            "tunnels.resources.read-only",
        ]
        create_permissions = ["tunnels.tunnels.create-delete"]
        list_permissions = ["tunnels.resources.read-only"]
        full_credential = create_mtls(name_prefix + "-mtls-full", cert, pro["id"], full_permissions)
        created_ids.append(full_credential["id"])
        duplicate_rejected = False
        try:
            create_mtls(name_prefix + "-mtls-duplicate", cert, pro["id"], full_permissions)
        except RuntimeError:
            duplicate_rejected = True
        denied_credential = create_mtls(name_prefix + "-mtls-denied", denied_cert, pro["id"], [])
        created_ids.append(denied_credential["id"])
        create_token_value = create_token(create_permissions, {
            "AND": [
                {"projects": [basic["id"]]},
                {"scopes": {"tunnels": {"create": {"filters": {"AND": [
                    {"name": {"exact": allowed_name}},
                    {"protocol": "http"},
                    {"publish": True}
                ]}}}}},
            ],
        })
        list_token_value = create_token(list_permissions, {
            "AND": [
                {"projects": [basic["id"]]},
                {"scopes": {"tunnels": {"list": {"filters": {"name": {"exact": allowed_name}}}}}},
            ],
        })
        print(json.dumps({
            "pro": pro,
            "basic": basic,
            "fullCredentialId": full_credential["id"],
            "deniedCredentialId": denied_credential["id"],
            "duplicateRejected": duplicate_rejected,
            "createToken": create_token_value,
            "listToken": list_token_value,
        }))
    except Exception:
        delete_credentials(created_ids)
        raise

def delete_credentials(ids):
    for credential_id in ids:
        try:
            request("DELETE", "/api/credentials/" + credential_id, expect=(200, 204, 404))
        except RuntimeError:
            pass

command = sys.argv[1]
if command == "setup":
    setup()
elif command == "delete-credentials":
    delete_credentials(sys.argv[2:])
else:
    raise SystemExit(f"unknown command: {command}")
PY

json_get() {
  "$PYTHON" - "$1" "$2" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    value = json.load(stream)
for part in sys.argv[2].split("."):
    value = value[part]
print(value)
PY
}

make_cert() {
  openssl genpkey \
    -algorithm EC \
    -pkeyopt ec_paramgen_curve:P-384 \
    -out "$2" >/dev/null 2>&1
  chmod 600 "$2"
  openssl req -x509 -new -days 1 \
    -key "$2" \
    -sha384 \
    -subj "/CN=$1" \
    -addext "keyUsage=digitalSignature" \
    -addext "extendedKeyUsage=clientAuth" \
    -out "$3" >/dev/null 2>&1
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
      fail "$label" "upstream exited early"
      tail -20 "$log" >&2 || true
      return 1
    fi
    sleep 0.2
  done
  fail "$label" "upstream did not become ready"
  tail -20 "$log" >&2 || true
  return 1
}

start_upstream() {
  local label=$1
  local log="$TMP_DIR/upstream-$label.log"
  "$PYTHON" "$ROOT/test/e2e/runtime_harness.py" serve http >"$log" 2>&1 &
  local pid=$!
  PIDS+=("$pid")
  UPSTREAM_ADDR=$(wait_ready "$pid" "$log" "$label")
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

start_forward_with_env() {
  local label=$1 engine=$2 token=$3 cert=$4 key=$5
  shift 5
  FORWARD_LOG="$TMP_DIR/forward-$label.log"
  : >"$FORWARD_LOG"
  env -u RSTREAM_CONTEXT \
    RSTREAM_ENGINE="$engine" \
    RSTREAM_AUTHENTICATION_TOKEN="$token" \
    RSTREAM_MTLS_CERT_FILE="$cert" \
    RSTREAM_MTLS_KEY_FILE="$key" \
    "$RSTREAM_BIN" forward "$UPSTREAM_ADDR" --output json --no-retry "$@" >"$FORWARD_LOG" 2>&1 &
  FORWARD_PID=$!
  PIDS+=("$FORWARD_PID")
  local deadline=$((SECONDS + TIMEOUT_SECONDS))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if FORWARDING=$(extract_forwarding "$FORWARD_LOG"); then
      return 0
    fi
    if ! kill -0 "$FORWARD_PID" 2>/dev/null; then
      tail -40 "$FORWARD_LOG" >&2 || true
      return 1
    fi
    if grep -Eiq "tunnel creation failed|connection failed|failed to create tunnel|Unauthorized|Forbidden" "$FORWARD_LOG"; then
      tail -40 "$FORWARD_LOG" >&2 || true
      return 1
    fi
    sleep 0.2
  done
  tail -40 "$FORWARD_LOG" >&2 || true
  return 1
}

stop_forward() {
  stop_pid "$FORWARD_PID"
}

stop_pid() {
  local pid=$1
  kill "$pid" 2>/dev/null || true
  wait "$pid" 2>/dev/null || true
}

expect_forward_denied() {
  local label=$1 engine=$2 token=$3
  shift 3
  local log="$TMP_DIR/denied-$label.log"
  if env -u RSTREAM_CONTEXT -u RSTREAM_MTLS_CERT_FILE -u RSTREAM_MTLS_KEY_FILE \
    RSTREAM_ENGINE="$engine" \
    RSTREAM_AUTHENTICATION_TOKEN="$token" \
    "$RSTREAM_BIN" forward "$UPSTREAM_ADDR" --output json --no-retry "$@" >"$log" 2>&1; then
    cat "$log" >&2
    return 1
  fi
}

check_https_ping() {
  "$PYTHON" "$ROOT/test/e2e/runtime_harness.py" check https-ping "$@"
}

make_cert "$NAME_PREFIX-client" "$TMP_DIR/client.key" "$TMP_DIR/client.crt"
make_cert "$NAME_PREFIX-denied" "$TMP_DIR/denied.key" "$TMP_DIR/denied.crt"
export RSTREAM_RUNTIME_API_URL="$API_URL"
export RSTREAM_RUNTIME_CONTROL_TOKEN="$CONTROL_TOKEN"
export RSTREAM_RUNTIME_NAME_PREFIX="$NAME_PREFIX"
export RSTREAM_RUNTIME_CERT="$TMP_DIR/client.crt"
export RSTREAM_RUNTIME_DENIED_CERT="$TMP_DIR/denied.crt"
export RSTREAM_RUNTIME_ALLOWED_NAME="$NAME_PREFIX-basic-allowed"
"$PYTHON" "$TMP_DIR/api.py" setup >"$TMP_DIR/setup.json"
CREDENTIAL_IDS+=("$(json_get "$TMP_DIR/setup.json" fullCredentialId)")
CREDENTIAL_IDS+=("$(json_get "$TMP_DIR/setup.json" deniedCredentialId)")
PRO_ENGINE="$(json_get "$TMP_DIR/setup.json" pro.url)"
BASIC_ENGINE="$(json_get "$TMP_DIR/setup.json" basic.url)"
CREATE_TOKEN="$(json_get "$TMP_DIR/setup.json" createToken)"
LIST_TOKEN="$(json_get "$TMP_DIR/setup.json" listToken)"
ALLOWED_NAME="$NAME_PREFIX-basic-allowed"
DENIED_NAME="$NAME_PREFIX-basic-denied"

if [ "$(json_get "$TMP_DIR/setup.json" duplicateRejected)" = "True" ]; then
  pass "api rejects duplicate mTLS certificate fingerprint"
else
  fail "api rejects duplicate mTLS certificate fingerprint" "duplicate credential was accepted"
fi

if env -u RSTREAM_CONTEXT -u RSTREAM_AUTHENTICATION_TOKEN \
  RSTREAM_ENGINE="$PRO_ENGINE" \
  RSTREAM_MTLS_CERT_FILE="$TMP_DIR/client.crt" \
  RSTREAM_MTLS_KEY_FILE="$TMP_DIR/client.key" \
  "$RSTREAM_BIN" tunnel list -o json >/dev/null; then
  pass "go agent mTLS authenticates Engine API"
else
  fail "go agent mTLS authenticates Engine API" "tunnel list failed"
fi

if env -u RSTREAM_CONTEXT -u RSTREAM_AUTHENTICATION_TOKEN \
  RSTREAM_ENGINE="$BASIC_ENGINE" \
  RSTREAM_MTLS_CERT_FILE="$TMP_DIR/client.crt" \
  RSTREAM_MTLS_KEY_FILE="$TMP_DIR/client.key" \
  "$RSTREAM_BIN" tunnel list -o json >/dev/null 2>&1; then
  fail "mTLS project grant denies another project" "Basic project accepted Pro-only credential"
else
  pass "mTLS project grant denies another project"
fi

if env -u RSTREAM_CONTEXT \
  RSTREAM_ENGINE="$PRO_ENGINE" \
  RSTREAM_AUTHENTICATION_TOKEN="conflict" \
  RSTREAM_MTLS_CERT_FILE="$TMP_DIR/client.crt" \
  RSTREAM_MTLS_KEY_FILE="$TMP_DIR/client.key" \
  "$RSTREAM_BIN" tunnel list -o json >"$TMP_DIR/conflict.log" 2>&1; then
  fail "go rejects token and mTLS agent auth conflict" "conflicting auth succeeded"
elif grep -q "token and mTLS authentication cannot be used together" "$TMP_DIR/conflict.log"; then
  pass "go rejects token and mTLS agent auth conflict"
else
  fail "go rejects token and mTLS agent auth conflict" "unexpected error: $(tail -1 "$TMP_DIR/conflict.log")"
fi

if env -u RSTREAM_CONTEXT -u RSTREAM_AUTHENTICATION_TOKEN \
  RSTREAM_ENGINE="$PRO_ENGINE" \
  RSTREAM_MTLS_CERT_FILE="$TMP_DIR/denied.crt" \
  RSTREAM_MTLS_KEY_FILE="$TMP_DIR/denied.key" \
  "$RSTREAM_BIN" tunnel list -o json >/dev/null 2>&1; then
  fail "mTLS credential without Engine API permissions is denied" "permissionless credential listed tunnels"
else
  pass "mTLS credential without Engine API permissions is denied"
fi

start_upstream "mtls-published"
if start_forward_with_env "mtls-published" "$PRO_ENGINE" "" "$TMP_DIR/client.crt" "$TMP_DIR/client.key" --http --publish --mtls --name "$NAME_PREFIX-published"; then
  if check_https_ping --addr "$FORWARDING" >/dev/null 2>&1; then
    fail "published mTLS rejects missing client certificate" "request without certificate succeeded"
  else
    pass "published mTLS rejects missing client certificate"
  fi
  if check_https_ping --addr "$FORWARDING" --cert "$TMP_DIR/client.crt" --key "$TMP_DIR/client.key"; then
    pass "published mTLS accepts registered certificate"
  else
    fail "published mTLS accepts registered certificate" "request with registered certificate failed"
  fi
  if check_https_ping --addr "$FORWARDING" --cert "$TMP_DIR/denied.crt" --key "$TMP_DIR/denied.key" >/dev/null 2>&1; then
    fail "published mTLS rejects certificate without stream permission" "permissionless certificate succeeded"
  else
    pass "published mTLS rejects certificate without stream permission"
  fi
  status=$(curl -sk --cert "$TMP_DIR/client.crt" --key "$TMP_DIR/client.key" -H "Authorization: Bearer $CONTROL_TOKEN" -o "$TMP_DIR/conflict-body.txt" -w "%{http_code}" "$FORWARDING/ping" || true)
  if [ "$status" = "200" ]; then
    fail "published mTLS rejects request with token conflict" "request with certificate and bearer token succeeded"
  else
    pass "published mTLS rejects request with token conflict"
  fi
  status=$(curl -sk --cert "$TMP_DIR/client.crt" --key "$TMP_DIR/client.key" -b "rstream_auth=session" -o "$TMP_DIR/rstream-conflict-body.txt" -w "%{http_code}" "$FORWARDING/ping" || true)
  if [ "$status" = "200" ]; then
    fail "published mTLS rejects request with rstream Auth conflict" "request with certificate and rstream_auth cookie succeeded"
  else
    pass "published mTLS rejects request with rstream Auth conflict"
  fi
  stop_forward
else
  fail "published mTLS tunnel creation with agent mTLS" "forward did not become ready"
fi

start_upstream "mtls-rstream-auth"
if start_forward_with_env "mtls-rstream-auth" "$PRO_ENGINE" "" "$TMP_DIR/client.crt" "$TMP_DIR/client.key" --http --publish --mtls --rstream-auth --name "$NAME_PREFIX-rstream-auth"; then
  if check_https_ping --addr "$FORWARDING" --cert "$TMP_DIR/client.crt" --key "$TMP_DIR/client.key"; then
    pass "published mTLS and rstream Auth accepts certificate-only request"
  else
    fail "published mTLS and rstream Auth accepts certificate-only request" "certificate-only request failed"
  fi
  status=$(curl -sk --cert "$TMP_DIR/client.crt" --key "$TMP_DIR/client.key" -b "rstream_auth=session" -o "$TMP_DIR/combined-rstream-conflict-body.txt" -w "%{http_code}" "$FORWARDING/ping" || true)
  if [ "$status" = "200" ]; then
    fail "published mTLS and rstream Auth rejects combined proofs" "request with certificate and rstream_auth cookie succeeded"
  else
    pass "published mTLS and rstream Auth rejects combined proofs"
  fi
  stop_forward
else
  fail "published mTLS and rstream Auth tunnel creation" "forward did not become ready"
fi

start_upstream "mtls-token-rstream-auth"
if start_forward_with_env "mtls-token-rstream-auth" "$PRO_ENGINE" "" "$TMP_DIR/client.crt" "$TMP_DIR/client.key" --http --publish --mtls --token-auth --rstream-auth --name "$NAME_PREFIX-token-rstream-auth"; then
  status=$(curl -sk -o "$TMP_DIR/triple-no-proof-body.txt" -w "%{http_code}" "$FORWARDING/ping" || true)
  if [ "$status" = "302" ]; then
    pass "published mTLS token rstream Auth redirects request without proof"
  else
    fail "published mTLS token rstream Auth redirects request without proof" "status=$status"
  fi
  status=$(curl -sk -H "Authorization: Bearer $CONTROL_TOKEN" -o "$TMP_DIR/triple-token-body.txt" -w "%{http_code}" "$FORWARDING/ping" || true)
  if [ "$status" = "200" ]; then
    pass "published mTLS token rstream Auth accepts bearer-only request"
  else
    fail "published mTLS token rstream Auth accepts bearer-only request" "status=$status"
  fi
  if check_https_ping --addr "$FORWARDING" --cert "$TMP_DIR/client.crt" --key "$TMP_DIR/client.key"; then
    pass "published mTLS token rstream Auth accepts certificate-only request"
  else
    fail "published mTLS token rstream Auth accepts certificate-only request" "certificate-only request failed"
  fi
  status=$(curl -sk --cert "$TMP_DIR/client.crt" --key "$TMP_DIR/client.key" -H "Authorization: Bearer $CONTROL_TOKEN" -o "$TMP_DIR/triple-cert-token-body.txt" -w "%{http_code}" "$FORWARDING/ping" || true)
  if [ "$status" = "200" ]; then
    fail "published mTLS token rstream Auth rejects certificate bearer conflict" "request with certificate and bearer token succeeded"
  else
    pass "published mTLS token rstream Auth rejects certificate bearer conflict"
  fi
  status=$(curl -sk --cert "$TMP_DIR/client.crt" --key "$TMP_DIR/client.key" -b "rstream_auth=session" -o "$TMP_DIR/triple-cert-rstream-body.txt" -w "%{http_code}" "$FORWARDING/ping" || true)
  if [ "$status" = "200" ]; then
    fail "published mTLS token rstream Auth rejects certificate rstream Auth conflict" "request with certificate and rstream_auth cookie succeeded"
  else
    pass "published mTLS token rstream Auth rejects certificate rstream Auth conflict"
  fi
  stop_forward
else
  fail "published mTLS token rstream Auth tunnel creation" "forward did not become ready"
fi

start_upstream "h2-reuse-plain"
if start_forward_with_env "h2-reuse-plain" "$PRO_ENGINE" "" "$TMP_DIR/client.crt" "$TMP_DIR/client.key" --http --publish --name "$NAME_PREFIX-h2-reuse-plain"; then
  first_forward_pid=$FORWARD_PID
  first_forwarding=$FORWARDING
  start_upstream "h2-reuse-mtls"
  if start_forward_with_env "h2-reuse-mtls" "$PRO_ENGINE" "" "$TMP_DIR/client.crt" "$TMP_DIR/client.key" --http --publish --mtls --name "$NAME_PREFIX-h2-reuse-mtls"; then
    second_forward_pid=$FORWARD_PID
    second_forwarding=$FORWARDING
    if "$PYTHON" "$ROOT/test/e2e/runtime_harness.py" check h2-reuse-requires-mtls-handshake \
      --first "$first_forwarding/ping" \
      --second "$second_forwarding/ping"; then
      pass "published mTLS requires new TLS handshake after h2 reuse"
    else
      fail "published mTLS requires new TLS handshake after h2 reuse" "reused non-mTLS h2 connection reached mTLS-only tunnel"
    fi
    stop_pid "$second_forward_pid"
  else
    fail "published mTLS h2 reuse target tunnel creation" "forward did not become ready"
  fi
  stop_pid "$first_forward_pid"
else
  fail "published mTLS h2 reuse source tunnel creation" "forward did not become ready"
fi

start_upstream "basic-scoped"
if start_forward_with_env "basic-allowed" "$BASIC_ENGINE" "$CREATE_TOKEN" "" "" --http --publish --name "$ALLOWED_NAME"; then
  if env -u RSTREAM_CONTEXT -u RSTREAM_MTLS_CERT_FILE -u RSTREAM_MTLS_KEY_FILE \
    RSTREAM_ENGINE="$BASIC_ENGINE" \
    RSTREAM_AUTHENTICATION_TOKEN="$LIST_TOKEN" \
    "$RSTREAM_BIN" tunnel list --filter "name=$ALLOWED_NAME" -o json >"$TMP_DIR/list-allowed.json"; then
    if grep -q "$ALLOWED_NAME" "$TMP_DIR/list-allowed.json"; then
      pass "Basic plan scoped list sees allowed tunnel"
    else
      fail "Basic plan scoped list sees allowed tunnel" "allowed tunnel missing from list output"
    fi
  else
    fail "Basic plan scoped list sees allowed tunnel" "list command failed"
  fi
  if env -u RSTREAM_CONTEXT -u RSTREAM_MTLS_CERT_FILE -u RSTREAM_MTLS_KEY_FILE \
    RSTREAM_ENGINE="$BASIC_ENGINE" \
    RSTREAM_AUTHENTICATION_TOKEN="$LIST_TOKEN" \
    "$RSTREAM_BIN" tunnel list --filter "name=$DENIED_NAME" -o json >"$TMP_DIR/list-denied.json" &&
    ! grep -q "$DENIED_NAME" "$TMP_DIR/list-denied.json"; then
    pass "Basic plan scoped list hides denied tunnel names"
  else
    fail "Basic plan scoped list hides denied tunnel names" "denied tunnel name appeared or list failed"
  fi
  stop_forward
else
  fail "Basic plan scoped create allows matching tunnel" "allowed create failed"
fi

start_upstream "basic-denied"
if expect_forward_denied "basic-denied" "$BASIC_ENGINE" "$CREATE_TOKEN" --http --publish --name "$DENIED_NAME"; then
  pass "Basic plan scoped create rejects non matching tunnel"
else
  fail "Basic plan scoped create rejects non matching tunnel" "denied create succeeded"
fi

printf "\nResults: %d passed, %d failed\n" "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
