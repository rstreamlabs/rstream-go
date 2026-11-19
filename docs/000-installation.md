# Install the rstream CLI

The CLI forwards traffic, shares files and opens WebTTY sessions. For a
released binary, choose instructions for your platform. A source checkout
has its own build and verification steps.

| Where you run the CLI | Installation |
| --- | --- |
| Linux | [Linux packages and standalone installation](https://rstream.io/docs/getting-started/installation/linux) |
| macOS | [Homebrew and standalone installation](https://rstream.io/docs/getting-started/installation/macos) |
| Windows | [WinGet and PowerShell installation](https://rstream.io/docs/getting-started/installation/windows) |
| Container | [Docker setup](https://rstream.io/docs/getting-started/installation/docker) and the [Compose example](../examples/run-docker/README.md) |
| Another supported Unix target | Select an available [release archive](https://rstream.io/docs/getting-started/installation/manual-downloads) or [build from source](001-building-from-source.md). |

## Verify the installed binary

```sh
rstream --version
rstream --help
```

## First connection

Follow the [CLI walkthrough](002-cli-workflow.md#connect-the-cli) to choose a
hosted project or a direct Engine context and verify a first HTTP request.

## Build from source

Follow [Build from source](001-building-from-source.md) to build the CLI
or develop the SDK. Use [Embed the Go SDK](003-go-sdk.md) to add it to a Go
application. The [FIPS profile](017-fips-140-3-profile.md) has separate build
and verification instructions.
