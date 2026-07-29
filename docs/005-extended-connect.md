# Extended CONNECT and MASQUE

The Go SDK can publish and dial HTTP/3 datagram tunnels that carry Extended CONNECT protocols such as WebTransport, CONNECT-UDP, and CONNECT-IP. Plain authority-form CONNECT is not Extended CONNECT and is documented separately in [HTTP_CONNECT.md](004-http-connect.md). The SDK does not implement MASQUE itself; the samples and runtime tests use the upstream protocol libraries directly:

- `github.com/quic-go/masque-go` for CONNECT-UDP.
- `github.com/quic-go/connect-ip-go` for CONNECT-IP.

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

The MASQUE samples follow the same published/private flag shape as other examples:

```bash
go run ./examples/masque-server --variant connect-udp
go run ./examples/masque-client --variant connect-udp --target 127.0.0.1:9000

go run ./examples/masque-server --variant connect-udp --publish
go run ./examples/masque-client --variant connect-udp --publish --target 127.0.0.1:9000
```

CONNECT-IP uses the same flags with `--variant connect-ip`. Published clients can pass `--addr` to skip tunnel lookup and connect to a specific published forwarding address.

## Browser support

WebTransport is exposed to browsers through the WebTransport API. CONNECT-UDP and CONNECT-IP are not exposed as general-purpose browser JavaScript APIs. They are intended for native clients, system proxy integrations, VPN-like clients, or application runtimes that can open HTTP/3 Extended CONNECT sessions directly.

## Runtime tests

The runtime MASQUE suite publishes an HTTP/3 datagram tunnel and verifies both CONNECT-UDP and CONNECT-IP end-to-end against a live engine:

```bash
make test-bins
BIN=out/test bash test/e2e/runtime-forward.sh
```

The cases are listed as `forward/connect udp` and `forward/connect ip` in the runtime forwarding output.
