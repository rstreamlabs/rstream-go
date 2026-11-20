# Plain HTTP CONNECT

The Go SDK can publish HTTP tunnels that carry plain authority-form `CONNECT` requests. This is the standard TCP proxy mechanism used by forward proxies and system proxy integrations. For WebSocket, WebTransport or MASQUE, see [Extended CONNECT](014-extended-connect.md).

## Forward an existing HTTP proxy

Configure the proxy's authentication and destination allowlist before
publishing it. Point the CLI at your HTTP/1.1 proxy on loopback port 8080.

```sh
rstream forward 127.0.0.1:8080 --http --http-version http/1.1
```

Use the forwarding address to [verify a proxy connection](#verify-a-proxy-connection).
Community Edition can carry plain CONNECT over HTTP/1.1 or HTTP/2.

## Serve a proxy in Go

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

For an HTTP/3 upstream, use a datagram tunnel on a compatible managed
Engine and project. See the [HTTP/3 requirements](005-http-tunnels.md#upstream-http-mode).

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
printed HTTPS hostname and port as the proxy URL. Include port 443 when
the address omits the standard HTTPS port.

```sh
curl --proxy 'https://proxy-tunnel.example.com:443' 'https://allowed-origin.example.com/'
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
