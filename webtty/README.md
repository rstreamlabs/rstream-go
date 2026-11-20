# WebTTY Go package

Use the [Go WebTTY guide](../docs/webtty/003-go-runtime.md) for commands,
streaming sessions and managed execution. The package also implements the
terminal payload and key-envelope formats below.

## Encryption formats

The Go helpers implement two profiles. Select a profile when creating identities; an existing identity is not silently converted.

| Profile | Terminal payload suite | Session-key envelope suite | Availability |
| --- | --- | --- | --- |
| Standard default | `aes-256-gcm`, fresh 96-bit nonce per message | `hpke-x25519-hkdf-sha256-aes-256-gcm` | Standard build; X25519 encryption identity. |
| `fips-compatible` | `aes-256-gcm-random-nonce` | `p256-hkdf-sha256-aes-256-gcm-random-nonce` | Standard and FIPS builds; P-256 ECDH, HKDF-SHA256, internally generated nonces. |

ChaCha20-Poly1305 identifiers are reserved, not selectable implementations; the helpers reject unsupported suites. A FIPS build accepts only the second profile over WebTransport. Protocol interoperability, including browser WebCrypto, does not attest a CMVP-validated deployment. See the [FIPS profile](../docs/017-fips-140-3-profile.md) for artifact and transport boundaries.

Recorded E2E events expose both `key_context` and `key_context_raw`.
`key_context` is for inspection and may be represented as JSON. Consumers that
reconstruct an E2E session envelope for replay, join, or decrypt-material flows
must use `key_context_raw`, which is the unpadded base64url encoding of the
exact key-envelope/AAD context bytes used by the endpoint.
