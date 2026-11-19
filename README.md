# rstream Go SDK and CLI

rstream makes services and devices reachable through outbound connections, without opening inbound ports on the machines running them. Applications can expose an endpoint to ordinary clients or connect privately through an authenticated rstream client.

This repository contains the reference Go SDK and the `rstream` CLI. Embed the SDK when the application should own its connections and tunnels; use the CLI to forward an existing service, share files or operate remote terminals. Go provides the broadest protocol coverage in the stack. The [SDK guide](https://rstream.io/docs/sdks) explains the integration choices across languages.

| Goal                                 | Start here                                                                                                                                                         |
| ------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Try rstream with an existing service | [Install the CLI](docs/000-installation.md), then follow the CLI example below.                                                                                    |
| Add connectivity to a Go application | Follow the [Go SDK walkthrough](docs/003-go-sdk.md#serve-a-published-http-route) or run the [HTTP server example](examples/README.md#serve-http). |
| Explore an integration or protocol   | [Example catalog](examples/README.md).                                                                                                                             |
| Build or modify this checkout        | [Build from source](docs/001-building-from-source.md), then [Contributing](CONTRIBUTING.md).                                                                       |

## Connection model

An agent connects outbound to the edge; services behind it need no inbound port. The edge routes incoming connections through the tunnel and enforces the configured access policy.

| Concept          | Meaning                                                                                   |
| ---------------- | ----------------------------------------------------------------------------------------- |
| Published tunnel | Provides a forwarding address for ordinary HTTP, TLS or other supported protocol clients. |
| Private tunnel   | Is reached by name or ID through an authenticated rstream client.                         |
| Bytestream       | Carries reliable, ordered bytes for HTTP and stream protocols.                            |
| Datagram         | Preserves message boundaries for datagram protocols.                                      |

The [client-to-edge transport](docs/020-agent-transport.md) and the [protocol exposed by a tunnel](docs/004-tunnel-configuration.md) are separate choices. Go implements TLS and QUIC agent transports and both bytestream and datagram tunnels. The protocols available for publication depend on the deployment and project policy.

## Start with the CLI

[Install the CLI](docs/000-installation.md), start a local HTTP service on port 8080, then run:

```sh
rstream login
rstream project use my-project-endpoint
rstream forward 8080
```

Replace `my-project-endpoint` with the endpoint of your project. Login opens the browser approval flow. The last command prints the service’s forwarding address and keeps the tunnel open until you stop it. See the [CLI workflow](docs/002-cli-workflow.md) for project-scoped device credentials and certificate authentication.

## Use the Go library

A bytestream tunnel implements `net.Listener`, so a standard Go HTTP server can
serve it directly:

```go
server := &http.Server{Handler: handler}
return server.Serve(tunnel.(rstream.BytestreamTunnel))
```

Follow [the Go SDK walkthrough](docs/003-go-sdk.md#serve-a-published-http-route)
to install the module, create the tunnel and run the complete program. The
[example catalog](examples/README.md) has paired programs for private
connections and other protocols.

## Documentation

| Task                                          | Start here                                                                                                            |
| --------------------------------------------- | --------------------------------------------------------------------------------------------------------------------- |
| Install and configure the CLI                 | [Installation](docs/000-installation.md) and [CLI workflow](docs/002-cli-workflow.md)                                 |
| Choose a tunnel or service protocol        | [Tunnel configuration](docs/004-tunnel-configuration.md), [HTTP](docs/005-http-tunnels.md) and [TLS/DTLS](docs/006-tls-dtls-tunnels.md)         |
| Share files or operate remote terminals       | [File sharing](docs/008-file-sharing.md) and [WebTTY](docs/009-webtty.md)                                             |
| Integrate webhooks or agent tools             | [Webhooks](docs/010-webhooks.md) and [MCP](docs/011-mcp.md)                                                           |
| Find API, security and troubleshooting guides | [Documentation index](docs/README.md) and [Go API reference](https://pkg.go.dev/github.com/rstreamlabs/rstream-go)    |
| Build or contribute                           | [Source build](docs/001-building-from-source.md), [Contributing](CONTRIBUTING.md) and [coding style](CODING_STYLE.md) |

## Security and support

Report vulnerabilities through the channels in [Security](SECURITY.md). For usage questions and reproducible bugs, use [GitHub issues](https://github.com/rstreamlabs/rstream-go/issues), with credentials and customer details removed. The SDK and CLI are licensed under [Apache-2.0](LICENSE).
