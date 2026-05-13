# Tunnel Properties

Tunnel properties define how a tunnel behaves end-to-end: whether it is publicly reachable, which protocol the edge accepts, how the edge connects to your upstream service, and which access policy is enforced before traffic is forwarded. The server validates the configuration as a whole; invalid values or inconsistent combinations are rejected to keep runtime behavior deterministic.

A practical way to think about configuration is:
- Decide whether the tunnel is **published** or **private**
- If it is published, select the **edge protocol** (`protocol`)
- Select the **upstream mode** (HTTP upstream details, TLS termination vs passthrough, mTLS)
- Add **access policy** where relevant

## Published vs private

A tunnel is either **published** (`publish=true`) or **private** (`publish=false`).

A published tunnel exposes a forwarding hostname on the edge. Standard clients connect to that hostname using the configured edge protocol. Published tunnels are where edge policy applies: TLS termination, minimum TLS version, mTLS, and (for HTTP) request-level access control.

A private tunnel has no public entrypoint. It is reachable only via an rstream client connection, by **ID or name**. Private tunnels are intended for internal connectivity where callers explicitly use rstream rather than a public hostname.

`publish` defaults to `true`. A tunnel becomes private only when it is explicitly set to `false`.

## Identification

`id` is assigned by the server and uniquely identifies the tunnel.

`name` is optional. When set, it provides a stable identifier for dialing **private** tunnels (instead of using the tunnel ID). Names are validated to remain safe as operational identifiers and lookup keys.

## Transport model: protocol and type

`protocol` describes what the edge accepts for a **published** tunnel. It is one of `http`, `tls`, `dtls`, or `quic`. It has no meaning for private tunnels.

`type` describes transport semantics: `bytestream` (reliable, ordered; TCP-like) or `datagram` (message-oriented; UDP-like). You may set it explicitly, or omit it and let the server select a coherent default.

The edge enforces compatibility between these fields. Conceptually: TLS is always a bytestream; DTLS and QUIC are always datagrams; HTTP is a bytestream for HTTP/1.1 and HTTP/2, and a datagram for HTTP/3. For private tunnels, `type` defaults to `bytestream`, but selecting `datagram` is valid when your private use case is UDP-like.

## Forwarding hostname

For published tunnels, the server returns the public forwarding hostname in `hostname` and the public forwarding port in `port`. `port` is server-managed and read-only.

Clients may set `hostname` to request a stable domain when it matches the engine-owned pattern `<slug>-<project-endpoint>.t.<cluster-domain>`. The server validates the hostname, verifies that the embedded project endpoint matches the authenticated project, and rejects hostnames already used by another active tunnel.

When no hostname is provided, the CLI generates a stable domain for reconnecting commands when it can infer the project endpoint from the configured engine. If generation is not possible, the server falls back to an allocated hostname.

`host` is deprecated and read-only. It remains populated as a compatibility authority string for older clients, but new clients should read `hostname` and `port`.

## HTTP tunnels

HTTP tunnels accept HTTP traffic at the edge and forward HTTP requests to your upstream service. Downstream clients may negotiate HTTP/1.1, HTTP/2, or HTTP/3 with the edge; this is independent from how the edge reaches your upstream.

### Upstream HTTP mode

The upstream hop supports two modes.

If `upstream_tls=false`, the upstream HTTP variant is explicit via `http_version`: use `http/1.1` for cleartext HTTP/1.1, or `h2c` for HTTP/2 over cleartext (prior knowledge).

If `upstream_tls=true`, the edge connects to the upstream over TLS and selects the upstream application protocol via ALPN. In practice this means the upstream becomes HTTP/1.1-over-TLS or HTTP/2-over-TLS depending on what the upstream negotiates. Because ALPN is a negotiated choice, forcing `http_version` while enabling `upstream_tls` expresses conflicting intent and is not allowed.

`http_use_tls` is deprecated. It is still accepted for compatibility with existing clients, but setting both `http_use_tls` and `upstream_tls` with conflicting values is rejected.

For non-HTTP published protocols, `upstream_tls` controls the upstream security mode where applicable: TLS tunnels use a TLS upstream connection when TLS is terminated at the edge, DTLS tunnels use DTLS upstream, and QUIC tunnels require upstream TLS semantics. TLS passthrough tunnels reject `upstream_tls` because the edge does not terminate or re-originate TLS.

### Token-based access control

`token_auth` enables a lightweight, request-level gate on the edge. When enabled, each incoming HTTP request must carry a valid token. Requests without a token, or with an invalid token, are rejected at the edge and never reach the upstream.

The token can be provided in either form:
- An `Authorization: Bearer <token>` header
- A `rstream.token=<token>` query parameter

This mechanism is intentionally scoped to HTTP tunnels, since it operates at the HTTP request layer. For non-HTTP tunnels (TLS/DTLS/QUIC), access control is enforced at the connection level (for example via TLS policy and mTLS on terminated TLS tunnels).
Published tunnel policy can allow more than one authentication method. Each incoming request or connection must still authenticate with one unambiguous method; requests that combine multiple authentication proofs are rejected.

## TLS tunnels

TLS tunnels accept TLS at the edge. TLS behavior depends on `tls_mode`.

With `tls_mode=terminated`, the edge terminates TLS and forwards a clear bytestream upstream. In this mode you can set `tls_min_version` (`tls1.2` or `tls1.3`) and optionally require client certificates via mTLS.

With `tls_mode=passthrough`, the edge forwards encrypted bytes upstream without decrypting. Because the edge does not terminate the TLS session, it cannot enforce TLS policy (such as minimum TLS version) and cannot validate client certificates. Those constraints must be enforced by the upstream.

### mTLS

When `mtls_auth=true`, the edge requires client certificate authentication for the published tunnel. Certificate admission is configured through mTLS credentials. mTLS requires TLS termination at the edge.

## Access policy

Access policy is evaluated at the edge before forwarding.

Network-level restrictions apply to the connection itself. `trusted_ips` restricts access by source IP range (CIDR notation). `geoip` restricts access by country (ISO 3166-1 alpha-2 codes).

Request-level controls apply to HTTP tunnels. `rstream_auth` requires end-user authentication via an rstream account before requests are forwarded. `challenge_mode` introduces an interactive step (challenge/captcha) before requests reach the upstream.
