# rstream run

`rstream run` keeps a set of local services connected through tunnels described in YAML or Docker labels. Use it when a daemon should discover and reconcile services over time; `rstream forward` is the simpler path for one foreground tunnel. Install and configure the CLI first with the [CLI workflow](001-cli-workflow.md).

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

| Option | Purpose | Default or condition |
| --- | --- | --- |
| `--apply <file>` | Load tunnel definitions from YAML. | Select a local file. |
| `--docker` | Discover tunnels from Docker labels. | Requires access to the Docker socket. |
| `--watch` | Reconcile when the selected source changes. | Without it, apply the initial configuration once. |
| `--docker-socket` | Select the Docker socket. | `unix:///var/run/docker.sock`. |
| `--docker-network` | Select a container network for forwarding. | Optional; otherwise use the discovery rules below. |
| `--docker-default-context` | Select the default CLI context for Docker tunnels. | Individual container context labels can select their context. |

## Command Modes

| Command | Use it for |
| --- | --- |
| `forward` | One foreground tunnel configured through command arguments. |
| `run --apply` | A declared set of tunnels from YAML, with optional watch/reconciliation. |
| `run --docker` | A set discovered from container labels, with optional watch/reconciliation. |

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
      protocol: "http"           # http|tls|tcp|dtls|quic|webtty
      type: "bytestream"         # bytestream|datagram (optional)
      host: "web-project.t.cluster.example.com" # optional stable domain
      upstreamTLS: true          # optional, applies to published protocols
      trustedIPs: ["10.0.0.0/8"]
      geoip: ["FR", "DE"]
      http:
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
        ciphers: ["TLS_AES_128_GCM_SHA256"]
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

`tunnel.tls.ciphers` selects TLS ciphers, equivalent to `forward --tls-ciphers`.
Each apply file must contain exactly one YAML document. Unknown fields are
rejected, including nested transport fields in inline contexts. Enum values and
hostnames must be nonempty when set; omit the field to leave it unspecified.

`forward`, apply YAML and Docker labels use the same tunnel normalization and
validation rules. Explicit `false` is preserved. Upstream TLS uses `--upstream-tls`
in the CLI, `tunnel.upstreamTLS` in YAML and `upstream-tls` in Docker labels.
Docker defaults to `publish: true` and `protocol: http`; forward/apply leave
unspecified exposure settings to the tunnel API.

Forward targets accept a bare port, `host:port` or bracketed IPv6 such as
`[2001:db8::1]:8080`.

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

Each `<name>` identifies one tunnel declared on the container. The label suffix selects a tunnel property:

| Label | Value or requirement |
| --- | --- |
| `rstream.tunnel.<name>.forward` | required |
| `rstream.tunnel.<name>.publish` | true or false, default true |
| `rstream.tunnel.<name>.protocol` | http or tls or tcp or dtls or quic or webtty, default http |
| `rstream.tunnel.<name>.type` | bytestream or datagram |
| `rstream.tunnel.<name>.host` | Stable domain |
| `rstream.tunnel.<name>.upstream-tls` | true or false |
| `rstream.tunnel.<name>.label.<k>=<v>` | repeatable |
| `rstream.tunnel.<name>.trusted-ips` | comma-separated |
| `rstream.tunnel.<name>.geoip` | comma-separated |
| `rstream.tunnel.<name>.http.version` | http/1.1 or h2c or h3 |
| `rstream.tunnel.<name>.http.auth.token` | true or false |
| `rstream.tunnel.<name>.http.auth.rstream` | true or false |
| `rstream.tunnel.<name>.http.gate.challenge` | true or false |
| `rstream.tunnel.<name>.tls.mode` | terminated or passthrough |
| `rstream.tunnel.<name>.tls.minVersion` | tls1.2 or tls1.3 |
| `rstream.tunnel.<name>.tls.alpns` | comma-separated |
| `rstream.tunnel.<name>.tls.ciphers` | comma-separated |
| `rstream.tunnel.<name>.port` | reserved published TCP port |
| `rstream.tunnel.<name>.allow-cross-region-routing` | true or false |
| `rstream.tunnel.<name>.datagram-guaranteed-delivery` | true or false, datagram tunnels only |
| `rstream.tunnel.<name>.tls.mtls` | true or false |

`http.auth.*` and `http.gate.*` are valid only for HTTP tunnels (`protocol=http`).

`tls.mtls` enables mTLS for clients connecting to the published tunnel endpoint. It is separate from agent authentication, which controls how `rstream run` authenticates its own control-channel connection.

For HSM identities exposed through a local executable, use a central [external mTLS signer context](012-external-mtls-signer.md). Select it as the default context or reference it with `external: true`; YAML tunnel entries and Docker labels do not define executable commands.

For agent authentication, `rstream run` uses the selected CLI context or explicit environment variables. Token authentication and mTLS authentication are mutually exclusive on the control-channel connection. Inline `contexts.<name>.token` entries are supported for self-contained apply files; for mTLS agent authentication, use a named CLI configuration context with `auth.mtls`, or set `RSTREAM_MTLS_CERT_FILE` and `RSTREAM_MTLS_KEY_FILE` in the process environment. Engine HTTP API operations use token authentication.

### Forward Target Resolution (Docker)

- A bare port uses the container address on `--docker-network` when available,
  otherwise the first discovered network address (ordered by network name).
- Each network prefers IPv4 when available, falling back to its global IPv6
  address, supporting IPv6-only Docker networks.
- Explicit `host:port` and `[IPv6]:port` targets must name the discovered container
  or one of its discovered network addresses. Arbitrary external/internal hosts
  remain rejected; use `--apply` for those targets.

## Managed Labels

`rstream run` adds ownership labels to each tunnel:

| Label | Value |
| --- | --- |
| `rstream.managed-by` | `run`. |
| `rstream.source` | `apply` or `docker`, identifying the source. |

The [YAML example](../examples/run-yaml) and [Docker example](../examples/run-docker) contain complete configurations.

## Connection lifecycle

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


## HTTP CORS

`forward` uses the six `--cors-*` options documented in [Tunnel properties](003-tunnel-properties.md#cross-origin-browser-access-cors). The equivalent `run --apply` configuration is:

```yaml
version: 1
tunnels:
  - name: dashboard-api
    forward: 8080
    tunnel:
      protocol: http
      publish: true
      http:
        auth:
          token: true
        cors:
          allowedOrigins: [https://dashboard.example]
          allowedMethods: [GET, PUT]
          allowedHeaders: [Authorization, Content-Type]
          exposedHeaders: [ETag]
          allowCredentials: false
          maxAge: 600
```

Docker labels use `rstream.tunnel.<name>.http.cors.allowedOrigins`, `.allowedMethods`, `.allowedHeaders`, `.exposedHeaders` (comma-separated lists), `.allowCredentials` (boolean) and `.maxAge` (integer seconds). Lists omitted from the policy use the generic HTTP defaults. Omitting `cors` leaves upstream CORS unchanged; an empty policy is invalid. These options require a published HTTP tunnel. Unknown spellings are rejected.
