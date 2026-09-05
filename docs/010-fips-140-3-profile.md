# FIPS 140-3 Profile

The rstream FIPS 140-3 profile is a distinct, restricted build of the Go SDK and `rstream` CLI. It links the Go Cryptographic Module selected at build time, enables its FIPS 140-3 mode by default, and rejects features outside the reviewed profile.

This profile does not state that the complete rstream product is itself a CMVP-validated cryptographic module. It uses the [Go Cryptographic Module](https://csrc.nist.gov/projects/cryptographic-module-validation-program/certificate/5247), validated under certificate 5247, within the module's documented boundary and approved mode. Deployment, configuration, key management, operating environment, and protocols remain part of the complete system assessment.

The upstream Go behavior and limitations are defined in [Go FIPS 140-3 compliance](https://go.dev/doc/security/fips140).

## Supported Scope

The implemented phase-one and phase-two profile covers:

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
- published QUIC tunnels and ordinary HTTP/3 tunnels.

The current phase excludes:

- proxied QUIC transport;
- generic datagram tunnels, DTLS, and TURN; datagram tunnel metadata is accepted
  only for published QUIC and HTTP/3;
- ECH;
- WebTTY and its current X25519/HPKE payload suite;
- CLI WebSocket event streaming;
- WebSocket, WebTransport, CONNECT-UDP, CONNECT-IP, and other Extended CONNECT
  paths, including their HTTP/3 variants;
- custom SDK transports whose cryptographic behavior cannot be established by the profile.

WebTTY requires a versioned FIPS-compatible payload and key-envelope suite
before it can enter the profile.

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

The phase-two profile also freezes `github.com/quic-go/quic-go` at `v0.60.0`.
The FIPS build fails its metadata check, and the executable fails closed at
startup, if that reviewed dependency identity changes.

## Runtime Verification

A FIPS-profile CLI identifies itself and the exact linked module build:

```text
rstream version <version> (FIPS 140-3 profile, Go module v1.0.0-c2097c7c, quic-go v0.60.0)
```

At process startup, the CLI verifies that:

- the rstream FIPS profile was compiled in;
- Go reports FIPS 140-3 mode as enabled;
- the linked Go module is the exact frozen build;
- the linked `quic-go` module is the reviewed phase-two version.

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
module semantic version, exact module build, and reviewed `quic-go` version.

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

Changing `FIPS_GO_MODULE`, Go itself, or `quic-go` is a security-relevant
release change. The Makefile values are intentionally not caller-overridable.
An upgrade requires:

1. confirming the module's CMVP certificate status and applicable operating environments;
2. reviewing the module Security Policy, Go release notes, and the `quic-go`
   FIPS integration;
3. running the standard and FIPS-specific test suites;
4. rebuilding both Linux architectures and checking their build metadata;
5. updating this document, release evidence, and product documentation with the exact module version.
