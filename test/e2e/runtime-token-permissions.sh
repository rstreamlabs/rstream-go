#!/usr/bin/env bash
# See LICENSE file in the project root for license information.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
. "$ROOT/test/e2e/runtime_common.sh"
PYTHON="${PYTHON:-python3}"
RSTREAM_BIN=$(resolve_rstream_cli "$ROOT")
require_control_plane_api_url
require_control_plane_token
API_URL="${RSTREAM_RUNTIME_API_URL}"
CONTROL_TOKEN="${RSTREAM_RUNTIME_CONTROL_TOKEN}"
TIMEOUT_SECONDS="${RSTREAM_RUNTIME_TIMEOUT:-60}"
NAME_PREFIX="${RSTREAM_RUNTIME_NAME_PREFIX:-runtime-token-$$}"
TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/rstream-go-token-runtime.XXXXXX")
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

fail() {
    printf "FAIL %-58s %s\n" "$1" "$2" >&2
    FAIL=$((FAIL + 1))
}

pass() {
    printf "PASS %-58s\n" "$1"
    PASS=$((PASS + 1))
}

require_executable "$RSTREAM_BIN"

cat >"$TMP_DIR/runtime_api.py" <<'PY'
import argparse
import base64
import json
import os
import socket
import ssl
import sys
import urllib.error
import urllib.parse
import urllib.request

api_url = os.environ["RSTREAM_RUNTIME_API_URL"].rstrip("/")
control_token = os.environ["RSTREAM_RUNTIME_CONTROL_TOKEN"]

def load_control_plane_headers():
    raw = os.environ.get("RSTREAM_CONTROL_PLANE_HEADERS", "").strip()
    if not raw:
        return {}
    try:
        values = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise RuntimeError("RSTREAM_CONTROL_PLANE_HEADERS must contain a JSON object") from exc
    if not isinstance(values, dict):
        raise RuntimeError("RSTREAM_CONTROL_PLANE_HEADERS must contain a JSON object")
    headers = {}
    for name, value in values.items():
        if not isinstance(name, str) or not isinstance(value, str):
            raise RuntimeError("RSTREAM_CONTROL_PLANE_HEADERS keys and values must be strings")
        if name.lower() in {"authorization", "content-type"}:
            raise RuntimeError(f"RSTREAM_CONTROL_PLANE_HEADERS cannot override {name}")
        headers[name] = value
    return headers

control_plane_headers = load_control_plane_headers()

def append_query(path, values):
    parts = urllib.parse.urlsplit(path)
    query = urllib.parse.parse_qsl(parts.query, keep_blank_values=True)
    query.extend((key, value) for key, value in values if value is not None)
    return urllib.parse.urlunsplit(("", "", parts.path, urllib.parse.urlencode(query), parts.fragment))

def request_status(base_url, token, method, path, body=None, insecure_tls=False, query_token=False):
    if query_token and token:
        path = append_query(path, [("rstream.token", token)])
    data = None
    if body is not None:
        data = json.dumps(body).encode()
    headers = dict(control_plane_headers) if base_url.rstrip("/") == api_url else {}
    headers["content-type"] = "application/json"
    if token and not query_token:
        headers["authorization"] = "Bearer " + token
    req = urllib.request.Request(base_url.rstrip("/") + path, data=data, method=method, headers=headers)
    context = ssl._create_unverified_context() if insecure_tls else None
    try:
        with urllib.request.urlopen(req, timeout=20, context=context) as resp:
            payload = resp.read().decode()
            return resp.status, payload
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode()

def request_json(base_url, token, method, path, body=None, insecure_tls=False, query_token=False, expect=(200,)):
    status, payload = request_status(base_url, token, method, path, body, insecure_tls, query_token)
    if status not in expect:
        raise RuntimeError(f"{method} {path} returned {status}: {payload}")
    return json.loads(payload) if payload else None

def control_request(token, method, path, body=None, expect=(200,)):
    return request_json(api_url, token, method, path, body, False, False, expect)

def create_token(permissions, resources=None, token=control_token):
    body = {"permissions": permissions}
    if resources is not None:
        body["resources"] = {"tunnels": resources}
    return control_request(token, "POST", "/api/tokens", body)["token"]

def create_named_token(name, permissions, resources=None, token=control_token):
    try:
        return create_token(permissions, resources, token)
    except Exception as exc:
        raise RuntimeError(f"failed to create {name} token: {exc}") from exc

def project_selection():
    projects = control_request(control_token, "GET", "/api/projects/tunnels?pageSize=100")["projects"]
    endpoint = (
        os.environ.get("RSTREAM_RUNTIME_PROJECT_ENDPOINT", "").strip()
        or os.environ.get("RSTREAM_RUNTIME_BASIC_PROJECT_ENDPOINT", "").strip()
    )
    if endpoint:
        project = next((p for p in projects if p["endpoint"] == endpoint), None)
        if project is None:
            raise RuntimeError("configured runtime project endpoint was not found")
        return project
    project = next((p for p in projects if p["plan"] == "basic"), None)
    if project is None:
        raise RuntimeError("no Basic project found; set RSTREAM_RUNTIME_PROJECT_ENDPOINT")
    return project

def project_resource(project_id, scopes):
    return {"AND": [{"projects": [project_id]}, {"scopes": {"tunnels": scopes}}]}

def setup():
    project = project_selection()
    all_projects = control_request(control_token, "GET", "/api/projects/tunnels?pageSize=100")["projects"]
    other_project = next((p for p in all_projects if p["id"] != project["id"]), None)
    if other_project is None:
        raise RuntimeError("runtime token permission suite requires at least two accessible projects")
    project_id = project["id"]
    allowed_name = os.environ["RSTREAM_RUNTIME_ALLOWED_NAME"]
    denied_name = os.environ["RSTREAM_RUNTIME_DENIED_NAME"]
    suite = os.environ["RSTREAM_RUNTIME_SUITE"]
    create_filters = {
        "AND": [
            {"name": {"exact": allowed_name}},
            {"protocol": "http"},
            {"publish": True},
            {"token_auth": True},
            {"labels": {"suite": suite}},
        ]
    }
    connect_scope = {
        "connect": {
            "filters": {"name": {"exact": allowed_name}},
            "params": {"path": {"exact": "/ping"}},
        }
    }
    invalid_resource_rejected = False
    status, _ = request_status(
        api_url,
        control_token,
        "POST",
        "/api/tokens",
        {"permissions": ["tunnels.resources.read-only"], "resources": {"tunnels": [{"projects": [project_id]}]}},
    )
    invalid_resource_rejected = status == 400
    status, _ = request_status(
        api_url,
        control_token,
        "POST",
        "/api/tokens",
        {
            "permissions": ["tunnels.resources.read-only"],
            "resources": {"tunnels": project_resource(project_id, {"list": False})},
        },
    )
    permission_resource_mismatch_rejected = status == 400
    tokens = {
        "controlProjectsRead": create_named_token("controlProjectsRead", ["account.projects.read-only"]),
        "controlCredentialsRead": create_named_token("controlCredentialsRead", ["account.credentials.read-only"]),
        "tokenCreator": create_named_token("tokenCreator", ["account.tokens.create"]),
        "emptyPermissions": create_named_token("emptyPermissions", []),
        "engineReadOnly": create_named_token("engineReadOnly", ["tunnels.resources.read-only"], project_resource(project_id, {"list": True})),
        "createForced": create_named_token("createForced", ["tunnels.tunnels.create-delete"], project_resource(project_id, {"create": {"filters": create_filters}})),
        "listSelected": create_named_token(
            "listSelected",
            ["tunnels.resources.read-only"],
            project_resource(project_id, {
                "list": {
                    "filters": {"name": {"exact": allowed_name}},
                    "select": {"name": True, "host": True},
                }
            }),
        ),
        "listAndDeniedProject": create_named_token(
            "listAndDeniedProject",
            ["tunnels.resources.read-only"],
            {"AND": [
                {"projects": [project_id]},
                {"projects": [other_project["id"]]},
                {"scopes": {"tunnels": {"list": True}}},
            ]},
        ),
        "listOrNames": create_named_token(
            "listOrNames",
            ["tunnels.resources.read-only"],
            {"AND": [
                {"projects": [project_id]},
                {"OR": [
                    {"scopes": {"tunnels": {"list": {"filters": {"name": {"exact": allowed_name}}}}}},
                    {"scopes": {"tunnels": {"list": {"filters": {"name": {"exact": denied_name}}}}}},
                ]},
            ]},
        ),
        "connectAllowedPath": create_named_token("connectAllowedPath", ["tunnels.streams.create-delete"], project_resource(project_id, connect_scope)),
        "connectNoPermission": create_named_token("connectNoPermission", [], project_resource(project_id, connect_scope)),
    }
    print(json.dumps({
        "project": project,
        "tokens": tokens,
        "invalidResourceRejected": invalid_resource_rejected,
        "permissionResourceMismatchRejected": permission_resource_mismatch_rejected,
    }))

def split_host_port(value):
    value = value.strip()
    if value.startswith("["):
        host, rest = value[1:].split("]", 1)
        port = int(rest[1:]) if rest.startswith(":") else 443
        return host, port
    if ":" in value:
        host, port = value.rsplit(":", 1)
        return host, int(port)
    return value, 443

def engine_base(engine):
    return "https://" + engine.strip()

def assert_tunnels(payload, expected_names, denied_names, selected):
    names = set()
    selected_keys = {"name", "host"}
    for item in payload:
        if isinstance(item, dict) and isinstance(item.get("name"), str):
            names.add(item["name"])
            if selected and item["name"] in expected_names:
                extra = set(item.keys()) - selected_keys
                if extra:
                    raise RuntimeError(f"selected tunnel exposed unexpected fields: {sorted(extra)}")
    missing = sorted(set(expected_names) - names)
    if missing:
        raise RuntimeError(f"missing expected tunnels: {missing}; visible={sorted(names)}; payload={json.dumps(payload)[:2000]}")
    leaked = sorted(set(denied_names) & names)
    if leaked:
        raise RuntimeError(f"denied tunnels were visible: {leaked}")

def initial_event_tunnels(event):
    if event.get("type") != "state.initial":
        raise RuntimeError(f"unexpected event type: {event.get('type')!r}")
    obj = event.get("object")
    if not isinstance(obj, dict) or not isinstance(obj.get("tunnels"), list):
        raise RuntimeError("initial event does not contain a tunnel snapshot")
    return obj["tunnels"]

def read_sse_initial(engine, token, query_token):
    path = "/api/sse"
    path = append_query(path, [("rstream.token", token)] if query_token else [])
    headers = {}
    if not query_token:
        headers["authorization"] = "Bearer " + token
    req = urllib.request.Request(engine_base(engine) + path, method="GET", headers=headers)
    context = ssl._create_unverified_context()
    with urllib.request.urlopen(req, timeout=20, context=context) as resp:
        for _ in range(64):
            line = resp.readline().decode().strip()
            if not line:
                continue
            if line.startswith("data: "):
                return json.loads(line[len("data: "):])
    raise RuntimeError("SSE initial event not received")

def read_websocket_initial(engine, token, query_token):
    host, port = split_host_port(engine)
    path = append_query("/api/websocket", [("rstream.token", token)] if query_token else [])
    key = base64.b64encode(os.urandom(16)).decode()
    headers = [
        f"GET {path} HTTP/1.1",
        f"Host: {engine}",
        "Upgrade: websocket",
        "Connection: Upgrade",
        f"Sec-WebSocket-Key: {key}",
        "Sec-WebSocket-Version: 13",
    ]
    if not query_token:
        headers.append("Authorization: Bearer " + token)
    request = "\r\n".join(headers) + "\r\n\r\n"
    context = ssl._create_unverified_context()
    with socket.create_connection((host, port), timeout=20) as raw:
        with context.wrap_socket(raw, server_hostname=host) as conn:
            conn.sendall(request.encode())
            header = b""
            while b"\r\n\r\n" not in header:
                chunk = conn.recv(1)
                if not chunk:
                    raise RuntimeError("websocket closed during handshake")
                header += chunk
            if not header.startswith(b"HTTP/1.1 101"):
                raise RuntimeError(header.decode(errors="replace").splitlines()[0])
            frame_header = conn.recv(2)
            if len(frame_header) != 2:
                raise RuntimeError("websocket frame header missing")
            length = frame_header[1] & 0x7F
            if length == 126:
                length = int.from_bytes(conn.recv(2), "big")
            elif length == 127:
                length = int.from_bytes(conn.recv(8), "big")
            payload = b""
            while len(payload) < length:
                chunk = conn.recv(length - len(payload))
                if not chunk:
                    raise RuntimeError("websocket closed during initial event")
                payload += chunk
            return json.loads(payload.decode())

def main():
    parser = argparse.ArgumentParser()
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("setup")
    control = sub.add_parser("control-status")
    control.add_argument("token")
    control.add_argument("method")
    control.add_argument("path")
    control.add_argument("body", nargs="?")
    engine = sub.add_parser("engine-status")
    engine.add_argument("engine")
    engine.add_argument("token")
    engine.add_argument("path")
    engine.add_argument("--query-token", action="store_true")
    engine_list = sub.add_parser("engine-list-check")
    engine_list.add_argument("engine")
    engine_list.add_argument("token")
    engine_list.add_argument("--expect", action="append", default=[])
    engine_list.add_argument("--deny", action="append", default=[])
    engine_list.add_argument("--selected", action="store_true")
    watch = sub.add_parser("watch-check")
    watch.add_argument("transport", choices=("sse", "websocket"))
    watch.add_argument("engine")
    watch.add_argument("token")
    watch.add_argument("--query-token", action="store_true")
    watch.add_argument("--expect", action="append", default=[])
    watch.add_argument("--deny", action="append", default=[])
    watch.add_argument("--selected", action="store_true")
    args = parser.parse_args()
    if args.command == "setup":
        setup()
    elif args.command == "control-status":
        body = None if args.body is None else json.loads(args.body)
        status, _ = request_status(api_url, args.token, args.method, args.path, body)
        print(status)
    elif args.command == "engine-status":
        status, _ = request_status(engine_base(args.engine), args.token, "GET", args.path, insecure_tls=True, query_token=args.query_token)
        print(status)
    elif args.command == "engine-list-check":
        payload = request_json(engine_base(args.engine), args.token, "GET", "/api/tunnels", insecure_tls=True)
        assert_tunnels(payload, args.expect, args.deny, args.selected)
    elif args.command == "watch-check":
        if args.transport == "sse":
            event = read_sse_initial(args.engine, args.token, args.query_token)
        else:
            event = read_websocket_initial(args.engine, args.token, args.query_token)
        assert_tunnels(initial_event_tunnels(event), args.expect, args.deny, args.selected)

if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        print(f"ERROR {exc}", file=sys.stderr)
        sys.exit(1)
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
    local label=$1 engine=$2 token=$3
    shift 3
    FORWARD_LOG="$TMP_DIR/forward-$label.log"
    : >"$FORWARD_LOG"
    env -u RSTREAM_CONTEXT -u RSTREAM_MTLS_CERT_FILE -u RSTREAM_MTLS_KEY_FILE \
        RSTREAM_ENGINE="$engine" \
        RSTREAM_AUTHENTICATION_TOKEN="$token" \
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

control_status() {
    "$PYTHON" "$TMP_DIR/runtime_api.py" control-status "$@"
}

engine_status() {
    "$PYTHON" "$TMP_DIR/runtime_api.py" engine-status "$@"
}

http_status() {
    local token=$1 url=$2
    if [ -n "$token" ]; then
        curl -sk -H "Authorization: Bearer $token" -o "$TMP_DIR/http-body.txt" -w "%{http_code}" "$url" || true
    else
        curl -sk -o "$TMP_DIR/http-body.txt" -w "%{http_code}" "$url" || true
    fi
}

export RSTREAM_RUNTIME_API_URL="$API_URL"
export RSTREAM_RUNTIME_CONTROL_TOKEN="$CONTROL_TOKEN"
export RSTREAM_RUNTIME_SUITE="$NAME_PREFIX"
export RSTREAM_RUNTIME_ALLOWED_NAME="$NAME_PREFIX-allowed"
export RSTREAM_RUNTIME_DENIED_NAME="$NAME_PREFIX-denied"

"$PYTHON" "$TMP_DIR/runtime_api.py" setup >"$TMP_DIR/setup.json"
PROJECT_ENGINE="$(json_get "$TMP_DIR/setup.json" project.url)"
ALLOWED_NAME="$NAME_PREFIX-allowed"
DENIED_NAME="$NAME_PREFIX-denied"
CONTROL_PROJECTS_TOKEN="$(json_get "$TMP_DIR/setup.json" tokens.controlProjectsRead)"
CONTROL_CREDENTIALS_TOKEN="$(json_get "$TMP_DIR/setup.json" tokens.controlCredentialsRead)"
TOKEN_CREATOR_TOKEN="$(json_get "$TMP_DIR/setup.json" tokens.tokenCreator)"
EMPTY_TOKEN="$(json_get "$TMP_DIR/setup.json" tokens.emptyPermissions)"
ENGINE_READ_TOKEN="$(json_get "$TMP_DIR/setup.json" tokens.engineReadOnly)"
CREATE_FORCED_TOKEN="$(json_get "$TMP_DIR/setup.json" tokens.createForced)"
LIST_SELECTED_TOKEN="$(json_get "$TMP_DIR/setup.json" tokens.listSelected)"
LIST_AND_DENIED_PROJECT_TOKEN="$(json_get "$TMP_DIR/setup.json" tokens.listAndDeniedProject)"
LIST_OR_NAMES_TOKEN="$(json_get "$TMP_DIR/setup.json" tokens.listOrNames)"
CONNECT_ALLOWED_TOKEN="$(json_get "$TMP_DIR/setup.json" tokens.connectAllowedPath)"
CONNECT_NO_PERMISSION_TOKEN="$(json_get "$TMP_DIR/setup.json" tokens.connectNoPermission)"

if [ "$(json_get "$TMP_DIR/setup.json" invalidResourceRejected)" = "True" ]; then
    pass "invalid tunnel resource shape is rejected"
else
    fail "invalid tunnel resource shape is rejected" "root array was accepted"
fi

if [ "$(json_get "$TMP_DIR/setup.json" permissionResourceMismatchRejected)" = "True" ]; then
    pass "token mint rejects resources that do not allow requested permission"
else
    fail "token mint rejects resources that do not allow requested permission" "resource/permission mismatch was accepted"
fi

status=$(control_status "$CONTROL_PROJECTS_TOKEN" GET "/api/projects/tunnels?pageSize=1")
if [ "$status" = "200" ]; then pass "Control plane API project read permission allows project list"; else fail "Control plane API project read permission allows project list" "status=$status"; fi

status=$(control_status "$ENGINE_READ_TOKEN" GET "/api/projects/tunnels?pageSize=1")
if [ "$status" = "403" ]; then pass "Engine API permission cannot list Control plane projects"; else fail "Engine API permission cannot list Control plane projects" "status=$status"; fi

status=$(control_status "$CONTROL_CREDENTIALS_TOKEN" GET "/api/credentials?pageSize=1")
if [ "$status" = "200" ]; then pass "Control plane API credential read permission allows credential list"; else fail "Control plane API credential read permission allows credential list" "status=$status"; fi

status=$(control_status "$ENGINE_READ_TOKEN" GET "/api/credentials?pageSize=1")
if [ "$status" = "403" ]; then pass "Engine API permission cannot list credentials"; else fail "Engine API permission cannot list credentials" "status=$status"; fi

status=$(control_status "$TOKEN_CREATOR_TOKEN" POST "/api/tokens" '{"permissions":[]}')
if [ "$status" = "200" ]; then pass "Control plane API token create permission can mint auth token"; else fail "Control plane API token create permission can mint auth token" "status=$status"; fi

status=$(control_status "$ENGINE_READ_TOKEN" POST "/api/tokens" '{"permissions":[]}')
if [ "$status" = "403" ]; then pass "Engine API permission cannot mint auth tokens"; else fail "Engine API permission cannot mint auth tokens" "status=$status"; fi

status=$(control_status "$EMPTY_TOKEN" GET "/api/user")
if [ "$status" = "403" ]; then pass "empty permission token cannot read Control plane user"; else fail "empty permission token cannot read Control plane user" "status=$status"; fi

start_upstream "allowed"
if start_forward_with_env "forced-create" "$PROJECT_ENGINE" "$CREATE_FORCED_TOKEN"; then
    ALLOWED_FORWARDING="$FORWARDING"
    ALLOWED_PID="$FORWARD_PID"
    pass "create scope forces published HTTP token-auth tunnel properties"
else
    fail "create scope forces published HTTP token-auth tunnel properties" "forward did not become ready"
    printf "\nResults: %d passed, %d failed\n" "$PASS" "$FAIL"
    exit 1
fi

start_upstream "denied"
if start_forward_with_env "unrestricted-denied" "$PROJECT_ENGINE" "$CONTROL_TOKEN" --http --publish --token-auth --name "$DENIED_NAME" --label "suite=$NAME_PREFIX"; then
    DENIED_FORWARDING="$FORWARDING"
    DENIED_PID="$FORWARD_PID"
    pass "unrestricted token creates comparison tunnel"
else
    fail "unrestricted token creates comparison tunnel" "forward did not become ready"
    printf "\nResults: %d passed, %d failed\n" "$PASS" "$FAIL"
    exit 1
fi

status=$(http_status "" "$ALLOWED_FORWARDING/ping")
if [ "$status" = "401" ]; then pass "forced token-auth tunnel rejects missing bearer token"; else fail "forced token-auth tunnel rejects missing bearer token" "status=$status"; fi

status=$(http_status "$CONNECT_ALLOWED_TOKEN" "$ALLOWED_FORWARDING/ping")
if [ "$status" = "200" ]; then pass "connect scope allows matching HTTP path"; else fail "connect scope allows matching HTTP path" "status=$status"; fi

status=$(http_status "$CONNECT_ALLOWED_TOKEN" "$ALLOWED_FORWARDING/private")
if [ "$status" = "403" ]; then pass "connect scope rejects non matching HTTP path"; else fail "connect scope rejects non matching HTTP path" "status=$status"; fi

status=$(http_status "$CONNECT_NO_PERMISSION_TOKEN" "$ALLOWED_FORWARDING/ping")
if [ "$status" = "403" ]; then pass "connect scope still requires Engine API stream permission"; else fail "connect scope still requires Engine API stream permission" "status=$status"; fi

start_upstream "wrong-name"
if expect_forward_denied "wrong-name" "$PROJECT_ENGINE" "$CREATE_FORCED_TOKEN" --http --publish --token-auth --name "$DENIED_NAME" --label "suite=$NAME_PREFIX"; then
    pass "create scope rejects explicit non matching tunnel name"
else
    fail "create scope rejects explicit non matching tunnel name" "denied create succeeded"
fi

start_upstream "wrong-label"
if expect_forward_denied "wrong-label" "$PROJECT_ENGINE" "$CREATE_FORCED_TOKEN" --http --publish --token-auth --name "$ALLOWED_NAME" --label "suite=wrong"; then
    pass "create scope rejects explicit non matching label"
else
    fail "create scope rejects explicit non matching label" "denied create succeeded"
fi

start_upstream "no-publish"
if expect_forward_denied "no-publish" "$PROJECT_ENGINE" "$CREATE_FORCED_TOKEN" --no-publish --name "$ALLOWED_NAME"; then
    pass "create scope rejects explicit non matching publish mode"
else
    fail "create scope rejects explicit non matching publish mode" "denied create succeeded"
fi

status=$(engine_status "$PROJECT_ENGINE" "$LIST_SELECTED_TOKEN" "/api/tunnels")
if [ "$status" = "200" ]; then pass "Engine API read permission allows tunnel list"; else fail "Engine API read permission allows tunnel list" "status=$status"; fi

if "$PYTHON" "$TMP_DIR/runtime_api.py" engine-list-check "$PROJECT_ENGINE" "$LIST_SELECTED_TOKEN" --expect "$ALLOWED_NAME" --deny "$DENIED_NAME" --selected; then
    pass "list scope filters and projects selected tunnel fields"
else
    fail "list scope filters and projects selected tunnel fields" "unexpected tunnel list"
fi

status=$(engine_status "$PROJECT_ENGINE" "$CREATE_FORCED_TOKEN" "/api/tunnels")
if [ "$status" = "403" ]; then pass "create-only Engine API token cannot list tunnels"; else fail "create-only Engine API token cannot list tunnels" "status=$status"; fi

status=$(engine_status "$PROJECT_ENGINE" "$LIST_AND_DENIED_PROJECT_TOKEN" "/api/tunnels")
if [ "$status" = "403" ]; then pass "AND resource denies other project branch"; else fail "AND resource denies other project branch" "status=$status"; fi

if "$PYTHON" "$TMP_DIR/runtime_api.py" engine-list-check "$PROJECT_ENGINE" "$LIST_OR_NAMES_TOKEN" --expect "$ALLOWED_NAME" --expect "$DENIED_NAME"; then
    pass "OR resource allows either listed tunnel name"
else
    fail "OR resource allows either listed tunnel name" "unexpected tunnel list"
fi

status=$(engine_status "$PROJECT_ENGINE" "$LIST_SELECTED_TOKEN" "/api/tunnels" --query-token)
if [ "$status" = "401" ]; then pass "query auth token is rejected on non streaming Engine API"; else fail "query auth token is rejected on non streaming Engine API" "status=$status"; fi

if "$PYTHON" "$TMP_DIR/runtime_api.py" watch-check sse "$PROJECT_ENGINE" "$LIST_SELECTED_TOKEN" --expect "$ALLOWED_NAME" --deny "$DENIED_NAME" --selected; then
    pass "SSE watch applies list scope filters and selection"
else
    fail "SSE watch applies list scope filters and selection" "unexpected initial event"
fi

if "$PYTHON" "$TMP_DIR/runtime_api.py" watch-check sse "$PROJECT_ENGINE" "$LIST_SELECTED_TOKEN" --query-token --expect "$ALLOWED_NAME" --deny "$DENIED_NAME" --selected; then
    pass "SSE watch accepts short-lived read-only query token"
else
    fail "SSE watch accepts short-lived read-only query token" "query token watch failed"
fi

if "$PYTHON" "$TMP_DIR/runtime_api.py" watch-check websocket "$PROJECT_ENGINE" "$LIST_SELECTED_TOKEN" --query-token --expect "$ALLOWED_NAME" --deny "$DENIED_NAME" --selected; then
    pass "WebSocket watch accepts short-lived read-only query token"
else
    fail "WebSocket watch accepts short-lived read-only query token" "query token watch failed"
fi

kill "$ALLOWED_PID" "$DENIED_PID" 2>/dev/null || true
wait "$ALLOWED_PID" "$DENIED_PID" 2>/dev/null || true

printf "\nResults: %d passed, %d failed\n" "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
