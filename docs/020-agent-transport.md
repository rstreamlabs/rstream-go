# Configure the agent transport

Transport configuration controls the connection from an rstream process to the
engine: TLS or QUIC, local routing, proxies, DNS and certificate trust. The
[tunnel configuration](004-tunnel-configuration.md) selects the service carried
through that authenticated connection.

## Choose TLS, QUIC or automatic selection

The default `auto` mode gives QUIC a brief head start, then starts TLS if QUIC has not connected. The first transport that connects is pinned for the lifetime of the client and its control channel. Later streams and datagram sessions do not switch transport independently. Create a new client to run selection again.

Fallback covers transport establishment only. Once QUIC has connected, an authentication or rstream protocol error is returned as-is; it does not trigger a second control channel over TLS.

Select a mode through the CLI flag, environment, or shared YAML configuration:

```bash
rstream --tunnel-transport auto forward 8080
export RSTREAM_TUNNEL_TRANSPORT=quic
```

```yaml
transport:
  mode: auto # auto, tls, or quic
```

`RSTREAM_QUIC_TRANSPORT=1` and `transport.useQuic` remain supported for compatibility. New configurations should use the canonical mode setting.

## Configure a context

Set defaults under an environment in the shared YAML. A context linked to that
Control plane through its `apiUrl` inherits those defaults and can override
individual fields. Fields omitted from the context retain their environment
value. Explicit `false` overrides `true`, and HTTP proxy headers merge by name.

For example, share an HTTP CONNECT proxy across an environment while letting
one context set its own transport mode:

```yaml
environments:
  - apiUrl: https://rstream.io
    transport:
      proxy:
        http: http://proxy.corp:3128
contexts:
  - name: service
    apiUrl: https://rstream.io
    engine: project.engine.example:443
    transport:
      mode: tls
```

Use an existing context with admission credentials for your project.
`rstream doctor -o json` probes TLS and QUIC independently. Its
`tunnel_transport` check reports the configured and selected modes. In `auto`,
working TLS with unavailable QUIC is a fallback warning.

### Shared YAML fields

Paths below are relative to `environments[].transport` or `contexts[].transport`. Omitted context values inherit linked environment settings; boolean pointers preserve explicit false. Defaults apply after that merge.

| Field                              | Format and purpose                                       | Default or condition                                                                   |
| ---------------------------------- | -------------------------------------------------------- | -------------------------------------------------------------------------------------- |
| `mode`                             | `auto`, `tls`, `quic`; agent connection selection.       | auto; explicit mode takes precedence over legacy useQuic.                              |
| `useQuic`                          | Legacy boolean transport selector.                       | Omit in new configurations; true selects QUIC, false TLS.                              |
| `bind.mode`                        | `interface` or `address` local binding.                  | Omit the bind block for OS routing; an omitted mode with address uses address binding. |
| `bind.interface`                   | Local interface name such as en0.                        | Used with mode=interface; selected address must fit first-hop family.                  |
| `bind.address`                     | Local IP literal.                                        | Used with mode=address; not the remote Engine.                                         |
| `ipFamily`                         | `ipv4`, `ipv6` or omitted.                               | Omitted permits both; selects destination family, not DNS-server family.               |
| `dns.override`                     | Resolver IP/host with optional port.                     | Platform resolvers when omitted; DNS-over-TLS requires an override.                    |
| `dns.tls`                          | Boolean enabling DNS over TLS.                           | false; omitted port becomes 853.                                                       |
| `dns.serverName`                   | TLS resolver verification name.                          | Needed when DoT resolver address is an IP.                                             |
| `dns.dnssec`                       | Boolean requiring authenticated DNSSEC answers.          | false; a validation requirement, not a separate resolver.                              |
| `mptcp`                            | Boolean requesting MPTCP on TCP connections.             | false; requires OS support, does not alter QUIC.                                       |
| `tls.caFile`                       | PEM CA bundle for the Engine.                            | System roots when omitted.                                                             |
| `tls.serverName`                   | Engine SNI/certificate verification name.                | Original Engine hostname when omitted.                                                 |
| `tls.insecureSkipVerify`           | Boolean disabling Engine certificate verification.       | false; diagnostics only, rejected by FIPS.                                             |
| `proxy.http`                       | HTTP/HTTPS CONNECT proxy URL.                            | No explicit proxy by default; QUIC requires HTTPS MASQUE.                              |
| `proxy.socks5`                     | socks5/socks5h URL.                                      | Alternative to proxy.http; TCP CONNECT or QUIC UDP ASSOCIATE.                          |
| `proxy.username`, `proxy.password` | Proxy credentials as strings.                            | Optional; keep secret values out of logs.                                              |
| `proxy.headers`                    | String map of HTTP proxy headers.                        | Empty; merged by key from environment/context.                                         |
| `proxy.fromEnvironment`            | Boolean enabling standard process proxy-variable lookup. | Opt-in; explicit proxy URL takes precedence.                                           |
| `proxy.tls.caFile`                 | PEM CA bundle for an HTTPS/MASQUE proxy.                 | System roots; independent from Engine roots.                                           |
| `proxy.tls.serverName`             | Proxy SNI/certificate verification name.                 | Proxy hostname when omitted.                                                           |
| `proxy.tls.insecureSkipVerify`     | Boolean disabling proxy-hop verification.                | false; diagnostics only.                                                               |

## Engine identity and admission

The agent and Engine use TLS 1.3, either directly over TCP or within QUIC.
Token admission presents a credential in the native rstream handshake.
Certificate admission uses the agent's enrolled client certificate during TLS.
These credentials establish the agent's identity. To authorize callers of a
published service separately, configure [HTTP tunnel access](005-http-tunnels.md).

## Connect through a proxy or a private CA

An HTTP or HTTPS forward proxy creates a TCP connection to the Engine with HTTP
CONNECT. The rstream TLS session is established through it. An HTTPS proxy
also encrypts the first hop between the agent and the proxy. QUIC through an
HTTP proxy requires HTTPS MASQUE, which can carry UDP.

SOCKS5 proxy connectivity is available with `proxy.socks5`. The TCP/TLS transport uses SOCKS5 CONNECT. QUIC transport uses SOCKS5 UDP ASSOCIATE.

```yaml
transport:
  proxy:
    socks5: "socks5://proxy.corp:1080"
    username: "agent"
    password: "<proxy-password>"
```

Only one proxy type may be configured at a time. Configurations that set both `proxy.http` and `proxy.socks5` are rejected before the engine session is established.

Engine TLS verification uses the system trust store by default. Self-hosted engines with a private CA can attach that CA to the selected context:

```yaml
transport:
  tls:
    caFile: "/etc/rstream/engine-ca.pem"
    serverName: "engine.internal.example"
```

Use `caFile` to trust a private CA and `serverName` when the verification name
differs from the address. Keep verification enabled in deployed agents.

The same settings are available when creating or updating a context:

```bash
rstream context create local \
  --engine engine.internal.example:443 \
  --token-file /etc/rstream/token \
  --engine-tls-ca-file /etc/rstream/engine-ca.pem \
  --default \
  --no-api-url
```

HTTPS and MASQUE proxy verification uses the system trust store by default. Private proxy PKI can be configured with a dedicated CA bundle, and the setting applies only to the proxy hop; Engine TLS verification remains independent.

```yaml
transport:
  proxy:
    http: "https://masque.proxy.corp"
    tls:
      caFile: "/etc/rstream/proxy-ca.pem"
      serverName: "masque.proxy.corp"
      insecureSkipVerify: false
```

Use `proxy.fromEnvironment: true` to resolve a proxy from process environment
variables. Supported names include `HTTPS_PROXY`, `HTTP_PROXY`, `ALL_PROXY`
and `NO_PROXY` in upper- or lowercase. An explicit proxy URL takes precedence.
To configure an agent running as a service, set the variables in its process
environment or use the YAML proxy settings.

## Select the outbound address

Transport settings can bind the outbound connection to a specific local IP address or select an address from a specific network interface. This is useful on multi-homed hosts, split-routing setups, and environments where the default route is not the route you want for reaching the edge.

IPv4-only or IPv6-only dialing can be enforced when one address family is filtered, unstable, or operationally undesirable. This is an address-selection constraint; it does not change the security model. The constraint applies to destination records (A or AAAA), independently of the DNS server connection: an IPv6 destination can be resolved through an IPv4 DNS server, or vice versa. The same separation applies to UDP queries, TCP fallback and DNS over TLS. DNS-over-TLS certificate verification still uses `dns.serverName`.

## Choose a resolver

DNS override allows the client to use a dedicated resolver for edge name resolution instead of the system resolver. This is useful when system DNS is constrained, inconsistent across environments, or must follow a specific egress route.

rstream uses the resolver for Engine lookup and for the `HTTPS` and `SVCB`
records used in ECH discovery. The operating system`s resolver configuration
is the default. To configure DNS over TLS, set `dns.tls` and provide a
`dns.override`; the default port is `853`. When the resolver`s address is an
IP, use `dns.serverName` to verify its certificate. Set `dns.dnssec` to require
authenticated answers.

On macOS, rstream reads the active resolver configuration from the system DNS state so scoped per-domain resolvers are respected. On Linux and other Unix targets, the fallback source remains `/etc/resolv.conf`.

When a proxy is configured without explicit DNS settings or address-family constraints, the proxy resolves the engine hostname whenever the proxy protocol supports remote names. This keeps HTTP CONNECT, MASQUE CONNECT-UDP, and SOCKS5 aligned with normal proxy behavior. When a custom DNS policy or `ipFamily` constraint is configured, the SDK resolves the engine name first and sends the selected IP address to the proxy. Use that mode when the client must rely on DNS over TLS, DNSSEC validation, IPv4-only/IPv6-only target selection, or a specific resolver path.

Interface binding and address-family settings are applied to the client side of the proxy path. For TCP/TLS, the selected local address is used for the TCP connection to the HTTP or SOCKS5 proxy. For QUIC over SOCKS5, the same local address is used for the SOCKS5 control TCP connection and UDP socket. For QUIC over MASQUE, the local selection applies to the QUIC connection to the MASQUE proxy. Custom DNS settings and address-family constraints apply before dialing the proxy path and therefore also control which IP address is sent as the engine target.

## Use multipath TCP

Optional MPTCP support can be enabled where available to improve resilience when multiple network paths exist. This affects how the underlying TCP connectivity behaves; it does not change the TLS 1.3 requirement.

## Select the transport in a Go application

Use `AutoTransport` when constructing a client directly, or select a strict transport when fallback is not desired:

```go
client, err := rstream.NewClient(rstream.ClientOptions{
    Engine:    engine,
    Token:     token,
    Transport: &rstream.AutoTransport{},
})

strictQUIC, err := rstream.NewClient(rstream.ClientOptions{
    Engine:    engine,
    Token:     token,
    Transport: &rstream.QUICTransport{},
})
```

`QUICTransport` accepts the same network options as the standard `Transport`:

| SDK option                     | Purpose                                                                |
| ------------------------------ | ---------------------------------------------------------------------- |
| `LocalAddr`                    | bind to a specific local IP.                                           |
| `ForceIPv4` / `ForceIPv6`      | constrain address family.                                              |
| `DNSOverride`                  | use a custom resolver for the edge hostname.                           |
| `DNSOverTLS` / `DNSServerName` | protect DNS lookups with DNS over TLS.                                 |
| `DNSSECEnabled`                | require authenticated DNSSEC answers.                                  |
| `ProxyHTTP`                    | use an HTTPS MASQUE CONNECT-UDP proxy for the QUIC engine session.     |
| `ProxySOCKS5`                  | use SOCKS5 UDP ASSOCIATE for the QUIC engine session.                  |
| `ProxyFromEnvironment`         | read process proxy variables when no explicit proxy URL is configured. |

For QUIC transport, `ProxyHTTP` must point at an HTTPS MASQUE proxy. If the URL path is empty, the SDK uses `/.well-known/masque/udp/{target_host}/{target_port}/`. A custom path must include both `{target_host}` and `{target_port}`.

### Lifecycle

`AutoTransport` and `QUICTransport` are stateful and are designed to be held for the lifetime of the client. Create fresh instances when reconnecting after a connection failure.

## Address selection and private dialing

The SDK tries available IPv4 and IPv6 destinations in order of preference.
A caller deadline covers DNS, proxy and transport setup. It also cancels
connection attempts that are still in progress. When the caller supplies no
deadline, the SDK bounds setup to 30 seconds. The logical Engine hostname
continues to determine TLS name verification.

For a published tunnel, these settings apply to the agent. For a private
tunnel, its owner and each dialing client need their own Engine connectivity.
A Go client can use `Client.PacketDial` to dial a private datagram tunnel it owns.
Native datagrams may be lost; opt into reliable delivery when the application
needs it. See [tunnel configuration](004-tunnel-configuration.md) and the
[FIPS profile](017-fips-140-3-profile.md) for deployment constraints.
