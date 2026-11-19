# Go SDK and CLI documentation

The rstream CLI connects an existing service. The Go SDK gives an application ownership of its connections, streams and datagrams. Both can use the same project configuration.

## Start here

| Task | Guide |
| --- | --- |
| Install a released CLI | [Installation](000-installation.md) covers Linux, macOS, Windows and containers. |
| Build the current checkout | [Build from source](001-building-from-source.md) gives the prerequisites, build commands and output paths. |
| Publish an existing HTTP service | [CLI workflow](002-cli-workflow.md) walks through project selection, forwarding, verification and shutdown. |
| Embed rstream in a Go application | [Go SDK](003-go-sdk.md) shows how to serve requests and reach a private service. The [example catalog](../examples/README.md) has runnable client and server programs. |

## Choose and configure a tunnel

| Guide | What it helps you decide |
| --- | --- |
| [Tunnel configuration](004-tunnel-configuration.md) | Choose public or private access, a service protocol, routing and an address. |
| [HTTP tunnels](005-http-tunnels.md) | Forward a web service and choose its upstream HTTP version and access policy. |
| [TLS and DTLS tunnels](006-tls-dtls-tunnels.md) | Choose where TLS terminates and understand which cryptographic policies apply to each connection. |
| [Declarative tunnels](007-declarative-tunnels.md) | Manage tunnel configuration from YAML or Docker labels. |

## Share files, operate terminals and integrate tools

| Guide | What it helps you do |
| --- | --- |
| [File sharing](008-file-sharing.md) | Serve files over HTTPS, restrict access and choose read or write access. |
| [WebTTY](009-webtty.md) | Run a command remotely, then set up terminal access, identity and trust. |
| [Webhooks](010-webhooks.md) | Choose live events or durable deliveries and test a receiver locally. |
| [MCP](011-mcp.md) | Connect an AI client to rstream and choose the right authorization boundary. |
| [Interactive inventory](012-cli-ui.md) | View clients, tunnels and WebTTY servers and switch contexts from a terminal. |

## Protocol and deployment references

| Guide | When to use it |
| --- | --- |
| [HTTP CONNECT](013-http-connect.md) and [Extended CONNECT](014-extended-connect.md) | Carry TCP proxy requests, WebTransport or MASQUE through HTTP. |
| [Engine API mTLS and TURN](015-engine-mtls-turn.md) | Use an enrolled identity for Engine HTTPS calls or obtain relay credentials. |
| [External mTLS signers](016-external-mtls-signer.md) | Configure a machine identity that delegates private-key signing to a local helper. |
| [FIPS profile](017-fips-140-3-profile.md) | Assess and build the restricted Go distribution. |
| [Go diagnostic API](018-doctor-api.md) | Diagnose an Engine or project connection from a Go program. |
| [Agent transport](020-agent-transport.md) | Set up TLS or QUIC to the Engine, proxies, custom DNS or interface binding. |
| [Filesystem over WebRTC](021-filesystem-webrtc.md) | Choose a peer-to-peer backend for reading files. |

For repository development, see [Contributing](../CONTRIBUTING.md), the [test guide](../test/README.md) and the [roadmap](../roadmap/README.md).
