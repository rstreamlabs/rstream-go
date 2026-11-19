# Plain HTTP CONNECT

The Go SDK can publish HTTP tunnels that carry plain authority-form `CONNECT` requests. This is the standard TCP proxy mechanism used by forward proxies and system proxy integrations. For WebSocket, WebTransport or MASQUE, see [Extended CONNECT](014-extended-connect.md).

## Tunnel setup

Plain CONNECT follows normal HTTP tunnel version support:

```go
props := rstream.TunnelProperties{
  Name:        rstream.StringPtr("connect-egress"),
  Type:        rstream.TunnelTypePtr(rstream.TunnelTypeBytestream),
  Publish:     rstream.BoolPtr(true),
  Protocol:    rstream.ProtocolPtr(rstream.ProtocolHTTP),
  HTTPVersion: rstream.HTTPVersionPtr(rstream.HTTP1_1), // or HTTP2 for h2c
}
```

For HTTP/3 upstreams, use a datagram tunnel:

```go
props := rstream.TunnelProperties{
  Name:        rstream.StringPtr("connect-egress-h3"),
  Type:        rstream.TunnelTypePtr(rstream.TunnelTypeDatagram),
  Publish:     rstream.BoolPtr(true),
  Protocol:    rstream.ProtocolPtr(rstream.ProtocolHTTP),
  HTTPVersion: rstream.HTTPVersionPtr(rstream.HTTP3),
}
```

The upstream service behind the tunnel must be a real forward proxy. The rstream engine forwards the `CONNECT host:port` request to that service and relays bytes only after the upstream returns a 2xx response.

## Verify a proxy connection

Configure the upstream proxy's authentication and destination allowlist.
Choose an HTTPS target that proxy is allowed to reach. Use the tunnel's
printed HTTPS address as the proxy URL, then make a request to the target.

```sh
curl --proxy 'https://proxy-tunnel.example.com' 'https://allowed-origin.example.com/'
```

Supply the credentials required by your proxy. curl verifies both server
certificates. Expect the target service's response after the proxy accepts
CONNECT. If the tunnel uses a private CA, supply it with `--proxy-cacert`.

## Security model

The downstream TLS or QUIC connection selects the rstream tunnel. The
`CONNECT` authority names the final TCP target that the upstream proxy will
dial. Configure authentication, destination allowlists and logging in that
proxy. rstream preserves `Proxy-Authorization` and `Proxy-Authenticate` so
the proxy can authenticate the caller.

Open a dedicated connection for proxy traffic over HTTP/2 or HTTP/3. The Engine
returns `421 Misdirected Request` if a connection already used for reverse
HTTP is reused for CONNECT egress, or the reverse.
