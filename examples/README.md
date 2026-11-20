# Go SDK examples

These programs show how a Go application accepts connections through rstream, dials a private service or calls an API. They use the same credentials and endpoint configuration as the CLI. Start with HTTP to see the connection model; the other examples explore particular protocols and integrations.

For complete applications with framework setup and their own lifecycle, see [rstream application examples](https://github.com/rstreamlabs/rstream-examples).

## Prepare the checkout

Install the tools in [Build from source](../docs/001-building-from-source.md).
Run the build for the HTTP server and client from the repository root on Linux or macOS.

```sh
make http-h1-server http-h1-client
```

The binaries are written to `out/examples/`. `make examples` builds the
complete catalog. On Windows, [prepare the source checkout](../docs/001-building-from-source.md#build-on-windows),
then build the pair in PowerShell.

```powershell
go build -modfile=out/ice-patch/go.mod -o out/examples/http-h1-server.exe ./examples/http-h1-server
go build -modfile=out/ice-patch/go.mod -o out/examples/http-h1-client.exe ./examples/http-h1-client
```

Use `.\out\examples\http-h1-server.exe` and
`.\out\examples\http-h1-client.exe` for the commands below.

The examples use an existing [CLI context](../docs/002-cli-workflow.md#connect-the-cli)
through `config.NewClientFromEnv()`. On hosted rstream, choose a project with
the protocol and access mode you intend to try. Community Edition uses a
context for the direct Engine hostname and its locally signed token.

## Serve HTTP

[Build the pair and select a context](#prepare-the-checkout) first. The example
uses a project that permits an unauthenticated caller. When the project
requires caller authentication, configure the
[Go access properties](../docs/005-http-tunnels.md#http-authentication)
before running the server and verify with the corresponding
[request credentials](https://rstream.io/docs/tunnels/http-tunnels/authentication#token-authentication).
In the first terminal, publish the example server:

```sh
./out/examples/http-h1-server --publish
```

It prints `Server listening on` followed by the forwarding address. Open that address in a browser or request it with `curl`; the response body is the server's hostname. The Go handler receives connections directly through the tunnel, without a local TCP listening port. Stop the server with an interrupt.

## Connect privately

[Build the pair and select a context](#prepare-the-checkout) first. Private bytestreams are available on hosted Pro and Enterprise or through
a direct Community Edition Engine. Both processes need the same project or
Engine. On managed projects, grant tunnel creation to the server and private
dialing to the client.

Run the server without publication in the first terminal:

```sh
./out/examples/http-h1-server
```

In a second terminal using the same project context, run:

```sh
./out/examples/http-h1-client
```

The client dials the tunnel named `h1-example` through rstream and prints the HTTP status and hostname. This path needs no public service address. After the client exits, stop the server with Ctrl+C.

See [editions and plans](https://rstream.io/docs/support/editions) before running a protocol that requires an additional listener or project entitlement.

## Choose another example

Build a selected program with `make <directory-name>`, then use its `-h` output for the flags it accepts. A server/client pair normally runs in two terminals with the server started first.

| Topic | Programs | What it demonstrates |
| --- | --- | --- |
| HTTP/1.1 | [Server](http-h1-server) and [client](http-h1-client) | Published HTTP or a private request through the Go dialer. |
| HTTP/2 | [Server](http-h2c-server) and [client](http-h2c-client) | An h2c application carried over a tunnel. |
| Application TLS | [Server](http-tls-server) and [client](http-tls-client) | HTTPS over accepted bytestreams, with a test certificate in private mode. Published mode needs the service certificate and key. |
| HTTP/3 | [Server](http-h3-server) and [client](http-h3-client) | An HTTP/3 application with a demonstration certificate over datagram connectivity. |
| Server-sent events | [Server](http-sse-server) and [client](http-sse-client) | A continuing HTTP event response. |
| WebSocket | [HTTP/1.1 server](http-ws-h1-server), [h2c server](http-ws-h2c-server), [HTTP/3 server](http-ws-h3-server) and [client](http-ws-h1-client) | WebSocket traffic over the different HTTP paths; the client selects a server with `--tunnel`. |
| WebTransport | [Server](http-wt-server) and [client](http-wt-client) | Bidirectional stream exchange over WebTransport. |
| Raw bytestream | [Server](stream-echo-server) and [client](stream-echo-client) | Private echo connections or a published TLS endpoint. |
| Datagrams and DTLS | [Server](datagram-echo-server) and [client](datagram-echo-client) | Message-preserving echo traffic, including the published DTLS path. |
| QUIC | [Server](quic-echo-server) and [client](quic-echo-client) | QUIC streams and datagrams. |
| SCTP | [Server](sctp-echo-server) and [client](sctp-echo-client) | SCTP streams over datagram tunnels or a published DTLS endpoint. |
| SSH | [Server](tcp-ssh-server) and [client](tcp-ssh-client) | Published TCP with SSH password authentication and explicit host-key verification. |
| MASQUE | [Server](masque-server) and [client](masque-client) | CONNECT-UDP and CONNECT-IP in a private test tunnel. |
| Engine API | [List tunnels](api-list-tunnels) | Engine inventory using a token or enrolled mTLS identity. |
| TURN credentials | [Credential helper](turn-credentials) | Request short-lived relay credentials from the active context. Keep the JSON response private. |
| CLI orchestration | [YAML](run-yaml) and [Docker labels](run-docker) | Declarative forwarding with the CLI rather than an embedded SDK. |

The [external mTLS signer guide](../docs/016-external-mtls-signer.md) explains the [signing helper example](mtls-exec-helper).

## Protocol-specific setup

The internal HTTPS and HTTP/3 demo servers use self-signed application certificates. Their clients have an explicit `--insecure-demo-tls` option for that isolated demonstration. It changes application-server verification, not the agent's engine authentication; published endpoints continue to use their normal edge certificate. Keep this opt-in confined to the demo.

For SSH, set `RSTREAM_SSH_PASSWORD` in both processes. Start the server, then pass its printed address and fingerprint to the client:

```sh
./out/examples/tcp-ssh-server
```

```sh
./out/examples/tcp-ssh-client -address '<hostname>:<port>' -fingerprint 'SHA256:<fingerprint>'
```

SSH supplies encryption and peer authentication for this TCP example. Raw published TCP by itself does not add those properties.

For MASQUE, follow the [CONNECT-UDP and CONNECT-IP examples](../docs/014-extended-connect.md#samples).
The page shows the matching programs and the required echo target.
