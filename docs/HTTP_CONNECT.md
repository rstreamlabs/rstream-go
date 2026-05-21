# Plain HTTP CONNECT

The Go SDK can publish HTTP tunnels that carry plain authority-form `CONNECT` requests. This is the standard TCP proxy mechanism used by forward proxies and system proxy integrations. It is not Extended CONNECT and it does not use a `:protocol` token.

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

## Security model

The `CONNECT` authority is the final TCP target, not the rstream tunnel host. The engine therefore binds plain CONNECT to the tunnel selected by the downstream TLS or QUIC connection and does not use the target authority for tunnel routing.

On HTTP/2 and HTTP/3, multiple CONNECT streams can share one connection. To prevent accidental cross-use of a reused connection, the engine separates normal reverse HTTP traffic from proxy egress traffic:

- normal HTTP requests use reverse HTTP mode;
- plain `CONNECT`, `CONNECT-UDP`, and `CONNECT-IP` use proxy egress mode.

Once a connection enters one mode, requests from the other mode are rejected with `421 Misdirected Request`. Destination policy remains the upstream proxy's responsibility; keep target allowlists, authentication, and audit logging in that proxy.

`Proxy-Authorization` and `Proxy-Authenticate` are preserved for plain CONNECT so the upstream proxy can authenticate clients. Sensitive proxy credentials are still redacted from engine logs.

## Runtime coverage

The runtime forwarding suite includes a real CONNECT proxy and TCP echo target:

```sh
make test-bins
bash test/e2e/runtime-forward.sh
```

The cases are listed as `forward/http connect h1`, `forward/http connect h2`, and `forward/http connect h3`.
