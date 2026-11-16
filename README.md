# rstream Go SDK and CLI

rstream makes services and devices reachable through outbound connections, without opening inbound ports on the machines running them. Applications can expose an endpoint to ordinary clients or connect privately through an authenticated rstream client.

This repository contains the reference Go SDK and the `rstream` CLI. Embed the SDK when the application should own its connections and tunnels; use the CLI to forward an existing service, share files or operate remote terminals. Go provides the broadest protocol coverage in the stack. The [SDK guide](https://rstream.io/docs/sdks) explains the integration choices across languages.

| Goal                                 | Start here                                                                                                                                                         |
| ------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Try rstream with an existing service | [Install the CLI](docs/000-installation.md), then follow the CLI example below.                                                                                    |
| Add connectivity to a Go application | [Use the Go library](#use-the-go-library), follow the [SDK integration path](docs/003-go-sdk.md), or run the [HTTP server example](examples/README.md#serve-http). |
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

The [client-to-edge transport](docs/004-transport.md) and the [protocol exposed by a tunnel](docs/005-tunnel-configuration.md) are separate choices. Go implements TLS and QUIC agent transports and both bytestream and datagram tunnels. The protocols available for publication depend on the deployment and project policy.

## Start with the CLI

[Install the CLI](docs/000-installation.md), start a local HTTP service on port 8080, then run:

```sh
rstream login
rstream project use my-project-endpoint
rstream forward 8080
```

Replace `my-project-endpoint` with the endpoint of your project. Login opens the browser approval flow. The last command prints the service’s forwarding address and keeps the tunnel open until you stop it. See the [CLI workflow](docs/002-cli-workflow.md) for project-scoped device credentials and certificate authentication.

## Use the Go library

Use Go 1.27.4 for this walkthrough. Create a small module and add the SDK:

```sh
mkdir rstream-hello
cd rstream-hello
go mod init example.com/rstream-hello
go get github.com/rstreamlabs/rstream-go
```

[Install the CLI](docs/000-installation.md) and select a project with `rstream login` and `rstream project use my-project-endpoint`. The program reads that context, creates a published HTTP/1.1 tunnel and serves requests directly, without binding a local TCP port.

```go
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func main() {
	if err := serve(); err != nil {
		log.Fatal(err)
	}
}

func serve() error {
	client, err := config.NewClientFromEnv()
	if err != nil {
		return err
	}
	defer client.Close()
	control, err := client.Connect(context.Background(), nil)
	if err != nil {
		return err
	}
	defer control.Close()
	tunnel, err := control.CreateTunnel(context.Background(), rstream.TunnelProperties{
		Protocol:    rstream.ProtocolPtr(rstream.ProtocolHTTP),
		HTTPVersion: rstream.HTTPVersionPtr(rstream.HTTP1_1),
		Publish:     rstream.BoolPtr(true),
	})
	if err != nil {
		return err
	}
	defer tunnel.Close()
	address, err := tunnel.ForwardingAddress()
	if err != nil {
		return err
	}
	fmt.Println(address)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "Hello from rstream")
	})}
	return server.Serve(tunnel.(rstream.BytestreamTunnel))
}
```

Save the program as `main.go` in that module. Resolve its imports, including the configuration package, then run it:

```sh
go mod tidy
go run .
```

Open the printed forwarding address to see `Hello from rstream`; stop the process with an interrupt. Select a project context first, as in the CLI setup above.

The runnable [HTTP server example](examples/README.md#serve-http) adds signal handling and graceful shutdown. The [example catalog](examples/README.md) explains which programs to pair for private connections, other protocols and API integrations.

## Documentation

| Task                                          | Start here                                                                                                            |
| --------------------------------------------- | --------------------------------------------------------------------------------------------------------------------- |
| Install and configure the CLI                 | [Installation](docs/000-installation.md) and [CLI workflow](docs/002-cli-workflow.md)                                 |
| Choose connectivity and tunnel options        | [Transport configuration](docs/004-transport.md) and [tunnel configuration](docs/005-tunnel-configuration.md)         |
| Share files or operate remote terminals       | [File sharing](docs/007-file-sharing.md) and [WebTTY](docs/009-webtty.md)                                             |
| Integrate webhooks or agent tools             | [Webhooks](docs/010-webhooks.md) and [MCP](docs/011-mcp.md)                                                           |
| Find API, security and troubleshooting guides | [Documentation index](docs/README.md) and [Go API reference](https://pkg.go.dev/github.com/rstreamlabs/rstream-go)    |
| Build or contribute                           | [Source build](docs/001-building-from-source.md), [Contributing](CONTRIBUTING.md) and [coding style](CODING_STYLE.md) |

## Security and support

Report vulnerabilities through the channels in [Security](SECURITY.md). For usage questions and reproducible bugs, use [GitHub issues](https://github.com/rstreamlabs/rstream-go/issues), with credentials and customer details removed. The SDK and CLI are licensed under [Apache-2.0](LICENSE).
