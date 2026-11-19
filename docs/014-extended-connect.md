# Extended CONNECT and MASQUE

The Go SDK can publish and dial HTTP/3 datagram tunnels that carry Extended CONNECT protocols such as WebTransport, CONNECT-UDP, and CONNECT-IP. See [HTTP CONNECT](013-http-connect.md) for a TCP forward proxy. The Go
examples use dedicated libraries for the Extended CONNECT protocols.

| Protocol | Example library |
| --- | --- |
| CONNECT-UDP | `github.com/quic-go/masque-go` |
| CONNECT-IP | `github.com/quic-go/connect-ip-go` |

## Tunnel shape

Published MASQUE endpoints use a datagram HTTP tunnel with an HTTP/3 upstream:

```go
tunnel, err := ctrl.CreateTunnel(ctx, rstream.TunnelProperties{
  Name:        rstream.StringPtr("masque-server"),
  Type:        rstream.TunnelTypePtr(rstream.TunnelTypeDatagram),
  Publish:     rstream.BoolPtr(true),
  Protocol:    rstream.ProtocolPtr(rstream.ProtocolHTTP),
  HTTPVersion: rstream.HTTPVersionPtr(rstream.HTTP3),
})
```

Private SDK-to-SDK MASQUE examples use the same upstream HTTP/3 server shape without publishing the tunnel:

```go
tunnel, err := ctrl.CreateTunnel(ctx, rstream.TunnelProperties{
  Name:    rstream.StringPtr("masque-server"),
  Type:    rstream.TunnelTypePtr(rstream.TunnelTypeDatagram),
  Publish: rstream.BoolPtr(false),
})
```

In both modes, the server side exposes an HTTP/3 server on `rstream.PacketConnFromPacketListener(packetListener)`. Published mode lets standard HTTP/3 MASQUE clients connect through the edge hostname. Private mode requires an rstream SDK client to dial the tunnel first and then run HTTP/3 over the returned packet connection.

## CONNECT-UDP

CONNECT-UDP follows RFC 9298 and RFC 9297. Clients send `CONNECT` with `:protocol=connect-udp`, `Capsule-Protocol: ?1`, and a path expanded from a URI template such as:

```text
https://proxy.example/.well-known/masque/udp/{target_host}/{target_port}/
```

The upstream server owns UDP proxy behavior: template parsing, DNS resolution, UDP socket lifetime, packet validation, and target policy. The rstream engine only relays the HTTP/3 request stream and datagrams.

## CONNECT-IP

CONNECT-IP follows RFC 9484 and RFC 9297. Clients send `CONNECT` with `:protocol=connect-ip`, `Capsule-Protocol: ?1`, and a URI template selected by the IP proxy. The sample uses `/connect-ip` for a full-tunnel style exchange.

The upstream server owns IP proxy behavior: route scoping, address assignment, route advertisement, packet validation, and forwarding policy. CONNECT-IP control capsules and IP packet datagrams stay end-to-end between the client and upstream service.

## Samples

Use a project that allows datagram tunnels, with an Engine that supports
the selected protocol. On hosted rstream, use Pro or Enterprise. The
following demo uses a private test tunnel and a self-signed upstream
certificate. The demo client accepts that certificate for the test. Run both
programs in an isolated test environment. For a
published proxy, configure caller authentication and restrict the targets or
routes it can reach. Configure TLS trust in the clients that will use it.

Build both programs from the repository root.

```sh
make masque-server masque-client
```

For CONNECT-UDP, start an echo service on the machine running the MASQUE
server. Save the script as `udp_echo.py` and run it with Python 3.

```python
import socket

with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as server:
    server.bind(("127.0.0.1", 9000))
    while True:
        message, address = server.recvfrom(65535)
        server.sendto(message, address)
```

Start `./out/examples/masque-server --variant connect-udp` in a second
terminal. Run the client with the same project context.

```sh
./out/examples/masque-client --variant connect-udp --target 127.0.0.1:9000
```

Expect `CONNECT-UDP echo ok` and the returned payload. Stop the server
and the echo process with Ctrl+C.

For CONNECT-IP, run both programs with `--variant connect-ip` and omit
`--target`. The programs exchange a test IP packet directly. Expect
`CONNECT-IP packet echo ok`. Stop the server after the test.

## WebTransport

Use the [Go WebTransport server](../examples/http-wt-server) and
[matching client](../examples/http-wt-client) to test a stream exchange.
In published mode, the server defaults to a raw QUIC tunnel. Use
`--published-protocol=http` to select the HTTP/3 WebTransport path in a
project that supports it. See
[Connection Upgrades](https://rstream.io/docs/tunnels/http-tunnels/connection-upgrades#webtransport)
for edge routing and authentication.

## Browser support

WebTransport is exposed to browsers through the WebTransport API. CONNECT-UDP and CONNECT-IP are not exposed as general-purpose browser JavaScript APIs. They are intended for native clients, system proxy integrations, VPN-like clients, or application runtimes that can open HTTP/3 Extended CONNECT sessions directly.
