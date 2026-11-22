# FIPS 140-3 Profile

The rstream FIPS 140-3 profile is a distinct, restricted build of the Go SDK
and `rstream` CLI. Its artifacts embed and use the
[NIST CMVP-validated Go Cryptographic Module, certificate 5247](https://csrc.nist.gov/projects/cryptographic-module-validation-program/certificate/5247),
which has Overall Security Level 1. The build selects the exact module revision,
enables its approved FIPS 140-3 mode by default, and rejects features outside
the reviewed profile.

The accurate artifact description is **FIPS 140-3 Inside — Go Cryptographic
Module, Certificate #5247 (Overall Security Level 1)**. The certificate and
level apply to the embedded module. The complete rstream product has not
undergone a separate CMVP module validation. Deployment, configuration, key
management, operating environment, and protocols remain part of the complete
system assessment.

The upstream Go behavior and limitations are defined in [Go FIPS 140-3 compliance](https://go.dev/doc/security/fips140).

[Verify an artifact](#verify-an-artifact) if you already have a CLI binary.
[Build the profile](#build) to produce the restricted binary from this
checkout. Use [FIPS 140-3 Profiles](https://rstream.io/docs/security/fips-140-3)
to select a deployment.

## What the profile covers

FIPS 140-3 levels 1 through 4 describe the assurance level of a validated
cryptographic module. They are not graduated self-attestation levels for the
application that embeds it. NIST permits a product that incorporates an
unaltered validated module to identify that module with the
[`FIPS 140-3 Inside` phrase](https://csrc.nist.gov/Projects/Cryptographic-Module-Validation-Program/Use-of-FIPS-140-2-Logo-and-Phrases)
and its certificate number. It remains inaccurate to say that the complete
rstream product is itself "FIPS 140-3 validated" or "FIPS 140-3 Level 1".

A delivered FIPS client release should include or reference:

- the rstream FIPS SDK or CLI artifact and its digest;
- the source commit, Go release, build tags, and frozen `GOFIPS140` identity;
- [CMVP certificate 5247](https://csrc.nist.gov/projects/cryptographic-module-validation-program/certificate/5247)
  and the associated
  [module Security Policy](https://csrc.nist.gov/CSRC/media/projects/cryptographic-module-validation-program/documents/security-policies/140sp5247.pdf);
- the supported operating environment and rstream feature boundary;
- standard and strict-profile test evidence for the delivered release.

Use of the official NIST FIPS logo is separate from this factual product
description and must follow the NIST logo requirements.

## Supported Scope

| Area | Supported | Outside the profile |
| --- | --- | --- |
| Runtime and artifacts | Go 1.27 on Linux x86-64 and arm64. Static builds use `CGO_ENABLED=0`. | Other platforms and build profiles. |
| Engine transport | Direct TLS and QUIC, TLS through supported TCP proxies in explicit TLS mode, or direct automatic QUIC selection with TLS fallback. The Engine connection uses TLS 1.3. | Proxied QUIC, ECH and custom transports whose cryptography cannot be established by the profile. |
| Agent identity | Token admission and enrolled client certificates. | Executable mTLS signers. |
| Service protocols | Private and published bytestreams with supported protocols, published QUIC and ordinary HTTP/3. | Generic datagrams, DTLS, TURN, WebSocket, CONNECT-UDP and CONNECT-IP. |
| WebTTY | WebTransport, with optional authenticated end-to-end encryption. Explicit endpoint keys and Enterprise standalone workspace-managed sessions are supported. | Plain and WebSocket terminal transports, managed participant streams, the legacy X25519/HPKE suite and the filesystem sidecar. |
| CLI events and file sharing | SSE events. | WebSocket events and `rstream files`. |

See [TLS policy](#tls-policy) and the [WebTTY profile](#webtty-profile) for
cryptographic requirements beyond protocol selection.

## File-sharing compatibility

| Operation | Standard build | FIPS build |
| --- | --- | --- |
| `rstream files --backend webdav` (default) | Read-only by default; optional WebDAV writes and UI, downloads and ZIP | Rejected |
| `rstream files --backend webrtc` | Read-only transfers with rstream STUN/TURN | Rejected; DTLS and TURN are outside the profile |
| WebTTY filesystem, WebDAV | Supported with WebSocket terminal transport and terminal E2E disabled | Rejected |
| WebTTY filesystem, WebRTC | Read-only with WebSocket terminal transport and terminal E2E disabled | Rejected |

The terminal transport and filesystem backend are independent choices. The current CLI only mounts filesystem HTTP and WebRTC signaling routes on its WebSocket server; a WebTransport terminal cannot yet expose either backend. WebDAV itself does not require a non-approved cryptographic algorithm, but the complete files CLI and filesystem client paths remain outside the qualified profile.

Neither filesystem backend adds recipient-key file encryption or shares the terminal E2E envelope. Selecting WebDAV does not enable file sharing in the restricted profile. Use the standard build for these features; there is no automatic profile downgrade.

## Mixed-Profile Interoperability

A FIPS-profile client can connect to a standard rstream Engine for the features
allowed by the client profile. This compatibility includes direct TLS and QUIC
control transports, private and published bytestream forwarding, published
QUIC, ordinary HTTP/3, token admission, SSE events, and E2E WebTTY over
WebTransport.

That mixed topology does not turn the standard Engine or the complete
connection into a FIPS-profile deployment. The `FIPS 140-3 Inside` statement
applies to the client artifact and its embedded module. A supported
client-and-server FIPS deployment requires both a FIPS client artifact and the
FIPS Enterprise Edition standalone Engine, each operated inside its documented
profile and environment.

The standard build accepts both the legacy and FIPS-compatible WebTTY suites.
The FIPS build accepts only
`p256-hkdf-sha256-aes-256-gcm-random-nonce` paired with
`aes-256-gcm-random-nonce`, and only on WebTransport. Suite negotiation,
endpoint identities, client/server proof transcripts, workspace-device keys,
recording metadata, and replay material all carry the selected suite.

## Build

Use the Go toolchain specified in [`go.mod`](../go.mod). The Makefile pins the
reviewed cryptographic module. Run `make fips-test` from a checkout path
without spaces so Go can parse the generated module-file flag.

```bash
make fips-test
make fips-build
```

The binaries are written to:

```text
out/fips/linux/x86_64/rstream
out/fips/linux/arm64/rstream
```

Stable releases expose distinct, checksummed GitHub assets named
`rstream-fips-<version>-linux-x86_64.tar.gz` and
`rstream-fips-<version>-linux-arm64.tar.gz`. Download these assets explicitly
to install the restricted build.
Each archive contains the binary, its `go version -m` output, the source commit,
the profile documentation, and an internal checksum manifest. Verify the
adjacent `.sha256` file before extracting an archive.

The build combines:

```text
GOFIPS140=v1.0.0-c2097c7c
CGO_ENABLED=0
-tags=rstream_fips
```

`GOFIPS140` selects a frozen Go Cryptographic Module and enables FIPS mode by default. The `rstream_fips` build tag enables the rstream feature policy and fail-closed runtime checks. Both are required.

The [Makefile](../Makefile) pins the reviewed Go cryptographic module,
QUIC and WebTransport identities. The build checks artifact metadata, and the
CLI checks the same identities and the enabled FIPS runtime at startup.
Setting `GODEBUG=fips140=off` causes the restricted binary to exit before executing a
command.

## Build a Go application

From your application module, use the compatible SDK and the same reviewed
toolchain and profile. The published profile targets Linux on x86-64 and
arm64. This command builds for x86-64.

```sh
GOOS=linux GOARCH=amd64 GOFIPS140=v1.0.0-c2097c7c CGO_ENABLED=0 \
  go build -tags=rstream_fips -o app .
```

Choose `GOARCH=arm64` for Linux arm64. Compare the transport versions in
`go version -m app` with `FIPS_QUIC_MODULE` and `FIPS_WEBTRANSPORT_MODULE`
in the SDK's [Makefile](../Makefile) for the revision you build against.
Use `rstream.RequireFIPS` at startup to verify that the enabled runtime
and dependency graph satisfy the profile.

## Verify an artifact

On Linux, run the binary by its path so the check uses the intended
artifact. For example, run `./out/fips/linux/x86_64/rstream --version` from
the checkout, or `./rstream --version` from the extracted release folder.
Expect the FIPS profile along with the module, QUIC and WebTransport versions.

On any build host, inspect the build metadata:

```bash
go version -m out/fips/linux/x86_64/rstream
```

The output must contain the `rstream_fips` tag and the exact `GOFIPS140` version.

SDK consumers can enforce the same startup invariant:

```go
if err := rstream.RequireFIPS(); err != nil {
    return err
}
status := rstream.CurrentFIPSStatus()
```

`CurrentFIPSStatus` reports the rstream profile flag, Go FIPS runtime state,
module semantic version, exact module build, and reviewed `quic-go` and
`webtransport-go` versions.

## WebTTY Profile

Application E2E is optional in the FIPS WebTTY profile. A transport-only session
uses verified TLS 1.3/WebTransport and the approved cryptography of each FIPS
artifact on its path. A protocol name or tunnel label does not attest that a
remote endpoint runs a FIPS artifact: the deployment must establish that each
TLS endpoint, including an Engine that terminates the connection, is inside its
supported FIPS operating boundary.

Server and workspace policies can still require authenticated E2E. The client
cannot bypass that policy by omitting its E2E options. When application E2E is
selected, only the P-256 key-envelope and random-nonce AES-GCM payload suites
are accepted. Application E2E protects terminal content from an Engine that
terminates transport encryption; transport-only sessions do not provide that
additional protection. Managed recording remains encrypted at rest in both
modes.

The FIPS CLI defaults `webtty server`, `webtty client`, and `webtty exec` to
WebTransport. Direct commands may still pass `--transport=webtransport`
explicitly; any other live transport is rejected before network I/O.
For a published registered server, an `rstrm://` target is resolved to its
verified public HTTPS/WebTransport endpoint so EE standalone can apply managed
session policy and retain encrypted audit events. The FIPS CLI rejects an
unpublished WebTransport target instead of using an unqualified private
WebTransport path. An already known published HTTPS endpoint can be used with
`--no-discovery --transport=webtransport`, a local `--auth-token-file` and,
if needed, `--tls-ca-file`, without client control-plane discovery. These explicit
options do not waive server admission or a workspace E2E requirement.

FIPS builds create P-256 WebTTY endpoint identities and workspace-device
identities by default. A standard CLI can prepare compatible material
explicitly:

```bash
rstream webtty identity create --name secure-shell --crypto-profile=fips-compatible
rstream workspace device rotate --crypto-profile=fips-compatible
```

An existing identity is never silently converted. When a standard-profile
workspace device already exists, rotate it and approve the replacement before
using it with a FIPS WebTTY server. Registered-server enrollment records the
actual key algorithm and validates its public-key size.

The browser `@rstreamlabs/webtty` implementation is protocol-compatible with
this suite and uses WebTransport for the FIPS-compatible product path. Browser
WebCrypto is outside the Go Cryptographic Module boundary; the browser itself
must not be represented as a CMVP-validated Go FIPS module.

SDK code that opens a QUIC, HTTP/3, or WebTTY packet tunnel must declare
the reviewed profile explicitly. Generic `PacketDial` remains unavailable in a
FIPS build because it carries no protocol metadata:

```go
packetConn, err := client.PacketDialWithProperties(ctx, rstream.Addr{IdOrName: "terminal"}, rstream.TunnelProperties{
    Type:     rstream.TunnelTypePtr(rstream.TunnelTypeDatagram),
    Protocol: rstream.ProtocolPtr(rstream.ProtocolWebTTY),
})
```

Use `ProtocolQUIC` for a QUIC application or `ProtocolHTTP` together
with `HTTPVersion: rstream.HTTPVersionPtr(rstream.HTTP3)` for HTTP/3. The engine
independently checks the registered tunnel metadata; the declaration is not an
authorization bypass.

## TLS Policy

The profile rejects an SDK client configuration before network I/O when it:

- disables certificate verification;
- sets an explicit minimum or maximum older than TLS 1.2;
- explicitly enables ECH;
- explicitly selects a curve outside P-256, P-384, or the approved Go hybrid groups;
- explicitly selects a TLS 1.2 cipher suite outside ECDHE with AES-GCM;
- supplies a proxied QUIC transport or an unreviewed custom transport.

Normal Go TLS defaults remain controlled by the linked Go Cryptographic Module in approved mode. Private certificate authorities must be installed through the system trust store or the rstream CA configuration; certificate verification cannot be bypassed.

Use `transport.mode: tls` for a shared context configured with a proxy.
Automatic mode also configures a QUIC branch and the restricted profile
rejects proxied QUIC.

QUIC always requires TLS 1.3. `quic-go` uses the Go FIPS-aware TLS cipher
selection, rejects ChaCha20 in FIPS mode, delegates QUIC packet AES-GCM to the
Go cryptographic module, and uses AES for header protection.
