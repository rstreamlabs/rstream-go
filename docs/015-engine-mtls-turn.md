# Engine HTTP mTLS and TURN credentials

Use an [enrolled certificate](#use-a-certificate-with-the-engine-api) to
authenticate native agent connections and Engine HTTPS requests from the same
Go client. For peer-to-peer applications,
[request TURN credentials](#request-turn-credentials-in-go) to connect to the
relay when peers cannot establish a direct path.

## Use a certificate with the Engine API

Start with a [certificate-authenticated context](002-cli-workflow.md#authenticate-an-agent-with-mtls).
Use the normal project Engine address. Discovery verifies the project's
HTTPS endpoint before sending credentials. If the deployment provisions the
certificate-authenticated API URL explicitly, set `ClientOptions.MTLSAPIURL`
to its HTTPS `/api` URL for the same project and keep certificate verification
enabled. To test the configured identity, use [the Go inventory example](../examples/README.md#choose-another-example)
or run `rstream tunnel list` with inventory permission.

The API uses HTTP/1.1 or HTTP/2 over TLS/TCP, or HTTP/3 through QUIC. In
automatic transport mode, an unavailable QUIC handshake can fall back to TCP
before sending the request. A credential refusal or an already-sent request
does not trigger fallback. The WebSocket adapter uses TLS/TCP.

## Request TURN credentials in Go

Use a managed project with TURN enabled. Community Edition and the
[FIPS profile](017-fips-140-3-profile.md#supported-scope) are outside this
relay workflow. Configure an identity for the project. API issuance needs
`turn.credentials.create`; local derivation needs `turn.relay.allocate`.
Use the selected project context to choose the issuer. Keep the client for
renewal and refresh the credentials before their effective TTL expires. See
[WebRTC ICE configuration](https://rstream.io/docs/integrations/stun-turn#webrtc-ice-configuration)
for how to use the returned URLs, username and password.

```go
client, err := config.NewClientFromEnv()
if err != nil {
    return err
}
defer client.Close()
credentials, err := config.CreateTURNCredentialsFromEnv(ctx, config.TURNCredentialsEnvOptions{
    Client: client,
    TTL:    10 * time.Minute,
})
```

## TURN selection and authorization

| Available identity or configuration | Where the client obtains credentials |
| --- | --- |
| Eligible personal access token or application identity with relay coordinates | Automatic mode derives credentials locally. A token needs allocation rights, a reference and a valid expiry. |
| Other token or application identity with a Control plane URL | The config helper asks the Control plane with a project ID or endpoint. Engine fallback requires a project endpoint and applies to connection or DNS failures, timeouts and HTTP 502, 503 or 504. Authorization, certificate and quota failures stop the request. |
| Enrolled client certificate | The client asks its discovered Engine HTTPS API. |
| Programmatic client configured with an Engine only | The client asks the Engine directly. |
| Explicit `api`, `engine`, `pat` or `app` mode | The specified issuer or derivation method is used. Local application derivation uses the client ID, PKCS#8 DER-hex secret and a relay realm key. |

Automatic API issuance has a ten-second total budget by default, with up to
five seconds for the Control plane. The config helper uses the configured
Control plane when present.

Use a whole-second duration between one second and one hour for the request (default ten
minutes). Go returns the effective `TTL` in seconds with the relay URLs, username
and credential. Managed issuance needs
`turn.credentials.create` on either API. Local PAT/APP derivation needs
`turn.relay.allocate`. Project scope, membership, status, credential revocation
and expiry remain enforced. Usernames are opaque. Never log TURN passwords.

For Engine-only issuance, call `Client.CreateTURNCredentials` directly
with the default options. The config helper loads the Control plane URL,
project and relay settings. To derive credentials directly from an
application key, pass its client ID and secret to the package function
`rstream.CreateTURNCredentials`.

## CLI access with a certificate

Engine-backed `client list`, `tunnel list`, `events`, `ui`, WebTTY inventory and
session methods use the same certificate identity. Permissions still apply.
The UI retains local contexts without optional Control plane enrichment.
Doctor verifies the configured transport, discovers the mTLS API and
checks HTTP readiness and inventory. Denied reads appear as warnings and can
leave readiness unverified.
To verify native session admission, open a control channel with
`Client.Connect` and close it after the check.

Project/workspace administration, enrollment and managed WebTTY trust provisioning
still require Control plane token access. Known local/runtime WebTTY targets can
work with their separate admission/E2E material. Region selectors need authoritative
Control plane metadata; provision an explicit regional Engine context without a
selector for certificate-only devices. No hostname is inferred from a region.
