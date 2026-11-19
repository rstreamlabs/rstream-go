# Tunnel configuration

A tunnel connects an application to its callers. Start with who can reach it,
then choose the kind of traffic and its public entry point. Engine admission,
project settings and permissions apply to the complete configuration.

## Private or published

| Access                    | Callers connect through                                                      | Typical use                                                           |
| ------------------------- | ---------------------------------------------------------------------------- | --------------------------------------------------------------------- |
| Private, `Publish=false`  | An authenticated rstream client dials the tunnel's name or ID.               | A database, worker or internal application connection.                |
| Published, `Publish=true` | An ordinary client uses the forwarding hostname/port returned by the engine. | A browser, webhook sender, SSH client or other public service client. |

Publication defaults to true. Published endpoints can still require authentication. Set `Publish=false` explicitly for private access.
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

## Try a private connection

Use a project that permits private tunnels (hosted Pro or Enterprise), or a
direct Community Edition Engine. Configure both terminals for the same project
or Engine. In a managed project, the tunnel owner needs
`tunnels.tunnels.create-delete` and the dialing client needs
`tunnels.streams.create-delete` on the tunnel.

With an HTTP service listening on `127.0.0.1:8080`, forward it through a private tunnel:

```sh
rstream forward 127.0.0.1:8080 --no-publish --name internal-api
```

In a second terminal, dial the named tunnel through a loopback listener:

```sh
rstream nc -L 127.0.0.1:8081 -R rstrm://internal-api
```

Request `http://127.0.0.1:8081/` from a browser or with curl. Expect the same
response as the upstream service. Stop the listener and the forwarder with
Ctrl+C. Use the [private Go example pair](../examples/README.md#connect-privately)
when both endpoints are Go programs.

## Client transport, stream type and service protocol

Three choices describe different parts of the path:

| Choice                    | Field or API                     | Meaning                                                                                                                 |
| ------------------------- | -------------------------------- | ----------------------------------------------------------------------------------------------------------------------- |
| Agent transport | `Client` transport configuration | TLS or QUIC for the authenticated agent connection. [Transport configuration](020-agent-transport.md) covers this connection. |
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
| `dtls`   | Datagram                                                                  | Raw datagrams, or a DTLS upstream when enabled.                                                                                   |
| `quic`   | Datagram                                                                  | QUIC upstream.                                                                                   |
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
| Public HTTP authentication   | Tokens, rstream browser sessions or admitted client certificates.                                   |
| Public TLS policy            | Termination, minimum TLS version, ALPN and client-certificate admission.                                        |

Agent credentials authenticate the creating/dialing process. Public edge policy
authenticates the service's callers. Configure the latter for the selected
protocol in [HTTP](005-http-tunnels.md) or [TLS/DTLS](006-tls-dtls-tunnels.md).
Managed terminal payload encryption belongs to [WebTTY](009-webtty.md#end-to-end-encryption).

## Continue with the service protocol

| Service | Guide |
| --- | --- |
| Website, API or WebSocket | [HTTP tunnels](005-http-tunnels.md) covers forwarding, upstream HTTP versions and access policy. |
| TLS or DTLS service | [TLS and DTLS tunnels](006-tls-dtls-tunnels.md) explains termination, passthrough and certificate requirements. |
| A TCP service with its own security, such as SSH | Select TCP and let the Engine assign a port, or request a port reserved for the project. See the [SSH client and server](../examples/README.md#choose-another-example). |
