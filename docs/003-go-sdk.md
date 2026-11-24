# Embed the Go SDK

The reference Go SDK lets your application own its Engine connection, accept
or dial streams through standard Go interfaces, and carry datagram protocols.
This walkthrough uses the released module. To forward a service that already
listens on a local port, use the [CLI walkthrough](002-cli-workflow.md#publish-a-first-http-service).

## Prepare a connection

| Where you connect | Before running Go code |
| --- | --- |
| Hosted rstream | [Install the CLI](000-installation.md) and use the commands below to select a project. Basic can publish HTTP. Choose Pro or Enterprise to create a private tunnel. |
| Self-hosted Community Edition | [Configure a CLI context for the Engine](https://rstream.io/docs/tunnels/self-hosted/deployment#agent-context) using its hostname, CA if needed, and JWT. CE supports published HTTP and private bytestreams. |
| An existing deployment | Select the context provided by your operator. See [editions and plans](https://rstream.io/docs/support/editions) for listener and policy requirements. |

For a hosted project, sign in and approve the request in the browser.

```sh
rstream login
```

List the projects available to this identity.

```sh
rstream project list
```

If the list is empty, open the workspace in the [Dashboard](https://rstream.io/dashboard).
Workspace owners and admins can select **Create new project**. Other members
can ask an owner or admin for access to an existing project. If the CLI login
was limited to specific projects, run `rstream login` again to include the
intended project. Run `rstream project list` again after gaining access.

Choose a project that allows HTTP publication. Replace `my-project-endpoint`
with its `ENDPOINT` value.

```sh
rstream project use my-project-endpoint
```

The program below reads the saved context through `config.NewClientFromEnv()`.
A service with an explicit Engine address and runtime credential can use
the [shared environment settings](002-cli-workflow.md#contexts-and-environments)
instead. For a private Engine CA, configure that trust before connecting.

## Serve a published HTTP route

Install Go 1.27.4 or newer for this version of the SDK. From an empty
application directory, initialize the module and add the SDK.

```sh
go mod init example.com/rstream-hello
go get github.com/rstreamlabs/rstream-go@v1.38.1
```

The program publishes a disposable HTTP response. It uses the project or
Engine you selected in [Prepare a connection](#prepare-a-connection) and
serves the handler directly on the tunnel.
Run the unchanged example on a project that permits public HTTP requests.
For a service that needs caller authentication, set the
[Go access properties](005-http-tunnels.md#http-authentication) before creating
the tunnel.

```go
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func main() {
	if err := serve(); err != nil {
		log.Fatal(err)
	}
}

func serve() error {
	ctx := context.Background()
	client, err := config.NewClientFromEnv()
	if err != nil {
		return err
	}
	defer client.Close()
	control, err := client.Connect(ctx, nil)
	if err != nil {
		return err
	}
	defer control.Close()
	tunnel, err := control.CreateTunnel(ctx, rstream.TunnelProperties{
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
	listener, ok := tunnel.(rstream.BytestreamTunnel)
	if !ok {
		return fmt.Errorf("expected a bytestream tunnel")
	}
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprintln(w, "Hello from rstream")
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return server.Serve(listener)
}
```

Save the program as `main.go` in that module. Resolve its imports, including the configuration package, then run it:

```sh
go mod tidy
go run .
```

Open the printed forwarding address to see `Hello from rstream`. Stop
this disposable demo with Ctrl+C.

For a project that requires caller authentication, verify the route with
the configured credentials. The [HTTP tunnel guide](005-http-tunnels.md#http-authentication)
shows which Go properties select the caller policy.

For a direct Engine using a private CA, pass that CA to the client that
checks the published address. In curl, use `--cacert /path/to/engine-ca.pem`
with the printed HTTPS hostname. The CLI context trusts the Engine for the
SDK connection.

The runnable [HTTP server example](../examples/README.md#serve-http) adds signal handling and graceful shutdown.

## Run a private client and server

Use the same project or direct CE Engine on both sides. In a managed
project, the server needs `tunnels.tunnels.create-delete` and the client
needs `tunnels.streams.create-delete` on the tunnel.

The server opens a listener named `h1-example`. The client's
`http.Transport.DialContext` returns the connection obtained by dialing that
name through rstream. To adapt the first server, set `Name` to
`h1-example`, set `Publish` to false and omit the public HTTP settings.
On the caller, use the SDK client with this transport.

```go
func privateHTTPClient(client *rstream.Client) *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return client.Dial(ctx, rstream.Addr{IdOrName: "h1-example"})
			},
		},
	}
}
```

Add `net` to the imports shown above.
Use this dedicated client for requests to `http://h1-example/`. Close
each response body and close idle connections when done. The
[private HTTP pair](../examples/README.md#connect-privately) shows these
steps in complete programs.

## Network interfaces and ownership

| Object/API                                       | Go integration                                                        | Owner closes                                                                                             |
| ------------------------------------------------ | --------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------- |
| `Client`                                         | Shared authenticated runtime and Engine HTTP APIs.                    | Close after its controls and application operations stop; this releases owned transports and HTTP pools. |
| `Client.Connect`              | Returns a control channel that creates tunnels and receives their connections.                         | Close the channel after tunnels/accept loops stop.                                                       |
| `BytestreamTunnel`                               | `net.Listener`; accepted streams implement `net.Conn`.                | Close accepted connections, tunnel and channel. `http.Server.Serve` accepts the tunnel directly.         |
| `http.Transport` over a private dialer | Go HTTP transport with a `DialContext` that returns an rstream connection. | Close each response body and then the client's idle HTTP connections before closing the rstream client. |
| `Client.Dial`                                    | `net.Conn` for a private tunnel selected by name/ID.                  | Close each connection, including on request cancellation.                                                |
| `DatagramTunnel`, `PacketListener`, `PacketDial` | Packet sessions and `net.PacketConn` adapters for protocol libraries. | Close sessions/listeners and stop owned workers before their client.                                     |

The context passed to `Client.Connect` bounds connection establishment.
Use `ControlChannel.Done` and `Err` to detect when the channel finishes.
The application owns reconnection and any replacement tunnels. During
shutdown, drain the HTTP server or connection workers before releasing
tunnels and the SDK client.

## Choose the next capability

| Requirement                                                             | Reference or sample                                                                                                 |
| ----------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| TLS/QUIC client transport, proxy, private CA, DNS and interface binding | [Transport configuration](020-agent-transport.md) covers setup and ownership.                                                                        |
| HTTP/1.1, h2c, HTTP/3, published TCP/TLS/DTLS/QUIC or private access    | [Tunnel configuration](004-tunnel-configuration.md), [example catalog](../examples/README.md).                      |
| WebSocket or WebTransport, CONNECT-UDP/IP                               | [Extended CONNECT](014-extended-connect.md); application protocol code stays in its library.                        |
| Inventory, watch and session APIs with token or certificate                  | [Engine API mTLS/TURN](015-engine-mtls-turn.md). Control plane administration still needs its own authorized token. |
| Serve files or manage remote terminals | [Embed file sharing](../fileserver/README.md) and [WebTTY in Go](webtty/003-go-runtime.md). |
| Restricted cryptographic distribution                                   | [FIPS profile](017-fips-140-3-profile.md).                                                                              |

See the [Go API reference](https://pkg.go.dev/github.com/rstreamlabs/rstream-go)
for exported types and methods. To diagnose an application connection, use the [Go diagnostic API](018-doctor-api.md). For CLI and Engine checks, see [Troubleshooting](https://rstream.io/docs/support/troubleshooting).
