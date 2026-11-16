# Go SDK and CLI guides

Choose the artifact and owner of the connection first. A released CLI forwards an existing process; an embedded SDK makes connections part of your application; a source checkout lets you build or change either.

| Path                | Start here                                                                        | First result                                                                     |
| ------------------- | --------------------------------------------------------------------------------- | -------------------------------------------------------------------------------- |
| Use a released CLI  | [000 Installation](000-installation.md) → [002 CLI workflow](002-cli-workflow.md) | Publish a known local HTTP response, verify its URL, stop the tunnel.            |
| Build this checkout | [001 Build from source](001-building-from-source.md)                              | Prepare the verified dependency graph, compile a local CLI and inspect its help. |
| Embed Go            | [003 Go SDK](003-go-sdk.md) → [runnable examples](../examples/README.md)          | Serve HTTP with `net.Listener` or dial a private service with `net.Conn`.        |

The Go SDK is the reference implementation with the broadest rstream protocol coverage. Availability still depends on Engine version/edition, project policy and plan. A source checkout can contain additions that are not yet in a published package; choose the source-build path when evaluating those additions.

## Connection and service configuration

| Guide                                                   | What it explains                                                                 |
| ------------------------------------------------------- | -------------------------------------------------------------------------------- |
| [004 Transport](004-transport.md)                       | Agent TLS/QUIC, proxy, DNS, interface binding and certificate verification.      |
| [005 Tunnel configuration](005-tunnel-configuration.md) | Private/public access, transport versus protocol, routing, services and domains. |
| [006 Declarative command runner](006-cmd-run.md)        | Apply YAML or reconcile trusted Docker labels with explicit identity selection.  |
| [007 File sharing](007-file-sharing.md)                 | Browser/WebDAV shares, transfer limits and optional writes.                      |
| [008 Filesystem WebRTC](008-filesystem-webrtc.md)       | Read-only peer transfers, authenticated signaling and HTTP fallback.             |

## Operations and integration

| Guide                                      | What it explains                                                        |
| ------------------------------------------ | ----------------------------------------------------------------------- |
| [009 WebTTY](009-webtty.md)                | First remote command, server enrollment, OS identity, config and trust. |
| [010 Webhooks](010-webhooks.md)            | Durable destinations versus local event forwarding.                     |
| [011 MCP and Codex](011-mcp.md)            | Agent setup, local/hosted authority, remote exposure and cleanup.       |
| [012 Interactive inventory](012-cli-ui.md) | UI context switching and opening a permitted terminal.                  |

## Protocol, identity and maintenance reference

| Guide                                                   | What it explains                                                 |
| ------------------------------------------------------- | ---------------------------------------------------------------- |
| [013 HTTP CONNECT](013-http-connect.md)                 | Authority-form TCP proxy requests and destination policy.        |
| [014 Extended CONNECT](014-extended-connect.md)         | WebTransport and MASQUE application protocols.                   |
| [015 Engine mTLS and TURN](015-engine-mtls-turn.md)     | API discovery, certificate reuse and relay credential renewal.   |
| [016 External mTLS signer](016-external-mtls-signer.md) | A pinned machine identity using a local signing executable.      |
| [017 FIPS profile](017-fips-140-3-profile.md)           | Separate artifact, cryptographic boundary and excluded features. |
| [018 Diagnostic API](018-doctor-api.md)                 | Invoke the CLI's checks directly from Go.                        |
| [019 CLI releases](019-cli-releases.md)                 | Maintainer integrity/promotion and package-source configuration. |

## Focused references and SDK development

| Guide                                         | Topic                                                                       |
| --------------------------------------------- | --------------------------------------------------------------------------- |
| [020 HTTP tunnels](020-http-tunnels.md)       | Upstream variants, TLS, HTTP authentication, CORS and CONNECT.              |
| [021 TLS and DTLS](021-tls-dtls-tunnels.md)   | Termination, certificates and project cryptographic policy.                 |
| [022 SDK engineering](022-sdk-engineering.md) | Go ownership, concurrency, cancellation, wire changes and regression tests. |

[Contributing](../CONTRIBUTING.md), the [test guide](../test/README.md), [ICE build procedure](../tools/icepatch/README.md) and [roadmap](../roadmap/README.md) cover work on this repository. Runtime tests need their documented local fixtures; they do not supply a deployed service or credentials.
