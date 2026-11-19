# Engine HTTP mTLS and TURN credentials

An enrolled client certificate can authenticate native rstream connections and
Engine HTTPS APIs from the same Go client. TURN credentials let the application
connect to the relay when a direct peer connection is unavailable.

## Use a certificate with the Engine API

Start with a [certificate-authenticated context](002-cli-workflow.md#authenticate-an-agent-with-mtls).
Use the normal project Engine address. The SDK discovers the project's
certificate-authenticated HTTPS endpoint before sending credentials. For an
explicitly provisioned endpoint, configure `ClientOptions.MTLSAPIURL` instead.
The client verifies the server certificate and project binding. Choose either
token or certificate identity for the Engine request.

The API uses HTTP/1.1 or HTTP/2 over TLS/TCP, or HTTP/3 through QUIC. In
automatic transport mode, an unavailable QUIC handshake can fall back to TCP
before sending the request. A credential refusal or an already-sent request
does not trigger fallback. The WebSocket adapter uses TLS/TCP.

## Request TURN credentials in Go

The Engine identity needs `turn.credentials.create` for the following call.
Reuse the client for later renewals and close it when the application shuts down.

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
