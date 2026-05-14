# End-to-end tests

This directory contains end-to-end tests for the rstream Go SDK. Each subdirectory pairs a server binary and a client binary. The test runner exercises every supported protocol and transport combination against a live rstream engine instance.

## Coverage

| Suite | Cases | What it covers |
|-------|-------|----------------|
| `websocket` | 9 | All upstream × downstream HTTP version combinations (H1, H2C, H3) |
| `webtransport` | 1 | Aggregated WebTransport client run covering bidirectional streams, unidirectional streams, datagrams, multi-stream, and close codes |
| `http` | 3 | HTTP tunnels over H1, H2C, and H3, including GET and SSE streaming |
| `stream` | 7 | Bytestream tunnel: plain (unpublished), TLS via SDK dialer (unpublished), TLS via engine listener (published), TLS passthrough via engine listener, TLS via engine listener with upstream TLS, and ALPN rejection checks |
| `datagram` | 12 | Datagram tunnel: DTLS via SDK dialer (unpublished), DTLS via engine listener (published, with and without upstream DTLS), QUIC via SDK dialer (unpublished), QUIC via engine listener (published), SCTP via pion/sctp over SDK datagrams and published DTLS, and ALPN rejection checks |

**Total: 32 runner cases.** The WebTransport runner case contains multiple protocol subcases internally.

The stream and datagram suites each cover two connectivity modes:

- **Unpublished**: the SDK internal dialer (`client.Dial` / `client.PacketDial`) is used. The engine relays bytes between the two SDK endpoints without exposing a public socket.
- **Published**: the tunnel is registered on the engine's TLS, DTLS, or QUIC listener with an explicit `Protocol`. The client connects directly to the engine's edge endpoint, bypassing the SDK dialer entirely.

## Running

### Prerequisites

- A running rstream engine accessible via the SDK.
- The `RSTREAM_CONTEXT` environment variable set to the target context name, or `RSTREAM_ENGINE` plus the matching authentication environment.
- All test binaries built (see below).
- A project plan that supports the feature under test. The full `runtime-forward.sh` suite uses private tunnels, custom ALPN, DTLS, published QUIC, and published TLS/HTTP paths, so run it against a Pro or Enterprise project.

Runtime credential and permission suites also require:

- A running Control plane API. The default is `http://localhost:3000`; override with `RSTREAM_RUNTIME_API_URL`.
- A PAT in `RSTREAM_RUNTIME_CONTROL_TOKEN` with `account.projects.read-only`, `account.tokens.create`, and `account.credentials.read-write`. Use an unrestricted admin/dev PAT for the most complete runtime pass.
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
mkdir -p out/test/{websocket,webtransport,http,stream,datagram}
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
```

### Run

Set `BIN` to the directory containing the built binaries and execute the protocol matrix runner from the repository root:

```sh
export RSTREAM_CONTEXT=<context>
export BIN=out/test
bash run-e2e.sh
```

The script exits with status 0 if all cases pass, non-zero otherwise.
Before running cases, the script checks that all required binaries are executable and that either `RSTREAM_CONTEXT` or `RSTREAM_ENGINE` is set.

Run the runtime forwarding smoke suite:

```sh
export RSTREAM_CONTEXT=<context>
export RSTREAM_AUTHENTICATION_TOKEN='<pat or auth token valid for the selected project>'
export BIN=out/test
bash test/e2e/runtime-forward.sh
```

The forwarding suite covers private bytestreams, published TLS, HTTP, DTLS, and QUIC tunnels. It also validates published HTTP sub-path forwarding over HTTP/2 and HTTP/3, and verifies that reused HTTP/2 and HTTP/3 connections route each request by its current authority.

Run the same runtime forwarding checks over QUIC control-channel transport:

```sh
export RSTREAM_CONTEXT=<context>
export RSTREAM_AUTHENTICATION_TOKEN='<pat or auth token valid for the selected project>'
export BIN=out/test
bash test/e2e/runtime-forward.sh --quic-transport
```

Run the challenge mode runtime suite:

```sh
export RSTREAM_CONTEXT=<context>
export RSTREAM_RUNTIME_API_URL=http://localhost:3000
export BIN=out/test
bash test/e2e/runtime-challenge-mode.sh
```

The challenge suite expects the engine challenge backend to point at the same Control plane API URL. It verifies that the challenge API route exists, then checks the first browser redirect over HTTP/2 and HTTP/3.

Run the token, permission, scope, and Tunnel access runtime suite:

```sh
export RSTREAM_RUNTIME_API_URL=http://localhost:3000
export RSTREAM_RUNTIME_CONTROL_TOKEN='<pat with account.projects.read-only account.tokens.create account.credentials.read-write>'
export RSTREAM_RUNTIME_BASIC_PROJECT_ENDPOINT='<basic-project-endpoint>'

bash test/e2e/runtime-token-permissions.sh
```

Run the mTLS runtime suite:

```sh
export RSTREAM_RUNTIME_API_URL=http://localhost:3000
export RSTREAM_RUNTIME_CONTROL_TOKEN='<pat with account.projects.read-only account.tokens.create account.credentials.read-write>'
export RSTREAM_RUNTIME_BASIC_PROJECT_ENDPOINT='<basic-project-endpoint>'
export RSTREAM_RUNTIME_PRO_PROJECT_ENDPOINT='<pro-project-endpoint>'

bash test/e2e/runtime-mtls-permissions.sh
```

The runtime scripts resolve the CLI in this order: `RSTREAM_BIN`, a built repository binary under `out/cmd/rstream`, then `rstream` from `PATH`. Set `RSTREAM_BIN` when you need to test a specific binary.

For the standard local stack, keep the public inputs explicit:

```sh
export RSTREAM_CONTEXT=tests
export RSTREAM_RUNTIME_API_URL=http://localhost:3000
export RSTREAM_RUNTIME_PROJECT_ENDPOINT=<project-endpoint>
export RSTREAM_RUNTIME_BASIC_PROJECT_ENDPOINT=<basic-project-endpoint>
export RSTREAM_RUNTIME_PRO_PROJECT_ENDPOINT=<pro-project-endpoint>
export RSTREAM_AUTHENTICATION_TOKEN='<pat or auth token valid for RSTREAM_CONTEXT>'
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
export RSTREAM_AUTHENTICATION_TOKEN='<pat or auth token valid for the selected project>'
export RSTREAM_RUNTIME_API_URL=http://localhost:3000
export BIN=out/test
bash run-e2e.sh
bash test/e2e/runtime-forward.sh
bash test/e2e/runtime-forward.sh --quic-transport
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
│   ├── runtime-forward.sh             — runtime TLS, HTTP, DTLS, QUIC, sub-path forwarding, and HTTP/2/HTTP/3 connection reuse checks
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
└── datagram/
    ├── server/   — Datagram echo server (dtls / quic / sctp, published or not)
    └── client/   — Datagram client (dtls / quic / sctp, direct or via SDK dialer)
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
