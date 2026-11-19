# Engine HTTP mTLS and TURN credentials

Reuse a `Client` for native runtime operations, Engine inventory, watch streams,
WebTTY session requests and TURN renewal. Existing certificate configuration
(including supported hardware/external signers) supplies the identity. A token
and certificate cannot authenticate the same request.

```go
client, err := config.NewClientFromEnv()
if err != nil {
    return err
}
defer client.Close()
credentials, err := client.CreateTURNCredentials(ctx, rstream.CreateTURNCredentialsOptions{
    TTL: 10 * time.Minute,
})
```

The context needs the ordinary project Engine address and an enrolled certificate
with `turn.credentials.create`. `Client.EngineAPIURL(ctx)` anonymously discovers
`/.well-known/rstream/engine`, validates HTTPS and the project binding, and caches
its `mtlsApiUrl` in memory. Concurrent callers share discovery. The default cache
lifetime is one hour, capped at 24 hours; no-store/no-cache bypass reuse. A failed
transport clears the cached address for a later call without replaying a POST.
Discovery has its own five-second budget and never sends certificates, signer
callbacks, tokens, cookies or Control plane headers. Redirects are refused and
server verification remains enabled even if a runtime transport was configured
insecurely. There is no YAML migration or persistent cache. `MTLSAPIURL` is an
optional programmatic override for an explicitly provisioned deployment.

HTTP/1.1 and HTTP/2 use TLS/TCP. A QUIC client uses HTTP/3 for API calls, TURN and
SSE. Auto transport may fall back to TCP on an unavailable QUIC handshake before
sending a request; authentication, TLS verification or an already-sent POST are
not fallback reasons. The Go WebSocket adapter continues to use TLS/TCP. Close
the client to cancel pending work and release its owned HTTP/QUIC connections.

## TURN selection and authorization

| Available identity or configuration | Where the client obtains credentials |
| --- | --- |
| Eligible personal access token or application identity with relay coordinates | Automatic mode derives credentials locally. A token needs allocation rights, a reference and a valid expiry. |
| Other token or application identity with a Control plane URL | The client asks the Control plane first. It switches to the Engine for connection or DNS failures, timeouts and HTTP 502, 503 or 504. Authorization, certificate and quota failures stop the request. |
| Enrolled client certificate | The client asks its discovered Engine HTTPS API. |
| Programmatic client configured with an Engine only | The client asks the Engine directly. |
| Explicit `api`, `engine`, `pat` or `app` mode | The specified issuer or derivation method is used. Local application derivation uses the client ID, PKCS#8 DER-hex secret and a relay realm key. |

Automatic API issuance has a ten-second total budget by default, with up to
five seconds for the Control plane. The config helper uses the configured
Control plane when present.

Both issuers accept empty JSON or `{ "ttlSeconds": 600 }`, integer 1..3600,
with default 600. They return `username`, `credential`, `urls` and effective
`ttl`; Go exposes response `TTL` as integer seconds; the requested option uses a duration. Managed issuance needs
`turn.credentials.create` on either API. Local PAT/APP derivation needs
`turn.relay.allocate`. Project scope, membership, status, credential revocation
and expiry remain enforced. Usernames are opaque. Never log TURN passwords.

The config helper remains available for one-off operations. Applications should
pass a reusable client in `TURNCredentialsEnvOptions.Client` or call the client
method directly. Filesystem WebRTC providers retain one client until shutdown.

## CLI access with a certificate

Engine-backed `client list`, `tunnel list`, `events`, `ui`, WebTTY inventory and
session methods use the same certificate identity. Permissions still apply.
The UI retains local contexts without optional Control plane enrichment.
Doctor checks native admission, discovery and HTTP access without minting TURN;
a denied health or inventory read is a restricted-rights warning. When health
access is denied, Doctor reports that readiness could not be verified.

Project/workspace administration, enrollment and managed WebTTY trust provisioning
still require Control plane token access. Known local/runtime WebTTY targets can
work with their separate admission/E2E material. Region selectors need authoritative
Control plane metadata; provision an explicit regional Engine context without a
selector for certificate-only devices. No hostname is inferred from a region.
