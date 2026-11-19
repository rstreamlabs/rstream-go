# HTTP tunnels

HTTP tunnels accept HTTP traffic at the edge and forward HTTP requests to your upstream service. Downstream clients may negotiate HTTP/1.1, HTTP/2, or HTTP/3 with the edge; this is independent from how the edge reaches your upstream.

## Forward an HTTP service

Use a [configured CLI context](002-cli-workflow.md#connect-the-cli) and an
HTTP service listening on port 8080. Test the local service first so you know
its expected response. For a disposable upstream, use the
[CLI walkthrough](002-cli-workflow.md#publish-a-first-http-service). Set
[HTTP authentication](#http-authentication) first if the service requires it.

```sh
rstream forward 127.0.0.1:8080 --http
```

Open the printed HTTPS address and confirm it returns the response from your
local service. Stop the forwarder with Ctrl+C. For an application that owns its
tunnel, the [Go HTTP server example](../examples/README.md#serve-http) uses a
bytestream tunnel directly as a listener.

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

The Engine applies HTTP access policy before forwarding the request. The
application can still authenticate the requests it receives.

| Go property | Who can reach the upstream |
| --- | --- |
| `TokenAuth` | A client with a scoped rstream tunnel token. |
| `RstreamAuth` | A signed-in rstream user who has access to the tunnel. |
| `ChallengeMode` | A client that completes the configured browser challenge and any other required checks. |
| `MTLSAuth` | A client presenting a certificate admitted for the project. |

Use [HTTP tunnel authentication](https://rstream.io/docs/tunnels/http-tunnels/authentication)
for CLI flags and a request that verifies the chosen policy. That reference
also explains credential precedence and the difference between a tunnel token
and an application token.

## Cross-origin browser access (CORS)

Use CORS when a browser on another origin must call your published HTTP
service. For CLI flags, preflight behavior and verification, see
[HTTP advanced options](https://rstream.io/docs/tunnels/http-tunnels/advanced-options#cross-origin-browser-access-cors).

In Go, set `TunnelProperties.HTTPCORS` to an `*httpcors.Config` from
`github.com/rstreamlabs/rstream-go/httpcors`. An omitted configuration leaves
the application in control of CORS. A configured policy needs at least one
explicit origin. Authentication still applies to the browser's actual request.

| Go setting | Purpose |
| --- | --- |
| `AllowedOrigins` | Exact origins permitted to read responses. |
| `AllowedMethods` and `AllowedHeaders` | Methods and request headers allowed in preflight. |
| `ExposedHeaders` | Response headers readable by the browser. |
| `AllowCredentials` | Permit browser-managed credentials from allowed origins. |
| `MaxAge` | Cache duration in seconds for a preflight result. |

## WebSockets and proxy protocols

An HTTP tunnel can translate WebSocket upgrades between HTTP/1.1, HTTP/2 and
HTTP/3 when the deployment supports the selected versions. Use the
[WebSocket examples](../examples/README.md#choose-another-example) to choose an
upstream HTTP variant. See [HTTP CONNECT](013-http-connect.md) for TCP proxy
requests and [Extended CONNECT](014-extended-connect.md) for WebTransport
and the UDP and IP proxy protocols under MASQUE.
