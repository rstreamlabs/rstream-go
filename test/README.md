# End-to-end tests

This directory contains end-to-end tests for the rstream Go SDK. Each subdirectory pairs a server binary and a client binary. The test runner exercises every supported protocol and transport combination against a live rstream engine instance.

## Coverage

| Suite | Cases | What it covers |
|-------|-------|----------------|
| `websocket` | 9 | All upstream × downstream HTTP version combinations (H1, H2C, H3) |
| `webtransport` | 10 | Bidirectional streams, unidirectional streams, datagrams, multi-stream, close codes |
| `http` | 3 | HTTP tunnels over H1, H2C, and H3 |
| `stream` | 6 | Bytestream tunnel: plain (unpublished), TLS via SDK dialer (unpublished), TLS via engine listener (published), TLS via engine listener with upstream TLS, and ALPN rejection checks |
| `datagram` | 8 | Datagram tunnel: DTLS via SDK dialer (unpublished), DTLS via engine listener (published, with and without upstream DTLS), QUIC via SDK dialer (unpublished), QUIC via engine listener (published), and ALPN rejection checks |

**Total: 36 test cases.**

The stream and datagram suites each cover two connectivity modes:

- **Unpublished**: the SDK internal dialer (`client.Dial` / `client.PacketDial`) is used. The engine relays bytes between the two SDK endpoints without exposing a public socket.
- **Published**: the tunnel is registered on the engine's TLS, DTLS, or QUIC listener with an explicit `Protocol`. The client connects directly to the engine's edge endpoint, bypassing the SDK dialer entirely.

## Running

### Prerequisites

- A running rstream engine accessible via the SDK.
- The `RSTREAM_CONTEXT` environment variable set to the target context name.
- All test binaries built (see below).

### Build

From the repository root:

```sh
mkdir -p out/test/{websocket,webtransport,http,stream,datagram}
go build -o out/test/websocket/server  ./test/websocket/server
go build -o out/test/websocket/client  ./test/websocket/client
go build -o out/test/webtransport/server ./test/webtransport/server
go build -o out/test/webtransport/client ./test/webtransport/client
go build -o out/test/http/server       ./test/http/server
go build -o out/test/http/client       ./test/http/client
go build -o out/test/stream/server     ./test/stream/server
go build -o out/test/stream/client     ./test/stream/client
go build -o out/test/datagram/server   ./test/datagram/server
go build -o out/test/datagram/client   ./test/datagram/client
```

### Run

Set `BIN` to the directory containing the built binaries and execute the runner script:

```sh
export RSTREAM_CONTEXT=<context>
export BIN=out/test
bash run-e2e.sh
```

The script exits with status 0 if all cases pass, non-zero otherwise.

## Structure

```
test/
├── websocket/
│   ├── server/   — WS echo server, supports h1/h2c/h3 upstream
│   └── client/   — WS client, supports h1/h2/h3 downstream
├── webtransport/
│   ├── server/   — WebTransport echo server
│   └── client/   — runs all 10 test cases sequentially
├── http/
│   ├── server/   — HTTP tunnel server (h1/h2c/h3)
│   └── client/   — HTTP client exercising each variant
├── stream/
│   ├── server/   — Bytestream echo server (plain / tls, published or not)
│   └── client/   — Bytestream client (plain / tls, direct or via SDK dialer)
└── datagram/
    ├── server/   — Datagram echo server (dtls / quic, published or not)
    └── client/   — Datagram client (dtls / quic, direct or via SDK dialer)
```

## Flags

### stream/server

| Flag | Default | Description |
|------|---------|-------------|
| `--variant` | `plain` | `plain` or `tls` |
| `--publish` | false | Register on engine's TLS listener |
| `--host` | — | Requested Stable domain hostname |
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
| `--variant` | `dtls` | `dtls` or `quic` |
| `--publish` | false | Register on engine's DTLS/QUIC listener |
| `--host` | — | Requested Stable domain hostname |
| `--tls-alpn` | — | Custom ALPN for published DTLS or QUIC tunnels |
| `--upstream-tls` | false | Use DTLS between the edge and the server for published DTLS tunnels |
| `--name` | auto | Tunnel name |

### datagram/client

| Flag | Default | Description |
|------|---------|-------------|
| `--variant` | `dtls` | `dtls` or `quic` |
| `--addr` | — | Engine edge address for direct (published) connections |
| `--tls-alpn` | — | Custom ALPN for published DTLS or QUIC connections |
| `--tunnel` | `datagram-matrix` | Tunnel name prefix for SDK dialer |
