# Go SDK and CLI documentation

Start with the CLI to establish a working context, or use that same configuration in a Go application. Transport settings describe the client’s connection to the edge; tunnel properties describe the service carried over it.

## Get connected

| Guide | Use it to |
| --- | --- |
| [Install the CLI](000-installation.md) | Choose a package or build from source. |
| [CLI workflow](001-cli-workflow.md) | Authenticate, select a project and forward your first service. |
| [Transport configuration](002-transport.md) | Choose the engine transport, proxy, DNS and certificate settings. |
| [Tunnel properties](003-tunnel-properties.md) | Configure published and private services and their access policy. |
| [Runnable examples](../examples) | Integrate tunnels directly into Go applications. |

## Integrate a workflow

| Guide | Use it to |
| --- | --- |
| [WebTTY](006-webtty.md) | Operate remote terminals and command sessions. |
| [File sharing](010-file-sharing.md) | Serve files through the browser or WebDAV. |
| [Filesystem WebRTC transport](011-filesystem-webrtc.md) | Configure direct filesystem access and its HTTP fallback. |
| [Webhooks](007-webhooks.md) | Verify and handle lifecycle notifications. |
| [Command runner](008-cmd-run.md) and [MCP](009-mcp.md) | Integrate command execution and agent tools. |
| [Diagnostic API](013-doctor-api.md) | Reuse CLI diagnostics inside a Go application. |

## Protocol and security reference

| Guide | Scope |
| --- | --- |
| [HTTP CONNECT](004-http-connect.md) | Authority-form proxy requests. |
| [Extended CONNECT](005-extended-connect.md) | WebSocket and WebTransport. |
| [External mTLS signers](012-external-mtls-signer.md) | Connect a certificate identity without exporting its signing key. |
| [Engine HTTP mTLS and TURN](014-engine-mtls-turn.md) | Certificate discovery, API access and relay credential renewal. |
| [FIPS profile](010-fips-140-3-profile.md) | The restricted distribution, supported boundary and build requirements. |
| [Verified ICE build patch](../tools/icepatch/README.md) | Prepare builds using the filesystem WebRTC transport. |

For source changes, follow [Contributing](../CONTRIBUTING.md). Maintainers can use [CLI release maintenance](017-cli-releases.md) for package-source configuration and WinGet submission. The [test guide](../test/README.md) describes local fixtures and runtime integration checks.
