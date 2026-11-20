# Forward your first service with the CLI

Use the CLI to forward a service that already runs locally. The following
walkthrough publishes a disposable HTTP service and verifies the public
response. Use an [installed CLI](000-installation.md) or
[a binary from your checkout](001-building-from-source.md#try-the-cli).

## Connect the CLI

| Where you connect | How to get a context |
| --- | --- |
| Hosted rstream | Sign in, then select a project as shown below. A Basic project can publish the HTTP example. |
| Self-hosted Community Edition | Configure the direct Engine hostname, trusted certificate and locally signed JWT using the [CE agent context](https://rstream.io/docs/tunnels/self-hosted/deployment#agent-context). |
| An existing deployment | Use the context and credentials provided by your operator. See [identity options](#choose-an-identity) when provisioning a device or CI runner. |

For hosted rstream, approve sign-in in your browser. For a workstation,
choose **My computer** and **Development**. Copy the `ENDPOINT` value from
`rstream project list` into the project selection command.

```sh
rstream login
rstream project list
rstream project use 'my-project-endpoint'
```

If the list is empty, create a project in your workspace or ask its owner to
give you access. If your previous approval was restricted to specific projects,
run `rstream login` again and approve the intended project. See [hosted project setup](https://rstream.io/docs/getting-started/quickstart-guide#select-a-project).
The CLI saves the choice as its default context. Go programs that use
`config.NewClientFromEnv()` can reuse it.

## Publish a first HTTP service

You need a [working CLI context](#connect-the-cli), Python 3 and curl. The
public address is reachable from the Internet. If your service or project
requires caller authentication, follow the
[protected CLI example](https://rstream.io/docs/tunnels/http-tunnels/authentication#enable-access-control)
on a compatible managed project. On CE, configure authentication in the
upstream application or [try private access](004-tunnel-configuration.md#try-a-private-connection).

### Start the local service

In the first terminal, create a directory for the demo response and serve
only that directory on loopback.

```sh
demo_dir="$(mktemp -d)" &&
  cd "$demo_dir" &&
  printf 'Hello from rstream\n' > index.html &&
  python3 -m http.server 8080 --bind 127.0.0.1
```

In Windows PowerShell, create the directory and start the server with the Windows Python launcher.

```powershell
& {
  $demo = Join-Path ([IO.Path]::GetTempPath()) ([guid]::NewGuid().ToString())
  New-Item -ItemType Directory -Path $demo -ErrorAction Stop | Out-Null
  Set-Location $demo -ErrorAction Stop
  Set-Content -Encoding ASCII index.html 'Hello from rstream'
  py -3 -m http.server 8080 --bind 127.0.0.1
}
```

### Forward and verify

In a second terminal, check that the demo server started and returns
`Hello from rstream` locally. Use `curl.exe` in Windows PowerShell.

```sh
curl -fsS http://127.0.0.1:8080/
```

When that matches, forward the demo service.

```sh
rstream forward 127.0.0.1:8080 --http
```

Open the HTTPS address printed by the CLI. Expect `Hello from rstream` at
both addresses and a request in the local server log. For a CE Engine using a
private certificate authority, verify the printed hostname with the CA file.

```sh
curl --cacert /path/to/ca.pem 'https://your-tunnel-host/'
```

Use `curl.exe` in PowerShell and supply the real URL and CA path. The
[CE setup guide](https://rstream.io/guides/self-host-rstream-engine-ce-with-docker-compose)
walks through a local deployment.

Stop the public forwarder with Ctrl+C. Leave the HTTP server running to
[try private access](004-tunnel-configuration.md#try-a-private-connection)
or [verify TLS termination](006-tls-dtls-tunnels.md#forward-a-tls-service)
against the same service. Otherwise, stop the HTTP server as well. For
another service, [choose the protocol and access mode](004-tunnel-configuration.md).
If the request failed, start with [troubleshooting](https://rstream.io/docs/support/troubleshooting).

## Choose an identity

| Situation                       | Identity and setup                                      | Boundary                                                                                                         |
| ------------------------------- | ------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| Developer workstation           | `login`, then `project use`                             | Personal Token for the selected Control plane API, bounded by the browser approval.                                  |
| Server, CI or device            | Use the Service or application login choice, or supply a project-scoped Token            | A workspace Token belongs to one workspace and survives creator departure. Choose the required permissions and projects. |
| Certificate-authenticated agent | Enrolled mTLS certificate and key or a supported signer | Native Engine and Engine HTTP API identity; Control plane administration still needs a token.                    |
| Browser or immediate handoff | Short-lived delegated token from a trusted issuer | Expires quickly and can restrict access to a specific tunnel. |

### Request specific permissions

Omit `--permission` to review the preset choices. Repeat `--permission <scope>` to ask for an exact set of rights outside the presets. The approving user can narrow resources and choose an expiration or **No expiration**. `rstream login -o json` reports the verified owner and effective access without printing the Token.

### Import an existing Token

Import an existing token from a file or standard input.

```sh
rstream login --token-file /path/to/account-token
# Or pipe the token from your secret source:
secret-reader | rstream login --token-stdin
```

`secret-reader` represents your own secret-manager command, not an rstream tool. File and stdin inputs are mutually exclusive. `--token-storage macos-keychain` stores a token reference in YAML instead of its value on supported macOS builds.

### Configure a service or CI runner

Select **Service or application** during browser login on a compatible Control plane, then choose the workspace and projects. A workspace owner or admin approves the request. The Token grants service APIs within one workspace and can be used after its creator leaves. For an unlinked context, provision a project-scoped Token with the create/connect/list permissions needed by the service. Keep the token file owned by the service account, then create an unlinked context:

```sh
rstream context create service \
  --engine 'engine.example.com:443' \
  --token-file /path/to/project-token \
  --no-api-url --default
```

`context create` also accepts `--token-stdin` or `--token`; prefer file/stdin for secret injection. These three inputs are mutually exclusive. `--token-file` imports the credential into the context. After rotating it,
run `rstream context update service --token-file /path/to/project-token`.
For a credential supplied by the service's secret manager at launch, set
`RSTREAM_AUTHENTICATION_TOKEN` in that process. An unlinked context does
not inherit the workstation's account token. Replace 'engine.example.com:443' with the Engine hostname and port for that project. Use `--engine-tls-ca-file /path/to/engine-ca.pem` for a private CA. Run `rstream doctor` to check the context before using it.

### Authenticate an agent with mTLS

Enroll the public certificate with the project and grant its Engine permissions before using it. The shared connection config can contain:

```yaml
contexts:
  - name: service-mtls
    engine: <project-engine-host:port>
    auth:
      mtls:
        certificateFile: /etc/rstream/client.pem
        keyFile: /etc/rstream/client-key.pem
```

Select it with `rstream context use service-mtls`. File paths and inline `certificate`/`key` are alternatives; complete pairs are required. `RSTREAM_MTLS_CERT_FILE` and `RSTREAM_MTLS_KEY_FILE` explicitly select mTLS instead of a stored context token. Combining those variables with `RSTREAM_AUTHENTICATION_TOKEN` fails before connection. One native connection cannot authenticate with both a token and a certificate.

Engine listing, watch and session APIs reuse the certificate through [automatic Engine API discovery](015-engine-mtls-turn.md). Workspace/project administration, enrollment and region resolution still require Control plane token access. Certificate-only devices should use an explicit regional Engine address without a region selector. [External signers](016-external-mtls-signer.md) and [transport settings](020-agent-transport.md) cover hardware keys and private CAs.

## Contexts and environments

| Concept          | Stored values and purpose                                                                                                 |
| ---------------- | ------------------------------------------------------------------------------------------------------------------------- |
| Environment      | One Control plane `apiUrl`, a personal or workspace Token and transport defaults.                                                         |
| Project endpoint | Hosted project identifier used by `project use` to resolve its Engine and create a context.                               |
| Context          | Named Engine endpoint, authentication and transport overrides. Optional `apiUrl` links it to an environment.              |
| Default context  | A context name used unless an explicit selector overrides it. Use unique names or `--api-url` to disambiguate duplicates. |

Unlinked contexts have no `apiUrl` and do not inherit Control plane tokens. Linked contexts can inherit environment token and transport defaults; a context mTLS identity replaces an inherited token as a complete identity.

| Operation                            | Command                                                        |
| ------------------------------------ | -------------------------------------------------------------- |
| Select another hosted project        | `rstream project use <project-endpoint>`                       |
| Choose a local name for that project | `rstream project use <project-endpoint> --name <context-name>` |
| List or inspect local contexts       | `rstream context list`, `rstream context get <name>`           |
| Save a default context               | `rstream context use <name>`                                   |
| Remove local context                 | `rstream context delete <name>`                                |
| Select for one command               | `rstream --context <name> tunnel list`                         |

The process environment can override configuration. A nonempty value selects an override.

| Variable                                          | Format and purpose                                              | Default or condition                                                              |
| ------------------------------------------------- | --------------------------------------------------------------- | --------------------------------------------------------------------------------- |
| `RSTREAM_CONFIG`                                  | Path to the shared CLI/SDK YAML file                            | `~/.rstream/config.yaml`; `--config` overrides it.                                |
| `RSTREAM_API_URL`                                 | Absolute Control plane API URL                                  | Uses selected environment/configuration when omitted.                             |
| `RSTREAM_CONTEXT`                                 | Local context name                                              | Overrides the configured default.                                                 |
| `RSTREAM_ENGINE`                                  | Engine `host:port`                                              | Explicit runtime destination; do not substitute a project name.                   |
| `RSTREAM_AUTHENTICATION_TOKEN`                    | Bearer token string for the selected runtime                    | Cannot be combined with explicit mTLS material.                                   |
| `RSTREAM_MTLS_CERT_FILE`, `RSTREAM_MTLS_KEY_FILE` | Paths to the agent's PEM certificate and private key            | Both required; override the stored runtime identity.                              |
| `RSTREAM_REGION`                                  | Region selector resolved through the Control plane              | Requires project metadata; omit for explicit Engine-only devices.                 |
| `RSTREAM_TUNNEL_TRANSPORT`                        | `auto`, `tls` or `quic`                                         | `auto`; see [transport configuration](020-agent-transport.md).                          |
| `RSTREAM_QUIC_TRANSPORT`                          | Legacy selector: `1` for QUIC, any other nonempty value for TLS | Compatibility input; use `RSTREAM_TUNNEL_TRANSPORT` in new setups.                |
| `RSTREAM_CONTROL_PLANE_HEADERS`                   | JSON object of string HTTP headers                              | Optional trusted backend headers; never apply them to anonymous Engine discovery. |

## Automation, diagnostics and logout

Add `-o json` to supported inventory/config commands, such as `project list`, `project use`, `context list`, `context get`, `tunnel list`, `client list`, `webtty list`, `events` and `login`. Browser approval progress remains on stderr so stdout stays machine-readable.

Run `rstream doctor -o json` when configuration, identity or connectivity changes. It inspects the selected context and probes configured Engine/API paths without printing token values. Certificate permission denials can be warnings rather than failed admission. Applications can call the [Go diagnostic API](018-doctor-api.md).

```sh
rstream logout
```

Logout removes locally stored authentication for the current environment; it does not delete contexts or revoke their independent device credentials.

For the next task, use [declarative forwarding](007-declarative-tunnels.md), [file sharing](008-file-sharing.md), [WebTTY](009-webtty.md), the [interactive inventory](012-cli-ui.md), [webhooks](010-webhooks.md) or [Codex/MCP](011-mcp.md).
