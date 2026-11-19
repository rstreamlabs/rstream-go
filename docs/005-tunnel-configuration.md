# Tunnel configuration

A tunnel connects an application to its callers. Start with who can reach it,
then choose the kind of traffic and its public entry point. Engine admission,
project settings and permissions apply to the complete configuration.

## Private or published

| Access                    | Callers connect through                                                      | Typical use                                                           |
| ------------------------- | ---------------------------------------------------------------------------- | --------------------------------------------------------------------- |
| Private, `Publish=false`  | An authenticated rstream client dials the tunnel's name or ID.               | A database, worker or internal application connection.                |
| Published, `Publish=true` | An ordinary client uses the forwarding hostname/port returned by the engine. | A browser, webhook sender, SSH client or other public service client. |

Publication defaults to true. Set `Publish=false` explicitly for private access.
Private creation requires the project's private-tunnel entitlement; dialing
uses the caller's connect permission. The process that creates a tunnel accepts
its connections; a private peer uses
`Client.Dial` or `Client.PacketDial`. See the [examples](../examples/README.md)
for complete programs on each side.

These properties create a private bytestream; use them with an established
control channel:

```go
properties := rstream.TunnelProperties{
	Name:    rstream.StringPtr("worker"),
	Publish: rstream.BoolPtr(false),
}
```

The engine assigns `ID`. An optional `Name` gives private peers a lookup key
that the application can reuse when recreating the tunnel. Names belong to the
project; check that the requested name is available.

## Client transport, stream type and service protocol

Three choices describe different parts of the path:

| Choice                    | Field or API                     | Meaning                                                                                                                 |
| ------------------------- | -------------------------------- | ----------------------------------------------------------------------------------------------------------------------- |
| Client → engine transport | `Client` transport configuration | TLS or QUIC for the authenticated agent connection. [Transport configuration](004-transport.md) covers this connection. |
| Tunnel data model         | `Type`                           | `bytestream` preserves an ordered byte stream; `datagram` preserves messages.                                           |
| Service protocol          | `Protocol`                       | The protocol the public edge accepts, or the parser used for an explicitly declared private protocol.                   |

For a private raw tunnel, omit `Protocol` and choose the stream type the two
applications need. Private HTTP can declare `http` for engine HTTP dispatch;
`tls` keeps a raw stream with optional upstream TLS. Managed `webtty` has its
own envelope and enrollment path. Private DTLS/QUIC protocol dispatch is unsupported.

For published services, choose a coherent protocol/type pair:

| Protocol | Tunnel type                                                               | Application side                                                                                |
| -------- | ------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------- |
| `http`   | Bytestream for HTTP/1.1 or HTTP/2 upstream; datagram for HTTP/3 upstream. | HTTP handler, WebSocket service or compatible CONNECT application.                              |
| `tcp`    | Bytestream                                                                | Raw TCP application such as SSH.                                                                |
| `tls`    | Bytestream                                                                | Clear bytes after edge TLS termination, or the application's TLS handshake in passthrough mode. |
| `dtls`   | Datagram                                                                  | DTLS service.                                                                                   |
| `quic`   | Datagram                                                                  | QUIC service.                                                                                   |
| `webtty` | Bytestream                                                                | Engine-managed WebTTY envelope; use registered-server enrollment.                               |

Omit `Type` when the engine can derive it from the protocol. Explicit values
must agree. Native unreliable datagrams require QUIC transport; the transport
guide explains the separate framed reliable-delivery path.

## Region and routing

The engine destination selects where the agent establishes its connection and
creates tunnels. A global project's ingress can route callers to a tunnel
owned in another region when the deployment and project allow it.
`AllowCrossRegionRouting` selects that behavior for supported tunnel protocols;
when omitted, the engine's setting applies. The engine validates region and
routing permissions.

Configure regional selection through the client/CLI context rather than a
service's HTTP/TLS properties. Certificate-only devices can use a known regional
engine address; resolving a region through the Control plane requires its own
authorized API credential.

## Start with an HTTP service

For an application serving HTTP/1.1 on the tunnel, use:

```go
properties := rstream.TunnelProperties{
	Publish:     rstream.BoolPtr(true),
	Protocol:    rstream.ProtocolPtr(rstream.ProtocolHTTP),
	HTTPVersion: rstream.HTTPVersionPtr(rstream.HTTP1_1),
}
```

Ordinary clients reach the edge over HTTPS. The upstream HTTP variant is the
variant the application implements; edge negotiation with a browser is a
separate hop. A Go `http.Server` can serve a bytestream tunnel as a `net.Listener`.
The [first HTTP program](../README.md#use-the-go-library) shows the complete lifecycle.

Continue with [HTTP tunnels](020-http-tunnels.md) for upstream TLS, HTTP/2/3,
WebSocket/CONNECT and browser access policy.

## Publish TCP or TLS

| Service requirement                                                         | Choice                            |
| --------------------------------------------------------------------------- | --------------------------------- |
| Carry a protocol that already supplies its own security, such as SSH        | Published `tcp`.                  |
| Let the edge terminate TLS and enforce public TLS/client-certificate policy | `tls` with `TLSMode=terminated`.  |
| Let the application own the public TLS handshake and certificate            | `tls` with `TLSMode=passthrough`. |

For published TCP, leave `Port` unset for an ephemeral port or request one
already reserved by the project. The application supplies downstream encryption
and authentication. TCP accepts its port and routing policy; HTTP, TLS and
hostname options belong to their respective protocols.

The TCP creation properties for a reserved project port are:

```go
properties := rstream.TunnelProperties{
	Publish:  rstream.BoolPtr(true),
	Protocol: rstream.ProtocolPtr(rstream.ProtocolTCP),
	Port:     rstream.Uint32Ptr(10042),
}
```

[TLS and DTLS configuration](021-tls-dtls-tunnels.md) explains termination,
client certificates, minimum versions and cryptographic project policy.

## Hostnames, domains and ports

| Property                   | Use                                                                                              |
| -------------------------- | ------------------------------------------------------------------------------------------------ |
| `Hostname` in the response | Public hostname assigned or accepted by the engine.                                              |
| `Port` in the response     | Public forwarding port. A create request can select a reserved TCP port.                         |
| Requested stable hostname  | `<slug>-<project-endpoint>.t.<cluster-domain>` on the owning engine.                             |
| Requested custom domain    | A domain registered and authorized for the project. Its DNS and certificate setup must be ready. |
| `Host`                     | Compatibility response field; applications should read `Hostname` and `Port`.                    |

The engine checks domain ownership and active-name conflicts. CLI forwarding
commands generate a stable name when they can infer the project endpoint;
otherwise the engine allocates a hostname. See [stable domains](https://rstream.io/docs/tunnels/stable-domains)
and [custom domains](https://rstream.io/docs/tunnels/custom-domains) for their
DNS and registration workflows.

## Add access policy

| Policy                       | Controls                                                                                                        |
| ---------------------------- | --------------------------------------------------------------------------------------------------------------- |
| Project permissions/settings | Whether the identity may create/connect and which protocols, regions or cryptographic requirements are allowed. |
| `TrustedIPs`, `GeoIP`        | Source network/country restrictions at ingress.                                                                 |
| Public HTTP authentication   | Tokens, rstream browser sessions, challenges or admitted client certificates.                                   |
| Public TLS policy            | Termination, minimum TLS version, ALPN and client-certificate admission.                                        |

Agent credentials authenticate the creating/dialing process. Public edge policy
authenticates the service's callers. Configure the latter for the selected
protocol in [HTTP](020-http-tunnels.md) or [TLS/DTLS](021-tls-dtls-tunnels.md).
Managed terminal payload encryption belongs to [WebTTY](009-webtty.md#e2e-crypto).
