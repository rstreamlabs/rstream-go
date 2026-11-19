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

This example terminates public TLS on the Engine and forwards a clear byte
stream to a disposable local service. Use a [configured CLI context](002-cli-workflow.md#connect-the-cli)
on Community Edition or a hosted Pro or Enterprise project that allows
published TLS. Use an Engine with a certificate trusted by your client.

In a new directory, create a single demo file and start a local HTTP server.
Python provides the example's clear byte stream. On Windows use `py -3` in
place of `python3`.

```sh
mkdir rstream-tls-demo
cd rstream-tls-demo
python3 -c "from pathlib import Path; Path('index.html').write_text('Hello through TLS')"
python3 -m http.server 9000 --bind 127.0.0.1
```

In another terminal, run:

```sh
rstream forward 127.0.0.1:9000 --tls --tls-mode terminated
```

Use the hostname and port printed by the CLI in the following request. Use
`curl.exe` on Windows.

```sh
curl -fsS --http1.1 'https://tls-demo.example.com:443/'
```

The client verifies the certificate and returns `Hello through TLS`. If you
use a private CA, [supply the CA and verify the public hostname](https://rstream.io/guides/self-host-rstream-engine-ce-with-docker-compose#verify-the-engine-and-tunnel).
Stop the tunnel and the demo server with Ctrl+C.

Use this mode for a service whose TLS endpoint belongs at the Engine.
For an end-to-end TLS session, use `--tls-mode passthrough` when project
policy permits it. The upstream presents the certificate. When you need HTTP routing or request-level
authentication in front of an HTTPS origin, choose
[an HTTP tunnel with upstream TLS](005-http-tunnels.md#upstream-http-mode).

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
