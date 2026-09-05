# FIPS 140-3 Profile

The rstream FIPS 140-3 profile is a distinct, restricted build of the Go SDK and `rstream` CLI. It links the Go Cryptographic Module selected at build time, enables its FIPS 140-3 mode by default, and rejects features outside the reviewed profile.

This profile does not state that the complete rstream product is itself a CMVP-validated cryptographic module. It uses the [Go Cryptographic Module](https://csrc.nist.gov/projects/cryptographic-module-validation-program/certificate/5247), validated under certificate 5247, within the module's documented boundary and approved mode. Deployment, configuration, key management, operating environment, and protocols remain part of the complete system assessment.

The upstream Go behavior and limitations are defined in [Go FIPS 140-3 compliance](https://go.dev/doc/security/fips140).

## Supported Scope

The implemented phase-one through phase-three profile covers:

- Go 1.27;
- Linux x86-64 and arm64;
- static binaries with `CGO_ENABLED=0`;
- direct TLS transport and TLS through the supported TCP proxy paths;
- direct QUIC transport, including automatic QUIC-first selection with TLS
  fallback;
- TLS 1.2 or later, with the current rstream engine path using TLS 1.3;
- QUIC over TLS 1.3 with AES-GCM packet protection;
- token admission and mTLS client admission;
- private and published bytestream tunnels that do not select an excluded
  protocol;
- published QUIC tunnels and ordinary HTTP/3 tunnels;
- authenticated E2E WebTTY over HTTP/3/WebTransport;
- explicit-key and EE standalone workspace-managed WebTTY clients and servers;
- P-256 ECDH, HKDF-SHA256, and AES-256-GCM with internally generated random
  nonces for WebTTY session-key envelopes and terminal payloads.

The current phase excludes:

- proxied QUIC transport;
- generic datagram tunnels, DTLS, and TURN; datagram tunnel metadata is accepted
  only for published QUIC and HTTP/3;
- ECH;
- WebTTY over plain streams or WebSocket, including managed participant
  `sessions join` streams, which do not yet expose WebTransport;
- the WebTTY filesystem sidecar;
- the legacy WebTTY X25519/HPKE suite;
- CLI WebSocket event streaming;
- WebSocket, CONNECT-UDP, CONNECT-IP, and other non-WebTransport Extended
  CONNECT paths;
- custom SDK transports whose cryptographic behavior cannot be established by the profile.

The standard build accepts both the legacy and FIPS-compatible WebTTY suites.
The FIPS build accepts only
`p256-hkdf-sha256-aes-256-gcm-random-nonce` paired with
`aes-256-gcm-random-nonce`, and only on WebTransport. Suite negotiation,
endpoint identities, client/server proof transcripts, workspace-device keys,
recording metadata, and replay material all carry the selected suite.

## Build

The repository pins the reviewed module version instead of resolving `certified` during each release build:

```bash
make fips-test
make fips-build
```

The binaries are written to:

```text
out/fips/linux/x86_64/rstream
out/fips/linux/arm64/rstream
```

The build combines:

```text
GOFIPS140=v1.0.0-c2097c7c
CGO_ENABLED=0
-tags=rstream_fips
```

`GOFIPS140` selects a frozen Go Cryptographic Module and enables FIPS mode by default. The `rstream_fips` build tag enables the rstream feature policy and fail-closed runtime checks. Both are required.

The phase-three profile freezes `github.com/quic-go/quic-go` at `v0.60.0` and
`github.com/quic-go/webtransport-go` at `v0.11.1`.
The FIPS build fails its metadata check, and the executable fails closed at
startup, if that reviewed dependency identity changes.

## Runtime Verification

A FIPS-profile CLI identifies itself and the exact linked module build:

```text
rstream version <version> (FIPS 140-3 profile, Go module v1.0.0-c2097c7c, quic-go v0.60.0, webtransport-go v0.11.1)
```

At process startup, the CLI verifies that:

- the rstream FIPS profile was compiled in;
- Go reports FIPS 140-3 mode as enabled;
- the linked Go module is the exact frozen build;
- the linked `quic-go` module is the reviewed phase-two version;
- the linked `webtransport-go` module is the reviewed phase-three version.

The process exits before command execution when any condition is false. In particular, a caller cannot silently downgrade a FIPS-profile binary with `GODEBUG=fips140=off`.

Build metadata provides an independent artifact check:

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

The FIPS CLI defaults `webtty server`, `webtty client`, and `webtty exec` to
WebTransport. Direct commands may still pass `--transport=webtransport`
explicitly; any other live transport is rejected before network I/O.
For a published registered server, an `rstrm://` target is resolved to its
verified public HTTPS/WebTransport endpoint so EE standalone can apply managed
session policy and retain encrypted audit events. The FIPS CLI rejects an
unpublished WebTransport target instead of tunnelling an opaque inner HTTP/3
session that the engine cannot inspect.

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
- permits only a TLS version older than TLS 1.2;
- explicitly enables ECH;
- explicitly selects a curve outside P-256, P-384, or the approved Go hybrid groups;
- explicitly selects a TLS 1.2 cipher suite outside ECDHE with AES-GCM;
- supplies a proxied QUIC transport or an unreviewed custom transport.

Normal Go TLS defaults remain controlled by the linked Go Cryptographic Module in approved mode. Private certificate authorities must be installed through the system trust store or the rstream CA configuration; certificate verification cannot be bypassed.

QUIC always requires TLS 1.3. `quic-go` uses the Go FIPS-aware TLS cipher
selection, rejects ChaCha20 in FIPS mode, delegates QUIC packet AES-GCM to the
Go cryptographic module, and uses AES for header protection. The strict test
performs a real `QUICTransport` stream exchange and verifies that this exact
integration remains operational.

## Test Mode

`make fips-test` adds `GODEBUG=fips140=only` while running the profile-specific
test set. It exercises real TLS 1.3 and QUIC exchanges in addition to the
policy checks. Go documents `fips140=only` as an assessment and debugging aid
that can intentionally return errors or panic for non-approved operations. It
is not a production setting.

Standard tests continue to run without the profile:

```bash
make tests
```

Both test modes are required because the standard build retains the complete protocol surface while the FIPS build intentionally rejects excluded features.

## Module Upgrade Procedure

Changing `FIPS_GO_MODULE`, Go itself, `quic-go`, or `webtransport-go` is a security-relevant
release change. The Makefile values are intentionally not caller-overridable.
An upgrade requires:

1. confirming the module's CMVP certificate status and applicable operating environments;
2. reviewing the module Security Policy, Go release notes, and the `quic-go`
   and `webtransport-go` FIPS integration;
3. running the standard and FIPS-specific test suites;
4. rebuilding both Linux architectures and checking their build metadata;
5. updating this document, release evidence, and product documentation with the exact module version.
