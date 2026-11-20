# Control WebTTY sessions in Go

Use `webtty.CaptureCommand` for a bounded result from one command.
Use `webtty.OpenClientSession` to stream output or control the session
lifecycle. [WebTTY configuration](000-server-configuration.md) covers servers and
endpoint trust.

## Capture command output

Use a local file containing a bearer token to start a loopback WebSocket
listener.

```sh
rstream webtty server --listen 127.0.0.1:8080 --auth-token-file ./webtty-token
```

Pass the same token to the Go function to execute `whoami`. For a remote
listener, configure the client with its verified `wss://` address.

```go
func captureAccount(ctx context.Context, token string) (webtty.CommandResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return webtty.CaptureCommand(ctx, &webtty.ClientConfig{
		URL:       "ws://127.0.0.1:8080",
		AuthToken: &token,
		CmdArgs:   []string{"whoami"},
	}, 64*1024)
}
```

Import `context`, `time` and `github.com/rstreamlabs/rstream-go/webtty`.
If the command succeeds, `Stdout` contains the account name and `ExitCode`
points to zero. A nonzero code is a confirmed remote process result.
A local capture error can still return partial `Stdout` and `Stderr`.
`ExitCode` is nil until the process reports a final code.

The byte limit applies to both streams together. Zero selects 8 MiB. If
the limit is reached, check `OutputTruncated` and `ErrCommandOutputLimit`.
The SDK requests termination when supported and bounds the wait for
remaining output. A TTY combines stdout and stderr in its output stream.

## Stream a connection-bound command

Use a command that can finish without input and a `SessionConfig` with its
endpoint and terminal credential. Keep the context active for the reader's
entire lifetime. Cancelling it after opening closes the local reader.

```go
func streamCommand(ctx context.Context, cfg *webtty.SessionConfig, output func(webtty.ClientSessionEvent)) (int, error) {
	session, err := webtty.OpenClientSession(ctx, cfg)
	if err != nil {
		return -1, err
	}
	defer session.Close()
	for event := range session.Events() {
		output(event)
	}
	return session.Wait()
}
```

Read output promptly until the event channel closes. Each event identifies
stdout or stderr. `Wait` returns the native exit code when the remote process
reports completion. On a transport error or local cancellation it returns
an error.

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

## Bounded detached execution in the SDK

Use a registered server that supports detached execution. Take the
published hostname and advertised command path from the server inventory.
For a token-authenticated WebSocket endpoint, set `endpoint` to that
verified `wss://` URL and use a token authorized to open this terminal.
The Engine API client uses its own authorized context.

```go
cfg := webtty.SessionConfig{
	URL:       endpoint,
	Transport: webtty.WebTTYTransportWebSocket,
	AuthToken:  &terminalToken,
	CmdArgs:    []string{"whoami"},
}
```

For a private CA, supply its trust roots in `cfg.TLSConfig` while keeping
hostname verification. For WebTransport, use the published HTTPS URL and
`webtty.WebTTYTransportWebTransport`. Use the [endpoint trust guide](001-server-trust.md)
when the server requires terminal end-to-end encryption.

Pass the configuration to this helper with an authenticated Engine client
for the registered server.

```go
func startDetached(ctx context.Context, api *rstream.Client, cfg webtty.SessionConfig) (string, error) {
	capabilities, err := api.GetWebTTYCapabilities(ctx)
	if err != nil {
		return "", err
	}
	cfg.Managed = &webtty.ManagedSessionConfig{
		Capabilities:     *capabilities,
		ExecutionSeconds: 300,
	}
	cfg.Interactive, cfg.AllocateTTY = false, false
	session, err := webtty.OpenClientSession(ctx, &cfg)
	if err != nil {
		return "", err
	}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range session.Events() {
		}
	}()
	defer func() {
		_ = session.Close()
		<-drained
	}()
	id := session.ManagedSessionID()
	if err := session.SendEOF(); err != nil {
		return id, err
	}
	return id, session.Disconnect(ctx)
}
```

Keep any returned ID, including when the function reports an error while
closing stdin or disconnecting. Use it to inspect the acknowledged execution.

The SDK rejects unsupported capabilities, transports, conflicting URL options
and excessive durations before opening a connection. `OpenClientSession` returns
only after the relay confirms the ID. Zero `ExecutionSeconds` preserves the
ordinary connection-bound lifetime, including when the Engine does not advertise
the managed protocol. The capability snapshot still prevents unsupported
termination requests. Detached execution supports WebSocket and
WebTransport as advertised by the Engine; it cannot extend an existing Attach.

Pass the saved ID to `Client.GetWebTTYSession` to check the process result.
To rejoin a running command, use the
[participant stream reference](../../webtty/README.md#attach-to-a-live-managed-session).
If the opening response was lost, check the session inventory before
starting another command. Recording policy and permissions determine which
historical output remains available.

## Transports

`SessionConfig.Transport` selects how WebTTY frames the terminal traffic.
This setting is separate from the agent's TLS or QUIC connection to the
Engine. See [CLI session transport](002-session-operations.md#choose-the-webtty-transport)
when selecting a CLI URL or option.

| Go transport | Go integration |
| --- | --- |
| WebSocket | Provide `DialContext` for a private `rstrm://` endpoint. An explicit `DialTLSContext` can establish a secure WebSocket connection. |
| Plain | Use `DialContext` for the TCP socket or private bytestream. The SDK performs TLS for a `tls://` endpoint. |
| WebTransport | Provide `DialPacketContext` for a private endpoint. The SDK verifies the server's TLS certificate. |

Use WebTransport in a FIPS build. For local plain TLS and WebTransport,
server TLS requires version 1.3 or newer.

## E2E Crypto

WebTTY E2E keeps the protobuf session envelope visible while encrypting terminal
payload bytes. The engine can route sessions, record metadata, enforce policy,
and track lifecycle events without plaintext terminal content.

Use the [package encryption reference](../../webtty/README.md#encryption-formats)
when selecting an identity profile or consuming recorded encryption metadata.

Workspace-managed protection uses a separate device grant to control
access to encrypted terminal data. See [workspace device trust](001-server-trust.md#trust-a-cli-for-workspace-managed-webtty)
for provisioning those identities.

The filesystem sidecar (WebDAV or WebRTC) is rejected when WebTTY E2E payload encryption is
active, because it is a separate data surface.
