# Go SDK and CLI documentation

Use these guides to configure the Go client and CLI, serve traffic through a
tunnel, or work on this checkout. For a deployment or language choice, start
with [editions and plans](https://rstream.io/docs/support/editions) or
[choosing an SDK](https://rstream.io/sdks).

## Get started

| Task | Guide |
| --- | --- |
| Try an existing service with the CLI | [Install the CLI](000-installation.md), then [connect and publish a first HTTP service](002-cli-workflow.md#connect-the-cli). Use the [command map](002-cli-workflow.md#find-a-command-by-task) for other tasks. |
| Compile this checkout | [Build from source](001-building-from-source.md) covers tools, build and verification. |
| Embed rstream in Go | [Use the Go SDK](003-go-sdk.md) to serve HTTP or connect privately. |
| Run matching server and client programs | The [Go examples](../examples/README.md) cover published HTTP, private access and specialized protocols. |

## Choose and configure a tunnel

| Guide | Decision |
| --- | --- |
| [Tunnel configuration](004-tunnel-configuration.md) | Choose published or private access, a service protocol and an address. |
| [HTTP tunnels](005-http-tunnels.md) | Forward an HTTP service, select its upstream mode and configure access. |
| [TLS and DTLS tunnels](006-tls-dtls-tunnels.md) | Choose where encryption terminates and verify a TLS service. |
| [Declarative tunnels](007-declarative-tunnels.md) | Manage multiple tunnels from YAML or Docker labels. |

## Files, terminals and integrations

| Guide | Task |
| --- | --- |
| [File sharing](008-file-sharing.md) | Share a file or directory and limit access. |
| [WebTTY](009-webtty.md) | Run a remote command, then configure terminal access and trust. |
| [Webhooks](010-webhooks.md) | Choose live events or durable delivery and test a receiver. |
| [MCP](011-mcp.md) | Connect an AI client and choose the appropriate authorization boundary. |
| [Interactive inventory](012-cli-ui.md) | Inspect clients, tunnels and WebTTY servers in a terminal. |

## Advanced configuration and diagnosis

| Guide | Task |
| --- | --- |
| [HTTP CONNECT](013-http-connect.md) and [Extended CONNECT](014-extended-connect.md) | Carry proxy requests, WebTransport or MASQUE through HTTP. |
| [Engine API mTLS and TURN](015-engine-mtls-turn.md) | Use an enrolled identity for Engine HTTPS calls or obtain relay credentials. |
| [External mTLS signers](016-external-mtls-signer.md) | Delegate an agent's private-key signing to a local helper. |
| [FIPS profile](017-fips-140-3-profile.md) | Verify or build the restricted Go distribution. |
| [Go diagnostic API](018-doctor-api.md) | Diagnose a connection in an application. Use [product troubleshooting](https://rstream.io/docs/support/troubleshooting) for the CLI. |
| [Agent transport](020-agent-transport.md) | Select TLS or QUIC to the Engine, a proxy, DNS or a network interface. |
| [Filesystem over WebRTC](021-filesystem-webrtc.md) | Choose a peer-to-peer backend for reading files. |

[Contributing](../CONTRIBUTING.md) links to repository practices and the
[test guide](../test/README.md). See [CLI releases](019-cli-releases.md) for
release maintenance.
