# Forward a Docker service from labels

This example runs a `whoami` container and uses the rstream CLI to publish it. The CLI discovers labeled containers and updates their tunnels as Docker reports changes.

| Prerequisite | Purpose |
| --- | --- |
| Docker with Compose | Runs both services and provides the Docker socket used for discovery. |
| Engine endpoint and project token | Authorize the agent to publish HTTP tunnels. Use a token scoped to this example's project. |
| Container images | The [Compose file](docker-compose.yml) uses `rstream/rstream:latest` and `rstream/whoami:latest`. |

From this directory, copy [`.env.example`](.env.example) to `.env` and set these values. Keep the file private.

| Variable | Value |
| --- | --- |
| `RSTREAM_ENGINE` | Engine hostname and port for the project, such as `engine.example.com:443`. |
| `RSTREAM_AUTHENTICATION_TOKEN` | Token with the required permissions on that project. |

```sh
docker compose up
```

Open the forwarding URL from the rstream service logs and check that `whoami` returns JSON. Stop Compose when finished. The discovery container can access the Docker socket, so run this example on a host where you trust the container and the owners of its service labels. See [declarative tunnels](../../docs/007-declarative-tunnels.md) for label options.
