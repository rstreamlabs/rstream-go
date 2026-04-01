# Examples

The examples use the same config/env resolution as the CLI via `config.NewClientFromEnv()`. That keeps the sample code short and consistent with `rstream` usage.

## Prerequisites

Set a default context or `RSTREAM_ENGINE`. If your engine requires authentication, set `RSTREAM_AUTHENTICATION_TOKEN`.

Typical setup (CLI):

```bash
rstream login <token>
rstream project use <project-endpoint> --default
```

## Run

Build all examples:

```bash
make examples
```

Run an example:

```bash
./out/examples/http-h1-server --publish
```

Generate managed TURN credentials from the active config context:

```bash
go run ./examples/turn-credentials
```

Or override the current context with environment variables:

```bash
RSTREAM_API_URL=http://localhost:3000 \
RSTREAM_AUTHENTICATION_TOKEN="$RSTREAM_AUTHENTICATION_TOKEN" \
RSTREAM_PROJECT_ENDPOINT=9bfdaa8b \
go run ./examples/turn-credentials
```

When TURN routing fields are not already present in the local context, the SDK falls back to the control-plane API.

## CLI Run Examples

- `examples/run-yaml`: YAML-based `rstream -v run --apply` example with a whoami container.
- `examples/run-docker`: Docker-label-based `rstream -v run --docker` example with a whoami container.
