# Operate WebTTY sessions

Use an authenticated project context with access to a
[registered WebTTY server](001-server-trust.md#enroll-a-registered-webtty-server)
for managed session operations. To verify a lightweight server first,
[open a terminal](../009-webtty.md#open-a-terminal).

## Inspect managed sessions

Use the Engine API to inspect a registered server's sessions. List the
sessions in the current project.

```sh
rstream webtty sessions list
```

Replace `my-session-id` with the ID of the session to inspect.

```sh
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

Use a published WebSocket or WebTransport server on a deployment that
supports detached execution.

```sh
rstream webtty sessions start --url rstrm://build-server --execution-seconds 300 -- whoami
```

This example runs `whoami` on the remote server. The CLI prints the session ID in JSON. Run
`rstream webtty sessions show my-session-id` with that ID to see the actual
process result. Join the session while it is running if the Engine supports
live attachment.

Use discovery and set an execution budget within the Engine's limit. The command receives
empty standard input and the start operation does not capture its output.
Use `webtty exec` for connection-bound execution and local output capture.
If the start response is lost, check for the existing execution before retrying.
The FIPS profile requires WebTransport for starting a session.

## Stop an existing managed command

Use an identity that can grant a new control request for the session. Its
Engine and server must support explicit termination, and the Engine must
support live attachment and control transfer. See
[collaboration permissions](https://rstream.io/docs/webtty/live-collaboration#product-permissions).

```sh
rstream webtty sessions stop 'my-session-id' --reason 'maintenance' --timeout 65s
```

Replace the example ID with the session to stop. The result reports a confirmed native `exit_code` or
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
Use the explicit URI that matches the server's endpoint when you connect
directly.

| Client URI | Meaning | Condition |
| --- | --- | --- |
| `rstrm://<tunnel-id-or-name>` | Resolve inventory/security, then dial through rstream. | Standard live commands use private dial; see exceptions below. No public DNS name is needed. |
| `ws://host:port/path`, `wss://host:port/path` | Direct WebSocket endpoint. | `wss` verifies TLS; direct E2E needs explicit/local known-server trust. Default CLI URL is `ws://127.0.0.1:8080`. |
| `tcp://host:port`, `tls://host:port` | Direct plain WebTTY stream, with TLS for the latter. | Plain transport; default ports 80/443, use explicit port for local listeners. |
| `https://host/path` | Direct WebTransport endpoint. | Requires `--transport webtransport`; port 443 if omitted. `wt://`, `wts://` and `webtransport://` are accepted aliases with the same override. |
| `wss://host/path` with `--transport webtransport` | Same endpoint rewritten to HTTPS for WebTransport. | The server must actually provide WebTransport. |

The standard client opens a private stream or datagram for `rstrm://`,
including when that tunnel also has a published endpoint. The FIPS client
resolves its target to a verified HTTPS/WebTransport endpoint. A detached
command uses a published WebSocket or WebTransport endpoint so the Engine
can own its execution. Authentication and recording policy still apply.

## Connect without discovery

Use the standard CLI for this private connection. A device with a token scoped to private dialing can use an explicit Engine
context and local trust material without inventory access. Supply the exact
tunnel name or ID and the server's actual transport. Set `--exec-path` if
the server uses a non-default terminal endpoint. Use the
[configuration reference](000-server-configuration.md#environment-variables-and-local-trust-sources)
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

For a workspace-owned certificate, provision the workspace Token's service
device with an owner or admin login. Run these commands on the device that
prepares the terminal keys. Substitute the workspace ID, workspace Token
ID, project ID and registered server ID.

```sh
rstream workspace device enroll --workspace 'my-workspace-id' --for-token 'my-token-id'
```

Approve the displayed code under **Workspace Protection**, in the
**Services** tab. Check the approval, then prepare the files for that device.

```sh
rstream workspace device status --workspace 'my-workspace-id' --for-token 'my-token-id'
rstream webtty client prepare 'my-server-id' --project-id 'my-project-id' \
  --for-token 'my-token-id' --directory ./webtty-access
```

Use those generated files on the corresponding runtime host with its
certificate context. Keep the provisioning login separate. The identity
and client credential can also authorize decryption of recordings from the
same workspace, project and server. Engine authorization for
`webtty.logs.read-only` still applies. Set `RSTREAM_WEBTTY_IDENTITY_FILE` and
`RSTREAM_WEBTTY_CLIENT_CREDENTIAL_FILE` to the prepared files when
exporting an encrypted recording.

For FIPS, prepare a P-256-compatible identity and use a published
WebTransport server. For certificate access, start it with
`--transport webtransport --mtls`. Keep discovery enabled on the client so
it can obtain the published endpoint.
See the [FIPS compatibility guide](../017-fips-140-3-profile.md#supported-scope).

## Access files

Use `webtty fs` with a server that has a filesystem sidecar. The sidecar
requires WebSocket terminal transport and works when terminal end-to-end
encryption is disabled. Choose WebDAV for writes or
[WebRTC file access](../021-filesystem-webrtc.md) for read-only transfers.

The CLI and local MCP discover the server's backend and path. With
`webtty fs --no-discovery`, supply the exact tunnel ID or name and the
filesystem path if it differs from `/fs`. For sharing files without a
terminal, use [the standalone file-sharing command](../008-file-sharing.md).
