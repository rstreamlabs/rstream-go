# CLI Workflow

This document explains the operational path from authentication to the first forwarded port.

Tunnel creation is documented separately.

## Concepts

### Environment

An **environment** represents a Control plane API URL. It stores:
- the account-wide token for that Control plane API
- transport defaults for Engine API connections

Environments are identified by `apiUrl`.

### Project

A **project** is managed in rstream. It defines where tunnels live and which engine endpoint is used.
In the CLI, projects are selected using the **project endpoint**.

Projects are typically created and managed from the rstream dashboard.

### Context

A **context** is a local, named configuration used by the CLI at runtime. It defines:
- the engine endpoint to use for Engine API operations
- the authentication method and credentials

A single machine can store multiple contexts and switch between them.

Contexts are stored independently from environments. A context may optionally reference an environment via
`apiUrl` to inherit the environment token and transport defaults. Contexts without an `apiUrl` are unlinked
and do not inherit Control plane API tokens.

The default context stores only the context name. If duplicate names exist, use `--api-url` to disambiguate
or ensure context names are unique.

One context is selected as the **default context**. Most commands use it automatically unless a specific context is selected explicitly.

## Authentication modes

Two modes exist, depending on where the CLI runs.

### Developer machine (account-wide identity)

Appropriate for installations tied to a physical user (laptops, workstations).

- `rstream login` stores an **account-wide token** locally for the selected `apiUrl`.
- The same token is reused across projects and contexts.
- `rstream project use <project-endpoint>` derives a context from the selected project.
- Switching projects is typically done by running `rstream project use` again.

The account-wide token can be obtained via:
- `rstream login`, which uses OAuth 2.0 Device Authorization Grant by default
- local MCP tools `rstream_auth_start` and `rstream_auth_poll`, which initiate the same OAuth flow for Codex without returning the approved token to the prompt
- `rstream login --auth-flow legacy`, which keeps the older rstream-specific login flow available for compatibility tests
- token generated from the rstream dashboard
- token generated via the rstream API

All methods lead to the same outcome: an account-wide token stored locally and reused.

### Remote device (project-scoped token)

Appropriate for servers, CI runners, and embedded systems that must not rely on an account-wide identity.

- No `rstream login`.
- A **project-scoped token** is used.
- A context is created explicitly with `--engine` and `--token`.
- Credentials remain narrowly scoped to a single project.

### Agent mTLS

Use agent mTLS when the engine should authenticate the agent with a client certificate instead of a token.

- The context can store `auth.mtls.certificate` and `auth.mtls.key` inline, or `auth.mtls.certificateFile` and `auth.mtls.keyFile` paths.
- `RSTREAM_MTLS_CERT_FILE` and `RSTREAM_MTLS_KEY_FILE` can provide the certificate and key for automation.
- Token authentication and mTLS authentication cannot be used together on the same agent control-channel connection.
- When `RSTREAM_MTLS_CERT_FILE` and `RSTREAM_MTLS_KEY_FILE` are set, the resolver uses mTLS for the agent control-channel connection and does not fall back to a stored context token for that connection.
- If mTLS variables are set together with `RSTREAM_AUTHENTICATION_TOKEN`, resolution fails before connecting.
- Certificate and key inputs must be complete pairs. File-based and inline certificate material cannot be mixed in the same auth block.
- Engine HTTP API operations, such as resource listing and watch streams, use token authentication.

```yaml
contexts:
  - name: prod-mtls
    engine: project.cluster.example:443
    auth:
      mtls:
        certificateFile: /etc/rstream/client.pem
        keyFile: /etc/rstream/client-key.pem
```

## Developer machine path

### 1) Login

```bash
rstream login
```

If this is a new account, the browser step opened by `rstream login` is also the signup/sign-in step. The CLI uses OAuth Device Authorization Grant by default and stores the approved account-wide token locally after the user completes the hosted flow.

When Codex is using local MCP tools, the equivalent flow is:

```text
rstream_auth_start -> user opens login_url -> rstream_auth_poll
```

`rstream_auth_start` returns the browser approval URL, expiry, requested scopes, and local auth session ID. A separate user code is returned only when the provider cannot embed it in the approval URL. `rstream_auth_poll` exchanges the approved device code and stores the token in the same local config file used by the CLI. The token is never returned through MCP.

The default `rstream_auth_start` scope is intentionally limited to common CLI and agent setup operations. When a user explicitly asks Codex to create projects or update project settings, pass an explicit `permissions` array such as `account.projects.read-write` and `account.plan.read-only`; the browser approval page is the user confirmation point for that broader grant.

Alternative token inputs:

```bash
rstream login <token>
rstream login --stdin
rstream login --token-file /path/to/token
```

### 2) Select a project endpoint

List available projects:

```bash
rstream project list
```

Select a project endpoint and set it as default:

```bash
rstream project use <project-endpoint>
```

### 3) Forward a local port

`rstream forward 8080` creates a tunnel and forwards incoming traffic to `localhost:8080`.

```bash
rstream forward 8080
```

## Remote device path

### 1) Create a context with a project-scoped token

```bash
rstream context create <name> --engine <host:port> --token <project-scoped-token> --default --no-api-url
```

Alternative token inputs:

```bash
rstream context create <name> --engine <host:port> --stdin --default
rstream context create <name> --engine <host:port> --token-file /path/to/token --default
```

For mTLS agent authentication, store certificate paths in the selected config context or set `RSTREAM_MTLS_CERT_FILE` and `RSTREAM_MTLS_KEY_FILE`.

For self-hosted engines using a private CA, attach the CA to the context so CLI and SDK calls verify Engine TLS without relying on a workstation trust-store change:

```bash
rstream context create <name> \
  --engine <host:port> \
  --token-file /path/to/token \
  --engine-tls-ca-file /path/to/engine-ca.pem \
  --default \
  --no-api-url
```

### 2) Forward a local port

```bash
rstream forward 8080
```

## Switching projects and contexts

### Account-wide setups (developer machines)

Fast path: select another project endpoint and set it as default.

```bash
rstream project list
rstream project use <project-endpoint>
```

If a specific context name is preferred:

```bash
rstream project use <project-endpoint> --name <context-name>
```

### Project-scoped setups (remote devices)

Create a new context (or update an existing one), then set it as default.

```bash
rstream context create <name> --engine <host:port> --token <project-scoped-token>
rstream context use <name>
```

## Context management

List contexts:

```bash
rstream context list
```

Inspect a context:

```bash
rstream context get <name>
```

Set the default context:

```bash
rstream context use <name>
```

Delete a context:

```bash
rstream context delete <name>
```

## Automation output

Use JSON output for agent and CI workflows:

```bash
rstream login -o json
rstream project list -o json
rstream project use <project-endpoint> -o json
rstream context list -o json
rstream context get <name> -o json
rstream context create <name> --engine <host:port> --token-file /path/to/token --default -o json
rstream tunnel list -o json
rstream client list -o json
rstream webtty list -o json
rstream events -o json
```

Commands that wait for browser approval keep progress messages on stderr when `-o json` is selected, so stdout remains machine-readable.

For local webhook receiver tests, use the webhook-compatible event mode:

```bash
rstream events \
  --webhook \
  --events tunnel.created,tunnel.deleted \
  --forward-to http://localhost:3000/api/rstream/webhook
```

The command prints a generated `whsec_...` signing secret to stderr when
`--webhook-secret` is not provided. The receiving backend should use that same
secret to verify `rstream-signature`. `--forward-to` can target `http://`
receivers for local development and `https://` receivers when testing a deployed
backend.

## Codex and local MCP

For a Codex workstation, either complete the normal developer-machine path first or register the local MCP server before login and let Codex initiate OAuth.

```bash
rstream codex setup
```

`rstream codex setup` writes a Codex MCP server entry that runs `rstream mcp serve`. The local MCP server reuses the selected rstream configuration and can expose OAuth login start/poll tools, runtime preparation for a selected project, runtime status, workspace/project discovery, project creation options, explicit project creation or checkout start, project plan inspection, project logs, usage, TURN usage, short-lived TURN credential minting, stable domain inspection and management, project settings, short-lived delegated token creation, managed local tunnels, WebTTY command execution, WebTTY filesystem sidecar operations, remote network exposure through WebTTY, and remote MCP surface discovery and invocation.

Use rstream CLI 1.18.1 or later for the full Codex MCP workflow, including runtime preparation, WebTTY filesystem tools, remote exposure, and longer MCP tool timeouts for user-approved login.

MCP does not create a rstream account anonymously. On a clean workstation, login remains the user-approved boundary; if the user does not already have an account, the hosted browser step handles signup or sign-in.

After login, Codex should prepare a project with `rstream_runtime_prepare` before managed local tunnels, WebTTY, remote exposure, or remote MCP calls when no context is ready or the selected context is stale. That tool creates or repairs the local project context with the long-lived developer-machine login token. If several tunnel projects are available and the prompt did not name one, Codex should list the choices and ask instead of inferring from naming conventions alone or adding a preferred example. It must not call `rstream_token_create` for workstation setup; that token endpoint is for short-lived delegated handoff flows.

### Remote network and MCP bridge

When a machine already exposes WebTTY, Codex can use the local MCP server to ask that machine to run `rstream forward` for a service that is local to the remote host. This fills the third side of the remote access model: WebTTY provides command execution, the WebDAV sidecar provides filesystem access when enabled, and `rstream_remote_expose` provides network access to HTTP, TCP, UDP, TLS, DTLS, or QUIC services on the same machine. Agents should read `exec_path`, `fs_path`, and `fs_mode` from WebTTY inventory and pass those values to the matching MCP tools instead of assuming `/` or `/fs`.

The WebDAV sidecar rejects symlinks that resolve outside its configured `--fs-root`, but it is not a sandbox. It is still WebTTY-process filesystem access, so long-lived servers should use a dedicated OS user and the narrowest useful root.

For published exposures, the protocol is an edge-facing tunnel protocol. For private exposures, there is no public edge protocol: HTTP/1.x, HTTP/2, MCP, TCP, and TLS ride over a private bytestream tunnel, while UDP, HTTP/3, DTLS, and QUIC ride over a private datagram tunnel.

For non-MCP published remote services, set `token_auth=true` or `rstream_auth=true` in the MCP call when the endpoint must not be open to the internet. Remote MCP exposures default to token authentication when `mcp_path` is set.

The remote host must have a usable `rstream` binary and context, or the MCP call must pass the required environment with the `env` argument. The persistent remote runner currently expects a POSIX shell on the WebTTY host. The remote expose process is tracked under `$HOME/.rstream/remote-exposes` on the remote host and can be stopped with `rstream_remote_expose_stop`.

When `webtty_url` is a direct authenticated-E2E URL, pass `known_server=<name>`
to `rstream_remote_expose` and `rstream_remote_expose_stop`. The local MCP
server then uses the same known-server store as `rstream webtty exec`: it
verifies the pinned server endpoint identity and loads the associated client
identity without adding a Control plane lookup. For `rstrm://...` targets, MCP
uses live WebTTY inventory first and only reaches the Control plane when
workspace-managed trust requires it.

For local MCP servers running on devices or robots, pass `mcp_path=/mcp` to `rstream_remote_expose`. The created tunnel is labeled with `application-protocol=rstream.mcp`, `rstream.mcp.transport=streamable-http`, and `rstream.mcp.path=<path>`. Codex can then discover the surface with `rstream_remote_mcp_discover`, list its tools with `rstream_remote_mcp_tools`, and call a tool with `rstream_remote_mcp_call`.

## Diagnostics

Run `rstream doctor -o json` after setup changes or when troubleshooting. It checks local config, selected context, token claims, Control plane API authentication, project resolution, engine address, DNS, TLS or QUIC transport, engine clients, and engine tunnels without printing token values.

## Logout

Logout deletes locally stored authentication material for the current rstream environment.

```bash
rstream logout
```

Logout does not delete contexts. Contexts created for remote devices may remain usable if they contain a token, until deleted or updated.

## Summary

- **Projects** are managed in rstream and selected via **project endpoint**.
- **Contexts** are local and define engine endpoint selection and authentication; they can be linked (by `apiUrl`) or unlinked.
- **Developer machine**: `rstream login` then `rstream project use <project-endpoint>` or `rstream_runtime_prepare` through MCP (linked contexts using the long-lived login token).
- **Remote device**: `rstream context create ... --engine ... --token ...` with a project-scoped token (unlinked context; scoped does not necessarily mean short-lived).
- **Short-lived delegated tokens**: use them for immediate URL, browser, or runtime handoff flows, not as the stored workstation credential.
- Once a default context exists: `rstream forward <port>` creates a tunnel and forwards traffic to `localhost:<port>`.
