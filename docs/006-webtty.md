# WebTTY CLI and Runtime Reference

WebTTY exposes remote terminal sessions through rstream. A WebTTY server can run
as a Tunnel WebTTY server or as a registered server used by managed WebTTY
features.

## User Journeys

### Expose a Tunnel WebTTY Server

Use this path when you want a terminal quickly through the existing HTTP tunnel
model. The tunnel is published over HTTP/WebSocket with WebTTY inventory labels.

```bash
rstream webtty server -v --rstream --name shell
```

Private tunnel access remains available:

```bash
rstream webtty server -v --rstream --name shell --no-publish
rstream webtty client --url rstrm://shell -- whoami
```

### Require E2E Terminal Encryption

Create a server endpoint identity and a client endpoint identity, authorize the
client signing key on the server, trust the server endpoint identity on the
client, then run the server in protected mode.

```bash
rstream webtty identity create --name dev-shell
rstream webtty identity show --name dev-shell --endpoint-identity
rstream webtty identity create --name client-laptop
rstream webtty identity show --name client-laptop --endpoint-identity
rstream webtty identity list
rstream webtty known-server add dev-shell \
  --key server_endpoint_identity \
  --client-identity client-laptop
rstream webtty authorized-client add client-laptop --identity dev-shell --key client_endpoint_identity
rstream webtty server -v --rstream --name dev-shell --identity dev-shell
rstream webtty exec --url rstrm://dev-shell -- whoami
```

If explicit server key material is provided to the client, E2E is implied. If
server identity material is provided to the server, protected mode is implied.
In the normal operator workflow, the server identity and local known-server
store are enough for `rstrm://...` targets; the user does not pass an E2E flag
on every client command. Direct `ws://...` targets do not have inventory labels,
so they need a local known-server selector or explicit server trust material.
`--e2e` remains useful as a fail-closed assertion for scripts: the client exits
before connecting if no known server key or workspace-managed server key can be
resolved.

The client never creates a trust-on-first-use entry for authenticated E2E. If a
lightweight E2E server is not already present in `known_servers.json`, `exec`,
`client`, MCP, C++, and `rstream ui` fail before terminal content is sent. In
`rstream ui`, the identity picker appears only after the server is already
pinned and only when the known-server entry does not yet name a local
`client_identity`.

For a direct local WebSocket endpoint, select the same pinned server explicitly
because no rstream inventory labels are available:

```bash
rstream webtty exec --url ws://127.0.0.1:8080 --known-server dev-shell -- whoami
```

The full known-server endpoint value is public server identity material. It
lets the client verify the server proof and wrap the terminal session key for
that server. The authorized-client value is public client signing material
extracted from the client endpoint identity. It lets the server verify the
client proof before starting any process. Passing only
`encryption_key_id:encryption_public_key` remains useful for encryption-only
clients, but authenticated E2E requires the full endpoint value.

Remove a local server identity when the server key has been retired:

```bash
rstream webtty identity remove dev-shell
```

### Enroll a Registered WebTTY Server

Create the registered server from an authenticated project context, then run
the enrollment command returned by the CLI, product, or API on the host that
will run WebTTY.

If the current machine creates and runs the server, select the project once and
use the single-command path:

```bash
rstream project use project-endpoint
rstream webtty server create prod-shell --enroll
```

If the server record is created from another machine, run the printed
enrollment command on the runtime host:

```bash
rstream project use project-endpoint
rstream webtty server create prod-shell
```

```bash
rstream webtty server enroll server_id
```

Enrollment uses the local authenticated rstream context. It fails before
writing local state if the account cannot access the target project or if the
active context points to a different workspace. The generated enrollment file
defaults to:

```text
~/.rstream/webtty/enrollments/<server-id>.yaml
```

Keep that enrollment file and the referenced server identity together. If the
server identity is lost, create a new registered server and enroll it instead of
reusing the old server id. This keeps identity replacement explicit rather than
turning it into a silent key reset path.

Start the registered server with:

```bash
rstream webtty server -v --server-id server_id --login-user operator
```

`--server-id` implies `--rstream` and starts a managed `protocol=webtty` tunnel.
Registered servers publish by default; add `--no-publish` when the server should
stay reachable only through private rstream dials. Use `--server-enrollment`
only when the enrollment file is not at the default path.

Registered servers default to `login` execution mode. Configure `--login-user`
or `--allow-client-user` explicitly, or set `--execution-mode=spawn` when the
server should intentionally run commands as children of the server process.
The `--login-user` value is the username of an existing account on the server
machine. It selects the OS identity, home directory, and shell used for every
session; it is not the rstream account or the remote caller's username.

### Trust a CLI for Workspace-Managed WebTTY

Workspace-managed registered servers require a trusted workspace browser,
operator CLI, automation agent, or service device before protected WebTTY data
can be opened. A host that runs a workspace-managed WebTTY server must also be
a trusted workspace device before server enrollment. Enroll the local machine
once per workspace, approve the displayed verification code from a trusted
browser, then refresh the local status.

```bash
rstream workspace list
rstream project use project-endpoint
rstream workspace device enroll --label ops-cli
rstream workspace device status
```

`workspace device enroll` and `workspace device status` infer the workspace
from the active project context. Pass `--workspace workspace_id` only for
scripts or machines that intentionally do not have an active project context.

The default device kind is `cli`. Use `--kind agent` for an automation runtime
that acts on behalf of a workspace user, and `--kind service` for a
non-interactive daemon or service-account recipient. Browsers are trusted from
the workspace protection page, not with this command.

After approval, regular clients resolve E2E at runtime. The user does not need
to decide whether a server is encrypted before connecting:

```bash
rstream webtty exec --url rstrm://prod-shell -- whoami
rstream ui
```

In `rstream ui`, press `c` from the inventory to select another configured
context or a project discovered through the active Control Plane API. The
picker separates contexts (`1`) from projects (`2`); `Tab` toggles the views,
and the `CURRENT` and `DEFAULT` columns make both states explicit. `Enter` uses
the target only for the current UI process; `d` also saves it as the default
context. The WebTTY inventory and all subsequent session security resolution
use the newly selected runtime, including workspace-managed device proofs and
E2E key resolution. Close an open terminal with `Ctrl+g q` before switching
context.

The context picker remains usable when Control plane discovery is unavailable.
Unlinked contexts and project-scoped contexts can therefore switch Engine
inventory and open directly resolvable WebTTY servers without a Control plane.
Only workspace-managed trust paths that inherently require Control plane access
will fail, with the original context and inventory left active.

If the workspace requires E2E and the CLI is not trusted, the client fails before
opening the terminal and prints the enrollment command to run.

For workspace-managed servers, enrollment pins workspace trust automatically
from the local trusted device. If the machine later loses trusted-device state
and is trusted again, refresh the server trust pin with:

```bash
rstream webtty server trust server_id
```

`server trust` is a repair path. It does not allow an untrusted machine to run a
workspace-managed WebTTY server.

### Inspect Managed Sessions

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
omit it for permission-based owner/admin approval. The current CLI live join
path supports WebSocket-backed managed sessions; non-WebSocket sessions are
rejected before opening a terminal stream. The engine also rejects non-WebSocket
participant transports before issuing an attach grant, so clients do not receive
unusable grants. Commands that request or resolve control first check
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

### Run from a Daemon Config

Use `--webtty-config` or `RSTREAM_WEBTTY_CONFIG` when the server is managed by a
service manager and a long command line would be brittle.

```yaml
version: 1
server:
  serverId: srv_prod_shell_01
  transport: websocket
  executionMode: login
  loginUser: operator
  retry: true
  labels:
    env: production
    role: bastion
```

Run it with:

```bash
rstream webtty server -v --webtty-config /etc/rstream/webtty/prod-shell.yaml
```

CLI flags override YAML values. `server.serverId` loads the matching enrollment
from `~/.rstream/webtty/enrollments/<server-id>.yaml`. `server.serverEnrollment`
can point to a non-default enrollment path.

## Local Files

WebTTY files live under the WebTTY subtree, separate from workspace-managed
keys:

```text
~/.rstream/webtty/identities/<name>.identity.json
~/.rstream/webtty/authorized_clients/<name>.json
~/.rstream/webtty/enrollments/<server-id>.yaml
~/.rstream/webtty/known_servers.json
```

Workspace keys are broader product keys and are stored in the workspace key
hierarchy, not in the global WebTTY subtree:

```text
~/.rstream/workspaces/<workspace-id>/devices/<device-id>.json
~/.rstream/workspaces/<workspace-id>/webtty/identities/<device-id>.identity.json
```

The first file is the trusted workspace device record and signing material. The
second file is the X25519 WebTTY identity bound to that workspace device for
workspace-managed terminal decrypt grants. It is intentionally scoped under the
workspace tree; `~/.rstream/webtty/identities` is reserved for local WebTTY
server identities and explicit-key/direct WebTTY trust workflows.

Local WebTTY identity, enrollment, known-server, and workspace-device files
are written with `0600` file permissions on POSIX systems. Runtime reads reject
files that are accessible by group or others so local trust roots cannot be
silently weakened after creation.

## Environment Variables

Use environment variables for containerized or CI deployments:

```text
RSTREAM_WEBTTY_CONFIG
RSTREAM_WEBTTY_AUTH_TOKEN
RSTREAM_WEBTTY_IDENTITY
RSTREAM_WEBTTY_IDENTITY_FILE
RSTREAM_WEBTTY_AUTHORIZED_CLIENT_KEYS
RSTREAM_WEBTTY_AUTHORIZED_CLIENTS_FILE
RSTREAM_WEBTTY_KNOWN_SERVER_KEY
RSTREAM_WEBTTY_KNOWN_SERVERS_FILE
```

`RSTREAM_WEBTTY_AUTH_TOKEN` and `--auth-token-file` protect only local WebTTY
WebSocket/WebTransport servers. Registered servers use rstream Engine
authentication and reject local bearer-token configuration.

`RSTREAM_WEBTTY_IDENTITY` contains an inline endpoint identity JSON document.
Use it for container or secret-manager environments where mounting a separate
file is inconvenient. Do not combine it with `--identity`, `--identity-file`, or
`RSTREAM_WEBTTY_IDENTITY_FILE`.

Server identity file resolution is:

```text
--identity-file
--identity
RSTREAM_WEBTTY_IDENTITY_FILE
registered server enrollment identityFile
~/.rstream/webtty/identities/default.identity.json for ad hoc servers started with --e2e and no explicit identity
```

The identity document must contain both the WebTTY encryption identity and the
WebTTY signing identity.

E2E servers require an authorized client source. The normal operator path is
`rstream webtty authorized-client add`, which writes the default server-specific
file under `~/.rstream/webtty/authorized_clients`. CI and container deployments
can use `--authorized-client-key`, `RSTREAM_WEBTTY_AUTHORIZED_CLIENT_KEYS`,
`--authorized-clients-file`, or `RSTREAM_WEBTTY_AUTHORIZED_CLIENTS_FILE`.

Authorized client resolution for a server identity named `dev-shell` is:

```text
--authorized-client-key
RSTREAM_WEBTTY_AUTHORIZED_CLIENT_KEYS
--authorized-clients-file
RSTREAM_WEBTTY_AUTHORIZED_CLIENTS_FILE
~/.rstream/webtty/authorized_clients/dev-shell.json
```

The file is read at handshake time. Adding or removing clients affects new
sessions without restarting the WebTTY server.

Client identity file resolution uses the same endpoint identity sources, without
the registered server enrollment step. For authenticated E2E, the client does
not invent a default identity; use an explicit source or a target-scoped
known-server `client_identity` association.

```text
--identity-file
--identity
RSTREAM_WEBTTY_IDENTITY_FILE
RSTREAM_WEBTTY_IDENTITY
known-server client_identity -> ~/.rstream/webtty/identities/<name>.identity.json
```

Client known server resolution is:

```text
--known-server
--known-server-key
RSTREAM_WEBTTY_KNOWN_SERVER_KEY
--known-servers-file
RSTREAM_WEBTTY_KNOWN_SERVERS_FILE
workspace-managed server resolution for registered servers, when available
~/.rstream/webtty/known_servers.json when no explicit source was configured
```

`--known-server <name>` selects one local known-server entry for direct URLs
without calling the control plane.

`--known-server-key` and `RSTREAM_WEBTTY_KNOWN_SERVER_KEY` accept either
`encryption_key_id:encryption_public_key` or the full authenticated endpoint
form
`encryption_key_id:encryption_public_key:signing_key_id:signing_public_key`.

## Transports

Supported Go WebTTY transports are:

```text
plain, websocket, webtransport
```

Local plain TLS and local WebTransport use TLS 1.3 minimum. Published registered
servers use managed rstream WebTTY through the engine.

## Execution Modes

`spawn` starts commands as children of the WebTTY server process. It preserves
client working-directory and environment overrides and uses the server process
identity.

`login` is passwordless but it is not userless. Configure the username of an
existing local account with `--login-user <username>` (or `loginUser` in YAML).
Every session is resolved against that account and receives its OS identity,
home directory, and shell. Registered servers default to `login`, but still
require a configured target user unless `--allow-client-user` is explicitly
enabled.

On Unix-like systems, switching to a different account applies the target uid,
primary gid, and supplementary groups and requires the server process to have
the corresponding privileges. On Windows, `login` supports the same Windows
account that runs the server; switching to a different account is not supported.

The server builds a conservative administrative environment instead of copying
its complete environment. Sessions receive `PATH`, locale and timezone values,
and the resolved identity/home/shell. Linux also receives a validated
`XDG_RUNTIME_DIR` and user D-Bus address when the account has an active user
runtime; macOS preserves the current user's `TMPDIR` and Core Foundation text
encoding; Windows receives the standard profile, system, temp, and PowerShell
module variables. SSH agent sockets, cloud credentials, and rstream tokens are
not forwarded automatically.

## E2E Crypto

WebTTY E2E keeps the protobuf session envelope visible while encrypting terminal
payload bytes. The engine can route sessions, record metadata, enforce policy,
and track lifecycle events without plaintext terminal content.

Current helper suite:

```text
Payload: AES-256-GCM with a fresh 96-bit nonce per WebTTY data message
Key envelope: HPKE Base mode with DHKEM(X25519, HKDF-SHA256), HKDF-SHA256, AES-256-GCM
```

The protocol reserves ChaCha20-Poly1305 suite identifiers for future support.
Current helpers reject those suites until they are implemented and tested.

Recorded E2E events expose both `key_context` and `key_context_raw`.
`key_context` is for inspection and may be represented as JSON. Consumers that
reconstruct an E2E session envelope for replay, join, or decrypt-material flows
must use `key_context_raw`, which is the unpadded base64url encoding of the
exact HPKE/AAD context bytes used by the endpoint.

Workspace-managed protection uses separate workspace-device envelopes. Those
envelopes use ECDH P-256 recipient keys, HKDF-SHA-256, and AES-256-GCM for
browser and CLI device grants so they remain compatible with WebCrypto. That
workspace envelope suite is distinct from the WebTTY terminal payload HPKE suite
above.

The WebDAV filesystem sidecar is rejected when WebTTY E2E payload encryption is
active, because it is a separate data surface.

Registered servers use local WebTTY enrollment and identity files under
`~/.rstream/webtty`. When the control plane marks a registered server as
workspace-managed, the runtime host must be a trusted workspace device before
server enrollment. The server still uses its local WebTTY identity and pins the
public workspace keyset identity in its enrollment. If local trust material
changes after enrollment, refresh the pin:

```bash
rstream webtty server trust server_id
```

That pin lets the server verify workspace-managed WebTTY client proofs against
public workspace trust material. It does not copy workspace private keys to the
remote host, and clients still connect without passing WebTTY keys on each
command. It is a repair command, not a way to run a workspace-managed server
from an untrusted machine.

## Runtime Checks

The WebTTY runtime matrix validates direct Go, JS, and C++ interop across
WebSocket, plain bytestream, WebTransport, E2E, plaintext rejection, config, and
Go/C++ login behavior:

```bash
bash test/e2e/webtty-runtime-matrix.sh
```

The CLI workflow check validates local identity creation, known-server and
authorized-client management, default `.rstream/webtty` file layout, inferred
E2E, environment runtime config, workspace discovery, active-project workspace
inference, WebDAV/E2E rejection, and public help surface hygiene:

```bash
bash test/e2e/webtty-cli-workflows.sh
```

The `rstream ui` WebTTY path is covered by `cmd/rstream/cmd` tests. The runtime
tests cover context and project discovery without mandatory Control plane
access, temporary and persistent switching, Engine readiness and failure,
known-server client identity selection, missing-known-server fail-closed
behavior, post-ACK identity persistence, and workspace-managed resolution. The
workspace runtime test starts a real encrypted WebTTY server, signs a workspace
device proof, resolves the workspace-managed server key through a control-plane
test double, opens the session through the same configuration path used by
`rstream ui`, and verifies decrypted terminal output:

```bash
go test ./cmd/rstream/cmd -run 'TestUIRuntime|TestUITarget|TestUIWebTTYSessionConfigRequiresKnownServerForLightweightExplicitKey|TestUIWebTTYSessionConfigRequestsIdentitySelectionWhenKnownServerHasNoClientIdentity|TestUIWebTTYSessionConfigSelectedIdentityCanBeRememberedAfterAck|TestUIWebTTYSessionOpensWorkspaceManagedE2ERuntime$'
```

The local MCP `rstream_webtty_exec` path builds the same WebTTY client config as
the CLI before command execution. Its tests assert that plain sessions remain
plain when no trust signal exists and that resolved WebTTY labels, workspace
trust, or explicit known server keys enable E2E payload crypto without a
separate MCP-specific option.

Current validation commands:

```bash
go test ./...
bash test/e2e/webtty-cli-workflows.sh
bash test/e2e/webtty-runtime-matrix.sh
```

Windows runtime coverage lives in `test/e2e/webtty-windows-runtime.ps1`. It
validates WebSocket, plain, plain-over-TLS, WebTransport, current-user login,
explicit-key E2E, unauthorized clients, duplicate-error-log prevention, and
Go/C++ interop when C++ WebTTY binaries are passed:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass `
  -File test\e2e\webtty-windows-runtime.ps1 `
  -Rstream C:\path\to\rstream.exe `
  -CppServer C:\path\to\rstream-webtty-server.exe `
  -CppClient C:\path\to\rstream-webtty-client.exe
```

The managed engine runtime is validated from the engine repository:

```bash
bash test/e2e/webtty-managed-engine-runtime.sh
```
