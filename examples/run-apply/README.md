# Run Apply Example

This example runs `rstream -v run --apply` on the host and forwards to a local `whoami` service.

## Prerequisites

- A configured rstream context (default context), or set `RSTREAM_ENGINE` and `RSTREAM_AUTHENTICATION_TOKEN` in your shell.

## Run

Terminal 1 (whoami):

```bash
go run ./cmd/whoami --listen :8080
```

Terminal 2 (rstream -v run):

```bash
rstream -v run --apply examples/run-apply/tunnels.yaml --watch
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
