#!/usr/bin/env bash
# See LICENSE file in the project root for license information.

find_rstream_cli() {
  local root=$1
  local candidate

  if [ -n "${RSTREAM_BIN:-}" ]; then
    printf "%s\n" "$RSTREAM_BIN"
    return
  fi

  while IFS= read -r candidate; do
    if [ -x "$candidate" ] && "$candidate" --help >/dev/null 2>&1; then
      printf "%s\n" "$candidate"
      return
    fi
  done < <(find "$root/out/cmd/rstream" -type f -name rstream 2>/dev/null | sort -r)

  candidate=$(command -v rstream 2>/dev/null || true)
  if [ -n "$candidate" ] && "$candidate" --help >/dev/null 2>&1; then
    printf "%s\n" "$candidate"
  fi
}

resolve_rstream_cli() {
  local root=$1
  local binary

  binary=$(find_rstream_cli "$root" || true)
  if [ -z "$binary" ] || [ ! -x "$binary" ] || ! "$binary" --help >/dev/null 2>&1; then
    printf "ERROR missing or unusable rstream binary; set RSTREAM_BIN or build the CLI for this host first\n" >&2
    exit 2
  fi
  printf "%s\n" "$binary"
}

require_executable() {
  if [ ! -x "$1" ]; then
    printf "ERROR missing executable: %s\n" "$1" >&2
    exit 2
  fi
}

ready_value_from_log() {
  local log=$1
  awk '$1 == "READY" && length($2) > 0 { print $2; exit }' "$log" 2>/dev/null
}

print_server_log_tail() {
  local log=$1
  [ -s "$log" ] || return 0
  printf '%s\n' '  ---- server log (last 100 lines) ----'
  tail -n 100 "$log" | sed 's/^/  /'
}

rewrite_downstream_address() {
  local address=$1
  local host=${RSTREAM_E2E_DOWNSTREAM_HOST:-}
  local port=${RSTREAM_E2E_DOWNSTREAM_PORT:-}
  local port_map=${RSTREAM_E2E_DOWNSTREAM_PORT_MAP:-}
  if { [ -z "$host" ] && [ -z "$port" ] && [ -z "$port_map" ]; } || [[ "$address" == rstrm://* ]]; then
    printf "%s\n" "$address"
    return
  fi
  python3 - "$address" "$host" "$port" "$port_map" <<'PY'
import sys
import urllib.parse

address, override_host, fallback_port, mapping_spec = sys.argv[1:]
for suffix in (" (tls)", " (tcp)", " (dtls)", " (quic)", " (webtty)"):
    if address.endswith(suffix):
        address = address[:-len(suffix)]
        break
port_map = {}
for mapping in mapping_spec.split(","):
    if not mapping:
        continue
    source, target = mapping.split(":", 1)
    port_map[source] = target

if "://" in address:
    parsed = urllib.parse.urlsplit(address)
    port = port_map.get(str(parsed.port), fallback_port)
    if not port and not override_host:
        print(address)
        raise SystemExit
    port = port or str(parsed.port or "")
    host = override_host or parsed.hostname or ""
    if ":" in host:
        host = f"[{host}]"
    netloc = f"{host}:{port}" if port else host
    if parsed.username:
        credentials = parsed.username
        if parsed.password:
            credentials += f":{parsed.password}"
        netloc = f"{credentials}@{netloc}"
    print(urllib.parse.urlunsplit((parsed.scheme, netloc, parsed.path, parsed.query, parsed.fragment)))
else:
    parsed = urllib.parse.urlsplit(f"//{address}")
    port = port_map.get(str(parsed.port), fallback_port)
    if not port and not override_host:
        print(address)
        raise SystemExit
    port = port or str(parsed.port or "")
    host = override_host or parsed.hostname or ""
    if ":" in host:
        host = f"[{host}]"
    print(f"{host}:{port}" if port else host)
PY
}

require_control_plane_api_url() {
  if [ -z "${RSTREAM_RUNTIME_API_URL:-}" ]; then
    printf "ERROR set RSTREAM_RUNTIME_API_URL to the Control plane API URL for this test\n" >&2
    printf "This runtime suite is not engine-only; it creates or verifies Control plane resources.\n" >&2
    exit 2
  fi
}

require_control_plane_token() {
  if [ -z "${RSTREAM_RUNTIME_CONTROL_TOKEN:-}" ]; then
    printf "ERROR set RSTREAM_RUNTIME_CONTROL_TOKEN to a PAT with the permissions required by this test\n" >&2
    printf "Do not rely on the engine context token for Control plane setup checks.\n" >&2
    exit 2
  fi
}

control_plane_curl_headers() {
  local name value parsed=false
  CONTROL_PLANE_CURL_ARGS=()
  while IFS= read -r -d '' name && IFS= read -r -d '' value; do
    if [ "$name" = __RSTREAM_HEADERS_OK__ ]; then
      parsed=true
      continue
    fi
    CONTROL_PLANE_CURL_ARGS+=("-H" "$name: $value")
  done < <(python3 - <<'PY'
import json
import os
import sys

raw = os.environ.get("RSTREAM_CONTROL_PLANE_HEADERS", "").strip()
if raw:
    try:
        headers = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise RuntimeError("RSTREAM_CONTROL_PLANE_HEADERS must contain a JSON object") from exc
else:
    headers = {}
if not isinstance(headers, dict):
    raise RuntimeError("RSTREAM_CONTROL_PLANE_HEADERS must contain a JSON object")
for name, value in headers.items():
    if not isinstance(name, str) or not isinstance(value, str):
        raise RuntimeError("RSTREAM_CONTROL_PLANE_HEADERS keys and values must be strings")
    if name.lower() == "authorization":
        raise RuntimeError("RSTREAM_CONTROL_PLANE_HEADERS cannot override authorization")
    if not name or ":" in name or "\r" in name or "\n" in name or "\r" in value or "\n" in value:
        raise RuntimeError("RSTREAM_CONTROL_PLANE_HEADERS contains an invalid HTTP header")
    sys.stdout.buffer.write(name.encode() + b"\0" + value.encode() + b"\0")
sys.stdout.buffer.write(b"__RSTREAM_HEADERS_OK__\0\0")
PY
  )
  [ "$parsed" = true ]
}

require_runtime_project_engine_match() {
  local binary=$1
  local context_json=
  if [ -z "${RSTREAM_RUNTIME_PROJECT_ID:-}" ]; then
    printf "ERROR set RSTREAM_RUNTIME_PROJECT_ID before validating the runtime engine\n" >&2
    exit 2
  fi
  if [ -z "${RSTREAM_ENGINE:-}" ]; then
    if [ -z "${RSTREAM_CONTEXT:-}" ]; then
      printf "ERROR RSTREAM_CONTEXT or RSTREAM_ENGINE is required\n" >&2
      exit 2
    fi
    context_json=$("$binary" context get "$RSTREAM_CONTEXT" --output json)
  fi
  RSTREAM_RUNTIME_CONTEXT_JSON="$context_json" python3 - <<'PY'
import json
import os
import sys
import urllib.parse
import urllib.request

api_url = os.environ["RSTREAM_RUNTIME_API_URL"].rstrip("/")
project_id = os.environ["RSTREAM_RUNTIME_PROJECT_ID"]
token = os.environ["RSTREAM_RUNTIME_CONTROL_TOKEN"]
request = urllib.request.Request(
    f"{api_url}/api/projects/tunnels/{project_id}",
    headers={"authorization": f"Bearer {token}"},
)
raw_headers = os.environ.get("RSTREAM_CONTROL_PLANE_HEADERS", "").strip()
if raw_headers:
    try:
        extra_headers = json.loads(raw_headers)
    except json.JSONDecodeError as exc:
        raise RuntimeError("RSTREAM_CONTROL_PLANE_HEADERS must contain a JSON object") from exc
    if not isinstance(extra_headers, dict):
        raise RuntimeError("RSTREAM_CONTROL_PLANE_HEADERS must contain a JSON object")
    for name, value in extra_headers.items():
        if not isinstance(name, str) or not isinstance(value, str):
            raise RuntimeError("RSTREAM_CONTROL_PLANE_HEADERS keys and values must be strings")
        if name.lower() == "authorization":
            raise RuntimeError("RSTREAM_CONTROL_PLANE_HEADERS cannot override authorization")
        request.add_header(name, value)
with urllib.request.urlopen(request, timeout=20) as response:
    project_endpoint = json.load(response)["endpoint"]

engine = os.environ.get("RSTREAM_ENGINE", "").strip()
if engine:
    parsed = urllib.parse.urlsplit(engine if "://" in engine else f"//{engine}")
    engine_endpoint = (parsed.hostname or "").split(".", 1)[0]
else:
    context = json.loads(os.environ["RSTREAM_RUNTIME_CONTEXT_JSON"])
    engine_endpoint = context.get("ProjectEndpoint", "")

if engine_endpoint != project_endpoint:
    print(
        "ERROR runtime project and engine do not match: "
        f"project endpoint is {project_endpoint}, engine endpoint is {engine_endpoint or '<empty>'}",
        file=sys.stderr,
    )
    raise SystemExit(2)
PY
}
