# TLS and DTLS tunnels

A TLS tunnel carries a byte stream. DTLS uses datagrams and has its own version
and project policy requirements. Choose which endpoint owns the public handshake
before configuring the upstream connection. For HTTP routing or request
authentication in front of an HTTPS service, choose
[HTTP with upstream TLS](005-http-tunnels.md#upstream-http-mode).

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

Start the [loopback demo service](002-cli-workflow.md#start-the-local-service)
on port 8080, or keep it running from the first walkthrough. Stop the
previous public HTTP forwarder before testing the TLS address.

Use a second terminal to publish the TLS tunnel.

```sh
rstream forward 127.0.0.1:8080 --tls --tls-mode terminated
```

Use the hostname and port printed by the CLI in the following request. Use
`curl.exe` on Windows.

```sh
curl -fsS --http1.1 'https://tls-demo.example.com:443/'
```

The client verifies the certificate and returns `Hello from rstream`. For a
private CA, pass its certificate with `--cacert /path/to/ca.pem`.

To check a TLS 1.3 minimum, use a curl build that supports TLS 1.3.
Keep the demo service running. Restart only the forwarder with
`--tls-min-version tls1.3`. Repeat the curl request against the address printed
by that forwarder with `--tlsv1.3 --tls-max 1.3`, then with
`--tlsv1.2 --tls-max 1.2`. Keep the hostname, CA
and service unchanged. The first request succeeds, and the second fails
during TLS negotiation. Stop the forwarder and demo service when finished.

To relay an end-to-end TLS session, use `--tls-mode passthrough` when the
project permits it. Forward to a service that already speaks TLS and can
present a certificate for the public hostname. The loopback HTTP server
above serves cleartext and works with termination at the edge.

## Client certificate authentication

For an edge-terminated TLS tunnel on a deployment with published mTLS,
set `--mtls` in the CLI or `TunnelProperties.MTLSAuth` in Go.
[Enroll the caller's certificate](https://rstream.io/docs/security/mtls#client-certificate-credentials)
and scope it to the tunnel. Start the loopback HTTP service on port 8080
again if needed, then open a new terminated tunnel.

```sh
rstream forward 127.0.0.1:8080 --tls --tls-mode terminated --mtls
```

Use the printed address and your certificate and key paths for the request.

```sh
curl -fsS --http1.1 --cert ./client.pem --key ./client-key.pem 'https://tls-demo.example.com:443/'
```

An admitted certificate returns the demo response. Repeat the request
without `--cert` and `--key` to confirm refusal during TLS negotiation.
Use the same hostname and CA settings for both requests. See
[published tunnel access](https://rstream.io/docs/security/mtls#published-tunnel-access)
for the project policy that controls admission.

## Upstream encryption

The public TLS session and the connection to the application are separate:

| Public protocol     | `UpstreamTLS` behavior                                                                                 |
| ------------------- | ------------------------------------------------------------------------------------------------------ |
| Edge-terminated TLS | Enables a separate TLS connection to the application.                                                  |
| TLS passthrough     | The application receives the original TLS handshake; combining it with `UpstreamTLS=true` is rejected. |
| DTLS                | Selects DTLS on the application connection.                                                            |
| QUIC                | TLS is required and enabled; an explicit false is rejected.                                            |

## Verify a published DTLS tunnel

Use hosted Pro or Enterprise, or a managed deployment that enables a DTLS
listener for the project. Check the [cryptographic policy](#dtls-and-project-cryptographic-policy)
before choosing a client. The owner needs tunnel creation permission.
The example client uses Engine inventory to find the published endpoint,
so its identity needs `tunnels.resources.read-only` for that tunnel.

Build the [datagram echo pair](../examples/README.md#choose-another-example)
with `make datagram-echo-server datagram-echo-client`. Run both programs
with `--publish`, starting with the server. The example client verifies the
published hostname against the system trust store. For a private CA or a
project requiring client certificates, use a DTLS client configured with
that CA or the required certificate. The CLI context's Engine CA is for
the agent connection and does not configure the downstream client.

```sh
./out/examples/datagram-echo-server --publish
```

```sh
./out/examples/datagram-echo-client --publish
```

The client prints the echoed payload when the connection succeeds. Stop the
server with Ctrl+C.

### Check a minimum DTLS version

Start the echo server again, this time requiring DTLS 1.3. Choose a project
whose remaining cryptographic policies admit the sample client.

```sh
./out/examples/datagram-echo-server --publish --dtls-min-version tls1.3
```

While that server runs, use the same tunnel name and trust settings for both
requests. If the public endpoint uses a private CA, append
`--ca-file /path/to/downstream-ca.pem` to both commands.

```sh
./out/examples/datagram-echo-client --publish --dtls-version 1.3
./out/examples/datagram-echo-client --publish --dtls-version 1.2
```

The first client prints the echoed payload. The second exits with a
handshake error and receives no payload. To test a project-wide minimum,
set it on the project and repeat against a server started with `--publish`
alone.

## DTLS and project cryptographic policy

The public listener supports DTLS 1.2 and 1.3. The
[editions and plans table](https://rstream.io/docs/support/editions) lists
availability by deployment.
The tunnel and project minimum use the existing values `tls1.2` and `tls1.3`;
for DTLS these select a DTLS 1.2-or-newer or DTLS 1.3 minimum. Set the
tunnel minimum with `--tls-min-version tls1.3` on a published DTLS forwarder,
or use `TunnelProperties.TLSMinVersion` in Go. The connection must satisfy
both the tunnel minimum and the project policy.

Project post-quantum (PQ) key exchange and Encrypted Client Hello (ECH) settings
can narrow the allowed connections. For the equivalent TLS and QUIC behavior,
see [project transport policies](https://rstream.io/docs/security/access-policies).

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

## Use a DTLS connection from Go

The SDK's agent-to-Engine transports are independent of the published DTLS
protocol. Use the connection adapter matching the imported Pion type:

| Pion connection                | Packet adapter                                 |
| ------------------------------ | ---------------------------------------------- |
| `github.com/pion/dtls/v3.Conn` | `PacketConnFromDTLSConn`                       |
| `github.com/pion/dtls/v4.Conn` | `PacketConnFromConn(conn, nil, PacketModeRaw)` |

The DTLS examples use the latter adapter. WebRTC can use its own DTLS
dependency in the same module.
