# Embed the Go SDK

Use Go when your application should own the Engine connection, accept or dial streams, or compose datagram protocols. This is rstream's reference SDK, with broad protocol coverage and standard Go network interfaces.

Use a released Go module for an application. To modify the SDK or run its
examples from a checkout, follow [Build from source](001-building-from-source.md).
You can also [forward an existing process](002-cli-workflow.md) with the CLI.

## Serve a published HTTP route

Use the Go toolchain required by the SDK release. Create a small module and add the SDK:

```sh
mkdir rstream-hello
cd rstream-hello
go mod init example.com/rstream-hello
go get github.com/rstreamlabs/rstream-go
```

[Install the CLI](000-installation.md) and select a project with `rstream login` and `rstream project use my-project-endpoint`. The program reads that context, creates a published HTTP/1.1 tunnel and serves requests directly, without binding a local TCP port.

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

Open the printed forwarding address to see `Hello from rstream`; stop the process with an interrupt.

The runnable [HTTP server example](../examples/README.md#serve-http) adds signal handling and graceful shutdown. The [example catalog](../examples/README.md) explains which programs to pair for private connections, other protocols and API integrations.

## Run a private client and server

The CLI context supplies Engine credentials to both programs. Build and run the [private HTTP example pair](../examples/README.md#connect-privately) to see the server accept connections on a tunnel and the client use rstream as its HTTP dialer. Both programs must use the same project. To serve a public route instead, use the [published walkthrough](#serve-a-published-http-route) in a new module.

## Network interfaces and ownership

| Object/API                                       | Go integration                                                        | Owner closes                                                                                             |
| ------------------------------------------------ | --------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------- |
| `Client`                                         | Shared authenticated runtime and Engine HTTP APIs.                    | Close after its controls and application operations stop; this releases owned transports and HTTP pools. |
| `Client.Connect`              | Returns a control channel that creates tunnels and receives their connections.                         | Close the channel after tunnels/accept loops stop.                                                       |
| `BytestreamTunnel`                               | `net.Listener`; accepted streams implement `net.Conn`.                | Close accepted connections, tunnel and channel. `http.Server.Serve` accepts the tunnel directly.         |
| `Client.Dial`                                    | `net.Conn` for a private tunnel selected by name/ID.                  | Close each connection, including on request cancellation.                                                |
| `DatagramTunnel`, `PacketListener`, `PacketDial` | Packet sessions and `net.PacketConn` adapters for protocol libraries. | Close sessions/listeners and stop owned workers before their client.                                     |

Pass contexts/deadlines for bounded setup and requests. A context used to establish a tunnel is not a complete application shutdown policy. Stop the HTTP server or connection workers before releasing the tunnel. SDK objects do not supervise a service's reconnection loop for it; recreate failed control/tunnel generations under your own lifecycle. CLI `forward`, `run` and WebTTY provide their documented supervision when you choose those commands.

## Choose the next capability

| Requirement                                                             | Reference or sample                                                                                                 |
| ----------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| TLS/QUIC client transport, proxy, private CA, DNS and interface binding | [Transport configuration](020-agent-transport.md).                                                                        |
| HTTP/1.1, h2c, HTTP/3, published TCP/TLS/DTLS/QUIC or private access    | [Tunnel configuration](004-tunnel-configuration.md), [example catalog](../examples/README.md).                      |
| WebSocket or WebTransport, CONNECT-UDP/IP                               | [Extended CONNECT](014-extended-connect.md); application protocol code stays in its library.                        |
| Inventory/watch/session APIs with token or certificate                  | [Engine API mTLS/TURN](015-engine-mtls-turn.md). Control plane administration still needs its own authorized token. |
| Restricted cryptographic distribution                                   | [FIPS profile](017-fips-140-3-profile.md); its exclusions are intentional.                                          |

Coverage still depends on Engine version, edition, listeners, project permissions and plan. Native unreliable datagrams preserve message boundaries and can be lost. Applications own their protocol authorization and recovery.
