# Tunnel configuration

The process serving traffic opens an outbound connection to the Engine.
Published callers use the Engine's public address. Private callers connect
to the Engine as rstream clients and dial a tunnel by name or ID. Choose
who can connect before selecting the service protocol and address.

## Private or published

| Access                    | Callers connect through                                                      | Typical use                                                           |
| ------------------------- | ---------------------------------------------------------------------------- | --------------------------------------------------------------------- |
| Private, `Publish=false`  | An authenticated rstream client dials the tunnel's name or ID.               | A database, worker or internal application connection.                |
| Published, `Publish=true` | An ordinary client uses the forwarding hostname/port returned by the engine. | A browser, webhook sender, SSH client or other public service client. |

Publication defaults to true. Set `Publish=false` explicitly for private
access. Agent credentials authenticate the process that creates the tunnel
and any private rstream caller. Published services can separately require
[HTTP authentication](005-http-tunnels.md#http-authentication) or
[TLS client certificates](006-tls-dtls-tunnels.md#client-certificate-authentication).
The [Go examples](../examples/README.md#connect-privately) show a private
tunnel and its dialer.

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

Keep the [demo HTTP service](002-cli-workflow.md#start-the-local-service)
running on `127.0.0.1:8080`, or use your own listening service. Create a
private tunnel in the owner's terminal. If you are reusing the first
walkthrough, this is the second terminal, where the public forwarder ran.

```sh
rstream forward 127.0.0.1:8080 --no-publish --name internal-api
```

In a separate caller terminal, dial that tunnel through a loopback listener.
Keep both the service and the private forwarder running.

```sh
rstream nc -L 127.0.0.1:8081 -R rstrm://internal-api
```

To exercise separate identities on one machine, add `--context publisher`
before `forward` and `--context caller` before `nc`. Use the names of your
owner and caller contexts for that project.

Request `http://127.0.0.1:8081/` from a browser or with curl. Expect the same
response as the upstream service. Stop the listener and the forwarder with
Ctrl+C. You can keep the service running to
[verify TLS termination](006-tls-dtls-tunnels.md#forward-a-tls-service). Use the [private Go example pair](../examples/README.md#connect-privately)
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
| [`http`](005-http-tunnels.md) | Bytestream for HTTP/1.1 or HTTP/2 upstream; datagram for HTTP/3 upstream. | HTTP handler, WebSocket service or compatible CONNECT application.                              |
| `tcp`    | Bytestream                                                                | Raw TCP application such as the [SSH example](../examples/README.md#protocol-specific-setup).                                                                |
| [`tls`](006-tls-dtls-tunnels.md) | Bytestream                                                                | Clear bytes after edge TLS termination, or the application's TLS handshake in passthrough mode. |
| [`dtls`](006-tls-dtls-tunnels.md#verify-a-published-dtls-tunnel) | Datagram                                                                  | Raw datagrams, or a DTLS upstream when enabled.                                                                                   |
| [`quic`](../examples/README.md#verify-published-quic) | Datagram                                                                  | QUIC upstream.                                                                                   |
| `webtty` | Bytestream, or datagram for registered WebTransport                                                                | Engine-managed WebTTY envelope; use registered-server enrollment.                               |

Omit `Type` when the engine can derive it from the protocol. Explicit values
must agree. Native unreliable datagrams require QUIC transport; use `DatagramGuaranteedDelivery` when the application needs framed reliable
delivery.

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

For managed terminal payload encryption, see [WebTTY](009-webtty.md#end-to-end-encryption).
