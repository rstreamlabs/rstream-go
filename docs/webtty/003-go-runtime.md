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

| Task | Go API |
| --- | --- |
| Stop reading a session | Call `ClientSession.Close()` to close its reader. Explicitly detached executions retain their original deadline. |
| Disconnect after sending input | Drain `Events()` and call `Disconnect(ctx)` so the transport can finish sending. |
| Stop the remote process and read final output | Check `CanTerminate()`, call `RequestTermination(ctx)` with a controlling participant, and keep reading until `Wait()` returns the result. |

For live attachment, open the
[participant stream](../../webtty/README.md#attach-to-a-live-managed-session)
using the existing execution ID. The
[Go package lifecycle reference](../../webtty/README.md#managed-disconnect-and-termination-contracts)
describes disconnect guarantees, capability checks and cancellation.

## Bounded detached execution in the SDK

[Enroll a registered server](001-server-trust.md#enroll-a-registered-webtty-server)
on a managed deployment that supports detached execution. On the operator
machine, create an Engine client from the selected project with
`config.NewClientFromEnv()`. Use `ListTunnels` and
`webtty.ParseServers(*tunnels)` to select the intended registered server by
ID (`ServerID`). Use its `Host` and `ExecPath` to construct the published `wss://` URL.
Select a server with a published WebSocket endpoint for this example.

Set `terminalToken` to a credential that can open the published terminal,
for example a [token scoped to that tunnel](https://rstream.io/docs/tunnels/http-tunnels/authentication#token-authentication).
For Engine inventory, grant `tunnels.resources.read-only`. For inspecting
the execution and joining as a spectator, grant `webtty.sessions.read-only`
for that server. Control requires `webtty.sessions.read-write`. An operator
token can serve both roles when it grants the published and Engine access.

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
For live attachment, use a command that stays running long enough to join.
To rejoin that command, use the
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
