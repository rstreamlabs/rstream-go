# End-to-end tests

This directory contains end-to-end tests for the rstream Go SDK. Each subdirectory pairs a server binary and a client binary. The test runner exercises every supported protocol and transport combination against a live rstream engine instance.

## Coverage

| Suite | Cases | What it covers |
|-------|-------|----------------|
| `websocket` | 9 | All upstream × downstream HTTP version combinations (H1, H2C, H3), including public authority and browser Origin preservation |
| `webtransport` | 2 | Private SDK-dialer and published HTTP/3 reverse-proxy runs covering bidirectional streams, unidirectional streams, datagrams, multi-stream, close codes, and same-origin validation |
| `http` | 3 | HTTP tunnels over H1, H2C, and H3, including GET and SSE streaming |
| `stream` | 7 | Bytestream tunnel: plain (unpublished), TLS via SDK dialer (unpublished), TLS via engine listener (published), TLS passthrough via engine listener, TLS via engine listener with upstream TLS, and ALPN rejection checks |
| `datagram` | 13 | Datagram tunnel: DTLS via SDK dialer (unpublished), DTLS via engine listener (published, with and without upstream DTLS), QUIC via SDK dialer (unpublished), QUIC via engine listener (published), SCTP via pion/sctp over SDK datagrams and published DTLS, and ALPN rejection checks |
| `masque` | 2 | Published HTTP/3 datagram tunnels carrying CONNECT-UDP and CONNECT-IP Extended CONNECT sessions end-to-end, including public authority preservation |
| `connect` | 9 | All upstream × downstream HTTP version combinations for published authority-form CONNECT (H1, H2C, H3) |

The primary `run-e2e.sh` matrix executes 34 cases. Each WebTransport runner case
contains multiple protocol subcases internally. Additional runtime suites cover
MASQUE, the nine-case CONNECT matrix, and the six bandwidth-limit cases described below.

The HTTP upgrade and CONNECT coverage follows the versions each protocol permits:

| Protocol | Downstream | Upstream | Runtime matrix |
|----------|------------|----------|----------------|
| WebSocket | H1, H2, H3 | H1, H2C, H3 | All 9 translations |
| WebTransport | H3 | H3 | Private SDK dialer and published reverse proxy |
| Plain CONNECT | H1, H2, H3 | H1, H2C, H3 | All 9 translations |
| CONNECT-UDP | H3 | H3 | Published reverse proxy |
| CONNECT-IP | H3 | H3 | Published reverse proxy |

WebTransport, CONNECT-UDP, and CONNECT-IP have no H1 or H2 variant in this
matrix because the supported protocols require HTTP/3. Engine unit tests cover
rejection when those requests or their configured upstream tunnel use another
HTTP version.

The stream and datagram suites each cover two connectivity modes:

- **Unpublished**: the SDK internal dialer (`client.Dial` / `client.PacketDial`) is used. The engine relays bytes between the two SDK endpoints without exposing a public socket.
- **Published**: the tunnel is registered on the engine's TLS, DTLS, or QUIC listener with an explicit `Protocol`. The client connects directly to the engine's edge endpoint, bypassing the SDK dialer entirely.

## Running

### Prerequisites

- A running rstream engine accessible via the SDK.
- The `RSTREAM_CONTEXT` environment variable set to the target context name, or `RSTREAM_ENGINE` plus the matching authentication environment.
- All test binaries built (see below).
- A project plan that supports the feature under test. The full `runtime-forward.sh` suite uses private tunnels, custom ALPN, DTLS, published QUIC, CONNECT-UDP, CONNECT-IP, and published TLS/HTTP paths, so run it against a Pro or Enterprise project.

Runtime credential and permission suites also require:

- A running Control plane API passed explicitly with `RSTREAM_RUNTIME_API_URL`.
- A PAT in `RSTREAM_RUNTIME_CONTROL_TOKEN` with `account.projects.read-only`, `account.tokens.create`, and `account.credentials.read-write`. Use an unrestricted admin/dev PAT for the most complete runtime pass. The Control plane setup scripts do not reuse the engine context token implicitly.
- At least one Basic project for token/grant runtime checks. Set `RSTREAM_RUNTIME_BASIC_PROJECT_ENDPOINT` or `RSTREAM_RUNTIME_PROJECT_ENDPOINT` to make the selection explicit.
- One Pro project for published tunnel mTLS checks. Set `RSTREAM_RUNTIME_PRO_PROJECT_ENDPOINT`.
- An engine build with mTLS support enabled when running mTLS suites.

### Build

From the repository root:

```sh
make rstream
make test-bins
```

Equivalent manual commands:

```sh
make rstream
mkdir -p out/test/{websocket,webtransport,http,stream,datagram,masque,connect,bandwidth}
go build -o out/test/websocket/server    ./test/websocket/server
go build -o out/test/websocket/client    ./test/websocket/client
go build -o out/test/webtransport/server ./test/webtransport/server
go build -o out/test/webtransport/client ./test/webtransport/client
go build -o out/test/http/server         ./test/http/server
go build -o out/test/http/client         ./test/http/client
go build -o out/test/stream/server       ./test/stream/server
go build -o out/test/stream/client       ./test/stream/client
go build -o out/test/datagram/server     ./test/datagram/server
go build -o out/test/datagram/client     ./test/datagram/client
go build -o out/test/masque/server       ./test/masque/server
go build -o out/test/masque/client       ./test/masque/client
go build -o out/test/connect/server      ./test/connect/server
go build -o out/test/connect/client      ./test/connect/client
go build -o out/test/bandwidth/server    ./test/bandwidth/server
go build -o out/test/bandwidth/client    ./test/bandwidth/client
```

### Run

The runtime scripts are split into two families:

- Engine-only suites use the selected `RSTREAM_CONTEXT` or explicit `RSTREAM_ENGINE` and do not call the Control plane API.
- Control-plane setup suites require `RSTREAM_RUNTIME_API_URL` and `RSTREAM_RUNTIME_CONTROL_TOKEN` because they create or verify API resources before connecting to the engine.

Set `BIN` to the directory containing the built binaries and execute the protocol matrix runner from the repository root:

```sh
export RSTREAM_CONTEXT=<context>
export BIN=out/test
bash run-e2e.sh
```

The script exits with status 0 if all cases pass, non-zero otherwise.
Before running cases, the script checks that all required binaries are executable and that either `RSTREAM_CONTEXT` or `RSTREAM_ENGINE` is set.
Private datagram cases also assert the selected tunnel packet path: stream framing over TLS transport, QUIC datagrams over QUIC transport, and stream framing when guaranteed delivery is requested. Nested QUIC and WebTransport cases use a 1200-byte initial packet size so their packets fit the QUIC datagram tunnel budget.

The baseline command pins TLS so packet-path assertions remain deterministic. Use `bash run-e2e.sh --quic` for strict QUIC and `bash run-e2e.sh --auto` to verify that automatic selection prefers QUIC on a reachable local engine.

Run the runtime forwarding smoke suite:

```sh
export RSTREAM_CONTEXT=<context>
export BIN=out/test
bash test/e2e/runtime-forward.sh
```

The forwarding suite covers private bytestreams, published TLS, HTTP, DTLS, QUIC, CONNECT-UDP, and CONNECT-IP tunnels. It also validates published HTTP sub-path forwarding over HTTP/2 and HTTP/3, and verifies that reused HTTP/2 and HTTP/3 connections route each request by its current authority.

Run the same runtime forwarding checks over QUIC control-channel transport:

```sh
export RSTREAM_CONTEXT=<context>
export BIN=out/test
bash test/e2e/runtime-forward.sh --quic-transport
```

Use `--auto-transport` for the same matrix with automatic selection. The script pins TLS when neither option is provided.

Run the EE bandwidth-limit matrix against an engine configured with 1 Mbps
upstream and downstream limits for every plan used by the test context:

```sh
export RSTREAM_CONTEXT=<context>
export BIN=out/test
bash test/e2e/runtime-bandwidth-limit.sh
```

The matrix transfers 256 kB in each direction and checks a bounded duration.
It covers bytestream over TLS and QUIC, framed datagrams over TLS, the QUIC
datagram fast path with mixed and all-QUIC legs, and guaranteed datagrams over
QUIC streams. Override `RSTREAM_BANDWIDTH_MIN_DURATION` and
`RSTREAM_BANDWIDTH_MAX_DURATION` when intentionally testing another configured
rate.

To verify direction mapping independently, run the matrix once with only
`upstream_mbps` set to `1`, then once with only `downstream_mbps` set to `1`.
The default duration bounds apply to both passes.

The runtime transport proxy suite exercises the agent-to-engine proxy matrix with live local servers and proxies:

| Engine transport | Proxy path | Runtime path covered |
|------------------|------------|----------------------|
| TLS/TCP | HTTP CONNECT and HTTPS CONNECT | TCP proxy connection, CONNECT response handling, proxy authentication headers, end-to-end TLS to the engine target |
| TLS/TCP | SOCKS5 CONNECT | SOCKS5 negotiation, username/password authentication, and end-to-end TLS to the engine target |
| QUIC | HTTPS MASQUE CONNECT-UDP | HTTP/3 Extended CONNECT, HTTP datagrams, custom DNS for proxy and target, local bind/address-family selection, and end-to-end QUIC to the engine target |
| QUIC | SOCKS5 UDP ASSOCIATE | SOCKS5 control channel, UDP relay framing, local bind/address-family selection, and end-to-end QUIC to the engine target |

Run it with:

```sh
export RSTREAM_CONTEXT=<context>
export RSTREAM_AUTHENTICATION_TOKEN='<pat or auth token valid for the selected project>'
export RSTREAM_RUNTIME_MASQUE_PROXY_CERT_FILE=/path/to/masque-proxy.crt
export RSTREAM_RUNTIME_MASQUE_PROXY_KEY_FILE=/path/to/masque-proxy.key
export BIN=out/test
bash test/e2e/runtime-transport-proxy.sh
```

Run the challenge mode runtime suite:

```sh
export RSTREAM_CONTEXT=<context>
export RSTREAM_RUNTIME_API_URL=<control-plane-api-url>
export BIN=out/test
bash test/e2e/runtime-challenge-mode.sh
```

The challenge suite expects the engine challenge backend to point at the same Control plane API URL. It verifies that the challenge API route exists, then checks the first browser redirect over HTTP/2 and HTTP/3.

Run the token, permission, scope, and Tunnel access runtime suite:

```sh
export RSTREAM_RUNTIME_API_URL=<control-plane-api-url>
export RSTREAM_RUNTIME_CONTROL_TOKEN='<pat with account.projects.read-only account.tokens.create account.credentials.read-write>'
export RSTREAM_RUNTIME_BASIC_PROJECT_ENDPOINT='<basic-project-endpoint>'

bash test/e2e/runtime-token-permissions.sh
```

Run the mTLS runtime suite:

```sh
export RSTREAM_RUNTIME_API_URL=<control-plane-api-url>
export RSTREAM_RUNTIME_CONTROL_TOKEN='<pat with account.projects.read-only account.tokens.create account.credentials.read-write>'
export RSTREAM_RUNTIME_BASIC_PROJECT_ENDPOINT='<basic-project-endpoint>'
export RSTREAM_RUNTIME_PRO_PROJECT_ENDPOINT='<pro-project-endpoint>'

bash test/e2e/runtime-mtls-permissions.sh
```

Run the WebTTY CLI workflow suite:

```sh
bash test/e2e/webtty-cli-workflows.sh
```

This suite validates local WebTTY identities, trust-store files, registered
server config parsing, workspace-managed device config, public help output,
runtime E2E inference, and WebDAV sidecar rejection when E2E is active.

Run the WebTTY runtime matrix:

```sh
bash test/e2e/webtty-runtime-matrix.sh
```

The matrix starts live WebTTY servers and clients and covers direct Go
transports, JavaScript browser WebTransport, C++ client/server interop when the
C++ binaries are available, explicit-key E2E, connection setup, command
execution, and expected failure paths. It resolves companion repositories from
sibling checkouts named `rstream-js` and `rstream-cpp`; override with
`RSTREAM_JS_REPO` or `RSTREAM_CPP_REPO` when testing a different checkout.

The runtime scripts resolve the CLI in this order: `RSTREAM_BIN`, a built repository binary under `out/cmd/rstream`, then `rstream` from `PATH`. Set `RSTREAM_BIN` when you need to test a specific binary.

Run the Windows WebTTY runtime suite from a Windows host or VM:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass `
  -File test\e2e\webtty-windows-runtime.ps1 `
  -Rstream C:\path\to\rstream.exe
```

From another machine, cross-compile the CLI first, copy both the binary and the
script to the Windows host, then run the same command there:

```sh
GOOS=windows GOARCH=arm64 go build -o /tmp/rstream.exe ./cmd/rstream
scp /tmp/rstream.exe test/e2e/webtty-windows-runtime.ps1 winhost:C:/Temp/rstream-webtty/
ssh winhost 'powershell -NoProfile -ExecutionPolicy Bypass -File C:\Temp\rstream-webtty\webtty-windows-runtime.ps1 -Rstream C:\Temp\rstream-webtty\rstream.exe'
```

Use `GOARCH=amd64` for an x64 Windows VM. The Windows runtime suite covers
WebSocket, plain transport, plain-over-TLS, WebTransport, current-user login
mode, explicit-key E2E, unauthorized client rejection, and duplicate-error-log
prevention. The script generates its local test certificate with Go, so it does
not require OpenSSL on the Windows host.

Pass C++ WebTTY binaries when validating Go/C++ interop on Windows:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass `
  -File C:\Temp\rstream-webtty\webtty-windows-runtime.ps1 `
  -Rstream C:\Temp\rstream-webtty\rstream.exe `
  -CppServer C:\Temp\rstream-webtty\rstream-webtty-server.exe `
  -CppClient C:\Temp\rstream-webtty\rstream-webtty-client.exe
```

With those binaries, the Windows suite also covers Go client to C++ server,
C++ client to C++ server, C++ client to Go server, E2E authorized and
unauthorized C++ paths, and known-server client identity resolution.

For the standard local stack, keep the public inputs explicit:

```sh
export RSTREAM_CONTEXT=tests
export RSTREAM_RUNTIME_API_URL=<control-plane-api-url>
export RSTREAM_RUNTIME_PROJECT_ENDPOINT=<project-endpoint>
export RSTREAM_RUNTIME_BASIC_PROJECT_ENDPOINT=<basic-project-endpoint>
export RSTREAM_RUNTIME_PRO_PROJECT_ENDPOINT=<pro-project-endpoint>
export RSTREAM_RUNTIME_CONTROL_TOKEN='<pat with account.projects.read-only account.tokens.create account.credentials.read-write>'
```

If you need to select a specific local CLI binary without hard-coding platform-specific output paths:

```sh
export RSTREAM_BIN="$(find out/cmd/rstream -type f -name rstream | sort | tail -n 1)"
```

For a full local validation pass:

```sh
go test ./...
make rstream
make test-bins

export RSTREAM_CONTEXT=<context>
export BIN=out/test
bash run-e2e.sh
bash test/e2e/runtime-forward.sh
bash test/e2e/runtime-forward.sh --quic-transport

export RSTREAM_RUNTIME_API_URL=<control-plane-api-url>
bash test/e2e/runtime-challenge-mode.sh

export RSTREAM_RUNTIME_CONTROL_TOKEN='<pat with account.projects.read-only account.tokens.create account.credentials.read-write>'
export RSTREAM_RUNTIME_BASIC_PROJECT_ENDPOINT='<basic-project-endpoint>'
export RSTREAM_RUNTIME_PRO_PROJECT_ENDPOINT='<pro-project-endpoint>'
bash test/e2e/runtime-token-permissions.sh
bash test/e2e/runtime-mtls-permissions.sh
```

Runtime suites create temporary auth tokens, temporary mTLS credentials, local upstream servers, and live tunnels. The scripts clean up temporary credentials and child processes on exit.

## Structure

```
test/
├── e2e/
│   ├── runtime-forward.sh             — runtime TLS, HTTP, DTLS, QUIC, plain CONNECT, CONNECT-UDP/IP, sub-path forwarding, and HTTP/2/HTTP/3 connection reuse checks
│   ├── runtime-challenge-mode.sh      — challenge API route and published HTTP challenge redirects over HTTP/2 and HTTP/3
│   ├── runtime-token-permissions.sh   — Control plane API, Engine API, scopes, grants, watch, and published HTTP token checks
│   ├── runtime-mtls-permissions.sh    — mTLS agent auth, mTLS published tunnel auth, and mTLS permission checks
│   └── runtime_harness.py             — local TCP/TLS/HTTP/UDP helpers used by runtime scripts
├── websocket/
│   ├── server/   — WS echo server, supports h1/h2c/h3 upstream
│   └── client/   — WS client, supports h1/h2/h3 downstream
├── webtransport/
│   ├── server/   — WebTransport echo server
│   └── client/   — runs all 10 test cases sequentially
├── http/
│   ├── server/   — HTTP tunnel server (h1/h2c/h3, GET and SSE)
│   └── client/   — HTTP client exercising GET and SSE for each variant
├── stream/
│   ├── server/   — Bytestream echo server (plain / tls, published or not)
│   └── client/   — Bytestream client (plain / tls, direct or via SDK dialer)
├── datagram/
│   ├── server/   — Datagram echo server (dtls / quic / sctp, published or not)
│   └── client/   — Datagram client (dtls / quic / sctp, direct or via SDK dialer)
├── masque/
│   ├── server/   — CONNECT-UDP / CONNECT-IP server behind a published HTTP/3 datagram tunnel
│   └── client/   — MASQUE clients using quic-go/masque-go and quic-go/connect-ip-go
└── connect/
    ├── server/   — HTTP forward proxy CONNECT server behind h1, h2c, or h3 rstream tunnels
    └── client/   — CONNECT client for h1, h2, and h3 downstream sessions
```

## Flags

### stream/server

| Flag | Default | Description |
|------|---------|-------------|
| `--variant` | `plain` | `plain` or `tls` |
| `--publish` | false | Register on engine's TLS listener |
| `--host` | — | Requested Stable domain hostname |
| `--tls-mode` | — | TLS mode for published TLS tunnels (`terminated` or `passthrough`) |
| `--tls-alpn` | — | Custom ALPN for published TLS tunnels |
| `--upstream-tls` | false | Use TLS between the edge and the server |
| `--name` | auto | Tunnel name |

### stream/client

| Flag | Default | Description |
|------|---------|-------------|
| `--variant` | `plain` | `plain` or `tls` |
| `--addr` | — | Engine edge address for direct (published) connections |
| `--tls-alpn` | — | Custom ALPN for TLS connections |
| `--tunnel` | `stream-matrix` | Tunnel name prefix for SDK dialer |

### datagram/server

| Flag | Default | Description |
|------|---------|-------------|
| `--variant` | `dtls` | `dtls`, `quic`, or `sctp` |
| `--publish` | false | Register on engine's DTLS/QUIC listener |
| `--host` | — | Requested Stable domain hostname |
| `--tls-alpn` | — | Custom ALPN for published DTLS, QUIC, or SCTP tunnels |
| `--upstream-tls` | false | Use DTLS between the edge and the server for published DTLS tunnels |
| `--name` | auto | Tunnel name |

### datagram/client

| Flag | Default | Description |
|------|---------|-------------|
| `--variant` | `dtls` | `dtls`, `quic`, or `sctp` |
| `--addr` | — | Engine edge address for direct (published) connections |
| `--tls-alpn` | — | Custom ALPN for published DTLS, QUIC, or SCTP connections |
| `--tunnel` | `datagram-matrix` | Tunnel name prefix for SDK dialer |

### masque/server

| Flag | Default | Description |
|------|---------|-------------|
| `--variant` | `connect-udp` | `connect-udp`, `connect-ip`, or `all` |
| `--name` | `masque-runtime` | Tunnel name |
| `--hostname` | — | Requested Stable domain hostname |

### masque/client

| Flag | Default | Description |
|------|---------|-------------|
| `--variant` | `connect-udp` | `connect-udp` or `connect-ip` |
| `--addr` | — | Published rstream forwarding address |
| `--target` | — | UDP target host:port for `connect-udp` |
| `--timeout` | `20s` | Test timeout |

### connect/server

| Flag | Default | Description |
|------|---------|-------------|
| `--upstream` | `h1` | Upstream proxy protocol: `h1`, `h2c`, or `h3` |
| `--name` | `connect-runtime` | Tunnel name |

### connect/client

| Flag | Default | Description |
|------|---------|-------------|
| `--downstream` | `h1` | Downstream protocol to the published tunnel: `h1`, `h2`, or `h3` |
| `--addr` | — | Published rstream forwarding address |
| `--target` | — | TCP target host:port for the CONNECT tunnel |
