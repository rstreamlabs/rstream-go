# External mTLS signers

The standard Go SDK configuration package and Go CLI can authenticate an agent
with a private key held behind a local executable. This supports devices whose
HSM exposes a vendor API instead of PKCS#11. The executable supplies the public
certificate chain and signatures. TLS, certificate verification, connections
and tunnel traffic stay in rstream.

This backend does not export a private key, change Engine admission policy, or
enroll a credential. Enroll the public certificate with the intended project
first. It does not configure WebTTY end-to-end identities, local WebTTY HTTPS
certificates, published tunnel client admission, or TURN credentials.

## Configure an identity

Use the shared rstream config file:

```yaml
version: 1
contexts:
  - name: device
    engine: project.cluster.example:443
    auth:
      mtls:
        storage:
          kind: exec
          certificateSHA256: "<64 hexadecimal digits from the enrolled leaf certificate>"
          exec:
            command: /usr/libexec/device-mtls-helper
            args: ["--identity", "device"]
            timeout: 5s
            maxConcurrency: 1
            passEnv: []
```

The same auth block is supported on a configured environment. A context's
authentication takes precedence as a complete identity: a context using mTLS
does not inherit an environment token. Administrative Control plane requests
still require a separate token. An explicit token conflicting with mTLS is an
error. Explicit `RSTREAM_MTLS_CERT_FILE` and `RSTREAM_MTLS_KEY_FILE` replace
the stored identity; an incomplete pair is an error. A stored identity cannot
be reused with an explicit override to another Engine.

`certificateSHA256` is mandatory. It is the SHA-256 hash of the leaf
certificate's DER bytes, not of the PEM text or public key. Hexadecimal digits
may be upper- or lowercase and may contain colon/space separators.
For a trusted public certificate:

```sh
openssl x509 -in enrolled-client.pem -noout -fingerprint -sha256
```

Do not automatically trust a fingerprint returned by a newly installed helper.
Obtain it through the device's provisioning/enrollment process.

| Setting | Meaning |
| --- | --- |
| `command` | Required absolute executable path; no PATH lookup or shell expansion. Use a native executable on Windows. |
| `args` | Literal argument array, default empty. No quoting, variable expansion, or shell parsing. Keep secrets out of arguments. |
| `timeout` | Per-operation deadline, including time waiting for a concurrency slot. Default `5s`; greater than zero, at most `1m`. |
| `maxConcurrency` | Maximum helper operations for one resolved identity in a process. Default `1` (also selected by zero); maximum `32`. |
| `passEnv` | Names of environment variables to pass explicitly. Default empty; a named variable must exist when the helper runs. Values are not stored in YAML. |

Other storage backends' settings and separate certificate/key sources cannot
be mixed with `kind: exec`. Unknown fields inside `exec` are rejected.
Configuration paths must refer to trusted local files. On Unix, the executable
must be a regular executable file that is not writable by group or others.

The helper receives only `LANG=C`, `LC_ALL=C`, Windows `SystemRoot` where
applicable, and explicitly named variables. Provider/library loader variables
such as `OPENSSL_MODULES` or `LD_LIBRARY_PATH` are not inherited implicitly.
Pass them deliberately if the integration needs them. Prefer fixed library
locations where possible.

Create the context through the CLI if preferred:

```sh
rstream context create device --no-api-url \
  --engine project.cluster.example:443 \
  --mtls-exec /usr/libexec/device-mtls-helper \
  --mtls-exec-arg=--identity --mtls-exec-arg=device \
  --mtls-certificate-sha256 "<enrolled-fingerprint>" \
  --mtls-exec-timeout 5s
```

`context update` accepts the same flags and preserves unspecified exec
settings. Selecting exec replaces that context's previous token or mTLS source;
selecting a token replaces its mTLS source. Token and exec flags cannot be used
together. Advanced settings are edited in YAML. Context creation, inspection,
selection, and ordinary config resolution do not execute the helper.

## CLI, SDK and declarative tunnels

```sh
rstream --config /etc/rstream/config.yaml --context device doctor
rstream --config /etc/rstream/config.yaml --context device webtty server --rstream
rstream --config /etc/rstream/config.yaml --context device run --apply tunnels.yaml
```

WebTTY admission and optional E2E settings still need to match the deployment.
The Engine identity belongs in the central config, not in
`--webtty-config`'s `tls` block.

A tunnels file can use the default context or reference a named central context:

```yaml
version: 1
contexts:
  device:
    external: true
tunnels:
  - name: maintenance
    context: device
    forward: "127.0.0.1:8080"
    tunnel:
      publish: false
      protocol: http
```

Docker discovery uses the same configured identities. Container context labels
still require `--docker-allow-context-labels`; labels cannot define helper
commands. `run --watch` watches desired tunnels; restart the process to reload
the central identity configuration.

SDK applications can use `config.NewClientFromEnv()` or
`config.NewClientFromResolved()`. Applications already providing
`ClientOptions.TLSClientConfig` retain that API. No native dependency or CGO is
required by the external backend.

`doctor` checks the public certificate and signs a random challenge with each
advertised algorithm, then checks configured transports and Engine
control-channel admission. It does not require a token for these agent checks.
`doctor --deep` additionally tests a tunnel lifecycle under the identity's
existing permissions. Normal operations only load the identity when TLS first
requests a client certificate.

## Protocol v1

The executable is launched with exactly the configured arguments. Each process
handles one request: one JSON object on stdin followed by EOF, and one JSON
object on stdout followed by EOF. No banners or logging may appear on stdout.
Every request and response includes integer `version: 1`.

An identity request:

```json
{"version":1,"operation":"identity","certificateSHA256":"<64 lowercase hex digits>"}
```

Its response:

```json
{
  "version": 1,
  "certificateChain": "-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----\n",
  "capabilities": [
    {"algorithm": "ecdsa_secp521r1_sha512", "inputs": ["message"]}
  ]
}
```

`certificateChain` contains PEM certificates, leaf first, followed by any
intermediates. The public key comes from the leaf. Advertise only algorithms
actually available for that key. The caller rejects unknown/incompatible
algorithms, empty capabilities, duplicate capabilities, and unsupported modes.

A signing request:

```json
{
  "version": 1,
  "operation": "sign",
  "certificateSHA256": "<64 lowercase hex digits>",
  "algorithm": "ecdsa_secp521r1_sha512",
  "input": "message",
  "data": "<base64>"
}
```

The response is `{"version":1,"signature":"<base64>"}`.
The helper must select the enrolled identity consistently across processes and
reject a mismatching fingerprint. rstream also verifies every returned
signature against the cached enrolled public key.

| Algorithm | Signature encoding | Input modes |
| --- | --- | --- |
| `ecdsa_secp256r1_sha256` | ASN.1 DER ECDSA (r, s) | message, digest |
| `ecdsa_secp384r1_sha384` | ASN.1 DER ECDSA (r, s) | message, digest |
| `ecdsa_secp521r1_sha512` | ASN.1 DER ECDSA (r, s) | message, digest |
| `rsa_pss_rsae_sha256/384/512` | Raw RSA signature bytes | message, digest |
| `rsa_pkcs1_sha256/384/512` | Raw RSA signature bytes | message, digest |
| `ed25519` | 64 raw signature bytes, pure Ed25519 | message only |

The RSA names ending in `sha256/384/512` denote three distinct names, for
example `rsa_pss_rsae_sha384`. RSA keys must be 2048–8192 bits. RSA-PSS requests
also include integer `pssSaltLength`, equal to the hash length in bytes
(32, 48, or 64); MGF1 uses the same hash. RSA PKCS#1 v1.5 is only negotiated
where TLS permits it. The helper may support a subset of algorithms/modes.

For `message`, the helper applies the specified signature algorithm to the
original bytes. For `digest`, those bytes have already been hashed: **do not
hash them again**. A message-only OpenSSL DigestSign provider should advertise
only `message`. rstream uses Go's `crypto.MessageSigner` for TLS 1.2 and 1.3,
including QUIC. With a digest-only provider, rstream hashes the message once.
It cannot convert an existing digest back into an original message.

Protocol errors use a response such as:

```json
{"version":1,"error":"signing_denied"}
```

Allowed codes: `unsupported_version`, `unsupported_algorithm`,
`identity_unavailable`, `identity_mismatch`, `signing_denied`,
`invalid_request`, `internal_error`. A protocol response uses exit status
zero; a nonzero exit is reported as a process failure. Error messages are
controlled by rstream; arbitrary provider diagnostics are not copied into logs.

Limits: signing data 1 MiB; encoded request 2 MiB; stdout 256 KiB; stderr 4 KiB;
certificate chain 16 certificates; signature 1024 bytes. Output overflow
terminates the operation. Unknown response fields, malformed JSON, trailing
data, and unsupported protocol versions are rejected. Failures never fall back
to another credential.

## Lifetime, concurrency and rotation

The public identity is cached after its first successful load. A failed load
can be retried by the next connection attempt. Reconnection uses the same
certificate, with a fresh signer bound to the new handshake context. Its
validity dates are rechecked; it is not silently replaced on expiry.

The per-operation timeout and handshake cancellation terminate and reap the
helper. Closing a client cancels its pending dials. Established connections
retain normal SDK ownership. No helper is kept alive between operations.
Helpers must not daemonize or detach children. Unix helpers run in a dedicated
process group, which is terminated on completion or cancellation; on other
platforms only the direct helper process is owned.

The concurrency limit applies to one resolved identity inside one rstream
process, including its TLS config clones. Multiple contexts or applications
sharing one HSM may need serialization in the vendor helper/driver.

To rotate, enroll the replacement certificate, update the configured pin, and
restart/reload the application identity explicitly. Reading the new certificate
never approves it automatically. Measure process startup, signing latency, and
reconnection cost on the target device.

## Compatibility and example

`kind: exec` is implemented by the standard Go CLI and Go config package.
SDKs without this backend must reject the selected storage kind explicitly;
sharing the YAML file does not imply sharing backend capabilities.
The rstream FIPS distribution rejects this backend before execution: an
arbitrary external signer is outside its reviewed cryptographic boundary.
See [the FIPS profile](010-fips-140-3-profile.md).

[The example helper](../examples/mtls-exec-helper) is a standalone Go program
using a software PEM key for development and protocol testing. Build it with
`go build -o /absolute/path/mtls-exec-helper ./examples/mtls-exec-helper`, then
configure arguments `["--cert", "/path/client.pem", "--key",
"/path/client-key.pem", "--message-only"]`. It is not a hardware security
implementation. Replace key access/signing with the vendor API in a device
adapter; no vendor code belongs in rstream.

## Validate an integration

`go test ./internal/mtlsexec ./config ./cmd/rstream/...` covers the protocol,
real TLS 1.2/1.3 and QUIC handshakes, all supported key families, cancellation,
process cleanup, and CLI/configuration propagation. Run `go test -race ./...`
for the complete concurrency suite and `make fips-test` for profile rejection.

For an actual standalone CLI WebTTY session with an EE engine and two private
tunnels, run the opt-in fixture:

```sh
RSTREAM_ENGINE_REPO=/path/to/rstream-engine \
RSTREAM_NEXT_REPO=/path/to/rstream-nextjs \
test/e2e/external-mtls-webtty.sh
```

Install the Next.js checkout's npm dependencies first; the fixture applies its
product schema only to a fresh isolated PostgreSQL database. It builds the CLI,
the portable example helper and the EE engine, generates a P-521 test identity,
registers its fingerprint locally, and checks TLS, QUIC, reconnection and
`doctor`. It needs Go, Python 3, OpenSSL and Docker. To use an isolated local
PostgreSQL process instead of Docker, set `RSTREAM_TEST_POSTGRES_BIN` to the
absolute directory containing `initdb`, `pg_ctl`, `createdb` and `psql`.
The helper advertises message signing only, so the test exercises the HSM
integration path without depending on hardware or existing account credentials.
Generated artifacts are kept on failure, or with `RSTREAM_KEEP_RUNTIME=1`.

Measure launch/signature cost on the target device; it depends on the helper and
HSM. `go test ./internal/mtlsexec -run '^$' -bench ExecMessageSigning` measures a
software P-521 signing operation including subprocess startup and local
verification. Hardware qualification still requires the vendor implementation,
its device permissions and its actual enrolled certificate.
