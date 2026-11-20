# External signer protocol

This is the contract for an executable that supplies TLS signatures to rstream. For device provisioning and CLI configuration, see the [external signer guide](../../docs/016-external-mtls-signer.md).

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
