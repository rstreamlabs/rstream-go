# rstream Go SDK and CLI

rstream connects services and devices through outbound tunnels. Publish an endpoint for ordinary clients or let authenticated rstream clients dial a private service by name.

This repository contains the reference Go SDK and the `rstream` CLI. The CLI forwards an existing service. The SDK lets an application own its tunnel and use standard Go listeners, connections and packet connections.

| What you want to do | Start here |
| --- | --- |
| Forward an existing service | [Install the CLI](docs/000-installation.md) and [configure your first HTTP tunnel](docs/002-cli-workflow.md#connect-the-cli). |
| Integrate with a Go application | [Run an HTTP handler on a tunnel](docs/003-go-sdk.md) or [connect privately](docs/003-go-sdk.md#run-a-private-client-and-server). |
| Build or contribute to this checkout | [Build from source](docs/001-building-from-source.md), then see [Contributing](CONTRIBUTING.md). |
| Evaluate a hosted or self-hosted deployment | [Editions and plans](https://rstream.io/docs/support/editions) and the [security model](https://rstream.io/docs/security/authentication). |

## Start with the CLI

With a [CLI connection configured](docs/002-cli-workflow.md#connect-the-cli) and
an HTTP service on local port 8080, the following command publishes an
Internet-reachable endpoint. For a restricted service choose
[HTTP authentication](docs/005-http-tunnels.md#http-authentication) or
[private access](docs/004-tunnel-configuration.md#try-a-private-connection)
before forwarding.

```sh
rstream forward 127.0.0.1:8080 --http
```

Open the printed address to reach the service. Stop forwarding with Ctrl+C.
Use the [CLI walkthrough](docs/002-cli-workflow.md#publish-a-first-http-service)
to try this with a disposable service and understand its access policy.

## Use the Go library

A bytestream tunnel implements `net.Listener`. Pass it to a standard Go HTTP
server to accept requests inside your application:

```go
func serveHTTP(listener rstream.BytestreamTunnel, handler http.Handler) error {
	return (&http.Server{Handler: handler}).Serve(listener)
}
```

The [Go walkthrough](docs/003-go-sdk.md) creates the listener and runs a complete
application. The [example catalog](examples/README.md) includes matching
clients and servers for additional protocols.

## Documentation

Use the [documentation index](docs/README.md) to choose a tunnel protocol or
find guides for files, terminals, integrations and configuration. The
[Go API reference](https://pkg.go.dev/github.com/rstreamlabs/rstream-go)
documents exported types and methods. See
[Troubleshooting](https://rstream.io/docs/support/troubleshooting) for
connection and routing checks.

## Security and support

Report vulnerabilities through the channels in [Security](SECURITY.md).
Use [GitHub issues](https://github.com/rstreamlabs/rstream-go/issues) for
reproducible bugs and usage questions, with credentials and customer details
removed. The SDK and CLI are licensed under [Apache-2.0](LICENSE).
