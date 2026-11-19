# TLS and DTLS tunnels

TLS tunnels accept TLS at the edge. TLS behavior depends on `tls_mode`.

With `tls_mode=terminated`, the edge terminates TLS and forwards a clear bytestream upstream. In this mode you can set `tls_min_version` (`tls1.2` or `tls1.3`) and optionally require client certificates via mTLS.

With `tls_mode=passthrough`, the edge forwards encrypted bytes upstream without decrypting. Because the edge does not terminate the TLS session, it cannot enforce TLS policy (such as minimum TLS version) and cannot validate client certificates. Those constraints must be enforced by the upstream.

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

Published DTLS endpoints support DTLS 1.2 and 1.3 when the engine enables DTLS.
The tunnel and project minimum use the existing values `tls1.2` and `tls1.3`;
for DTLS these select a DTLS 1.2-or-newer or DTLS 1.3 minimum.

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
