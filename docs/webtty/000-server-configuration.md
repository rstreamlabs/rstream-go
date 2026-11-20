# Configure a WebTTY server

Choose a host account and use a runtime file to keep the server's settings
with its service definition. The [first WebTTY session](../009-webtty.md#open-a-terminal)
shows the CLI commands for a short-lived test. For an encrypted session or a
registered server, start with [server identities and enrollment](001-server-trust.md).

## Operating system identity

| Mode | Account and session settings |
| --- | --- |
| `spawn` | Starts the command as a child of the server. The process account is the default. A client can request another user or UID if the server has OS permission to switch. The server preserves working-directory and environment overrides. |
| `login` | Starts the session as an existing OS account with that account's home and shell. Set `--login-user` or `loginUser` to select it. When `--allow-client-user` is enabled, a client may override it with an account the server can use. |

Lightweight servers default to `spawn`. Registered servers default to
`login`. For a fixed account, use `login` with an existing user and leave
`allowClientUser` disabled.

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

Use a runtime file when a service manager runs the server. The following
configuration keeps a private lightweight server running on a direct CE
Engine or a managed project that allows private tunnels. Replace `operator`
with the existing account that should run commands.

```yaml
version: 1
server:
  rstream: true
  name: dev-shell
  publish: false
  transport: websocket
  executionMode: login
  loginUser: operator
  retry: true
```

Save the file as `dev-shell.yaml` and select the configured connection for
the service, named `service` in this example.

```sh
rstream --context service webtty server --webtty-config ./dev-shell.yaml
```

For a registered server, [enroll the host](001-server-trust.md#enroll-a-registered-webtty-server)
and set `server.serverId` instead of `server.rstream` and `server.name`.
Select publication to suit the project and terminal transport.
`server.serverId` reads the default enrollment; use `server.serverEnrollment`
for a different location. CLI flags override YAML values.

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
| `--identity-file` | `e2e.identityFile` | Path to endpoint identity JSON with private encryption/signing keys. | See [identity source precedence](001-server-trust.md#key-formats-and-source-precedence); alternative to identity. |
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
| `--url` | URI from the [transport reference](002-session-operations.md#choose-the-webtty-transport). | `ws://127.0.0.1:8080`. |
| `--transport` | `plain`, `websocket`, `webtransport`; explicit discovery override. | Discovered/inferred in standard builds; WebTransport in FIPS. Must match advertised transport. |
| `--no-discovery` | Skip Engine inventory and Control plane metadata. | false. Supply the transport and any local security material. An `rstrm://` target also needs a configured Engine. |
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
| `--identity`, `--identity-file` | Local named identity or endpoint JSON file. | Alternative selectors; see [identity source precedence](001-server-trust.md#key-formats-and-source-precedence). |
| `--known-server` | Named local pinned server entry. | Optional direct-target selector; conflicts with inline known-server key. |
| `--known-server-key` | Repeatable server public encryption/endpoint value. | Combined with the environment key; see [public key formats](001-server-trust.md#key-formats-and-source-precedence). |
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
