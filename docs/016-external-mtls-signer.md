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

When implementing a hardware or vendor-backed helper, follow the
[external signer protocol](../internal/mtlsexec/README.md). Enroll each
replacement certificate before switching the local identity configuration.

## Compatibility and example

`kind: exec` is implemented by the standard Go CLI and Go config package.
SDKs without this backend must reject the selected storage kind explicitly;
sharing the YAML file does not imply sharing backend capabilities.
The rstream FIPS distribution rejects this backend before execution: an
arbitrary external signer is outside its reviewed cryptographic boundary.
See [the FIPS profile](017-fips-140-3-profile.md).

Build the [example helper](../examples/mtls-exec-helper) with `make mtls-exec-helper`.
It reads a PEM key and certificate for local integration. Point the external
signer configuration at the resulting binary under `out/examples/`, with
arguments `["--cert", "/path/client.pem", "--key", "/path/client-key.pem", "--message-only"]`.
For a hardware-backed integration, implement signing through the device or
vendor API. Validate it against the enrolled certificate and measure signing
and reconnect latency on the target device.
