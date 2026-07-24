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
    if [ -x "$candidate" ]; then
      printf "%s\n" "$candidate"
      return
    fi
  done < <(find "$root/out/cmd/rstream" -type f -name rstream 2>/dev/null | sort)

  if command -v rstream >/dev/null 2>&1; then
    command -v rstream
  fi
}

resolve_rstream_cli() {
  local root=$1
  local binary

  binary=$(find_rstream_cli "$root" || true)
  if [ -z "$binary" ] || [ ! -x "$binary" ]; then
    printf "ERROR missing rstream binary; set RSTREAM_BIN or build the CLI first\n" >&2
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

rewrite_downstream_address() {
  local address=$1
  local port=${RSTREAM_E2E_DOWNSTREAM_PORT:-}
  local port_map=${RSTREAM_E2E_DOWNSTREAM_PORT_MAP:-}
  if { [ -z "$port" ] && [ -z "$port_map" ]; } || [[ "$address" == rstrm://* ]]; then
    printf "%s\n" "$address"
    return
  fi
  python3 - "$address" "$port" "$port_map" <<'PY'
import sys
import urllib.parse

address, fallback_port, mapping_spec = sys.argv[1:]
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
    if not port:
        print(address)
        raise SystemExit
    host = parsed.hostname or ""
    if ":" in host:
        host = f"[{host}]"
    netloc = f"{host}:{port}"
    if parsed.username:
        credentials = parsed.username
        if parsed.password:
            credentials += f":{parsed.password}"
        netloc = f"{credentials}@{netloc}"
    print(urllib.parse.urlunsplit((parsed.scheme, netloc, parsed.path, parsed.query, parsed.fragment)))
else:
    parsed = urllib.parse.urlsplit(f"//{address}")
    port = port_map.get(str(parsed.port), fallback_port)
    if not port:
        print(address)
        raise SystemExit
    host = address
    if address.startswith("["):
        closing = address.find("]")
        if closing >= 0:
            host = address[:closing + 1]
    elif ":" in address:
        host = address.rsplit(":", 1)[0]
    print(f"{host}:{port}")
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
