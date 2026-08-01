# rstream-go

`rstream-go` is the Go SDK for **rstream**, a secure connectivity platform built around a globally distributed edge network and lightweight agents. Agents maintain outbound-only tunnels from local and private environments, while the edge network authenticates traffic, enforces access policy, and routes requests to upstream services. rstream supports HTTP and non-HTTP workloads and provides end-to-end visibility through connection logs and metrics.

The Go SDK is the **reference implementation**. It covers the broadest rstream API surface and is the most complete SDK in terms of protocol support and tunnel lifecycle features. The rstream CLI is implemented in Go and lives in this repository, so the SDK and CLI share the same configuration model and operational behavior.

Looking for native integration? The C++ SDK is available at https://github.com/rstreamlabs/rstream-cpp.

## What is a tunnel?

A tunnel is a secure way to expose services without requiring inbound ports, public IPs, or NAT changes. In rstream, tunnels are established **outbound** to the edge network, reducing exposure while keeping access controllable and observable.

When you create a **published** tunnel with rstream, you get a forwarding address that routes inbound traffic to a local service. For example, a tunnel for `localhost:8080` provides a forwarding address like `https://abc123.rstream.io` that forwards HTTP requests to local port 8080.

## How rstream works

rstream establishes outbound tunnels between environments running services or devices and the rstream edge network. Clients connect to the edge using a forwarding address for published tunnels or a tunnel identifier for private tunnels. The edge authenticates the connection, applies policy, and forwards traffic through the existing tunnel path to the upstream service.

Tunnel transports are encrypted, and edge enforcement decisions are surfaced through logs and metrics.

## Tunnel types

rstream supports two fundamental tunnel types:

**Bytestream tunnels** (TCP-like) provide reliable, ordered transmission for protocols such as HTTP and TLS, as well as custom bytestream services.

**Datagram tunnels** (UDP-like) provide low-latency, message-oriented communication for protocols such as QUIC and DTLS, as well as custom datagram services.

## Published vs private tunnels

**Published tunnels** are accessible via standard clients (browsers, curl, etc.) through forwarding addresses. Published tunnels can be configured with edge authentication and access policies depending on protocol and deployment.

**Private tunnels** require an rstream client to connect. Private tunnels are accessed by name (if specified) or by ID through the rstream dialer instead of a public forwarding address.

## Use cases

**Local development**: Expose a local service for testing, demos, and collaboration without changing network configuration.

**Fleet operations**: Provide controlled access to devices and machines across environments with consistent identity, policy, and observability.

**Infrastructure and platforms**: Use rstream as a connectivity layer for internal tools, CI workflows, and production access paths.

**Generative AI workflows**: Distribute work across fleets of runners or machines while keeping access scoped and auditable.

**Real-time systems**: Support low-latency traffic patterns for telemetry, streaming, and datagram workloads.

## Supported features

**Core tunneling**: Create tunnels for TCP-like and UDP-like workloads with outbound-only connectivity.

**Multi-protocol support**: HTTP (1.1, 2, 3), TLS, DTLS, QUIC, plus WebSocket and WebTransport in HTTP tunnels.

**Access control**: IP restrictions, GeoIP policies, mutual TLS, token-based access, and account-based access depending on tunnel configuration.

**Operational visibility**: Connection logs and metrics for traffic, enforcement decisions, and performance signals.

**Transport configuration**: IPv4/IPv6 selection, DNS override, interface binding, HTTP CONNECT proxy support, SOCKS5 proxy support, and MASQUE proxy support for QUIC transport.

**Resilience**: Long-lived agents, reconnect behavior, and transport-level multiplexing for stable connectivity.

## Supported protocols

**HTTP protocols**: HTTP/1.1, HTTP/2 (H2C), HTTP/3 with WebSocket and WebTransport support.

**Secure transports**: TLS- and QUIC-based transports for agent-to-edge connectivity, plus DTLS and QUIC as published tunnel protocols when enabled by the deployment.

**Network options**: IPv4/IPv6, MPTCP, HTTP CONNECT, SOCKS5, MASQUE CONNECT-UDP for QUIC transport, and custom DNS resolution.

## Compatibility

rstream is compatible with Linux, macOS and Windows. Additionally, rstream supports other UNIX systems such as FreeBSD, OpenBSD and NetBSD through manual installation.

## Installation (rstream CLI)

The installation paths in this section install the `rstream` CLI binary and its runtime dependencies. They do not install the Go SDK as a library dependency.

### Local build
To build the CLI locally from this repository on the current platform, run:
```bash
make
```

### Debian/Ubuntu
For Debian-based distributions, the installer deploys packaged CLI binaries and dependencies:
```bash
sudo /bin/bash -i -c "$(curl -fsSL https://rstream.io/scripts/install-debian.sh)"
```

### macOS
On macOS, the Homebrew tap provides the standard CLI installation path:
```bash
brew tap rstreamlabs/rstream && brew install rstream
```

### Windows
On Windows, add the `rstream` source once from an elevated terminal and install with `winget`:
```powershell
winget source add -n rstream -a https://winget.rstream.io/api -t Microsoft.Rest
winget install rstream
```

If `winget` is not available, use the PowerShell installer:
```powershell
& { Invoke-Expression ([System.Text.Encoding]::UTF8.GetString((Invoke-WebRequest -Uri 'https://rstream.io/scripts/install.ps1' -UseBasicParsing).Content)) }
```

### Manual installation
For generic environments, use the manual installer script for the CLI binary:
```bash
/bin/bash -i -c "$(curl -fsSL https://rstream.io/scripts/install.sh)"
```

### Docker
If you run the CLI in containers, pull the public image:
```bash
docker pull rstream/rstream:latest
```

## Authentication

The standard developer-machine path is browser-based login:

```bash
rstream login
```

This uses OAuth 2.0 Device Authorization Grant by default. The legacy rstream login flow remains available for compatibility checks with `rstream login --auth-flow legacy`.

If this is a new rstream account, the browser step opened by `rstream login` is also where the user signs up or signs in and approves CLI access.

Codex can start the same flow through local MCP after `rstream codex setup`: `rstream_auth_start` returns the approval URL, and `rstream_auth_poll` stores the approved token locally without returning it to the prompt. The approval code is only returned separately when the provider cannot embed it in the URL. The default MCP login scope is limited; when the user explicitly asks Codex to create projects or change project settings, `rstream_auth_start` can request a broader `permissions` array and the hosted approval page shows that elevated grant.

For advanced authentication modes (token-based login, remote device flows, and project-scoped contexts), see [docs/001-cli-workflow.md](docs/001-cli-workflow.md).

Before running SDK examples, ensure a project context is set up with the CLI (`rstream project use <project-endpoint>`). The SDK and CLI share the same configuration model and config file.

For agent and CI checks, run:

```bash
rstream doctor -o json
```

The diagnostic output covers config, context, token claims, Control plane API authentication, project resolution, DNS, TLS, and engine inventory without printing secrets.

## Environment variables

These variables are shared across CLI and SDK configuration resolution. Prefer configuration contexts for regular usage, and use overrides for automation or constrained environments.

- `RSTREAM_CONFIG`: Override the CLI config file path.
- `RSTREAM_CONTEXT`: Select the context by name.
- `RSTREAM_ENGINE`: Override the engine URL used for Engine API operations.
- `RSTREAM_AUTHENTICATION_TOKEN`: Override the authentication token.
- `RSTREAM_MTLS_CERT_FILE`: Client certificate file for mTLS agent authentication.
- `RSTREAM_MTLS_KEY_FILE`: Client private key file for mTLS agent authentication.
- `RSTREAM_API_URL`: Override the Control plane API URL.
- `RSTREAM_REGION`: Select an authorized region for a managed project.
- `RSTREAM_CONTROL_PLANE_HEADERS`: Add Control plane request headers as a JSON object.

Resolution behavior follows the same model used by `config.NewClientFromEnv()`: explicit SDK options are evaluated first, then environment overrides, then context/environment values from the config file. `RSTREAM_CONFIG` selects the config file path before fallback to the default config location. Token authentication and mTLS agent authentication are mutually exclusive for the control-channel connection. When the mTLS certificate and key variables are set, config-derived tokens are not used for that connection; setting mTLS variables together with `RSTREAM_AUTHENTICATION_TOKEN` is an error. Engine HTTP API requests use token authentication.

Region selection requires a managed project endpoint and cannot be combined
with an explicit engine override. Control plane headers are intended for an
additional deployment access layer. Authentication, forwarding, and hop-by-hop
headers are reserved; malformed values and case-insensitive duplicates are
rejected before network I/O.

## Usage

### Basic HTTP tunnel
Use this command to publish a local HTTP service with default protocol and publication settings.

```bash
# Create an HTTP tunnel for local port 8080 (default: HTTP protocol, published)
rstream forward 8080
```

This command creates a public HTTP tunnel and displays the forwarding address (e.g., `https://abc123.rstream.io`). Any HTTP request sent to this URL will be redirected to `localhost:8080`. The tunnel remains active until you stop the command.

### TLS tunnel
Use `--tls` when the exposed tunnel endpoint must terminate TLS.

```bash
# Create a secure TLS tunnel for local port 8080
rstream forward 8080 --tls
```

Creates a secure TLS-encrypted tunnel accessible through the rstream network. Standard TLS clients can connect to the tunnel's forwarding address.

### Private tunnels
Use `--no-publish` to create private tunnels that are reachable only through rstream clients.

```bash
# Create a private tunnel (not publicly accessible)
rstream forward 22 --tls --no-publish --name ssh-tunnel
```

Private tunnels require rstream clients to connect and are identified by name or ID rather than public URLs.

### WebTTY remote terminal
Use `rstream webtty server -v --rstream` to expose a remote shell through rstream. In rstream mode, WebTTY defaults to a published HTTP/WebSocket tunnel with the standard WebTTY labels. Add `--no-publish` to create a private tunnel instead.

```bash
# Start a published WebTTY server over rstream
rstream webtty server -v --rstream --name shell

# Start a private WebTTY server over rstream
rstream webtty server -v --rstream --name shell --no-publish

# Create and enroll a registered WebTTY server on this machine
rstream project use project-endpoint
rstream webtty server create prod-shell --enroll

# Start an enrolled registered WebTTY server over managed rstream WebTTY
rstream webtty server -v --server-id server_id

# If the server record is created from another machine, enroll on the runtime host
rstream project use project-endpoint
rstream webtty server create prod-shell
rstream webtty server enroll server_id

# Start a WebTTY daemon from an operator-managed runtime config
rstream webtty server -v --webtty-config /etc/rstream/webtty/prod-shell.yaml

# Start a local plain WebTTY server over TLS
rstream webtty server -v --transport plain --allow-unauthenticated --tls-cert-file server.crt --tls-key-file server.key

# Start a local WebTransport WebTTY server for a browser app
rstream webtty server -v --transport webtransport --allow-unauthenticated --allowed-origin http://127.0.0.1:3000 --tls-cert-file server.crt --tls-key-file server.key

# Create and show local WebTTY endpoint identities
rstream webtty identity create --name dev-shell
rstream webtty identity show --name dev-shell --endpoint-identity
rstream webtty identity create --name client-laptop
rstream webtty identity show --name client-laptop --endpoint-identity
rstream webtty identity list

# Authorize the client public endpoint identity on the server host
rstream webtty authorized-client add client-laptop --identity dev-shell --key client_endpoint_identity
rstream webtty authorized-client list --identity dev-shell

# Start a local WebTTY server that requires E2E and client proof
rstream webtty server -v --allow-unauthenticated --identity dev-shell

# Start a server that requires an explicit OS login identity
rstream webtty server -v --execution-mode login --login-user operator

# Connect through a standard websocket endpoint
rstream webtty client --url wss://example.rstream.io/ -- whoami

# Connect through the native rstream dialer using a tunnel name or ID
rstream webtty client --url rstrm://shell -- whoami

# Execute a command with JSON output for agents and scripts
rstream webtty exec --url rstrm://shell -- uname -a

# Execute through a locally known E2E WebTTY server endpoint identity
rstream webtty known-server add dev-shell --key server_endpoint_identity --client-identity client-laptop
rstream webtty known-server list
rstream webtty exec --url rstrm://dev-shell -- whoami
rstream webtty exec --url ws://127.0.0.1:8080 --known-server dev-shell -- whoami

# Expose a WebDAV filesystem sidecar rooted at $HOME
rstream webtty server -v --rstream --name shell --fs-root "$HOME"

# Read a file through the filesystem sidecar
rstream webtty fs read --url rstrm://shell /README.md

# List the available WebTTY servers
rstream webtty list

# Inspect managed WebTTY sessions and recorded events
rstream webtty sessions list
rstream webtty sessions show session_id
rstream webtty sessions events session_id
rstream webtty sessions export session_id --format text
rstream webtty sessions export session_id --format json --file session-export.json
rstream webtty sessions participants session_id
rstream webtty sessions join session_id
rstream webtty sessions join session_id --interactive --request-control
rstream webtty sessions control-requests session_id

# List workspaces, select a project, and enroll this CLI as a trusted device
rstream workspace list
rstream project use project-endpoint
rstream workspace device enroll --label ops-cli
rstream workspace device status

# Use advertised exec_path and fs_path values even when they match the defaults

# Register local rstream MCP tools for Codex
rstream codex setup

# From Codex MCP, expose a service that is local to the remote WebTTY host
# rstream_remote_expose webtty_url=rstrm://shell port=8765 protocol=http
# For a direct authenticated-E2E WebTTY URL, also pass known_server=shell

# From Codex MCP, expose and discover a remote MCP surface on that host
# rstream_remote_expose webtty_url=rstrm://shell port=8765 mcp_path=/mcp labels=role=robot
# rstream_remote_mcp_discover

# Expose a local dev server through an MCP-managed local tunnel
# Available to Codex through rstream_local_tunnel_expose
rstream forward 3000 --name codex-local-tunnel --label role=codex

# Publish the local rstream MCP server over an HTTP tunnel
rstream mcp publish --name codex-rstream-mcp --label role=codex

# Open the live terminal UI for clients, tunnels, and WebTTY servers
rstream ui
```

Inside `rstream ui`, press `c` to open the context and project picker. Configured
local contexts are available immediately, including unlinked contexts that have
no Control plane API. Projects for the active Control plane API are loaded in
the background when the selected credentials permit project discovery. Use `1`
and `2` (or `Tab`) to move between the separate context and project views. Each
view shows explicit `CURRENT` and `DEFAULT` columns and a detail panel for the
selected row. Press `Enter` to switch only for the current UI process, or `d` to
switch and save the selection as the default context in the active rstream
config file. The picker also supports `/` to search and `r` to refresh remote
projects.

Project discovery failures never hide or disable local contexts. An explicit
`--api-url` or `RSTREAM_API_URL` scopes linked contexts and remote projects to
that API, while unlinked contexts remain selectable. `RSTREAM_ENGINE` remains a
hard override: the UI rejects a switch to a context that targets a different
engine. Close an active WebTTY session with `Ctrl+g q` before changing context.

Login execution mode is passwordless. On Unix-like systems, switching to a
different configured or client-requested OS user applies the target uid, primary
gid, and supplementary groups, and requires the WebTTY server process to have
the corresponding OS privileges. On Windows, run the server under the desired
Windows account; password-based user switching is not supported.

Published WebTTY tunnels can be reached either through their forwarding `wss://` address or through the native `rstrm://<tunnel-id-or-name>` form. Private WebTTY tunnels are reachable only through the native `rstrm://` form. WebTTY servers advertise capabilities, execution mode, and endpoint paths through labels: command execution uses `exec_path`, currently `/` by default, and the optional filesystem sidecar uses `fs_path`, currently `/fs` when `--fs-root` is set. Filesystem paths are relative to that configured root: if the server starts with `--fs-root "$HOME/project"`, read `compose.yaml` as `/compose.yaml`, not `/home/user/project/compose.yaml`. The sidecar rejects symlinks that resolve outside the configured root, but it is not a sandbox and still uses the WebTTY server process permissions. The filesystem sidecar is a separate WebDAV surface and is rejected when WebTTY E2E payload encryption is active.

Tunnel WebTTY servers created with `rstream webtty server -v --rstream` use the
HTTP/WebSocket tunnel path with standard WebTTY inventory labels. Registered
WebTTY servers are normally created and enrolled on the runtime host with
`rstream webtty server create <name> --enroll`. Split-machine setup creates the
record first, then runs `rstream webtty server enroll <server-id>` on the
runtime host. The generated local enrollment is stored under
`~/.rstream/webtty/enrollments/<server-id>.yaml`, and publish managed
`protocol=webtty` tunnels when started with `--server-id`. The enrollment stores
the control-plane server ID, project ID, server public key fingerprint, local
identity file path, and server encryption policy. Enrollment uses the local
authenticated rstream context and is not the operator runtime config.

Live managed attach is engine-coordinated: the participant and control request
resources use the HTTP control API, then the terminal stream upgrades to WebTTY
protobuf with an `Attach` handshake. Direct WebTTY servers accept only new
`Open` sessions and reject managed attach handshakes with a clear protocol
error. CLI live attach first checks the engine capability document and then
resolves E2E decrypt material from trusted workspace devices or local WebTTY
identity grants before opening the terminal stream.

Use `--webtty-config` or `RSTREAM_WEBTTY_CONFIG` when a daemon should be
configured from YAML instead of a long flag list. This runtime config is
operator-managed and may contain `server.serverId`; when it does, the CLI loads
the generated enrollment for that server ID before publishing managed WebTTY:

```yaml
version: 1
server:
  serverId: srv_prod_shell_01
  transport: websocket
  executionMode: login
  loginUser: operator
  labels:
    env: production
    role: bastion
```

CLI flags override values from `--webtty-config`, and `--server-id` implies
rstream managed mode. `server.serverEnrollment` can be used in the runtime
config only when the generated enrollment lives outside the default
`~/.rstream/webtty/enrollments/<server-id>.yaml` path.

WebTTY E2E keeps the protobuf session envelope visible while encrypting
stdin/stdout/stderr payload bytes. Server identities are local host identities,
stored by default under `~/.rstream/webtty/identities/*.identity.json` with file
mode `0600`. Workspace keys are separate product keys and should live under the
workspace key hierarchy, not in the WebTTY identity directory. Explicit server
identity material, known server keys, or an enrolled server whose policy
requires encryption imply protected mode. For `rstrm://...` targets, the client
can resolve labels and local known-server entries before opening the session.
Direct `ws://...` targets need direct server trust material. Use
`--known-server <name>` to select a pinned server from the local trust store, or
pass `--known-server-key` when automation owns the trust material directly.
`--e2e` is only a fail-closed assertion when a command must refuse plaintext.

Local WebTTY files use this layout:

- `~/.rstream/webtty/identities/<name>.identity.json`
- `~/.rstream/webtty/enrollments/<server-id>.yaml`
- `~/.rstream/webtty/known_servers.json`
- `~/.rstream/workspaces/<workspace-id>/devices/<device-id>.json`
- operator-managed daemon configs such as `/etc/rstream/webtty/prod-shell.yaml`

Identity, enrollment, known-server, and workspace-device files are created
with `0600` permissions on POSIX systems and rejected at runtime if group or
other users can read them.

Environment overrides are available for container and CI deployments:
`RSTREAM_WEBTTY_CONFIG`, `RSTREAM_WEBTTY_AUTH_TOKEN`,
`RSTREAM_WEBTTY_IDENTITY`, `RSTREAM_WEBTTY_IDENTITY_FILE`,
`RSTREAM_WEBTTY_AUTHORIZED_CLIENT_KEYS`, `RSTREAM_WEBTTY_KNOWN_SERVER_KEY`,
and `RSTREAM_WEBTTY_KNOWN_SERVERS_FILE`.

Server identity precedence is `RSTREAM_WEBTTY_IDENTITY` for an inline endpoint
identity JSON document, then `--identity-file`, then `--identity`, then
`RSTREAM_WEBTTY_IDENTITY_FILE`, then the registered server enrollment identity
file, then the default
`~/.rstream/webtty/identities/default.identity.json` for ad hoc servers started
with `--e2e` and no explicit identity. E2E servers require authorized client
signing keys from the default authorized-client store, `--authorized-client-key`,
`RSTREAM_WEBTTY_AUTHORIZED_CLIENT_KEYS`, or an explicit authorized-clients file.

Client identity precedence uses the same endpoint identity sources:
`RSTREAM_WEBTTY_IDENTITY`, then `--identity-file`, then `--identity`, then
`RSTREAM_WEBTTY_IDENTITY_FILE`, then the target-scoped `client_identity`
stored in `~/.rstream/webtty/known_servers.json`. Authenticated E2E clients do
not silently use the default identity. Client known server material comes from
`--known-server`, repeated `--known-server-key`,
`RSTREAM_WEBTTY_KNOWN_SERVER_KEY`, `--known-servers-file`, or
`RSTREAM_WEBTTY_KNOWN_SERVERS_FILE`; the key value should normally be the
endpoint identity printed by `rstream webtty identity show --endpoint-identity`.
The default known-server store is target-scoped for resolved `rstrm://...`
connections. Direct `ws://...` connections should pass `--known-server <name>`
or explicit trust material when no resolved tunnel metadata is available.
The two-field encryption-only form remains accepted by lower-level helpers for
specialized encryption-only integrations, but it is not the recommended
operator workflow. The default
`~/.rstream/webtty/known_servers.json` is loaded when no explicit trusted
server keys were provided, so E2E can be inferred without an extra client flag.

Workspace-managed registered servers pin the public workspace keyset identity
automatically during server enrollment when the runtime host is already a
trusted workspace device. If local trust material changes later, refresh the
pin with `rstream webtty server trust <server-id>`. That repair command lets
the remote server verify workspace-managed client proofs without copying
workspace private keys to the host.

The implemented E2E helper suite is AES-256-GCM payload encryption with fresh
96-bit nonces and HPKE Base
`DHKEM(X25519, HKDF-SHA256), HKDF-SHA256, AES-256-GCM` key envelopes.
Go WebTTY `tls://` plain transport and local WebTransport use TLS 1.3 minimum.

See [docs/006-webtty.md](docs/006-webtty.md) for the complete WebTTY CLI, local file,
runtime config, and E2E workflow reference.

`rstream codex setup` preserves an explicit `RSTREAM_CONFIG` in the generated MCP server entry when one is set. `rstream mcp serve` exposes local rstream tools over MCP stdio for Codex and other local agent runtimes. The tool surface includes OAuth login start/poll tools, runtime preparation for a selected project, local context/status inspection, workspace and tunnel project discovery, project creation options, explicit project creation or checkout start, project plan inspection, project logs, usage, TURN usage, short-lived TURN credential minting, stable domain inspection and management, project settings, short-lived delegated auth token minting, managed local tunnels that can be listed and stopped across Codex sessions, WebTTY inventory, WebTTY command execution, WebDAV filesystem sidecar access, remote service exposure through WebTTY, and remote MCP surface discovery and invocation. `rstream mcp publish` serves the same local tools over Streamable HTTP at `/mcp` and publishes them through a token-protected HTTP tunnel.

Use rstream CLI 1.18.1 or later for the full Codex MCP workflow, including runtime preparation, WebTTY filesystem tools, remote exposure, and longer MCP tool timeouts for user-approved login.

On a Codex workstation, `rstream_runtime_prepare` is the setup tool for managed local tunnels, WebTTY, remote exposure, and remote MCP. It uses the long-lived rstream login credential stored in the CLI config, creates or repairs the selected project context, and does not mint a short-lived delegated token. If several tunnel projects are available and the user has not named one, the agent should list the choices and ask instead of inferring from naming conventions alone or adding a preferred example. `rstream_token_create` is reserved for immediate handoff flows such as protected URLs, browser-constrained clients, or remote runtime startup commands.

The remote exposure tools intentionally run `rstream forward` on the WebTTY host. That makes network access complete for agent workflows: Codex can execute commands, read or write the advertised filesystem root, and expose a remote-local HTTP, TCP, UDP, TLS, DTLS, or QUIC service through the same rstream project. The persistent remote runner currently expects a POSIX shell on the WebTTY host. When `mcp_path` is set, the created tunnel is labeled as an MCP surface so agents can discover it with `rstream_remote_mcp_discover` and call it through `rstream_remote_mcp_tools` and `rstream_remote_mcp_call`.

For direct authenticated-E2E WebTTY URLs, pass `known_server=<name>` to `rstream_remote_expose` or `rstream_remote_expose_stop`. The MCP server then uses the same local known-server store as `rstream webtty exec`: it verifies the pinned server endpoint identity, loads the associated client identity, and does not call the Control plane for direct URLs. For `rstrm://...` targets, MCP resolves live WebTTY inventory first and only uses the Control plane when workspace-managed trust requires it.

For private remote exposures, HTTP/1.x, HTTP/2, MCP, TCP, and TLS services are carried over a private bytestream tunnel, while UDP, HTTP/3, DTLS, and QUIC services are carried over a private datagram tunnel. Published exposures use the requested edge-facing protocol.

Published MCP clients must send a rstream token with `Authorization: Bearer <token>` or `rstream.token=<token>`. Use a short-lived token scoped to the MCP tunnel and `/mcp` path instead of reusing a broad personal token.

### Netcat-style TCP and rstream streams
Use `rstream netcat` for bytestream sessions over plain TCP or native rstream tunnels. The command is also available as `rstream ncat` and `rstream nc`.

```bash
# Connect to a plain TCP service
rstream nc 127.0.0.1:1234

# Expose a command over TCP
rstream nc -L 127.0.0.1:1234 -c "date"

# Expose a local SSH daemon through a private rstream tunnel
rstream nc -L rstrm://ssh-server -R 127.0.0.1:22

# Connect to that private rstream tunnel by name or ID
rstream nc rstrm://ssh-server
```

When `--listen` uses `rstrm://[name]`, the CLI creates a private unpublished bytestream tunnel. If no name is provided, the generated tunnel identifier is printed on startup so another rstream client can dial it.

For SSH access to a private machine, `rstream forward` is usually the simpler server-side entrypoint. Start it on the remote machine that has access to the local SSH daemon:

```bash
rstream forward 22 --bytestream --no-publish --name ssh-server
```

On the client machine, validate the path with a one-off SSH command:

```bash
ssh -o 'ProxyCommand rstream nc rstrm://ssh-server' admin@ssh-server hostname
```

For regular use, move the client-side configuration into SSH `ProxyCommand`:

```sshconfig
Host ssh-server
  HostName ssh-server
  User admin
  ProxyCommand rstream nc rstrm://%h
```

This keeps the tunnel private while SSH still performs its normal host key verification and user authentication.

In client mode, `--exec`/`--sh-exec` run a local command and bridge its stdin/stdout to the connection, which provides a bidirectional path without shell pipe plumbing:

```bash
rstream nc rstrm://ssh-server -c "my-local-client"
```

### Netcat datagram mode

`rstream nc --datagram` (`-u`) carries packets instead of a byte stream. Datagram mode requires `rstrm://` endpoints on both sides: `--listen rstrm://[name]` creates a private unpublished datagram tunnel, and `rstream nc -u rstrm://<id-or-name>` dials one. `--remote` is not supported in datagram mode.

Since stdin/stdout are byte streams, packet boundaries on stdio are preserved with explicit framing, selected with `--framing`. The default and currently only supported framing is `rfc4571`, where each datagram is prefixed with a 2-byte big-endian length as defined by RFC 4571. One frame on stdio equals one datagram on the tunnel, so any program that reads and writes this framing exchanges packets through the tunnel without further adaptation.

```bash
# Expose a datagram producer; one child process per accepted session
rstream nc -u -L rstrm://media -c "media-producer"

# Dial the tunnel; stdio carries RFC 4571 frames
rstream nc -u rstrm://media

# Or run a local consumer with bidirectional framed stdio
rstream nc -u rstrm://media -c "media-consumer" --idle-timeout 60s
```

Datagram tunnels can also bridge local UDP sockets instead of stdio, which connects UDP-native applications without any framing concern. One UDP packet equals one tunnel datagram, and `--framing` does not apply to udp endpoints.

```bash
# Bridge tunnel sessions to a local UDP service (one connected socket per session)
rstream nc -u -L rstrm://media -R udp://127.0.0.1:5004

# Bind a local UDP socket and bridge it to a tunnel (one session per local peer)
rstream nc -u -L udp://127.0.0.1:5004 -R rstrm://media

# Receive-only local apps never send first; pin the peer to open the session eagerly
rstream nc -u -L udp://127.0.0.1:5004 -R rstrm://media --udp-peer 127.0.0.1:5006
```

The CLI defaults to automatic tunnel transport selection: it prefers QUIC and falls back to TLS when the UDP path is unavailable. When QUIC is selected, tunnel-side packets can ride QUIC datagrams (RFC 9221): they are congestion-controlled but never retransmitted, so delivery is not guaranteed and each datagram must fit the path MTU budget (roughly 1200 bytes is a safe payload target, for stdio frames and UDP packets alike). Oversized datagrams are dropped and logged without terminating the session. Use `--datagram-guaranteed-delivery` when packet boundaries must be preserved without loss; that mode carries packets over reliable streams even when the control channel uses QUIC.

Closing either side of a datagram channel closes the peer session as well. `--idle-timeout` handles a different case: it closes a session after no datagram has been received for the given duration. Use it only on a side that expects inbound packets. A send-only producer receives no traffic to refresh the deadline and must not set it. In datagram exec sessions the child's stderr goes to the local stderr rather than the connection, since raw stderr bytes would corrupt the framing.

### UDP/datagram tunnels
For datagram transport, choose DTLS mode when you need encrypted UDP-style traffic.

```bash
# Create a DTLS tunnel for UDP traffic on port 5000
rstream forward 5000 --dtls
```

DTLS tunnels automatically handle datagram traffic. The `--datagram` flag is implied with `--dtls`.

### Run (declarative tunnels)

`rstream run` keeps tunnels in sync from a YAML file or Docker labels, with optional watch/reconcile.

- `forward`: creates a single tunnel from CLI args and forwards immediately (single tunnel, interactive).
- `run --apply`: declarative list of tunnels from YAML, supports watch/reconcile.
- `run --docker`: discovers tunnels from Docker labels, supports watch/reconcile.

```bash
# Apply a YAML spec once
rstream -v run --apply examples/run-yaml/tunnels.yaml

# Watch the YAML for changes and reconcile
rstream -v run --apply examples/run-yaml/tunnels.yaml --watch

# Discover tunnels from Docker labels
rstream -v run --docker --watch
```

See `docs/008-cmd-run.md` for the full YAML schema, Docker label reference, and reconciliation details.

## Build and compilation

rstream includes a comprehensive Makefile supporting multiple platforms and packaging formats.

### Development
For daily development loops, these targets build, test, and clean local artifacts.
```bash
make          # Build for current platform
make clean    # Clean build artifacts
make tests    # Run test suite
make examples # Build example applications
```

### Cross-platform compilation
When producing binaries for multiple targets, use:
```bash
make cross    # Build for all supported platforms
```

Supports Linux, macOS, Windows, and BSD systems across multiple architectures including x86, ARM, MIPS, PowerPC, and RISC-V.

### Packaging
For release packaging workflows, these targets build archives and platform packages:
```bash
make pkg         # Create packages for current platform
make pkg-cross   # Create packages for all platforms
make deb         # Create Debian/Ubuntu packages
make docker      # Build Docker images
make nupkg       # Create Windows NuGet packages
```

## Code examples

The Go SDK enables applications to create and manage tunnels programmatically. The examples below use `config.NewClientFromEnv()` to read the same config and environment settings as the CLI. Ensure a default context (or `RSTREAM_ENGINE`) is set, and provide either `RSTREAM_AUTHENTICATION_TOKEN` or the mTLS certificate/key environment variables if the selected agent control channel requires authentication. Engine HTTP API operations require token authentication.

### Managed TURN credentials

The SDK also exposes helpers to generate managed TURN credentials.

- `config.CreateTURNCredentialsFromEnv(...)` resolves the current config, context, and token automatically.
- Auto mode selects local PAT derivation when the active token is a PAT carrying `token_endpoint`. It falls back to the hosted API for other token types.
- Explicit `PAT` mode requires a PAT token. Explicit `API` mode can use either a project ID or a project endpoint.

```go
package main

import (
	"context"
	"fmt"

	"github.com/rstreamlabs/rstream-go/config"
)

func main() {
	turn, err := config.CreateTURNCredentialsFromEnv(
		context.Background(),
		config.TURNCredentialsEnvOptions{},
	)
	if err != nil {
		panic(err)
	}
	fmt.Println(turn.Username)
	fmt.Println(turn.URLs)
}
```

### Explicit client options (exception)

Use this only when bypassing config and environment resolution is required.

```go
package main

import "github.com/rstreamlabs/rstream-go"

func main() {
	client, err := rstream.NewClient(rstream.ClientOptions{
		Engine: "engine.example:443",
		Token:  "authentication_token",
	})
	if err != nil {
		panic(err)
	}
	_ = client
}
```

### HTTP server (published tunnel)

This example creates a published HTTP tunnel and serves requests through the tunnel listener. The forwarding address printed by `ForwardingAddress()` is the public URL that can be used from a browser or `curl`.

```go
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func main() {
	client, err := config.NewClientFromEnv()
	if err != nil {
		panic(err)
	}
	ctrl, err := client.Connect(context.Background(), nil)
	if err != nil {
		panic(err)
	}
	defer ctrl.Close()
	tunnel, err := ctrl.CreateTunnel(context.Background(), rstream.TunnelProperties{
		Protocol:    rstream.ProtocolPtr(rstream.ProtocolHTTP),
		HTTPVersion: rstream.HTTPVersionPtr(rstream.HTTP1_1),
		Publish:     rstream.BoolPtr(true),
	})
	if err != nil {
		panic(err)
	}
	defer tunnel.Close()
	addr, _ := tunnel.ForwardingAddress()
	fmt.Printf("Server accessible at: %s\n", addr)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "Hello from rstream!")
	})
	http.Serve(tunnel.(net.Listener), nil)
}
```

### TLS echo (private tunnel)

This example shows a private tunnel workflow. The server creates a non-published tunnel named `echo` and accepts inbound tunnel connections. The client dials the private tunnel by name and exchanges data over the resulting stream.

**Server code:**
```go
package main

import (
	"context"
	"io"
	"log"
	"net"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func main() {
	client, err := config.NewClientFromEnv()
	if err != nil {
		panic(err)
	}
	ctrl, err := client.Connect(context.Background(), nil)
	if err != nil {
		panic(err)
	}
	defer ctrl.Close()
	tunnel, err := ctrl.CreateTunnel(context.Background(), rstream.TunnelProperties{
		Name:    rstream.StringPtr("echo"),
		Publish: rstream.BoolPtr(false),
	})
	if err != nil {
		panic(err)
	}
	defer tunnel.Close()
	props, err := tunnel.Properties()
	if err != nil {
		panic(err)
	}
	log.Printf("Echo server running as private tunnel: %s", *props.Name)
	listener := tunnel.(net.Listener)
	for {
		conn, err := listener.Accept()
		if err != nil {
			break
		}
		go func(c net.Conn) {
			defer c.Close()
			log.Printf("New connection from %s", c.RemoteAddr())
			io.Copy(c, c)
		}(conn)
	}
}
```

**Client code:**
```go
package main

import (
	"context"
	"fmt"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func main() {
	client, err := config.NewClientFromEnv()
	if err != nil {
		panic(err)
	}
	conn, err := client.Dial(context.Background(), rstream.Addr{
		IdOrName: "echo",
	})
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	message := "Hello from private tunnel!"
	conn.Write([]byte(message))
	buffer := make([]byte, len(message))
	conn.Read(buffer)
	fmt.Printf("Sent: %s\nReceived: %s\n", message, string(buffer))
}
```

### DTLS datagram server

This example creates a published DTLS datagram tunnel. The forwarding address is where datagram clients connect. The server uses the packet listener API to accept datagram sessions and echo packets.

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func main() {
	client, err := config.NewClientFromEnv()
	if err != nil {
		panic(err)
	}
	ctrl, err := client.Connect(context.Background(), nil)
	if err != nil {
		panic(err)
	}
	defer ctrl.Close()
	tunnel, err := ctrl.CreateTunnel(context.Background(), rstream.TunnelProperties{
		Name:     rstream.StringPtr("dtls"),
		Type:     rstream.TunnelTypePtr(rstream.TunnelTypeDatagram),
		Protocol: rstream.ProtocolPtr(rstream.ProtocolDTLS),
		Publish:  rstream.BoolPtr(true),
	})
	if err != nil {
		panic(err)
	}
	defer tunnel.Close()
	addr, _ := tunnel.ForwardingAddress()
	fmt.Printf("DTLS server accessible at: %s\n", addr)
	packetListener := tunnel.(rstream.PacketListener)
	for {
		conn, _, err := packetListener.Accept()
		if err != nil {
			log.Printf("Accept error: %v", err)
			continue
		}
		go func() {
			defer conn.Close()
			buffer := make([]byte, 1024)
			for {
				n, addr, err := conn.ReadFrom(buffer)
				if err != nil {
					break
				}
				log.Printf("Received %d bytes from %s", n, addr)
				n, err = conn.WriteTo(buffer[:n], addr)
				if err != nil {
					break
				}
			}
		}()
	}
}
```

### QUIC server

This example creates a published QUIC datagram tunnel. QUIC is a modern transport designed for low latency and resilience to network changes. The sample generates a local TLS configuration and serves QUIC streams over the tunnel packet listener.

```go
package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"

	"github.com/quic-go/quic-go"
	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func generateTLSConfig() (*tls.Config, error) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		return nil, err
	}
	template := x509.Certificate{SerialNumber: big.NewInt(1)}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
	}, nil
}

func main() {
	client, err := config.NewClientFromEnv()
	if err != nil {
		panic(err)
	}
	ctrl, err := client.Connect(context.Background(), nil)
	if err != nil {
		panic(err)
	}
	defer ctrl.Close()
	tunnel, err := ctrl.CreateTunnel(context.Background(), rstream.TunnelProperties{
		Name:     rstream.StringPtr("quic"),
		Type:     rstream.TunnelTypePtr(rstream.TunnelTypeDatagram),
		Protocol: rstream.ProtocolPtr(rstream.ProtocolQUIC),
		Publish:  rstream.BoolPtr(true),
	})
	if err != nil {
		panic(err)
	}
	defer tunnel.Close()
	addr, _ := tunnel.ForwardingAddress()
	fmt.Printf("QUIC server accessible at: %s\n", addr)
	tlsCfg, err := generateTLSConfig()
	if err != nil {
		panic(err)
	}
	transport := quic.Transport{
		Conn: rstream.PacketConnFromPacketListener(tunnel.(rstream.PacketListener)),
	}
	listener, err := transport.Listen(tlsCfg, nil)
	defer listener.Close()
	for {
		conn, err := listener.Accept(context.Background())
		if err != nil {
			log.Printf("Accept error: %v", err)
			continue
		}
		go func() {
			defer conn.CloseWithError(0, "server done")
			stream, err := conn.AcceptStream(context.Background())
			if err != nil {
				return
			}
			defer stream.Close()
			buffer := make([]byte, 1024)
			for {
				n, err := stream.Read(buffer)
				if err != nil {
					break
				}
				log.Printf("Received %d bytes from %s", n, conn.RemoteAddr())
				n, err = stream.Write(buffer[:n])
				if err != nil {
					break
				}
			}
		}()
	}
}
```

## References

- Documentation: https://rstream.io/docs
- Go SDK (reference implementation): https://github.com/rstreamlabs/rstream-go
- C++ SDK: https://github.com/rstreamlabs/rstream-cpp

Operational and advanced CLI/SDK workflows:

- CLI workflow and authentication: [docs/001-cli-workflow.md](docs/001-cli-workflow.md)
- Declarative run workflows: [docs/008-cmd-run.md](docs/008-cmd-run.md)
- Transport configuration: [docs/002-transport.md](docs/002-transport.md)
- Tunnel property reference: [docs/003-tunnel-properties.md](docs/003-tunnel-properties.md)

## Contributing

Pull requests are encouraged and appreciated. Whether you're fixing bugs, adding features, improving documentation, or suggesting enhancements, your contributions help make rstream better for everyone. Build locally, run checks, and submit focused pull requests with clear validation notes. See [CONTRIBUTING.md](CONTRIBUTING.md) for the repository-specific contribution guidelines.

## Support

**Get help:**  
support@rstream.io

**Report security concerns:**  
reports@rstream.io

See [SECURITY.md](SECURITY.md) for the security reporting guidance used by this repository.

## License

This repository is licensed under the Apache License 2.0. See [LICENSE](LICENSE).
