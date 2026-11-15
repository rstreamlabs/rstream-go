# Resolve one WebTTY server directly

Single-server WebTTY operations currently list online tunnels before matching a target. Add an authorized project-scoped lookup by tunnel ID, server ID, name or hostname, with explicit not-found and ambiguous results. Switch runtime callers only after it preserves identity, E2E policy, host keys, capabilities and older-engine compatibility.
