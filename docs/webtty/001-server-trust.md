# Trust and enroll WebTTY servers

Choose how the client identifies the server and how the server authorizes
clients. Use endpoint keys for direct trust between two hosts. Managed
workspace protection uses approved devices and registered servers.

| Task | Start here |
| --- | --- |
| Trust a specific endpoint | [Set up end-to-end encryption](#require-end-to-end-terminal-encryption) |
| Access a protected workspace server | [Approve a CLI device](#trust-a-cli-for-workspace-managed-webtty) |
| Register a server for your workspace | [Enroll the server](#enroll-a-registered-webtty-server) |

## Require end-to-end terminal encryption

For this private walkthrough, select the same hosted Pro or Enterprise
project on both hosts, or the same direct Community Edition Engine. Use
an identity allowed to create the tunnel on the server and credentials
that allow the client to discover and dial it.

Each host generates an endpoint identity. Exchange and verify only the
public values. The server then checks the client's signature before opening
a session.

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
rstream project use 'my-project-endpoint'
rstream workspace device enroll --label ops-cli
rstream workspace device status
```

Use the `ENDPOINT` value for the intended workspace's project. FIPS builds create the workspace-device WebTTY subidentity with P-256 by
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

To register a transport-only server, follow the
[product registration example](https://rstream.io/docs/webtty/registered-servers#create-the-server-record).
Use a managed project that grants server registration. The examples below
configure Workspace Protection on an eligible Enterprise workspace.

Create the registered server from an authenticated project context, then run
the enrollment command returned by the CLI, product, or API on the host that
will run WebTTY.

If the current machine creates and runs the server, select the project once and
enroll during creation:

```bash
rstream project use 'my-project-endpoint'
rstream webtty server create prod-shell --encryption-policy workspace_managed --enroll
rstream webtty server -v --server-id 'my-server-id' --login-user 'local-user'
```

Use the returned server ID and an existing operating system account that
will run the commands.

If the server record is created from another machine, run the printed
enrollment command on the runtime host:

```bash
rstream project use 'my-project-endpoint'
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
`--login-user`. To run it as a service,
[save the runtime configuration](000-server-configuration.md#load-a-daemon-configuration).
Once the server is online, [inspect its sessions](002-session-operations.md#inspect-managed-sessions).

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

## Key formats and source precedence

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
