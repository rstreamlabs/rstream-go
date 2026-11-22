# WebTTY Go package

Use the [Go WebTTY guide](../docs/webtty/003-go-runtime.md) for commands,
streaming sessions and managed execution. The package also implements the
terminal payload and key-envelope formats below.

## Encryption formats

The Go helpers implement two profiles. Select a profile when creating identities; an existing identity is not silently converted.

| Profile | Terminal payload suite | Session-key envelope suite | Availability |
| --- | --- | --- | --- |
| Standard default | `aes-256-gcm`, fresh 96-bit nonce per message | `hpke-x25519-hkdf-sha256-aes-256-gcm` | Standard build; X25519 encryption identity. |
| `fips-compatible` | `aes-256-gcm-random-nonce` | `p256-hkdf-sha256-aes-256-gcm-random-nonce` | Standard and FIPS builds; P-256 ECDH, HKDF-SHA256, internally generated nonces. |

ChaCha20-Poly1305 identifiers are reserved, not selectable implementations; the helpers reject unsupported suites. A FIPS build accepts only the second profile over WebTransport. Protocol interoperability, including browser WebCrypto, does not attest a CMVP-validated deployment. See the [FIPS profile](../docs/017-fips-140-3-profile.md) for artifact and transport boundaries.

Recorded E2E events expose both `key_context` and `key_context_raw`.
`key_context` is for inspection and may be represented as JSON. Consumers that
reconstruct an E2E session envelope for replay, join, or decrypt-material flows
must use `key_context_raw`, which is the unpadded base64url encoding of the
exact key-envelope/AAD context bytes used by the endpoint.

## Attach to a live managed session

Use an Engine and session that support live attachment. With an authorized
`rstream.Client`, call `GetWebTTYSession` for the session and
`AttachWebTTYParticipant` to obtain a participant ID and attach grant.
Use the returned role when opening the stream. A spectator can read live
output and needs a separate control grant to send input.

Resolve the verified base URL with `Client.EngineAPIURL(ctx)`. The participant
stream uses WebSocket on that authority at
`/api/webtty/sessions/<session-id>/participants/<participant-id>/stream`.
Escape each identifier as a URL path segment. Use the Engine credential
for this HTTP connection. A `SessionConfig.DialTLSContext` adapter can
call `Client.DialEngineHTTP1` to reuse the Engine's TLS trust and identity.

Supply a `SessionConfig.Attach` with the session, workspace, project and
server IDs and the returned participant ID, role and `AttachGrant`. Set the
attach transport to WebSocket and include `AttachCapabilityReadStream`.
Pass the Engine's capability snapshot in `SessionConfig.Managed`. For an
encrypted session, configure the authorized device and terminal encryption
material before opening the stream. Use `OpenClientSession`, drain its
events, close the reader, and call `DetachWebTTYParticipant` with a fresh
bounded context after the reader stops.

## Managed disconnect and termination contracts

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

`ClientSession.CanTerminate()` reports runtime support. Your participant
credentials determine whether you can request termination.
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
