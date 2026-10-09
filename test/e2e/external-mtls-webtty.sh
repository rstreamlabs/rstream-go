#!/usr/bin/env bash
set -euo pipefail
umask 077

# Real CLI + helper + EE engine, using only an isolated loopback PostgreSQL
# container and generated test certificates. No account credentials are needed.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ENGINE_REPO="${RSTREAM_ENGINE_REPO:?set RSTREAM_ENGINE_REPO to an rstream-engine checkout}"
NEXT_REPO="${RSTREAM_NEXT_REPO:?set RSTREAM_NEXT_REPO to an rstream-nextjs checkout with npm dependencies installed}"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/rstream-exec-mtls.XXXXXX")"
CONTAINER=""
POSTGRES_BIN="${RSTREAM_TEST_POSTGRES_BIN:-}"
ENGINE_PID=""
SERVER_PIDS=()
cleanup() {
  local status=$?
  for pid in "${SERVER_PIDS[@]}"; do
    kill "${pid}" 2>/dev/null || true
    wait "${pid}" 2>/dev/null || true
  done
  if [[ -n "${ENGINE_PID}" ]]; then
    kill "${ENGINE_PID}" 2>/dev/null || true
    wait "${ENGINE_PID}" 2>/dev/null || true
  fi
  if [[ -n "${CONTAINER}" ]]; then docker rm -f "${CONTAINER}" >/dev/null 2>&1 || true; fi
  if [[ -n "${POSTGRES_BIN}" && -f "${WORK}/pg/postmaster.pid" ]]; then
    "${POSTGRES_BIN}/pg_ctl" -D "${WORK}/pg" -m immediate -w stop >/dev/null 2>&1 || true
  fi
  if [[ "${status}" != 0 || "${RSTREAM_KEEP_RUNTIME:-0}" == 1 ]]; then
    echo "external mTLS test artifacts: ${WORK}" >&2
  else
    rm -rf "${WORK}"
  fi
}
trap cleanup EXIT

echo "Building the standalone CLI, example helper and EE engine"
(cd "${ROOT}" && go build -o "${WORK}/rstream" ./cmd/rstream)
(cd "${ROOT}" && CGO_ENABLED=0 go build -o "${WORK}/helper" ./examples/mtls-exec-helper)
(cd "${ENGINE_REPO}" && go build -tags ee,http2legacy -o "${WORK}/engine" ./cmd/rstream-engine-ee)
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-521 -nodes -days 1 \
  -keyout "${WORK}/client.key" -out "${WORK}/client.crt" -subj /CN=exec-test \
  -addext keyUsage=digitalSignature -addext extendedKeyUsage=clientAuth >/dev/null 2>&1
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 1 \
  -keyout "${WORK}/engine.key" -out "${WORK}/engine.crt" -subj /CN=localhost \
  -addext subjectAltName=IP:127.0.0.1,DNS:project.localhost >/dev/null 2>&1
PIN="$(openssl x509 -in "${WORK}/client.crt" -outform DER | openssl dgst -sha256 -r | cut -d' ' -f1)"
PASSWORD="$(openssl rand -hex 24)"
if [[ -n "${POSTGRES_BIN}" ]]; then
  printf '%s\n' "${PASSWORD}" >"${WORK}/pg-password"
  "${POSTGRES_BIN}/initdb" -D "${WORK}/pg" -U exec_test --auth=scram-sha-256 --pwfile="${WORK}/pg-password" >"${WORK}/initdb.log"
  PG_PORT="$(python3 - <<'PY'
import socket
with socket.socket() as sock:
    sock.bind(('127.0.0.1', 0))
    print(sock.getsockname()[1])
PY
)"
  "${POSTGRES_BIN}/pg_ctl" -D "${WORK}/pg" -l "${WORK}/pg.log" -o "-h 127.0.0.1 -p ${PG_PORT} -k ${WORK}" -w start >/dev/null
  PGPASSWORD="${PASSWORD}" "${POSTGRES_BIN}/createdb" -h 127.0.0.1 -p "${PG_PORT}" -U exec_test exec_test
else
CONTAINER="rstream-exec-mtls-$(openssl rand -hex 8)"
docker run --detach --rm --name "${CONTAINER}" -p 127.0.0.1::5432 \
  -e POSTGRES_USER=exec_test -e POSTGRES_DB=exec_test -e "POSTGRES_PASSWORD=${PASSWORD}" \
  "${RSTREAM_TEST_POSTGRES_IMAGE:-postgres:17}" >"${WORK}/container.txt"
PG_PORT="$(docker port "${CONTAINER}" 5432/tcp | cut -d: -f2)"
for _ in $(seq 1 100); do
  if docker exec "${CONTAINER}" pg_isready -U exec_test -d exec_test >/dev/null 2>&1; then break; fi
  sleep 0.1
done
fi
psql_fixture() {
  if [[ -n "${POSTGRES_BIN}" ]]; then
    PGPASSWORD="${PASSWORD}" "${POSTGRES_BIN}/psql" -h 127.0.0.1 -p "${PG_PORT}" -U exec_test -d exec_test -v ON_ERROR_STOP=1
  else
    docker exec -i "${CONTAINER}" psql -U exec_test -d exec_test -v ON_ERROR_STOP=1
  fi
}
# Apply the real product schema only to this newly created, isolated database.
(cd "${NEXT_REPO}" && POSTGRES_PRISMA_DIRECT_URL="postgresql://exec_test:${PASSWORD}@127.0.0.1:${PG_PORT}/exec_test" \
  RSTREAM_DATABASE_MIGRATION_TARGET=local \
  RSTREAM_DATABASE_MIGRATION_CONFIRM="local:127.0.0.1:${PG_PORT}/exec_test" \
  ./node_modules/.bin/prisma db push) >"${WORK}/schema.log" 2>&1
psql_fixture >"${WORK}/seed.log" <<SQL
INSERT INTO users (id, name, email, "createdAt", "updatedAt", role)
VALUES ('exec-user', 'Exec test', 'exec@example.test', now(), now(), 'user');
INSERT INTO workspaces (id, type, name, "ownerId", "createdAt", "updatedAt", "customerVersion", "subscriptionVersion", "billingVersion")
VALUES ('exec-workspace', 'organization', 'Exec test', 'exec-user', now(), now(), 0, 0, 0);
INSERT INTO tunnels_clusters (id, name, provider, region, tenancy, status, domain, "enginePort", "turnPort", "turnsPort", "allowedPlans", "workspaceId", "createdAt", "updatedAt")
VALUES ('exec-cluster', 'Exec test', 'other', 'local', 'dedicated', 'active', 'localhost', 443, 3478, 5349, ARRAY['enterprise'::"TunnelsClusterPlan"], 'exec-workspace', now(), now());
INSERT INTO tunnels_projects (id, "workspaceId", "clusterId", name, endpoint, status, plan, "billingEnabled", settings, "createdAt", "updatedAt")
VALUES ('exec-project', 'exec-workspace', 'exec-cluster', 'Exec test', 'project', 'active', 'enterprise', false, '{}'::jsonb, now(), now());
INSERT INTO credentials (id, name, type, data, permissions, "userId", "createdAt", "updatedAt") VALUES
('exec-cert', 'Exec test', 'mtls', '${PIN}', '["tunnels.tunnels.create-delete","tunnels.streams.create-delete","tunnels.resources.read-only"]', 'exec-user', now(), now());
SQL
PORT="$(python3 - <<'PY'
import socket
with socket.socket() as sock:
    sock.bind(('127.0.0.1', 0))
    print(sock.getsockname()[1])
PY
)"
psql_fixture >"${WORK}/cluster-port.log" <<SQL
UPDATE tunnels_clusters SET "enginePort" = ${PORT} WHERE id = 'exec-cluster';
SQL
cat >"${WORK}/engine.yaml" <<YAML
engine:
  host: localhost
  log_level: debug
  reverse_proxy: custom
http:
  enabled: false
tls:
  enabled: true
  listen_addr: "127.0.0.1:${PORT}"
quic:
  enabled: true
  listen_addr: "127.0.0.1:${PORT}"
dtls:
  enabled: false
certs:
  certmagic:
    enabled: false
  static:
    enabled: true
    cert_file: "${WORK}/engine.crt"
    key_file: "${WORK}/engine.key"
auth:
  jwt:
    enabled: false
  postgresql:
    enabled: true
idp:
  enabled: false
challenge:
  enabled: false
custom_domains:
  enabled: false
published_tcp:
  enabled: false
metrics:
  prometheus:
    enabled: false
events:
  postgresql:
    enabled: false
webhooks:
  postgresql:
    enabled: false
webtty:
  postgresql:
    enabled: false
packages:
  postgresql:
    enabled: false
geoip:
  enabled: false
ech:
  enabled: false
YAML
start_engine() {
  env -i PATH="${PATH}" GODEBUG=http2xconnect=1 \
    RSTREAM_ENGINE_POSTGRESQL__URL="postgresql://exec_test:${PASSWORD}@127.0.0.1:${PG_PORT}/exec_test" \
    RSTREAM_ENGINE_POSTGRESQL__MAX_CONNECTIONS=4 \
    RSTREAM_ENGINE_AUTH__POSTGRESQL__TOKEN_JWT_SECRET="${PASSWORD}" \
    "${WORK}/engine" --config "${WORK}/engine.yaml" >>"${WORK}/engine.log" 2>&1 &
  ENGINE_PID=$!
}
start_engine
python3 - "${PORT}" <<'PY'
import socket, sys, time
deadline = time.monotonic() + 30
while True:
    try:
        with socket.create_connection(('127.0.0.1', int(sys.argv[1])), timeout=0.5):
            break
    except OSError:
        if time.monotonic() >= deadline:
            raise
        time.sleep(0.1)
PY
cat >"${WORK}/exec.py" <<'PY'
import json, os, pathlib, subprocess, sys, time
root, config, name, expected = sys.argv[1:]
deadline = time.monotonic() + 90
while True:
    try:
        result = subprocess.run([
            root + '/rstream', '--config', config, '--context', 'external',
            '--log-level', 'none', 'webtty', 'exec', '--url', 'rstrm://' + name,
            '--no-discovery', '--transport', 'websocket', '-o', 'json',
            '--', 'sh', '-c', 'printf ' + expected,
        ], env={'PATH': os.environ['PATH'], 'RSTREAM_DATA_DIR': root + '/data'},
            capture_output=True, text=True, timeout=5)
        if result.returncode == 0:
            value = json.loads(result.stdout)
            assert value['exit_code'] == 0 and value['stdout'] == expected and value['stderr'] == '', value
            print('Verified ' + expected, flush=True)
            break
        pathlib.Path(root + '/client-error.log').write_text(result.stderr)
    except subprocess.TimeoutExpired:
        pass
    if time.monotonic() >= deadline:
        raise RuntimeError('WebTTY did not become ready; inspect client-error.log')
    time.sleep(0.2)
PY
for transport in tls quic; do
  echo "Checking message-only P-521 signer through CLI WebTTY over ${transport}"
  python3 - "${WORK}" "${PIN}" "${PORT}" "${transport}" <<'PY'
import json, pathlib, sys
root, pin, port, transport = sys.argv[1:]
config = {'version': 1, 'contexts': [{
    'name': 'external', 'engine': '127.0.0.1:' + port, 'projectEndpoint': 'project',
    'auth': {'mtls': {'storage': {'kind': 'exec', 'certificateSHA256': pin, 'exec': {
        'command': root + '/helper',
        'args': ['--cert', root + '/client.crt', '--key', root + '/client.key', '--message-only'],
    }}}},
    'transport': {'mode': transport, 'tls': {'caFile': root + '/engine.crt', 'serverName': 'project.localhost'}},
}]}
pathlib.Path(root + '/client.yaml').write_text(json.dumps(config))
PY
  CONFIG="${WORK}/client.yaml"
  env -i PATH="${PATH}" RSTREAM_DATA_DIR="${WORK}/data" "${WORK}/rstream" \
    --config "${CONFIG}" --context external doctor -o json >"${WORK}/doctor-${transport}.json"
  for name in shell-one shell-two; do
    env -i PATH="${PATH}" RSTREAM_DATA_DIR="${WORK}/data" "${WORK}/rstream" \
      --config "${CONFIG}" --context external --log-level info webtty server \
      --rstream --name "${name}" --no-publish --execution-mode spawn --transport websocket --verbose --retry-interval 100 \
      >"${WORK}/${transport}-${name}.log" 2>&1 &
    SERVER_PIDS+=("$!")
    python3 "${WORK}/exec.py" "${WORK}" "${CONFIG}" "${name}" "${transport}-${name}-ok"
  done
  kill "${ENGINE_PID}"
  wait "${ENGINE_PID}" 2>/dev/null || true
  ENGINE_PID=""
  start_engine
  for name in shell-one shell-two; do
    python3 "${WORK}/exec.py" "${WORK}" "${CONFIG}" "${name}" "${transport}-reconnected"
  done
  for pid in "${SERVER_PIDS[@]}"; do kill "${pid}"; wait "${pid}" 2>/dev/null || true; done
  SERVER_PIDS=()
done
echo "PASS: standalone CLI WebTTY, two private tunnels, TLS/QUIC, reconnection and doctor with external mTLS"
