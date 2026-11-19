# Embed the Go SDK

Use Go when your application should own the Engine connection, accept or dial streams, or compose datagram protocols. This is rstream's reference SDK, with broad protocol coverage and standard Go network interfaces.

Use a released module to add rstream to an application. The SDK opens the
connection in your process and delivers traffic over standard Go interfaces.
Use the CLI to [forward a service that already has a listener](002-cli-workflow.md#publish-a-first-http-service).

## Prepare a connection

| Where you connect | Before running Go code |
| --- | --- |
| Hosted rstream | [Install the CLI](000-installation.md) and use the commands below to select a project. Basic can publish HTTP. Choose Pro or Enterprise to create a private tunnel. |
| Self-hosted Community Edition | [Configure a CLI context for the Engine](https://rstream.io/docs/tunnels/self-hosted/deployment#agent-context) using its hostname, CA if needed, and JWT. CE supports published HTTP and private bytestreams. |
| An existing deployment | Select the context provided by your operator. See [editions and plans](https://rstream.io/docs/support/editions) for listener and policy requirements. |

For a hosted project, sign in and select the value in the `ENDPOINT` column.

```sh
rstream login
rstream project list
rstream project use 'my-project-endpoint'
```

Replace the example endpoint with that value. If you need a project, follow
[hosted project setup](https://rstream.io/docs/getting-started/quickstart-guide#select-a-project).
The program below reads the saved context through `config.NewClientFromEnv()`.

## Serve a published HTTP route

The published SDK declares its Go requirement in its module metadata. For the
source checkout, see [`go.mod`](../go.mod). Initialize the module from an
empty directory.

```sh
go mod init example.com/rstream-hello
go get github.com/rstreamlabs/rstream-go
```

The program publishes a disposable HTTP response. It uses the project or
Engine you selected in [Prepare a connection](#prepare-a-connection) and
serves the handler directly on the tunnel.
Set up [HTTP access control](005-http-tunnels.md#http-authentication) before
replacing the example handler with a service that needs restricted access.

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

For a project that requires caller authentication, verify the route with
the configured credentials. The [HTTP tunnel guide](005-http-tunnels.md#http-authentication)
shows which Go properties select the caller policy.

For a direct Engine using a private CA, pass that CA to the client that
checks the published address. In curl, use `--cacert /path/to/engine-ca.pem`
with the printed HTTPS hostname. The CLI context trusts the Engine for the
SDK connection.

The runnable [HTTP server example](../examples/README.md#serve-http) adds signal handling and graceful shutdown. The [example catalog](../examples/README.md) explains which programs to pair for private connections, other protocols and API integrations.

## Run a private client and server

Use the [private HTTP pair](../examples/README.md#connect-privately) to see
the same integration from both sides. The server opens a Go listener
called `h1-example`. The client uses a regular `http.Client` with a
`DialContext` callback that dials that tunnel name. Choose a deployment that
permits private bytestreams and let both sides use the same project or direct
CE Engine.

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
| Inventory, watch and session APIs with token or certificate                  | [Engine API mTLS/TURN](015-engine-mtls-turn.md). Control plane administration still needs its own authorized token. |
| Restricted cryptographic distribution                                   | [FIPS profile](017-fips-140-3-profile.md); its exclusions are intentional.                                          |

To diagnose an application connection, use the [Go diagnostic API](018-doctor-api.md). For CLI and Engine checks, see [Troubleshooting](https://rstream.io/docs/support/troubleshooting).
