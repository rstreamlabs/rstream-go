# rstream Go SDK and CLI

The Go SDK is rstream’s reference implementation. Use it to accept connections in your application, dial private services or build an agent that maintains outbound connectivity to the rstream edge. The `rstream` CLI lives in the same repository and provides port forwarding, file sharing and remote terminals using the same configuration.

Go offers the broadest protocol coverage in the rstream SDKs. For native applications built around Boost.Asio, see the [C++ SDK](https://github.com/rstreamlabs/rstream-cpp). The [SDK selection guide](https://rstream.io/sdks) compares the integration paths and capabilities suited to each runtime.

## Connection model

An agent connects outbound to the edge; services behind it need no inbound port. The edge routes incoming connections through the tunnel and enforces the configured access policy.

| Concept | Meaning |
| --- | --- |
| Published tunnel | Provides a forwarding address for ordinary HTTP, TLS or other supported protocol clients. |
| Private tunnel | Is reached by name or ID through an authenticated rstream client. |
| Bytestream | Carries reliable, ordered bytes for HTTP and stream protocols. |
| Datagram | Preserves message boundaries for datagram protocols. |

The [client-to-edge transport](docs/002-transport.md) and the [protocol exposed by a tunnel](docs/003-tunnel-properties.md) are separate choices. Go supports TLS and QUIC transports, with HTTP, TCP, TLS, DTLS, QUIC and SCTP tunnel workflows subject to deployment and project policy.

## Start with the CLI

[Install the CLI](docs/000-installation.md), start a local service on port 8080, then run:

```sh
rstream login
rstream project use my-project-endpoint
rstream forward 8080
```

Replace `my-project-endpoint` with the endpoint of your project. Login opens the browser approval flow. The last command prints the service’s forwarding address and keeps the tunnel open until you stop it. See the [CLI workflow](docs/001-cli-workflow.md) for project-scoped device credentials and certificate authentication.

## Use the Go library

Add the SDK to your Go module:

```sh
go get github.com/rstreamlabs/rstream-go
```

The example below reads the context selected with the CLI. It creates a published HTTP/1.1 tunnel and serves requests directly, without binding a local TCP port.

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

The runnable [HTTP server example](examples/http-h1-server) adds signal handling and graceful shutdown. The [examples](examples) cover private dialing and other protocols. Applications using the filesystem WebRTC transport should follow the [verified ICE build procedure](tools/icepatch/README.md).

## Documentation

| Task | Start here |
| --- | --- |
| Install and configure the CLI | [Installation](docs/000-installation.md) and [CLI workflow](docs/001-cli-workflow.md) |
| Choose connectivity and tunnel options | [Transport configuration](docs/002-transport.md) and [tunnel properties](docs/003-tunnel-properties.md) |
| Share files or operate remote terminals | [File sharing](docs/010-file-sharing.md) and [WebTTY](docs/006-webtty.md) |
| Integrate webhooks or agent tools | [Webhooks](docs/007-webhooks.md) and [MCP](docs/009-mcp.md) |
| Find API, security and troubleshooting guides | [Documentation index](docs/README.md) and [Go API reference](https://pkg.go.dev/github.com/rstreamlabs/rstream-go) |
| Build or contribute | [Contributing](CONTRIBUTING.md) and [coding style](CODING_STYLE.md) |

## Security and support

Report vulnerabilities through the channels in [Security](SECURITY.md). For usage questions and reproducible bugs, use [GitHub issues](https://github.com/rstreamlabs/rstream-go/issues), with credentials and customer details removed. The SDK and CLI are licensed under [Apache-2.0](LICENSE).
