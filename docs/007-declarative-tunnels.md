# Manage declarative tunnels

`rstream run` keeps a set of local services connected through tunnels described in YAML or Docker labels. Use it when a daemon should discover and reconcile services over time; `rstream forward` is the simpler path for one foreground tunnel. Install and configure the CLI first with the [CLI workflow](002-cli-workflow.md).

## Choose how to run

| Command        | Use it for                                                                  |
| -------------- | --------------------------------------------------------------------------- |
| `forward`      | One foreground tunnel configured through command arguments.                 |
| `run --apply`  | A declared set of tunnels from YAML, with optional watch/reconciliation.    |
| `run --docker` | A set discovered from container labels, with optional watch/reconciliation. |

## Apply YAML or discover Docker services

Start or reuse the [loopback service](002-cli-workflow.md#start-the-local-service)
on port 8080. The example needs a context that permits published HTTP.
Save the configuration as `tunnels.yaml` in the terminal where you run
the CLI. Keep that file outside the demo directory the HTTP server serves.

```yaml
version: 1
tunnels:
  - name: web
    forward: "127.0.0.1:8080"
    tunnel:
      publish: true
      protocol: http
      upstreamTLS: false
      http:
        version: http/1.1
      labels:
        app: web
```

If your project requires HTTP caller authentication, set its policy
before applying the file. For token authentication, add `auth: { token: true }`
under `tunnel.http`.

```sh
rstream -v run --apply ./tunnels.yaml
```

Open the `forwarding` URL from the `Tunnel created` log while the runner is active. Expect
`Hello from rstream` from the loopback demo. With token authentication,
[verify a scoped caller token and anonymous denial](https://rstream.io/docs/tunnels/http-tunnels/authentication#token-authentication).
Stop the runner with Ctrl+C. Add `--watch` when it should reconcile edits
to the YAML file.

For Docker discovery, use the complete [Compose example](../examples/run-docker/README.md).
Its containers declare the upstream and access policy through labels.

## YAML schema

The runner expands environment variables before decoding the YAML. Use variables such as `${RSTREAM_TOKEN}` for scalar values.

`tunnel.tls.ciphers` selects TLS ciphers, equivalent to `forward --tls-ciphers`.
Each apply file must contain exactly one YAML document. Unknown fields are
rejected, including nested transport fields in inline contexts. Enum values and
hostnames must be nonempty when set; omit the field to leave it unspecified.

`forward`, apply YAML and Docker labels use the same tunnel normalization and
validation rules. Explicit `false` is preserved. Upstream TLS uses `--upstream-tls`
in the CLI, `tunnel.upstreamTLS` in YAML and `upstream-tls` in Docker labels.
Docker defaults to `publish: true` and `protocol: http`; forward/apply leave
unspecified exposure settings to the tunnel API.

Forward targets accept a bare port, `host:port` or an IPv6 address in brackets.

### Apply fields

The example uses the selected context. Optional `contexts` entries can reference a central identity with `{external: true, name: service}` or define an inline `engine`, `token` and `transport`. Inline contexts require a token; use an external central context for mTLS and signers. Choose the fields that apply to each tunnel's protocol.

| Field                                           | Format and purpose                                                        | Default or condition                                                                 |
| ----------------------------------------------- | ------------------------------------------------------------------------- | ------------------------------------------------------------------------------------ |
| `version`                                       | Integer schema version                                                    | Required: `1`.                                                                       |
| `tunnels`                                       | List of tunnel entries                                                    | Required; unique nonempty names.                                                     |
| `name`, `forward`                               | Operational name and bare port, `host:port` or `[IPv6]:port` target       | Required on each entry.                                                              |
| `context`                                       | Named key in `contexts`, or inline object                                 | Omit to reuse the selected CLI context.                                              |
| `tunnel`                                        | Object containing the properties below                                    | Required.                                                                            |
| `tunnel.publish`                                | Boolean; expose an ordinary public endpoint                               | Omit to use API defaults; `false` requests private access permitted by the plan.     |
| `tunnel.protocol`, `tunnel.type`                | Protocol `http/tls/tcp/dtls/quic/webtty`; semantics `bytestream/datagram` | Must be compatible; see [tunnel properties](004-tunnel-configuration.md).            |
| `tunnel.host`, `tunnel.port`                    | Public hostname; reserved TCP port as integer 1–65535                     | Port applies to published TCP; do not set a hostname on TCP.                         |
| `tunnel.labels`                                 | String key/value map                                                      | Optional inventory metadata.                                                         |
| `tunnel.upstreamTLS`                            | Boolean controlling upstream TLS                                          | At tunnel level, never under `http`; incompatible with TLS passthrough.              |
| `tunnel.trustedIPs`, `tunnel.geoip`             | Lists of CIDRs and ISO two-letter country codes                           | Optional edge restrictions.                                                          |
| `tunnel.allowCrossRegionRouting`                | Boolean permitting cross-region routing                                   | Subject to Engine policy.                                                            |
| `tunnel.datagramGuaranteedDelivery`             | Boolean selecting reliable datagram framing                               | Datagram tunnels only; do not enable blindly for latency-sensitive RTP.              |
| `tunnel.http.version`                           | `http/1.1`, `h2c` or `h3`                                                 | HTTP only; upstream TLS/ALPN has its own rules.                                      |
| `tunnel.http.auth.token`, `.rstream`            | Booleans enabling machine token or account authentication                 | HTTP only; project policy and availability apply.                                    |
| `tunnel.http.gate.challenge`                    | Boolean browser challenge gate                                            | HTTP only; does not replace authentication.                                          |
| `tunnel.http.cors`                              | Object of origin/method/header lists and credentials/cache policy         | Published HTTP only; see the example below.                                          |
| `tunnel.tls.mode`                               | `terminated` or `passthrough`                                             | TLS tunnel behavior.                                                                 |
| `tunnel.tls.minVersion`                         | `tls1.2` or `tls1.3`                                                      | Terminated TLS/DTLS policy.                                                          |
| `tunnel.tls.alpns`, `.ciphers`                  | Lists of ALPN names and cipher-suite names                                | Protocol/Engine validation still applies.                                            |
| `tunnel.tls.mtls`                               | Boolean requiring published client certificates                           | Separate from the agent's own authentication.                                        |
| `contexts.<key>.external`, `.name`              | Boolean reference plus optional local context name                        | `external: true` loads the central context; name defaults to the key.                |
| `contexts.<key>.engine`, `.token`, `.transport` | Inline `host:port`, token string and shared transport object              | Engine and token required unless external; see [transport fields](020-agent-transport.md). |

### Context Resolution Order

When opening a control channel for a tunnel:

1. The entry’s `context`, if present
   - inline object: used directly
   - string: reference to `contexts.<name>`
2. CLI/global context resolution (`--context` or default configured context)

## Docker Labels (`--docker`)

A container can define one or more tunnels using labels:

```yaml
labels:
  rstream.tunnel.web.forward: "8080"
  rstream.tunnel.web.publish: "true"
  rstream.tunnel.web.protocol: "http"
  rstream.tunnel.web.http.version: "http/1.1"
  rstream.tunnel.web.label.app: "web"
```

The default path keeps identity under the agent operator's control: use `--docker-default-context prod` (or the selected context), without a container `rstream.context` label. To let trusted container owners select among local contexts, opt in explicitly:

```sh
rstream run --docker --watch --docker-allow-context-labels
```

Only that mode accepts `rstream.context: "prod"`. The label selects an existing central context; it cannot define credentials or signer commands. Container owners can then choose any context the agent can resolve, so this delegates identity selection as well as routing. Unknown contexts fail. Access to the Docker socket itself is privileged host access.

### Label Reference

Each `<name>` identifies one tunnel declared on the container. The label suffix selects a tunnel property:

| Label                                                | Value or requirement                                                              |
| ---------------------------------------------------- | --------------------------------------------------------------------------------- |
| `rstream.tunnel.<name>.forward`                      | Required port or explicit discovered container IP and port.  |
| `rstream.tunnel.<name>.publish`                      | true or false, default true                                                       |
| `rstream.tunnel.<name>.protocol`                     | http or tls or tcp or dtls or quic or webtty, default http                        |
| `rstream.tunnel.<name>.type`                         | bytestream or datagram                                                            |
| `rstream.tunnel.<name>.host`                         | Requested stable or verified custom public domain; optional, not allowed for TCP. |
| `rstream.tunnel.<name>.upstream-tls`                 | Boolean enabling upstream TLS; not TLS passthrough.                               |
| `rstream.tunnel.<name>.label.<k>`                    | String value for inventory metadata key k; one label per key.                     |
| `rstream.tunnel.<name>.trusted-ips`                  | Comma-separated source CIDRs permitted by edge policy.                            |
| `rstream.tunnel.<name>.geoip`                        | Comma-separated ISO two-letter country codes restricting edge access.             |
| `rstream.tunnel.<name>.http.version`                 | http/1.1 or h2c or h3                                                             |
| `rstream.tunnel.<name>.http.auth.token`              | Boolean enabling HTTP token access.                                               |
| `rstream.tunnel.<name>.http.auth.rstream`            | Boolean enabling edge account access, subject to plan.                            |
| `rstream.tunnel.<name>.http.gate.challenge`          | Boolean browser challenge gate; does not grant authorization.                     |
| `rstream.tunnel.<name>.tls.mode`                     | terminated or passthrough                                                         |
| `rstream.tunnel.<name>.tls.minVersion`               | tls1.2 or tls1.3                                                                  |
| `rstream.tunnel.<name>.tls.alpns`                    | comma-separated                                                                   |
| `rstream.tunnel.<name>.tls.ciphers`                  | comma-separated                                                                   |
| `rstream.tunnel.<name>.port`                         | reserved published TCP port                                                       |
| `rstream.tunnel.<name>.allow-cross-region-routing`   | true or false                                                                     |
| `rstream.tunnel.<name>.datagram-guaranteed-delivery` | true or false, datagram tunnels only                                              |
| `rstream.tunnel.<name>.tls.mtls`                     | true or false                                                                     |

`http.auth.*` and `http.gate.*` are valid only for HTTP tunnels (`protocol=http`).

`tls.mtls` enables mTLS for clients connecting to the published tunnel endpoint. It is separate from agent authentication, which controls how `rstream run` authenticates its own control-channel connection.

For HSM identities exposed through a local executable, use a central [external mTLS signer context](016-external-mtls-signer.md). Select it as the default context or reference it with `external: true`; YAML tunnel entries and Docker labels do not define executable commands.

For agent authentication, `rstream run` uses the selected CLI context or explicit environment variables. Token authentication and mTLS authentication are mutually exclusive on the control-channel connection. Inline `contexts.<name>.token` entries are supported for self-contained apply files; for mTLS agent authentication, use a named CLI configuration context with `auth.mtls`, or set `RSTREAM_MTLS_CERT_FILE` and `RSTREAM_MTLS_KEY_FILE` in the process environment. Engine HTTP API operations reuse the selected token or enrolled mTLS identity through [API discovery](015-engine-mtls-turn.md). Control plane administration still requires a token.

### Forward Target Resolution (Docker)

| Target | How the agent selects an address |
| --- | --- |
| Port, such as `8080` | Uses the container address on `--docker-network`, when available. Otherwise it selects the first discovered network in name order. |
| IPv4 address and port, such as `172.18.0.3:8080` | The address must match one discovered for that container. |
| IPv6 address and port, such as `[fd00::3]:8080` | The address must match one discovered for that container. |

Discovery prefers IPv4 on each network, then its global IPv6 address.
Use a bare port for dynamic container addresses. Use `--apply` to forward to
a DNS name or a target outside the discovered container addresses.

## Flags

| Option                     | Purpose                                            | Default or condition                                                                                       |
| -------------------------- | -------------------------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| `--apply <file>`           | Load tunnel definitions from YAML.                 | Select a local file.                                                                                       |
| `--docker`                 | Discover tunnels from Docker labels.               | Requires access to the Docker socket.                                                                      |
| `--watch`                  | Reconcile when the selected source changes.        | Without it, apply the initial configuration once.                                                          |
| `--docker-socket`          | Select the Docker socket.                          | `unix:///var/run/docker.sock`.                                                                             |
| `--docker-network`         | Select a container network for forwarding.         | Optional; otherwise use the discovery rules below.                                                         |
| `--docker-default-context` | Select the default CLI context for Docker tunnels. | Defaults to the normal selected context; containers cannot override it unless the opt-in below is enabled. |
| `--docker-allow-context-labels` | Allow `rstream.context` labels to select a configured identity. | `false`; delegate only to trusted container owners. |

For a published HTTP tunnel that requires a caller token,
[verify an authorized and an anonymous request](https://rstream.io/docs/tunnels/http-tunnels/authentication#token-authentication)
after applying the configuration.

## Managed Labels

`rstream run` adds ownership labels to each tunnel:

| Label                | Value                                        |
| -------------------- | -------------------------------------------- |
| `rstream.managed-by` | `run`.                                       |
| `rstream.source`     | `apply` or `docker`, identifying the source. |

The [YAML example](../examples/run-yaml) and [Docker example](../examples/run-docker) contain complete configurations.

## Connection lifecycle

`rstream run` keeps tunnels in sync from a YAML file (`--apply`) or Docker labels (`--docker`), with optional watch/reconcile.

Both launch modes retry temporary Engine failures and resource conflicts.
Watch mode also reloads the desired configuration when it changes. The
runner reports the first failure and subsequent recovery. A lost
control channel stops new connections through that channel. Established
connections can continue on their existing data paths while the runner
reopens the route. Stopping the runner closes its forwarding sessions.

## HTTP CORS

`forward` uses the six `--cors-*` options covered in [HTTP tunnels](005-http-tunnels.md#cross-origin-browser-access-cors). This apply file sets the same policy in YAML.

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

YAML fields below live under `tunnel.http.cors`; Docker labels use the same field after `rstream.tunnel.<name>.http.cors.`. Docker lists are comma-separated; YAML uses arrays.

| Field suffix       | Meaning and format                                                       | Default or condition                                                         |
| ------------------ | ------------------------------------------------------------------------ | ---------------------------------------------------------------------------- |
| `allowedOrigins`   | Explicit HTTP(S) origins, without paths, credentials, query or fragment. | Required when CORS is configured; no wildcard or null origin.                |
| `allowedMethods`   | Allowed HTTP method names.                                               | GET, HEAD, POST.                                                             |
| `allowedHeaders`   | Allowed request header names.                                            | Authorization, Content-Type.                                                 |
| `exposedHeaders`   | Response headers browser scripts may read.                               | No extra headers by default.                                                 |
| `allowCredentials` | Boolean permitting browser-managed credentials.                          | false; not needed for an explicit delegated bearer with credentials omitted. |
| `maxAge`           | Integer preflight cache lifetime in seconds.                             | 0; range 0–86400.                                                            |

An explicitly supplied list replaces its defaults. Omitting `cors` leaves upstream CORS unchanged; an empty policy is invalid. These options require a published HTTP tunnel. A preflight cannot authorize a subsequent request; normal authentication and resource constraints still apply. Unknown spellings are rejected.
