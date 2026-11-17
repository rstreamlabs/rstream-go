# Go SDK and CLI roadmap

These are proposed improvements, grouped by the layer a contributor changes. They are not a feature catalog or release promise. Start with [Build from source](../docs/001-building-from-source.md) and [Contributing](../CONTRIBUTING.md); use the [current guides](../docs/README.md) to determine supported behavior.

| Area | Proposal | Acceptance boundary |
| --- | --- | --- |
| Transport | [001 Native macOS DNS](001-native-macos-dns.md) | Preserve scoped resolver selection, cancellation and errors. |
| Transport | [002 QUIC companions](002-quic-companions.md) | Upgrade a compatible dependency tuple; separately qualify native datagram delivery. |
| Files | [003 Browser uploads](003-file-browser-uploads.md) | Respect write capability, limits, cancellation and read-only backends. |
| WebTTY discovery | [004 Single-target resolution](004-webtty-target-resolution.md) | Preserve authorization, capabilities and endpoint trust. |
| WebTTY identity | [005 Registered identity recovery](005-webtty-identity-rotation.md) | Owner approval, old-key revocation and audited admission. |
| WebTTY protocols | [006 Additional suites](006-webtty-cipher-suites.md) | Reviewed implementations and shared supporting-runtime vectors; no unsupported fallback. |
| WebTTY platform | [007 Windows pseudoconsole](007-windows-interactive-terminal.md) | Real PTY resize, process-tree cleanup and cancellation. |
| Agent delegation | [008 Restricted MCP](008-mcp-delegation.md) | Server-enforced tool subsets and controlled secret delivery together. |
