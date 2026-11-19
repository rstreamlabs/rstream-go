# Configure and secure a WebTTY server

Use this guide to choose who can access a WebTTY server, how endpoints establish trust and which operating system account runs a session.

| To configure | Start with |
| --- | --- |
| Trust directly between a client and one server | [Endpoint identities](#require-end-to-end-terminal-encryption) |
| Access governed by a workspace | [Trust a device](#trust-a-cli-for-workspace-managed-webtty), then [enroll the server](#enroll-a-registered-webtty-server) |
| A persistent server on a device or host | [Choose the operating system identity](#operating-system-identity) and [load a daemon configuration](#load-a-daemon-configuration) |

## Require end-to-end terminal encryption

Create a server endpoint identity and a client endpoint identity, authorize the
client signing key on the server, trust the server endpoint identity on the
client, then run the server in protected mode.

On the server host, create its identity and print only the public endpoint value:

```sh
rstream webtty identity create --name dev-shell
rstream webtty identity show --name dev-shell --endpoint-identity
```

On the operator host, create its own identity and print its public endpoint value:

```sh
rstream webtty identity create --name client-laptop
rstream webtty identity show --name client-laptop --endpoint-identity
rstream webtty known-server add dev-shell \
  --key '<server-public-endpoint-value>' \
  --client-identity client-laptop
```

Replace the quoted placeholder with the public value printed on the server and verify it through your provisioning/trust channel. Transfer only the operator's public endpoint value back to the server:

```sh
rstream webtty authorized-client add client-laptop --identity dev-shell \
  --key '<client-public-endpoint-value>'
rstream webtty server -v --rstream --no-publish --name dev-shell --identity dev-shell
```

Then run on the operator host:

```sh
rstream webtty exec --url rstrm://dev-shell --e2e -o text -- whoami
```

Expect the server process account from an authorized client. A client
with Engine access and the public server pin needs an authorized client
identity to run a command. Check that a second client without that trust
fails before opening a session. Keep private identity files on their owning
hosts and stop the test server with Ctrl+C.

To provision FIPS-compatible endpoint keys with the standard CLI, add
`--crypto-profile=fips-compatible` when creating both identities. A FIPS
deployment uses a verified published WebTransport endpoint on a compatible
managed Engine.
See the [FIPS profile](../017-fips-140-3-profile.md#webtty-profile) for compatible algorithms and deployment requirements.

The server identity and the client's known-server entry enable protection for
the named rstream target. Use `--e2e` in a script to require that the client
resolves trusted endpoint material before it opens a session.

For a direct local WebSocket listener already configured with the same server
identity, select the pinned server when connecting.

```sh
rstream webtty exec --url ws://127.0.0.1:8080 --known-server dev-shell -- whoami
```

Authenticated E2E uses the full public endpoint value. Its encryption key
protects the session key and its signing key identifies the peer. Use the
public-only values printed by `webtty identity show --endpoint-identity`.

Remove a local server identity when the server key has been retired:

```bash
rstream webtty identity remove dev-shell
```

## Trust a CLI for workspace-managed WebTTY

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

FIPS builds create the workspace-device WebTTY subidentity with P-256 by
default. To replace a legacy device from a standard CLI, use
`rstream workspace device rotate --crypto-profile=fips-compatible`, approve the
replacement, and refresh its status before opening the terminal.

`workspace device enroll` and `workspace device status` infer the workspace
from the active project context. Pass `--workspace workspace_id` only for
scripts or machines that intentionally do not have an active project context.

The default device kind is `cli`. Use `--kind agent` for an automation runtime
that acts on behalf of a workspace user, and `--kind service` for a
non-interactive daemon or service-account recipient. Browsers are trusted from
the workspace protection page, not with this command.

After approval, the CLI resolves protection from the selected server and
workspace policy.

```bash
rstream webtty exec --url rstrm://prod-shell -- whoami
rstream ui
```

The [interactive inventory guide](../012-cli-ui.md) explains how to select
another context or open a session from the UI. If the workspace requires
end-to-end encryption and the device is untrusted, the client prints the
enrollment command before opening a session. Use
[server trust](#refresh-registered-server-trust) to refresh an existing server
pin after restoring the device's workspace trust.

## Enroll a registered WebTTY server

Complete the [workspace device trust setup](#trust-a-cli-for-workspace-managed-webtty) on the operator and server hosts before enrollment when workspace-managed protection is required.

The following examples create a server protected by Workspace Protection.
Use an eligible Enterprise workspace. For a transport-only server, select
`--encryption-policy disabled` where the workspace permits it.

Create the registered server from an authenticated project context, then run
the enrollment command returned by the CLI, product, or API on the host that
will run WebTTY.

If the current machine creates and runs the server, select the project once and
enroll during creation:

```bash
rstream project use project-endpoint
rstream webtty server create prod-shell --encryption-policy workspace_managed --enroll
rstream webtty server -v --server-id 'my-server-id' --login-user 'local-user'
```

Use the returned server ID and an existing operating system account that
will run the commands.

If the server record is created from another machine, run the printed
enrollment command on the runtime host:

```bash
rstream project use project-endpoint
rstream webtty server create prod-shell --encryption-policy workspace_managed
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
Registered servers publish by default. A registered WebTransport server
requires publication. For a registered server reachable only by private
rstream dials, use a supported private transport such as WebSocket with
`--no-publish`. Use `--server-enrollment`
only when the enrollment file is not at the default path.

Registered servers default to login mode. Choose an existing account with
`--login-user`, or see the options below for other execution modes.

## Operating system identity

`spawn` starts commands as children of the WebTTY server process. It preserves
client working-directory and environment overrides and uses the server process
identity.

`login` runs a session as an existing operating system account without requesting the account password. Configure the username of an
existing local account with `--login-user <username>` (or `loginUser` in YAML).
Every session is resolved against that account and receives its OS identity,
home directory, and shell. Registered servers default to `login`, but still
require a configured target user unless `--allow-client-user` is explicitly
enabled.

Run the discovery command **on the machine that hosts the WebTTY server**, then
use its output as `<username>`:

```bash
# Linux and macOS
id -un
```

```powershell
# Windows PowerShell
$env:USERNAME
```

Create the local account before starting WebTTY. The server checks a fixed `--login-user` at startup.

On Unix-like systems, switching to a different account applies the target uid,
primary gid, and supplementary groups and requires the server process to have
the corresponding privileges. On Windows, `login` supports the same Windows
account that runs the server; switching to a different account is not supported.

Login sessions use the account's home directory and shell. The server supplies
a limited environment for that account. SSH agent sockets, cloud credentials and
rstream tokens are not forwarded automatically.

## Load a daemon configuration

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

## CLI and YAML option correspondence

Explicit CLI options override the corresponding YAML values, including `false`,
zero, empty strings and empty lists. Values are validated after merging; an
explicit empty hostname is an error. An explicit `--identity` replaces a YAML
`e2e.identityFile`, and `--identity-file` replaces a YAML `e2e.identity`. The same
selector precedence applies to `--server-id` and `--server-enrollment`. Supplying
both selectors explicitly retains their normal validation. Switching between
local listening and rstream mode still requires removing incompatible options.

`--publish=false` and `--no-publish` both request a private endpoint.
`--retry=false` and `--no-retry` both disable reconnection. Positive and negative
forms of the same option cannot be supplied together. Omitted settings retain
command defaults, including the private default for lightweight plain transport.

Runtime and enrollment YAML files must each contain exactly one document. Unknown
fields are rejected. Label keys and values are trimmed; empty or duplicate keys
after trimming are errors. Global account/context configuration remains a
separate file shared with SDKs.

| Server option | YAML field | Meaning and format | Default or condition |
| --- | --- | --- | --- |
| `--rstream` | `server.rstream` | Boolean; serve through rstream rather than bind a local listener. | false; registration implies true. |
| `--listen` | `server.listen` | Local `host:port` listener. | 127.0.0.1:8080; local mode only. |
| `--name` | `server.name` | Tunnel name for inventory/private dial. | Optional, rstream mode only. |
| `--host` | `server.host` | Stable or verified custom public hostname. | Optional; published rstream endpoint only. |
| `--server-id` | `server.serverId` | Enrolled registered server identifier. | Loads its enrollment; implies rstream. |
| `--server-enrollment` | `server.serverEnrollment` | Path to an enrollment YAML file. | Alternative to server-id; normal path is under webtty/enrollments. |
| `--transport` | `server.transport` | `plain`, `websocket` or `webtransport`. | websocket in standard builds; webtransport in FIPS. |
| `--execution-mode` | `server.executionMode` | `spawn` uses process identity; `login` resolves an OS account. | login for registered servers, spawn otherwise. |
| `--login-user` | `server.loginUser` | Existing OS username that owns login sessions. | Required in login mode unless client user selection is enabled. |
| `--allow-client-user` | `server.allowClientUser` | Boolean permitting the caller to request an OS account. | false; login mode only, host privileges still required. |
| `--retry`, `--no-retry` | `server.retry` | Boolean enabling reconnection after retryable failures. | true with rstream, false for local servers. |
| `--retry-interval` | `server.retryIntervalMs` | Positive integer delay in milliseconds. | 5000. |
| `--shutdown-timeout` | `server.shutdownTimeoutMs` | Integer millisecond budget for graceful session close/drain. | 5000. |
| `--publish`, `--no-publish` | `server.publish` | Boolean selecting a public endpoint. | true with rstream, except lightweight plain is private; registered WebTransport requires publication. |
| `--auth-token-file` | `server.authTokenFile` | Path to a nonempty local HTTP bearer token. | Local WebSocket/WebTransport only; replaces environment bearer source. |
| `--allow-unauthenticated` | `server.allowUnauthenticated` | Boolean explicitly permitting local access without HTTP auth. | false; use only for intentional isolated local service; plain has no HTTP bearer layer. |
| `--allowed-origin` | `server.allowedOrigins` | Repeatable browser Origin strings / YAML list. | No extra local origins by default; tunneled traffic uses Engine admission. |
| `--label` | `server.labels` | Repeatable key=value / YAML string map for inventory. | Optional; empty or duplicate trimmed keys fail. |
| `--e2e` | `e2e.enabled` | Boolean requiring encrypted terminal content. | false unless identity/enrollment policy implies protection; cannot weaken server policy. |
| `--identity` | `e2e.identity` | Local named endpoint identity. | Alternative to identityFile; never a remote rstream username. |
| `--identity-file` | `e2e.identityFile` | Path to endpoint identity JSON with private encryption/signing keys. | See identity resolution below; alternative to identity. |
| `--authorized-clients-file` | `e2e.authorizedClientsFile` | Path to authorized-client JSON generated by CLI. | Server-specific store by default; read on handshake; workspace policy uses trusted devices instead. |
| `--authorized-client-key` | `e2e.authorizedClientKeys` | Repeatable public signing value / YAML list. | Merged with environment key values; rejected for workspace-managed server authorization. |
| `--tls-cert-file` | `tls.certFile` | Path to PEM server certificate. | Together with keyFile for local WebTransport; optional pair for local plain TLS. |
| `--tls-key-file` | `tls.keyFile` | Path to matching PEM server private key. | Local listeners only; separate from agent mTLS and WebTTY E2E identity. |
| `--fs-root` | `filesystem.root` | Directory exposed by the filesystem sidecar. | Disabled when omitted; WebSocket terminal transport and E2E disabled required. |
| `--fs-backend` | `filesystem.backend` | `webdav` or `webrtc` transfer backend. | webdav; requires root, WebRTC is read-only. |
| `--fs-read-only` | `filesystem.readOnly` | Boolean disabling WebDAV mutation. | false for the WebTTY sidecar; WebRTC is always read-only. |
| `--fs-max-upload-size` | `filesystem.maxUploadSizeBytes` | Positive integer limit per WebDAV upload in bytes. | 67108864 (64 MiB); explicit setting rejected for WebRTC. |

The `--webtty-config` flag and `RSTREAM_WEBTTY_CONFIG` select the runtime file;
`version: 1` identifies its schema. Global flags such as `--config` and `--context`
select the connection context separately.

## Client options

Client/exec options are separate from server runtime YAML. Commands after `--` run on the remote host.

| Option | Purpose and format | Default or condition |
| --- | --- | --- |
| `--url` | URI from the [transport reference](002-go-runtime.md#transports). | `ws://127.0.0.1:8080`. |
| `--transport` | `plain`, `websocket`, `webtransport`; explicit discovery override. | Discovered/inferred in standard builds; WebTransport in FIPS. Must match advertised transport. |
| `--no-discovery` | Skip Engine inventory and Control plane metadata. | false; requires explicit transport, Engine and local security. |
| `--interactive`/`-i`, `--no-interactive`/`-I` | Enable/disable interactive input. | Enabled when no command is supplied, otherwise disabled; opposite forms conflict. |
| `--tty`/`-t`, `--no-tty`/`-T` | Request/disable remote PTY. | Requested when no command is supplied, otherwise disabled; allocation needs a local terminal and server PTY support. |
| `--env`/`-e` | Repeatable `KEY=VALUE`, or `KEY` copied from client environment. | No overrides by default; server execution policy still applies. |
| `--workdir`/`-w`, `--user`/`-u` | Remote directory and username/UID. | Server policy/defaults; login mode restricts account selection. |
| `--output`/`-o` | `text` streaming or `json` bounded capture. | text for client, json for exec. |
| `--auth-token-file` | File containing local/direct HTTP bearer token. | Overrides `RSTREAM_WEBTTY_AUTH_TOKEN`; not an E2E key. |
| `--tls-ca-file` | PEM CA bundle verifying the direct WebTTY endpoint. | System roots when omitted; separate from Engine TLS configuration. |
| `--tls-server-name` | SNI/certificate verification name. | Endpoint hostname when omitted. |
| `--tls-insecure-skip-verify` | Disable direct endpoint TLS verification for diagnostics. | false; incompatible with explicit CA file and prohibited in FIPS. |
| `--exec-path` | Origin-relative advertised terminal HTTP path. | Inventory path when discovered, `/` otherwise; unavailable for plain. |
| `--e2e` | Fail-closed requirement for encrypted terminal content. | Protection also follows resolved identity/server/workspace policy. |
| `--identity`, `--identity-file` | Local named identity or endpoint JSON file. | Alternative selectors; see source precedence below. |
| `--known-server` | Named local pinned server entry. | Optional direct-target selector; conflicts with inline known-server key. |
| `--known-server-key` | Repeatable server public encryption/endpoint value. | Combined with environment key; see formats below. |
| `--known-servers-file` | JSON trust store. | Explicit flag overrides environment file selection. |
| `--client-credential-file` | Signed local workspace credential JSON. | Environment fallback; authenticated E2E required. |

## Publication and filesystem options

Use a project stable domain or a verified custom domain belonging to the project:

```bash
rstream webtty server --rstream --host shell.example.com
```

The hostname is retained across automatic reconnections. `--host` requires a
published rstream endpoint; it is unavailable for local listeners, private
tunnels and lightweight plain transport. Configure and verify custom DNS before
starting the server.

The corresponding runtime file can also select a filesystem backend:

```yaml
version: 1
server:
  rstream: true
  transport: websocket
  host: shell.example.com
filesystem:
  root: /srv/exports
  backend: webrtc
```

`filesystem.backend` accepts `webdav` (default) or `webrtc`. WebRTC file access is
read-only and rejects `filesystem.maxUploadSizeBytes`. Both backends require a
filesystem root and WebSocket terminal transport; neither supports WebTTY E2E
payload encryption or the FIPS profile.

After a CLI upgrade, restart WebTTY through its service manager and verify
that an authorized client can still run a command. Use the stable executable
path in the service definition so it resolves to the updated binary.

## Local trust files

WebTTY files live under the WebTTY subtree, separate from workspace-managed
keys:

```text
~/.rstream/webtty/identities/<name>.identity.json
~/.rstream/webtty/authorized_clients/<name>.json
~/.rstream/webtty/enrollments/<server-id>.yaml
~/.rstream/webtty/known_servers.json
```

Workspace trust has its own directory. The device record establishes
workspace trust, and its separate WebTTY identity opens workspace-managed
terminal grants.

```text
~/.rstream/workspaces/<workspace-id>/devices/<device-id>.json
~/.rstream/workspaces/<workspace-id>/webtty/identities/<device-id>.identity.json
```

Local WebTTY identity, enrollment, known-server, and workspace-device files
are written with `0600` file permissions on POSIX systems. Runtime reads reject
files that are accessible by group or others so local trust roots cannot be
silently weakened after creation.

## Environment variables and local trust sources

These variables are read by CLI/UI/local MCP paths, not automatically by arbitrary embedded `webtty.SessionConfig` users. Set only the sources intended for that process. The [shared connection variables](../002-cli-workflow.md#contexts-and-environments) select Engine identity separately.

| Variable | Value and purpose | Default, precedence and condition |
| --- | --- | --- |
| `RSTREAM_WEBTTY_CONFIG` | Path to version 1 server runtime YAML. | No runtime file by default; `--webtty-config` overrides. Missing explicit file or unknown field fails. |
| `RSTREAM_WEBTTY_AUTH_TOKEN` | Nonempty local HTTP bearer token string. | `--auth-token-file` overrides; local WebSocket/WebTransport only. All rstream-backed servers and plain transport reject local bearer configuration. |
| `RSTREAM_WEBTTY_IDENTITY` | Inline endpoint identity JSON, including encryption and signing private material. | No default inline identity; cannot coexist with a named/file identity or `RSTREAM_WEBTTY_IDENTITY_FILE`. |
| `RSTREAM_WEBTTY_IDENTITY_FILE` | Path to endpoint identity JSON. | Explicit named/file flags take precedence; registered enrollment supplies its file when no override is selected. |
| `RSTREAM_WEBTTY_AUTHORIZED_CLIENT_KEYS` | Comma-separated public signing keys or full public endpoint values. | Added to repeatable `--authorized-client-key` values; no default keys. Workspace-managed servers use trusted-device authorization instead. |
| `RSTREAM_WEBTTY_AUTHORIZED_CLIENTS_FILE` | Path to authorized-client JSON. | `--authorized-clients-file` overrides; otherwise server-specific default file. The file is read for new handshakes. |
| `RSTREAM_WEBTTY_KNOWN_SERVER_KEY` | One public server encryption value or full endpoint value. | Added to explicit `--known-server-key` values; cannot be combined with `--known-server <name>`. No TOFU. |
| `RSTREAM_WEBTTY_KNOWN_SERVERS_FILE` | Path to known-server JSON generated by CLI. | `--known-servers-file` overrides; explicit key and file sources can combine. Default local store is used when no explicit source is selected. |
| `RSTREAM_WEBTTY_CLIENT_CREDENTIAL_FILE` | Path to a signed workspace client credential JSON document. | `--client-credential-file` takes precedence; needs authenticated E2E and a trusted server. Does not enroll/approve the device. |
| `RSTREAM_DATA_DIR` | Absolute root for WebTTY trust/enrollment and workspace-device state. | `~/.rstream`; relative values fail. Does not relocate the shared config selected by `RSTREAM_CONFIG`. |

Public key components use canonical unpadded base64url. Use CLI-produced values rather than constructing keys or IDs manually:

| Public value | Format | Use |
| --- | --- | --- |
| Authorized client signing key | `signing_public_key` or `signing_key_id:signing_public_key` | Server verifies the client's authenticated proof. A full public endpoint value is also accepted. |
| Server encryption key | `encryption_key_id:encryption_public_key` | Encryption-only recipient information; insufficient to authenticate the server endpoint. |
| Full public endpoint | `encryption_key_id:encryption_public_key:signing_key_id:signing_public_key` | Authenticated E2E server trust or extraction of a client's public signing identity. |

Server identity file selection is `--identity-file`, then `--identity`, then `RSTREAM_WEBTTY_IDENTITY_FILE`, then the enrollment's identity file. An ad hoc server started with `--e2e` and no identity source uses the default local identity. Inline identity JSON is an alternative source, not a fallback to an invalid file. The document must contain both encryption and signing identities and match any enrollment pin.

Client endpoint identity selection uses the same explicit file/name/environment sources without an enrollment file. Authenticated E2E requires an explicit source or the target known-server entry's `client_identity`; it does not invent a trusted client identity. `--known-server <name>` selects a pinned local entry for a direct URL without a Control plane lookup. Registered workspace resolution instead uses approved workspace-device material.

For server authorization, inline flag/environment signing keys are combined with the selected file resolver. The file path is selected by flag, then environment, then the server-specific default under `webtty/authorized_clients`. Workspace-managed authorization uses pinned public workspace trust and device credentials instead of operator-supplied client signing keys.

## Refresh registered server trust

Registered servers use local WebTTY enrollment and identity files under
`~/.rstream/webtty`. When the control plane marks a registered server as
workspace-managed, the runtime host must be a trusted workspace device before
server enrollment. The server still uses its local WebTTY identity and pins the
public workspace keyset identity in its enrollment. If local trust material
changes after enrollment, refresh the pin:

```bash
rstream webtty server trust server_id
```

The refreshed pin lets the server verify client proofs with public workspace
trust material. Keep workspace private keys on their trusted devices.
