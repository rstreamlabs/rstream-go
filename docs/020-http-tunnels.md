# HTTP tunnels

HTTP tunnels accept HTTP traffic at the edge and forward HTTP requests to your upstream service. Downstream clients may negotiate HTTP/1.1, HTTP/2, or HTTP/3 with the edge; this is independent from how the edge reaches your upstream.

HTTP tunnels also carry plain authority-form CONNECT and Extended CONNECT protocols when the selected HTTP versions support them. Plain CONNECT follows normal HTTP tunnel version support and is documented in [HTTP CONNECT](013-http-connect.md). WebSocket can be translated across HTTP/1.1 Upgrade, HTTP/2 Extended CONNECT, and HTTP/3 Extended CONNECT. WebTransport, CONNECT-UDP, and CONNECT-IP require an HTTP/3 upstream tunnel, which means `type=datagram`, `protocol=http`, and `http_version=h3` for published endpoints.

## Upstream HTTP mode

Select the upstream variant the application serves:

| `HTTPVersion`         | `UpstreamTLS` | Application connection                      | Tunnel type |
| --------------------- | ------------- | ------------------------------------------- | ----------- |
| `http/1.1` or omitted | Omitted/false | Clear HTTP/1.1.                             | Bytestream  |
| `h2c`                 | Omitted/false | Clear HTTP/2 with prior knowledge.          | Bytestream  |
| Omitted               | true          | TLS with ALPN selecting HTTP/1.1 or HTTP/2. | Bytestream  |
| `h3`                  | Omitted/true  | HTTP/3 over QUIC; TLS is required.          | Datagram    |

With a TLS-negotiated HTTP/1.1/2 upstream, omit `HTTPVersion` so ALPN selects
it. HTTP/3 is the explicit exception: `h3` selects QUIC and enables upstream TLS;
an explicit false TLS option is rejected. HTTP/3 also requires the project's
QUIC entitlement and an enabled engine QUIC listener.

`http_use_tls` is a deprecated alias. Prefer `UpstreamTLS` (`upstream_tls` on
the wire); conflicting values for the two fields are rejected.

## HTTP authentication

HTTP policy selects which proof the edge requires before forwarding a request.
Keep edge access and application authentication separate: the application's
`Authorization` header can serve its own purpose.

| Option                             | Effect                                                                                                       |
| ---------------------------------- | ------------------------------------------------------------------------------------------------------------ |
| `TokenAuth` / `token_auth`         | Accept a scoped edge token from `Authorization: Bearer ...` or the reserved `rstream.token` query parameter. |
| `RstreamAuth` / `rstream_auth`     | Use an authenticated rstream account and browser session.                                                    |
| `ChallengeMode` / `challenge_mode` | Require the configured interactive challenge before forwarding.                                              |
| `MTLSAuth` / `mtls_auth`           | Require the project's admitted client-certificate identity on the public TLS connection.                     |

With token authentication enabled, a reserved query token takes precedence over
an Authorization bearer and browser cookie. A malformed, empty, repeated,
invalid or unauthorized explicit token is rejected; a browser cookie cannot
rescue it. When no token is supplied, rstream Auth may use the browser session
if enabled. The query form lets an application keep its own Authorization
header while authenticating separately at the edge.

Client-certificate admission has a separate proof path and rejects conflicting
edge credentials. Network, project and resource permissions still apply to the
chosen identity. Follow the [HTTP authentication reference](https://rstream.io/docs/tunnels/http-tunnels/authentication)
for policy setup. Valid CORS preflights are handled before authentication and
cannot reach the upstream application.

## Cross-origin browser access (CORS)

CORS allows browser applications on another origin to access a published HTTP tunnel. Without a CORS configuration, the upstream application's CORS behavior is preserved. A configured policy applies to upstream responses and tunnel errors, including authentication failures. Responses preserve existing `Vary` values and add `Vary: Origin`.

| Option                          | Meaning                                                                                                                |
| ------------------------------- | ---------------------------------------------------------------------------------------------------------------------- |
| `--cors-allow-origin <origin>`  | Enable CORS for an explicit HTTP(S) origin; repeat for each origin. No wildcard, path, credentials, query or fragment. |
| `--cors-allow-method <method>`  | Repeatable allowed method. Default: GET, HEAD, POST.                                                                   |
| `--cors-allow-header <header>`  | Repeatable allowed request header. Default: Authorization, Content-Type.                                               |
| `--cors-expose-header <header>` | Repeatable response header made readable by browser scripts. Default: none beyond browser defaults.                    |
| `--cors-allow-credentials`      | Allow browser-managed credentials for the listed origins. Default: false.                                              |
| `--cors-max-age <seconds>`      | Preflight cache lifetime, 0–86400 seconds. Default: 0.                                                                 |

```bash
rstream forward 8080 --http --token-auth \
  --cors-allow-origin https://dashboard.example \
  --cors-allow-method GET --cors-allow-method PUT \
  --cors-expose-header ETag
```

Each list option replaces that list's defaults. Origins must be explicit, such as `https://dashboard.example` or `http://localhost:3000`; `*` and opaque `null` origins are rejected. Configuration is limited to 128 entries per list and 16 KiB of strings in total. Same-origin requests continue to work. Cross-origin requests outside the configured policy are rejected; requests without an Origin header still follow the existing authentication rules.

Valid browser preflights are answered without a token and cannot access the upstream application. Actual requests, including ordinary `OPTIONS` requests, still follow the tunnel's authentication and authorization rules. CORS does not make an authenticated tunnel public.

A browser can send a delegated tunnel token using `Authorization: Bearer …` with `credentials: "omit"`; the preflight does not need a token. The token can also be passed in the `rstream.token` query parameter. `--cors-allow-credentials` is not needed for either of these token forms with `credentials: "omit"`. It is required when intentionally using browser-managed credentials and `credentials: "include"`.

The Go API exposes `TunnelProperties.HTTPCORS` as `*httpcors.Config` from `github.com/rstreamlabs/rstream-go/httpcors`. Wire/JSON fields are `http_cors.allowed_origins`, `allowed_methods`, `allowed_headers`, `exposed_headers`, `allow_credentials` and `max_age` (seconds). A nil policy is omitted; a non-nil policy requires at least one origin.

## Extended CONNECT and MASQUE

Published HTTP/3 datagram tunnels can carry WebTransport, CONNECT-UDP, and CONNECT-IP. WebTransport is an HTTP/3 session protocol with browser and native client implementations when draft versions match. CONNECT-UDP and CONNECT-IP are MASQUE protocols for UDP and IP proxying over HTTP, defined on top of HTTP Datagrams and the Capsule Protocol.

The tunnel configuration for published MASQUE endpoints is:

```go
rstream.TunnelProperties{
	Type:        rstream.TunnelTypePtr(rstream.TunnelTypeDatagram),
	Publish:     rstream.BoolPtr(true),
	Protocol:    rstream.ProtocolPtr(rstream.ProtocolHTTP),
	HTTPVersion: rstream.HTTPVersionPtr(rstream.HTTP3),
}
```

The rstream engine relays MASQUE sessions but does not terminate UDP or IP proxy semantics itself. Your upstream service should implement CONNECT-UDP or CONNECT-IP using a protocol library such as `quic-go/masque-go` or `quic-go/connect-ip-go`.

See [014-extended-connect.md](014-extended-connect.md) for the application protocols and runnable SDK examples.
