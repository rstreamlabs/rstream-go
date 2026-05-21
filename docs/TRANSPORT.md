# Transport Configuration

Transport settings define how an rstream client establishes its upstream session to the rstream edge. This layer is about getting a reliable, policy-compliant network path from the client environment to the edge, and about how the client is admitted when the session is established.

Transport configuration is independent from tunnel properties. Tunnel properties control how traffic is exposed and forwarded once an upstream session exists; transport controls how that upstream session is created in the first place.

## Configuration

Transport settings live in the YAML config file and can be defined at:
- **Environment level** (`environments[].transport`) for defaults tied to a Control plane API `apiUrl`
- **Context level** (`contexts[].transport`) for overrides on a specific runtime profile

The effective transport used by the CLI is a safe merge: start from the environment transport (if the
context is linked to that `apiUrl`), then merge the context transport over it. Override values are applied
only when explicitly set (non-empty strings, non-nil pointers, non-empty maps). Proxy headers are merged
by key and never cleared unless explicitly overridden.

Example:

```yaml
environments:
  - apiUrl: "https://rstream.io"
    transport:
      dns:
        override: "1.1.1.1:53"
        tls: true
        serverName: "cloudflare-dns.com"
        dnssec: true
      proxy:
        http: "http://proxy.corp:3128"
        fromEnvironment: false
        headers:
          X-Company: "acme"
contexts:
  - name: "acme"
    apiUrl: "https://rstream.io"
    engine: "e7e8a732.aws-eu-west-3-1.c.rstream.io:8443"
    transport:
      bind:
        mode: "interface"
        interface: "en0"
      proxy:
        headers:
          X-Env: "ci"
```

## Security Invariant

The client-to-edge session is always negotiated over TLS 1.3. Transport settings never downgrade this requirement and do not change the cryptographic guarantees; they only influence which network path carries the TLS 1.3 session.

When an HTTP CONNECT proxy is used, the proxy only provides a TCP tunnel to the edge. The rstream TLS 1.3 session is then established through that tunnel and remains end-to-end between the rstream client and the edge.

## Admission

Admission is how the edge decides whether to accept the upstream session.

Token admission is the default. The token is presented inside the rstream protocol handshake within the TLS 1.3 session. It is not an HTTP header and is not related to request-level token access control on HTTP tunnels.

Certificate admission uses a TLS client certificate. This mode is typically used in enterprise environments where identity, rotation, and device provisioning are managed through PKI. The client authenticates at session establishment time, using mutual TLS at the transport layer.

## Connectivity Paths

Direct connectivity opens a TCP connection to the edge destination and negotiates the rstream TLS 1.3 session on top of it. This is the normal mode on networks with standard outbound connectivity.

HTTP CONNECT proxy connectivity first connects to an HTTP proxy, issues a CONNECT request to create a TCP tunnel to the edge, and then negotiates the rstream TLS 1.3 session through that tunnel. This mode is designed for environments where outbound Internet access is restricted and only allowed through a proxy, or where direct outbound TCP is blocked.

The proxy hop itself may be plain TCP or protected with TLS depending on whether the proxy endpoint is configured as HTTP or HTTPS. Regardless of the proxy hop, the client-to-edge session remains TLS 1.3 once the CONNECT tunnel is established.

SOCKS5 proxy connectivity is available with `proxy.socks5`. The TCP/TLS transport uses SOCKS5 CONNECT. QUIC transport uses SOCKS5 UDP ASSOCIATE.

```yaml
transport:
  proxy:
    socks5: "socks5://proxy.corp:1080"
    username: "agent"
    password: "<proxy-password>"
```

Only one proxy type may be configured at a time. Configurations that set both `proxy.http` and `proxy.socks5` are rejected before the engine session is established.

`proxy.fromEnvironment: true` lets the SDK read the standard process proxy environment when no explicit `proxy.http` or `proxy.socks5` value is configured. The lookup honors `HTTPS_PROXY`, `HTTP_PROXY`, `ALL_PROXY`, and `NO_PROXY` in upper- and lower-case forms. HTTP and HTTPS proxy URLs map to `proxy.http`; `socks5://` and `socks5h://` URLs map to `proxy.socks5`.

```yaml
transport:
  proxy:
    fromEnvironment: true
```

The SDK does not read macOS System Settings, Windows Internet Options, or desktop-session proxy stores directly. Long-lived agents and service processes need reproducible transport configuration; configure process environment variables or set the YAML proxy fields explicitly.

## Deterministic Egress

Transport settings can bind the outbound connection to a specific local IP address or select an address from a specific network interface. This is useful on multi-homed hosts, split-routing setups, and environments where the default route is not the route you want for reaching the edge.

IPv4-only or IPv6-only dialing can be enforced when one address family is filtered, unstable, or operationally undesirable. This is an address-selection constraint; it does not change the security model.

## DNS Resolution

DNS override allows the client to use a dedicated resolver for edge name resolution instead of the system resolver. This is useful when system DNS is constrained, inconsistent across environments, or must follow a specific egress route.

The same DNS path is used for:
- resolving the edge hostname before dialing when advanced DNS options are enabled;
- discovering `SVCB` and `HTTPS` records for ECH, using the target host and ALPN in the same way a browser would.

The transport DNS options are:
- `dns.override` — send DNS queries to a specific resolver address, for example `1.1.1.1:53` or `1.1.1.1:853`.
- `dns.tls` — use DNS over TLS for these queries. This requires `dns.override`; when the override omits a port, rstream defaults to `853`.
- `dns.serverName` — set the TLS server name used for DNS over TLS. This is required when `dns.override` is an IP address.
- `dns.dnssec` — require authenticated DNSSEC answers from the resolver.

These options are opt-in. Without them, the client uses the platform resolver configuration for ECH discovery and for direct hostname resolution.

On macOS, rstream reads the active resolver configuration from the system DNS state so scoped per-domain resolvers are respected. On Linux and other Unix targets, the fallback source remains `/etc/resolv.conf`.

When a proxy is configured without explicit DNS settings or address-family constraints, the proxy resolves the engine hostname whenever the proxy protocol supports remote names. This keeps HTTP CONNECT, MASQUE CONNECT-UDP, and SOCKS5 aligned with normal proxy behavior. When a custom DNS policy or `ipFamily` constraint is configured, the SDK resolves the engine name first and sends the selected IP address to the proxy. Use that mode when the client must rely on DNS over TLS, DNSSEC validation, IPv4-only/IPv6-only target selection, or a specific resolver path.

Interface binding and address-family settings are applied to the client side of the proxy path. For TCP/TLS, the selected local address is used for the TCP connection to the HTTP or SOCKS5 proxy. For QUIC over SOCKS5, the same local address is used for the SOCKS5 control TCP connection and UDP socket. For QUIC over MASQUE, the local selection applies to the QUIC connection to the MASQUE proxy. Custom DNS settings and address-family constraints apply before dialing the proxy path and therefore also control which IP address is sent as the engine target.

## Resilience Options

Optional MPTCP support can be enabled where available to improve resilience when multiple network paths exist. This affects how the underlying TCP connectivity behaves; it does not change the TLS 1.3 requirement.

## QUIC Transport

By default the rstream client connects to the edge over TLS 1.3/TCP. When the edge supports it, you can switch to `QUICTransport` to use a single QUIC connection for all client activity instead.

### Enabling QUIC transport

**Via environment variable** (CLI or SDK):
```bash
export RSTREAM_QUIC_TRANSPORT=1
```

**Via `QUICTransport` in Go code:**
```go
import (
    rstream "github.com/rstreamlabs/rstream-go"
    "github.com/rstreamlabs/rstream-go/config"
)

opts, _ := config.NewClientEnvOptions(config.ClientEnvOptions{RequireEngine: true})
client := &rstream.Client{
    EngineURL: opts.EngineURL,
    Token:     opts.Token,
    Transport: &rstream.QUICTransport{},
}
```

`QUICTransport` accepts the same network options as the standard `Transport`:
- `LocalAddr` — bind to a specific local IP.
- `ForceIPv4` / `ForceIPv6` — constrain address family.
- `DNSOverride` — use a custom resolver for the edge hostname.
- `DNSOverTLS` / `DNSServerName` — protect DNS lookups with DNS over TLS.
- `DNSSECEnabled` — require authenticated DNSSEC answers.
- `ProxyHTTP` — use an HTTPS MASQUE CONNECT-UDP proxy for the QUIC engine session.
- `ProxySOCKS5` — use SOCKS5 UDP ASSOCIATE for the QUIC engine session.
- `ProxyFromEnvironment` — read process proxy variables when no explicit proxy URL is configured.

For QUIC transport, `ProxyHTTP` must point at an HTTPS MASQUE proxy. If the URL path is empty, the SDK uses `/.well-known/masque/udp/{target_host}/{target_port}/`. A custom path must include both `{target_host}` and `{target_port}`.

When QUIC transport is selected, `rstream doctor -o json` runs a `quic_transport` check against the engine. This catches the common case where TCP/TLS works but UDP is blocked by the local network, VPN, firewall, or proxy path.

### Lifecycle

`QUICTransport` is stateful and is designed to be held for the lifetime of the client. Create a fresh instance when reconnecting after a connection failure.

## Published and Private Tunnels

For published tunnels, transport configuration primarily concerns the publishing client that maintains the upstream session to the edge. Downstream users connect with standard clients and do not use rstream transport settings.

For private tunnels, transport configuration applies on both sides. The publishing client must maintain its upstream session, and any rstream client dialing the private tunnel must also establish its own upstream session to the edge. Private connectivity therefore depends on both clients being able to reach the edge under their respective network constraints.
