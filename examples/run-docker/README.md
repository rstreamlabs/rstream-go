# Run Docker Example

This example runs `rstream -v run --docker` alongside a `whoami` container, Traefik-style, using Docker labels.

## Prerequisites

- A configured engine and token in `examples/run-docker/.env`.
- `rstream/rstream:latest` and `rstream/whoami:latest` images available (from registry or your local build).

## Run

```bash
docker compose up
```

The `rstream` service watches Docker events and reconciles tunnels automatically.

## docker-compose.yml

```yaml
services:
  whoami:
    image: rstream/whoami:latest
    networks:
      - rstream
    labels:
      rstream.tunnel.web.forward: "8080"
      rstream.tunnel.web.publish: "true"
      rstream.tunnel.web.protocol: "http"
      rstream.tunnel.web.http.version: "http/1.1"
      rstream.tunnel.web.label.app: "whoami"
  rstream:
    image: rstream/rstream:latest
    user: root
    command:
      - -v
      - run
      - --docker
      - --watch
    networks:
      - rstream
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    env_file:
      - .env
networks:
  rstream:
    name: rstream
```
