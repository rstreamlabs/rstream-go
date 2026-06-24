# Examples

The examples use the same config/env resolution as the CLI via `config.NewClientFromEnv()`. That keeps the sample code short and consistent with `rstream` usage.

## Prerequisites

Set a default context or `RSTREAM_ENGINE`. If your engine requires token authentication, set `RSTREAM_AUTHENTICATION_TOKEN`. If the example opens an agent control-channel connection and it requires mTLS agent authentication, set `RSTREAM_MTLS_CERT_FILE` and `RSTREAM_MTLS_KEY_FILE` instead. Examples that call the Engine HTTP API require token authentication.

Typical setup (CLI):

```bash
rstream login <token>
rstream project use <project-endpoint>
```

## Run

Build all examples:

```bash
make examples
```

Run an example:

```bash
./out/examples/http-h1-server --publish
```

Useful protocol-focused examples:

- `http-sse-server` / `http-sse-client`: Server-Sent Events over an HTTP tunnel.
- `sctp-echo-server` / `sctp-echo-client`: SCTP streams with `pion/sctp` over datagram tunnels, and over the published DTLS edge.
- `masque-server` / `masque-client`: CONNECT-UDP and CONNECT-IP over HTTP/3 datagram tunnels, using internal rstream dialing by default and published HTTP/3 endpoints with `--publish`.

Run the MASQUE examples in private mode:

```bash
go run ./examples/masque-server --variant connect-udp
go run ./examples/masque-client --variant connect-udp --target 127.0.0.1:9000
```

Run the same shape through a published HTTP/3 endpoint:

```bash
go run ./examples/masque-server --variant connect-udp --publish
go run ./examples/masque-client --variant connect-udp --publish --target 127.0.0.1:9000
```

Generate managed TURN credentials from the active config context:

```bash
go run ./examples/turn-credentials
```

Or override the current context with environment variables:

```bash
RSTREAM_API_URL=http://localhost:3000 \
RSTREAM_AUTHENTICATION_TOKEN="$RSTREAM_AUTHENTICATION_TOKEN" \
RSTREAM_PROJECT_ENDPOINT=9bfdaa8b \
go run ./examples/turn-credentials
```

When TURN routing fields are not already present in the local context, the SDK falls back to the Control plane API.

## CLI Run Examples

- `examples/run-yaml`: YAML-based `rstream -v run --apply` example with a whoami container.
- `examples/run-docker`: Docker-label-based `rstream -v run --docker` example with a whoami container.
