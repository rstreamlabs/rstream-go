# Operate WebTTY sessions

Use these commands after [opening a first session](../009-webtty.md#open-a-terminal). The host, authenticated context and transport determine which operations are available.

## Inspect managed sessions

Registered WebTTY servers expose managed session metadata, live coordination,
and recorded events through the engine API. Durable registered-server inventory
and workspace trust decisions remain control-plane concerns; live terminal bytes
stay on the WebTTY protobuf data path. The CLI can inspect what is implemented
today:

```bash
rstream webtty sessions list
rstream webtty sessions show session_id
rstream webtty sessions events session_id
rstream webtty sessions export session_id --format text
rstream webtty sessions export session_id --format json --file session-export.json
rstream webtty sessions participants session_id
rstream webtty sessions join session_id
rstream webtty sessions join session_id --interactive --request-control
rstream webtty sessions control-requests session_id
rstream webtty sessions request-control session_id --participant-id participant_id
rstream webtty sessions resolve-control session_id request_id --action grant
```

`sessions list` accepts operator filters such as `--server-id`, `--tunnel-id`,
`--user-id`, `--group-id`, `--origin`, `--status`, `--started-from`, and
`--started-to`. `sessions events` returns recorded event metadata and
ciphertext according to the current account permissions. `sessions export`
builds local artifacts from the recorded event stream:

- `--format text` writes a readable terminal transcript. Closed alternate-screen
  applications such as `htop` are omitted from the transcript, matching terminal
  scrollback behavior. If the session is still in alternate screen, the current
  alternate-screen snapshot is included by default.
- `--format json` writes JSON with the session metadata, all recorded events,
  original ciphertext, and decrypted plaintext bytes when local decrypt material
  is available. Plaintext bytes are base64-encoded by JSON.

For platform-managed recordings, export uses payload material returned by the
engine API when the current token has `webtty.logs.read-only`. For E2E
recordings, export uses the same local decrypt path as live join: trusted
workspace devices first, then local WebTTY endpoint identities that match a
`public_key` grant. A session that cannot be decrypted by either path fails
explicitly before writing a partial export.

`sessions join` first checks the engine capability document and refuses to run
unless `live_attach` is advertised. When enabled, it joins the live WebTTY data
path through the engine-managed participant stream; it does not connect directly
to the WebTTY server. Control requests are created and resolved through the HTTP
control API, while terminal bytes continue to use the WebTTY protobuf stream.
For an operator workflow, prefer
`sessions join --interactive --request-control`; it attaches the current CLI as
a participant, waits for the participant stream to become live, requests
control, and keeps stdin gated until the control request is granted. The
standalone `request-control` and `resolve-control` commands keep explicit ids
for automation, admin tooling, and test harnesses. Pass
`--approver-participant-id` when the current controller is granting the request;
omit it for permission-based owner/admin approval. The CLI uses a WebSocket
participant stream and checks the Engine's live/attachable state, including for
executions originally opened over WebTransport. The Engine rejects unsupported
participant transports before issuing an attach grant. Commands that request or resolve control first check
`control_transfer`; read-only metadata commands can still inspect stored
participants and requests. Go and JS clients both send an explicit `Attach`
protobuf message as the first frame after the transport opens; direct WebTTY
servers reject that message because spectators and control transfer are
engine-managed capabilities.

For E2E managed sessions, `sessions join` resolves decryption material at
runtime. It first tries active workspace devices stored under the session
workspace and requests only matching `workspace_device` key grants from the
engine. It then tries local WebTTY endpoint identities as `public_key`
recipients. If no local trusted device or endpoint identity can decrypt the
session grant, the command fails before opening terminal control.

## Start and retrieve a detached command

```bash
rstream webtty sessions start --url rstrm://build-server --execution-seconds 300 -- sh -c 'make check'
# Substitute the returned session ID in the two commands below.
rstream webtty sessions show 'my-session-id'
rstream webtty sessions join 'my-session-id'
```

`sessions start` returns JSON with the Engine-acknowledged `session_id` and
disconnects its reader. It requires an explicit budget of 1–86400 seconds,
within the Engine's advertised maximum. The CLI discovers the target through
the configured Engine and uses its published WebSocket or WebTransport endpoint;
private direct dialing cannot provide this managed lifetime. Unpublished
targets, disabled discovery, interactive/TTY options and unsupported capabilities
fail before Open. Existing endpoint identity and E2E requirements still apply.

The command receives empty stdin; local piped input is not forwarded. Output is
not captured by the start operation. Authorized recording export remains subject
to the server's recording policy and retention. Use ordinary `webtty exec` for
connection-bound execution and local output capture.

An acknowledged start is not process completion. Read the saved session to find
its actual result, or attach while it is still running. If the opening response
is lost, the command may already be running: do not automatically retry start.
No idempotency key or business-level deduplication is supplied. The start operation
has a 65-second bound independent of the remote execution budget. In the FIPS
profile it requires WebTransport, like `webtty exec`; live participant join and
stop retain their separate restriction below.

## Stop an existing managed command

```bash
rstream webtty sessions stop 'my-session-id' --reason "maintenance" --timeout 65s
```

The command joins the existing execution, requests and resolves control using
Engine's existing session-control permission, then sends Terminate. It never
reopens the command or forwards stdin. Older Engine/native versions fail
explicitly; a refused control grant sends no termination request. The CLI
prints JSON with `session_id` and the confirmed `exit_code`, or null when the
result is unknown. Successful native completion makes the stop operation
successful even if the process returned a nonzero code. An already closed
session returns its stored native result without another attachment.
If Engine has already removed the stop reader after native completion, its
cleanup response does not invalidate that confirmed result. Other cleanup
failures remain errors and preserve the native `exit_code` in the JSON output.

The wait is at most 65 seconds, followed by up to five seconds to release the
participant record. Cancellation releases the reader; it cannot retract an
already delivered termination request. Final output is drained without keeping
an output buffer; read authorized recordings separately with `sessions export`.
Other readers stay attached until the native process ends. Like `sessions join`,
`sessions stop` is unavailable in the restricted FIPS profile because live
participant attachment does not yet support WebTransport.

## Recover from a connection interruption

An rstream-backed server reconnects automatically unless you set `--no-retry`. A control-channel outage can interrupt admission of new sessions while established sessions continue on their original data paths. Check new connections separately from already-open terminals. An explicit server or tunnel stop ends remaining sessions after bounded shutdown. Local servers retry only if configured to do so.

## Connect without discovery

`rstream.webtty.transport=plain|websocket|webtransport` describes the single
WebTTY transport actually served by this tunnel. It is independent of the
TLS/QUIC transport used to reach the rstream engine and of the E2E policy.
Both lightweight and registered servers publish it before signing admission.
The inventory exposes it as `transport`, alongside `tunnel_type` and
`http_version`.

CLI `client`/`exec`, UI and local MCP resolve this information before opening a
session or producing E2E proofs. An explicit `--transport` must agree with an
advertised transport. Unknown labels and contradictory tunnel properties fail
without a fallback. For older servers without the label, a WebTTY datagram
tunnel implies WebTransport; other tunnels retain the historical WebSocket
default and accept an explicit override for ambiguous legacy `plain` servers.

```bash
rstream webtty server --rstream --name shell --transport webtransport
rstream webtty exec --url rstrm://shell -- whoami
```

Standard live `rstrm://` commands use a private dial, including when the tunnel is published; the FIPS and detached-start exceptions are described in [Transports](002-go-runtime.md#transports).
Lightweight `plain` servers create private raw bytestream tunnels; explicitly
publishing one is rejected. A browser cannot open this raw transport. Registered
WebTransport servers currently require publication, but their `rstrm://` address
supports standard live private dialing over TLS or QUIC with managed session recording.

Transport discovery uses the engine inventory. Workspace enrollment and online
credential resolution may use the control plane. An installed device with a
stream-only token can bypass both inventories and control-plane resolution:

```bash
rstream webtty exec --url rstrm://shell \
  --no-discovery --transport webtransport \
  --known-server-key "$SERVER_ENDPOINT_IDENTITY" \
  --identity-file /etc/rstream/client.identity.json \
  --client-credential-file /etc/rstream/client.credential.json \
  -- whoami
```

Use an explicit engine context without a region selector for this path. Supply
`--exec-path` if the server uses a non-default HTTP endpoint. Manual E2E does not
need a workspace client credential. Workspace E2E needs a valid signed local
credential; this option does not enroll or approve a device, change server
trust, or bypass engine/server authorization. The local MCP accepts `transport`
and `no_discovery`, with local trust, identity and credential environment
variables. `RSTREAM_WEBTTY_CLIENT_CREDENTIAL_FILE` selects a credential file
for CLI, UI and local MCP.

`RSTREAM_DATA_DIR` selects an absolute directory for local WebTTY identities,
known servers, enrollments and workspace device state. Its default is
`~/.rstream`. The main connection configuration still uses `--config` or
`RSTREAM_CONFIG`. This permits isolated device/test state without changing the
user's home directory.
## Prepare workspace access for an Engine-only device

Workspace enrollment and approval can use the control plane during setup. Runtime
connections do not require it when the device has its engine configuration, a
token permitting private dial, and local endpoint identities and credentials.

On the enrolled and approved device, prepare a registered workspace-managed server:

```sh
rstream webtty client prepare 'my-server-id' --directory ./webtty-access
```

This command uses the control plane to resolve the server and the approved local
device. It creates a new private directory containing `identity.json`,
`known-servers.json` and `client-credential.json`. It refuses to overwrite an
existing directory. The identity contains private keys: keep these files on the
approved device and protect them like its other credentials. No account or engine
token is exported.

After preparation, configure the engine address, TLS trust and restricted token
locally, then connect without discovery:

```sh
rstream webtty exec --url rstrm://my-tunnel \
  --no-discovery --transport webtransport \
  --identity-file ./webtty-access/identity.json \
  --known-servers-file ./webtty-access/known-servers.json \
  --client-credential-file ./webtty-access/client-credential.json \
  -- printf 'engine-only access\n'
```

Replace 'my-server-id' and 'my-tunnel' with the registered server ID and the running tunnel name or ID. The default
tunnel name of a registered server is its server ID; its display name requires
inventory resolution and is not an alias for engine-only dialing. Choose the
server's actual transport. `--no-discovery` requires an explicit engine address
instead of a region selector. The prepared files also work with the C++ client
for its supported transports (`plain` and `websocket`), using `--uri` instead of
`--url`. Preparation does not grant access beyond the workspace device and token
policies enforced by the server and engine.


## Access files

For read-only file sharing without a terminal, use [`rstream files`](../008-file-sharing.md). WebTTY's filesystem/WebDAV handler retains its write options; SDK embedders must close `NewFileSystemHandler` through `io.Closer` after stopping HTTP requests.

`webtty server --fs-root ./exports --fs-backend webdav` retains the existing writable sidecar (unless `--fs-read-only` is set). Select `--fs-backend webrtc` for read-only Pion DataChannel transfers with rstream STUN/TURN. Upload, write, mkdir, rename, copy and delete fail explicitly in that mode; there is no write fallback. `--fs-max-upload-size` cannot be configured for WebRTC. Terminal `--execution-mode` is independent.

CLI `webtty fs` and MCP filesystem tools discover the backend automatically. SDK `WebTTYFileSystem` uses the same `RemoteFileSystem` transport as the standalone file browser. `webtty list -o json` advertises `fs_backend`, `fs_mode` and `fs_path`; missing backend labels on older servers default to WebDAV. Filesystem E2E remains unsupported. See [WebRTC file access](../021-filesystem-webrtc.md).

The filesystem sidecar currently requires WebSocket terminal transport. The WebTransport server does not yet mount the WebDAV or WebRTC signaling endpoints; selecting WebRTC does not remove this limitation. With `webtty fs --no-discovery`, a restricted stream token can use an explicit tunnel id/name and `--fs-path` without inventory access. The default discovery path also accepts standalone `rstream files` HTTP tunnels.
