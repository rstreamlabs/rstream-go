# Embed and connect WebTTY in Go

Use this reference when your application creates WebTTY sessions directly. See [WebTTY configuration](000-configuration.md) for CLI setup and identity management.

## Disconnect and explicit termination

`ClientSession.Close()` releases the reader. An Engine-managed execution that
explicitly selected a bounded detached lifetime continues after this disconnect;
ordinary sessions keep their connection-bound lifetime. Rejoin a live managed
execution with `Attach`, without sending a new `Open` or repeating the command.
The session API retains its confirmed result under the existing retention policy.
After a local close without native completion, `Wait()` returns
`ErrSessionDisconnected`. `Close()` itself remains idempotent
and returns nil; a previously confirmed native result is preserved.

For a managed reader that has just written input or `SendEOF()`, use
`Disconnect(ctx)` to finish the transport exchange before releasing it. Continue
draining `Events()` during this wait. It is bounded by the caller context and
`CloseDeadline` (five seconds by default); failure forces a local close and does
not confirm input delivery. An orderly reader disconnect never confirms process
completion. This matters especially for WebTransport, where an immediate session
close can discard queued input. `Close()` remains available for immediate release.

`ClientSession.CanTerminate()` reports native and configured Engine support.
`RequestTermination(ctx)` sends a separate stop request and leaves the reader
open for final output. Keep draining `Events()` and use `Wait()` to obtain the
native exit code. A successful request write is not confirmation that the
process has exited. An older server returns `ErrTerminationUnsupported`; the
client does not substitute a disconnect. Cancelling a partially written request
closes the transport because that frame cannot be retried safely.

For managed sessions, pass the result of `GetWebTTYCapabilities()` in
`ManagedSessionConfig.Capabilities` and acquire control through the existing
participant/control APIs. The SDK checks `ExplicitTermination` as well as native
support. An Attach without Engine capabilities cannot request termination. Native
capability discovery alone cannot qualify an older Engine relay. Spectators
and revoked controllers cannot send termination. The session's original
execution deadline is not extended by detaching or attaching another reader.

## Bounded detached execution in the SDK

With an authenticated Engine API client and an already configured terminal
`SessionConfig`, fetch capabilities from that same Engine and opt in explicitly:

```go
capabilities, err := api.GetWebTTYCapabilities(ctx)
if err != nil {
    return err
}
cfg.Managed = &webtty.ManagedSessionConfig{
    Capabilities: *capabilities,
    ExecutionSeconds: 300,
}
cfg.Interactive, cfg.AllocateTTY = false, false
session, err := webtty.OpenClientSession(ctx, &cfg)
if err != nil {
    return err
}
executionID := session.ManagedSessionID()
// Persist executionID before releasing this reader.
fmt.Println(executionID)
return session.Close()
```

The SDK rejects unsupported capabilities, transports, conflicting URL options
and excessive durations before opening a connection. `OpenClientSession` returns
only after the relay confirms the ID. Zero `ExecutionSeconds` preserves the
ordinary connection-bound lifetime, including when the Engine does not advertise
the managed protocol. The capability snapshot still prevents unsupported
termination requests. Detached execution supports WebSocket and
WebTransport as advertised by the Engine; it cannot extend an existing Attach.

Use the saved ID with the existing session/participant API to inspect the result
or obtain a fresh authorized Attach grant. A running command is joined without
another Open. Receiving an ID grants no authority, and a lost opening response
does not make retrying Open safe. Recording retention and permissions determine
which historical output is available; this API adds no history/live cursor or
survival across process or Engine restarts.

JSON command capture and MCP execution return `exit_code: null` when no native
result was confirmed, including transport loss and local cancellation. Confirmed
nonzero or negative native codes remain unchanged. Partial stdout and stderr
remain separate from the local execution error.

`CaptureCommand(ctx, config, maxOutputBytes)` retains stdout and stderr within
one combined byte limit (zero selects 8 MiB) and returns `CommandResult` alongside
any local error. CLI JSON output preserves explicit `--interactive` and `--tty`
options: terminal input, resize events and local terminal restoration match
streaming output. TTY allocation still requires a local terminal; remote terminal
stdout and stderr share the terminal's output stream. Without those options,
SDK capture defaults to empty input when no input reader is supplied. Overflow sets
`OutputTruncated`, returns `ErrCommandOutputLimit`, requests explicit termination
when supported, and keeps draining for at most 65 seconds or until caller
cancellation. Its `ExitCode` remains nil until native completion is confirmed.
The CLI JSON and MCP collectors use the 8 MiB default and report `truncated`
and the MCP `output_limit` error kind. Streaming CLI output is not retained.

## Transports

| Transport | Application data path | Requirements |
| --- | --- | --- |
| `websocket` | WebSocket messages over a stream. | Standard server default; local HTTP bearer or rstream admission. |
| `plain` | Framed WebTTY protobuf over TCP, TLS or private bytestream. | No HTTP bearer layer; lightweight rstream servers are private. |
| `webtransport` | HTTP/3 WebTransport streams. | Local server needs TLS cert/key; registered server requires publication; FIPS requires this transport. |

Local plain TLS and local WebTransport use TLS 1.3 minimum. WebTTY's application transport is independent of the TLS/QUIC client-to-Engine transport and of payload E2E.

| Client URI | Meaning | Condition |
| --- | --- | --- |
| `rstrm://<tunnel-id-or-name>` | Resolve inventory/security, then dial through rstream. | Standard live commands use private dial; see exceptions below. No public DNS name is needed. |
| `ws://host:port/path`, `wss://host:port/path` | Direct WebSocket endpoint. | `wss` verifies TLS; direct E2E needs explicit/local known-server trust. Default CLI URL is `ws://127.0.0.1:8080`. |
| `tcp://host:port`, `tls://host:port` | Direct plain WebTTY stream, with TLS for the latter. | Plain transport; default ports 80/443, use explicit port for local listeners. |
| `https://host/path` | Direct WebTransport endpoint. | Requires `--transport webtransport`; port 443 if omitted. `wt://`, `wts://` and `webtransport://` are accepted aliases with the same override. |
| `wss://host/path` with `--transport webtransport` | Same endpoint rewritten to HTTPS for WebTransport. | The server must actually provide WebTransport. |

For standard live `client`/`exec`, an `rstrm://` target uses a private stream or datagram dial even when the tunnel is published. Registered WebTransport still requires publication at the server. The **FIPS** client instead resolves a published target to its verified public HTTPS/WebTransport endpoint and fails closed when that endpoint is unavailable. **Detached `sessions start`** also uses the published WebSocket/WebTransport endpoint so Engine can acknowledge and own the bounded execution. Neither exception permits weakening admission, E2E or recording policy.

## E2E Crypto

WebTTY E2E keeps the protobuf session envelope visible while encrypting terminal
payload bytes. The engine can route sessions, record metadata, enforce policy,
and track lifecycle events without plaintext terminal content.

The Go helpers implement two profiles. Select a profile when creating identities; an existing identity is not silently converted.

| Profile | Terminal payload suite | Session-key envelope suite | Availability |
| --- | --- | --- | --- |
| Standard default | `aes-256-gcm`, fresh 96-bit nonce per message | `hpke-x25519-hkdf-sha256-aes-256-gcm` | Standard build; X25519 encryption identity. |
| `fips-compatible` | `aes-256-gcm-random-nonce` | `p256-hkdf-sha256-aes-256-gcm-random-nonce` | Standard and FIPS builds; P-256 ECDH, HKDF-SHA256, internally generated nonces. |

ChaCha20-Poly1305 identifiers are reserved, not selectable implementations; the helpers reject unsupported suites. A FIPS build accepts only the second profile over WebTransport. Protocol interoperability, including browser WebCrypto, does not attest a CMVP-validated deployment. See the [FIPS profile](../017-fips-140-3-profile.md) for artifact and transport boundaries.

Recorded E2E events expose both `key_context` and `key_context_raw`.
`key_context` is for inspection and may be represented as JSON. Consumers that
reconstruct an E2E session envelope for replay, join, or decrypt-material flows
must use `key_context_raw`, which is the unpadded base64url encoding of the
exact key-envelope/AAD context bytes used by the endpoint.

Workspace-managed protection uses separate workspace-device envelopes. Those
envelopes use ECDH P-256 recipient keys, HKDF-SHA-256, and AES-256-GCM for
browser and CLI device grants so they remain compatible with WebCrypto. That
workspace envelope is distinct from the per-session terminal key envelope; matching algorithms do not make them interchangeable.

The filesystem sidecar (WebDAV or WebRTC) is rejected when WebTTY E2E payload encryption is
active, because it is a separate data surface.
