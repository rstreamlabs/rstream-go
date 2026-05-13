# Run Apply Example

This example runs `rstream -v run --apply` on the host and forwards to a local `whoami` service.

## Prerequisites

- A configured rstream context (default context), or set `RSTREAM_ENGINE` with either `RSTREAM_AUTHENTICATION_TOKEN` or the `RSTREAM_MTLS_CERT_FILE` / `RSTREAM_MTLS_KEY_FILE` pair in your shell.

## Run

Terminal 1 (whoami):

```bash
go run ./cmd/whoami --listen :8080
```

Terminal 2 (rstream -v run):

```bash
rstream -v run --apply examples/run-yaml/tunnels.yaml --watch
```

## tunnels.yaml

```yaml
version: 1
tunnels:
  - name: "whoami"
    forward: "127.0.0.1:8080"
    tunnel:
      publish: true
      protocol: "http"
      http:
        upstreamTLS: false
        version: "http/1.1"
      labels:
        app: "whoami"
```
