# rstream run

`rstream run` keeps tunnels in sync from a YAML file (`--apply`) or Docker labels (`--docker`), with optional watch/reconcile.

In watch mode the reconciler keeps retrying temporary Engine failures and
resource conflicts with bounded exponential backoff and downward jitter. The
jitter prevents a regional recovery from making every agent reconnect at the
same instant and never exceeds the configured retry ceiling. The first failure
is logged once, repeated failures are coalesced, and recovery reports the
number of failed attempts and outage duration before resetting the backoff.
This matters during container or agent restarts: an earlier control channel can
lose its route while established customer sessions are still carrying data.
The command treats each reconnect as a new admission generation: the ended
generation stops accepting immediately, its established TCP or datagram relays
remain owned and bounded by the root command lifecycle, and the replacement
generation opens the route again. A normal process stop still cancels and joins
every relay. Consequently, a transient control-plane failure does not turn into
payload loss, and reconnect does not leak goroutines, sockets or transports.

The negotiated heartbeat grace is deliberately much longer than the heartbeat
interval. Missing heartbeats below that grace do not change the route or active
sessions. Once it expires, the Engine withdraws the old route and rejects new
admissions; `rstream run` reconnects with bounded jitter while established
sessions finish on their original immutable data paths.

## Quick Start

```bash
# Apply a YAML spec once
rstream -v run --apply ./tunnels.yaml

# Watch the YAML for changes and reconcile
rstream -v run --apply ./tunnels.yaml --watch

# Discover tunnels from Docker labels
rstream -v run --docker --watch
```

## Flags

- `--apply <file>`: load tunnels from a YAML file
- `--docker`: discover tunnels from Docker labels
- `--watch`: watch the source and reconcile on change
- `--docker-socket`: Docker socket (default `unix:///var/run/docker.sock`)
- `--docker-network`: optional, select a specific Docker network when containers have multiple networks
- `--docker-default-context`: default CLI context for Docker tunnels

## Command Modes

- `forward`: creates a single tunnel from CLI args and forwards immediately (single tunnel, interactive).
- `run --apply`: declarative list of tunnels from YAML, supports watch/reconcile.
- `run --docker`: discovers tunnels from Docker labels, supports watch/reconcile.

## YAML Schema (`--apply`)

```yaml
version: 1
tunnels:
  - name: "web"                  # required
    forward: "127.0.0.1:8080"    # required (same semantics as rstream forward)
    context: "prod"              # optional (string ref to contexts)
    tunnel:                      # required
      publish: true
      labels:
        app: "web"
      protocol: "http"           # http|tls|dtls|quic|webtty
      type: "bytestream"         # bytestream|datagram (optional)
      host: "web-project.t.cluster.example.com" # optional stable domain
      upstreamTLS: true          # optional, applies to published protocols
      trustedIPs: ["10.0.0.0/8"]
      geoip: ["FR", "DE"]
      http:
        upstreamTLS: true        # deprecated alias for HTTP-only configs
        version: "http/1.1"      # http/1.1|h2c|h3
        auth:
          token: true
          rstream: false
        gate:
          challenge: false
      tls:
        mode: "terminated"       # terminated|passthrough
        minVersion: "tls1.2"     # tls1.2|tls1.3
        alpns: ["postgres"]
        mtls: true
contexts:
  prod:
    external: true
    name: "prod"                 # CLI context name
  main:
    engine: "engine.rstream.io:443"
    token: "${RSTREAM_TOKEN}"
    transport:
      bind:
        mode: "address"
        address: "0.0.0.0"
      ipFamily: "ipv4"
      dns:
        override: "1.1.1.1"
      mptcp: false
      proxy:
        http: "http://proxy.local:3128"
        username: "${PROXY_USER}"
        password: "${PROXY_PASS}"
        headers:
          X-Proxy-Tag: "rstream"
```

Environment variables expand in YAML values (e.g. `${RSTREAM_TOKEN}`).

### Context Resolution Order

When opening a control channel for a tunnel:

1. `tunnel.context` if present
   - inline object: used directly
   - string: reference to `contexts.<name>`
2. CLI/global context resolution (`--context` or default configured context)

## Docker Labels (`--docker`)

A container can define one or more tunnels using labels:

```yaml
labels:
  rstream.context: "prod"
  rstream.tunnel.web.forward: "8080"
  rstream.tunnel.web.publish: "true"
  rstream.tunnel.web.protocol: "http"
  rstream.tunnel.web.http.version: "http/1.1"
  rstream.tunnel.web.label.app: "web"
```

### Label Reference

- `rstream.tunnel.<name>.forward` (required)
- `rstream.tunnel.<name>.publish` (true|false, default true)
- `rstream.tunnel.<name>.protocol` (http|tls|dtls|quic|webtty, default http)
- `rstream.tunnel.<name>.type` (bytestream|datagram)
- `rstream.tunnel.<name>.host` (Stable domain)
- `rstream.tunnel.<name>.upstream-tls` (true|false)
- `rstream.tunnel.<name>.label.<k>=<v>` (repeatable)
- `rstream.tunnel.<name>.trusted-ips` (comma-separated)
- `rstream.tunnel.<name>.geoip` (comma-separated)
- `rstream.tunnel.<name>.http.version` (http/1.1|h2c|h3)
- `rstream.tunnel.<name>.http.upstreamTLS` (true|false, deprecated alias)
- `rstream.tunnel.<name>.http.auth.token` (true|false)
- `rstream.tunnel.<name>.http.auth.rstream` (true|false)
- `rstream.tunnel.<name>.http.gate.challenge` (true|false)
- `rstream.tunnel.<name>.tls.mode` (terminated|passthrough)
- `rstream.tunnel.<name>.tls.minVersion` (tls1.2|tls1.3)
- `rstream.tunnel.<name>.tls.alpns` (comma-separated)
- `rstream.tunnel.<name>.tls.mtls` (true|false)

`http.auth.*` and `http.gate.*` are valid only for HTTP tunnels (`protocol=http`).

`tls.mtls` enables mTLS for clients connecting to the published tunnel endpoint. It is separate from agent authentication, which controls how `rstream run` authenticates its own control-channel connection.

For agent authentication, `rstream run` uses the selected CLI context or explicit environment variables. Token authentication and mTLS authentication are mutually exclusive on the control-channel connection. Inline `contexts.<name>.token` entries are supported for self-contained apply files; for mTLS agent authentication, use a named CLI configuration context with `auth.mtls`, or set `RSTREAM_MTLS_CERT_FILE` and `RSTREAM_MTLS_KEY_FILE` in the process environment. Engine HTTP API operations use token authentication.

### Forward Target Resolution (Docker)

- If forward is a bare port (e.g. `8080`):
  - with `--docker-network`, uses the container IP in that network
  - otherwise uses the container name as host (or falls back to first container IP)
- If forward is `host:port`, it is used as-is

## Managed Labels

`rstream run` injects these labels into each created tunnel:

- `rstream.managed-by: run`
- `rstream.source: apply` or `rstream.source: docker`

## Examples

See the example folders:

- `examples/run-docker`
- `examples/run-yaml`
