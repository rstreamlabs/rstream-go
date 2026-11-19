# TLS and DTLS tunnels

A TLS tunnel carries a byte stream. DTLS uses datagrams and has its own version
and project policy requirements. Choose which endpoint owns the public handshake
before configuring the upstream connection.

| Mode | What reaches the application | Where to enforce public TLS policy |
| --- | --- | --- |
| Terminated TLS | A clear byte stream by default. | The Engine presents the public certificate and applies the minimum TLS version and optional client certificate policy. |
| TLS passthrough | The original TLS handshake and encrypted byte stream. | The application presents the certificate and enforces peer identity and version requirements. |
| Published DTLS | Datagrams after the public DTLS negotiation. | The Engine negotiates the DTLS version and applies applicable project policy. |

## Forward a TLS service

For a local service accepting clear bytes on port 9000, select a project that
allows published TLS tunnels and run:

```sh
rstream forward 127.0.0.1:9000 --tls --tls-mode terminated
```

Connect your TLS client to the printed hostname and port and verify the
certificate against that hostname. If the local service already handles public
TLS, use `--tls-mode passthrough` instead and configure the certificate
and minimum version on that service. Stop forwarding with Ctrl+C. The
[bytestream echo example](../examples/README.md#choose-another-example) shows
the SDK side of a published TLS endpoint.

## Client certificate authentication

When `mtls_auth=true`, the edge requires client certificate authentication for the published tunnel. Certificate admission is configured through mTLS credentials. mTLS requires TLS termination at the edge.

## Upstream encryption

The public TLS session and the connection to the application are separate:

| Public protocol     | `UpstreamTLS` behavior                                                                                 |
| ------------------- | ------------------------------------------------------------------------------------------------------ |
| Edge-terminated TLS | Enables a separate TLS connection to the application.                                                  |
| TLS passthrough     | The application receives the original TLS handshake; combining it with `UpstreamTLS=true` is rejected. |
| DTLS                | Selects DTLS on the application connection.                                                            |
| QUIC                | TLS is required and enabled; an explicit false is rejected.                                            |

## DTLS and project cryptographic policy

Published DTLS requires Enterprise Edition with a DTLS listener. It supports DTLS 1.2 and 1.3.
The tunnel and project minimum use the existing values `tls1.2` and `tls1.3`;
for DTLS these select a DTLS 1.2-or-newer or DTLS 1.3 minimum.

Project post-quantum (PQ) key exchange and Encrypted Client Hello (ECH) settings
can narrow the allowed connections.

| Project setting        | Published DTLS behavior                                          |
| ---------------------- | ---------------------------------------------------------------- |
| PQ preferred, NIST off | X25519MLKEM768 preferred on DTLS 1.3; classical fallback allowed |
| PQ required, NIST off  | X25519MLKEM768 and DTLS 1.3 required                             |
| PQ preferred, NIST on  | Classical P-256/P-384 fallback only                              |
| PQ required, NIST on   | Rejected: required hybrid groups unavailable                     |
| ECH preferred          | Permitted without ECH, subject to other policy                   |
| ECH required           | Rejected: ECH unavailable on this DTLS surface                   |

Requirements combine with the minimum version and access/authentication rules.
Early data, DTLS session resumption and CID migration are disabled. The
restricted FIPS profile excludes DTLS.

The SDK's agent-to-engine transports are independent of the published DTLS
protocol. Use the connection adapter matching the imported Pion type:

| Pion connection                | Packet adapter                                 |
| ------------------------------ | ---------------------------------------------- |
| `github.com/pion/dtls/v3.Conn` | `PacketConnFromDTLSConn`                       |
| `github.com/pion/dtls/v4.Conn` | `PacketConnFromConn(conn, nil, PacketModeRaw)` |

The DTLS examples use the latter adapter. WebRTC can use its own DTLS
dependency in the same module.

To exercise a published DTLS tunnel, use the published mode of the
[datagram echo pair](../examples/README.md#choose-another-example) against an
Engine that enables DTLS. Confirm that the client uses the advertised
hostname and that the requested version complies with the project policy.
