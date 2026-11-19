# Operate WebTTY sessions

Use these commands after [opening a first session](../009-webtty.md#open-a-terminal). The host, authenticated context and transport determine which operations are available.

## Inspect managed sessions

Use the Engine API to inspect a registered server's sessions. List the
sessions, then replace the example ID with the one you want to inspect:

```sh
rstream webtty sessions list
rstream webtty sessions show 'my-session-id'
```

| Task | Command |
| --- | --- |
| See recorded events | `rstream webtty sessions events my-session-id` |
| Save a readable transcript | `rstream webtty sessions export my-session-id --format text` |
| Save structured events and available plaintext | `rstream webtty sessions export my-session-id --format json --file session-export.json` |
| Join a live session | `rstream webtty sessions join my-session-id` |
| Request control of a terminal | Add `--interactive --request-control` to the join command. Input stays blocked until control is granted. |
| Inspect participants and control requests | Use the `participants` and `control-requests` subcommands with the session ID. |

The Engine must advertise live attachment for joining and control transfer
for requesting control. The `request-control` and `resolve-control` commands
support explicit control requests from automation.

Export depends on recording policy and access. For platform-managed recordings,
the account needs `webtty.logs.read-only`. For end-to-end encrypted recordings,
the operator also needs approved workspace-device material or the local endpoint
identity authorized for that recording. Protect exported plaintext as you would
the original session data.

## Start and retrieve a detached command

```sh
rstream webtty sessions start --url rstrm://build-server --execution-seconds 300 -- sh -c 'make check'
```

This example runs `make check` on the remote server. The server must have the
project checkout and build tools. The CLI prints a session ID in JSON. Run
`rstream webtty sessions show my-session-id` with that ID to see the actual
process result. Join the session while it is running if the Engine supports
live attachment.

Detached execution needs a published WebSocket or WebTransport server, discovery
and an explicit execution budget within the Engine's limit. The command receives
empty standard input and the start operation does not capture its output.
Use `webtty exec` for connection-bound execution and local output capture.
If the start response is lost, check for the existing execution before retrying.
The FIPS profile requires WebTransport for starting a session.

## Stop an existing managed command

```sh
rstream webtty sessions stop 'my-session-id' --reason 'maintenance' --timeout 65s
```

Replace the example ID with the session to stop. The operation requires
control of that session. The result reports a confirmed native `exit_code` or
null if completion is unknown. A local cancellation releases the reader and
can leave a termination request in flight. Read any retained output with
`sessions export`.

Live attachment, including `sessions join` and `sessions stop`, requires a
participant stream supported by the Engine. It is unavailable in the
restricted FIPS profile.

## Recover from a connection interruption

An rstream-backed server reconnects automatically unless you set
`--no-retry`. A control-channel outage can interrupt admission of new
sessions while established sessions continue on their original data paths.
Check new connections separately from already-open terminals. An explicit
server or tunnel stop ends remaining sessions after bounded shutdown. Local
servers retry only if configured to do so.

## Choose the WebTTY transport

The client normally discovers the WebTTY transport through Engine inventory.
The agent's TLS or QUIC connection to the Engine is a separate setting.
An explicit `--transport` must agree with the value advertised by the server.

A lightweight server using the plain transport accepts private rstream
connections. Registered WebTransport servers require a published endpoint.
See the [transport reference](002-go-runtime.md#transports) for direct URLs
and the FIPS and detached-execution publication requirements.

## Connect without discovery

A device with a token scoped to private dialing can use an explicit Engine
context and local trust material without inventory access. Supply the exact
tunnel name or ID and the server's actual transport. Set `--exec-path` if
the server uses a non-default terminal endpoint. Use the
[configuration reference](000-configuration.md#environment-variables-and-local-trust-sources)
to provide a pinned server identity and your local client identity.

### Prepare workspace access for an Engine-only device

On an approved device with Control plane access, prepare the files it needs
for a workspace-managed server:

```sh
rstream webtty client prepare 'my-server-id' --directory ./webtty-access
```

Replace the example ID with the registered server ID. The command creates a
new private directory containing the endpoint identity, known-server entry and
signed client credential. Keep these files on the approved device. Configure
the Engine address, certificate trust and restricted token separately.

```sh
rstream webtty exec --url rstrm://my-tunnel \
  --no-discovery --transport webtransport \
  --identity-file ./webtty-access/identity.json \
  --known-servers-file ./webtty-access/known-servers.json \
  --client-credential-file ./webtty-access/client-credential.json \
  -- printf 'engine-only access\n'
```

Replace `my-tunnel` with the advertised tunnel name or ID. The default tunnel
name for a registered server is its server ID. A display name requires
discovery. Choose the server's actual transport and configure an explicit
Engine address without a region selector.

## Access files

Use `webtty fs` with a server that has a filesystem sidecar. The sidecar
requires WebSocket terminal transport and works when terminal end-to-end
encryption is disabled. Choose WebDAV for writes or
[WebRTC file access](../021-filesystem-webrtc.md) for read-only transfers.

The CLI and local MCP discover the server's backend and path. With
`webtty fs --no-discovery`, supply the exact tunnel ID or name and the
filesystem path if it differs from `/fs`. For sharing files without a
terminal, use [the standalone file-sharing command](../008-file-sharing.md).
