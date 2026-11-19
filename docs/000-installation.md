# Install the rstream CLI

The CLI runs on Linux, macOS and Windows. Other supported Unix targets are available through the manual installer or source builds. It provides port forwarding, file sharing, terminal access and configuration shared with the Go SDK.

The installation paths in this section install the `rstream` CLI binary and its runtime dependencies. They do not install the Go SDK as a library dependency.

| Path | Tools and result |
| --- | --- |
| Released CLI below | Network access to the chosen package source; Go is not required. |
| Go library | A Go module and supported Go toolchain; use the [SDK path](003-go-sdk.md). Does not install a CLI. |
| Source checkout | Go, Git and the platform tools in [Build from source](001-building-from-source.md). Builds reviewed local code. |

## Debian/Ubuntu
Requires `curl`, Bash and sudo privileges. For Debian-based distributions, the installer deploys packaged CLI binaries and dependencies:
```bash
sudo /bin/bash -i -c "$(curl -fsSL https://rstream.io/scripts/install-debian.sh)"
```

## macOS
Use the rstream Homebrew tap. Homebrew 6 and later require one-time approval of the tap. On older Homebrew releases, skip `brew trust`.
```bash
brew tap rstreamlabs/rstream
brew trust rstreamlabs/rstream
brew install rstreamlabs/rstream/rstream
```

## Windows
On Windows, install rstream from the official WinGet Community Repository:
```powershell
winget install --id rstream.rstream --exact --source winget
```

If `winget` is not available, or a new stable release has not reached the
Community Repository yet, use the PowerShell installer:
```powershell
& { Invoke-Expression ([System.Text.Encoding]::UTF8.GetString((Invoke-WebRequest -Uri 'https://rstream.io/scripts/install.ps1' -UseBasicParsing).Content)) }
```

## Manual installation
Requires `curl` and Bash. For generic environments, use the manual installer script for the CLI binary:
```bash
/bin/bash -i -c "$(curl -fsSL https://rstream.io/scripts/install.sh)"
```

## Docker
If you run the CLI in containers, pull the public image:
```bash
docker pull rstream/rstream:latest
docker run --rm rstream/rstream:latest --help
```


`--help` verifies the image can run without credentials. For an authenticated container, mount only a dedicated configuration directory at `/state` and set `RSTREAM_CONFIG=/state/config.yaml`; reuse that mount for login, project selection and forwarding. Interactive browser approval still needs the user's action. The container's `localhost` is its own network namespace: use a reachable service address, a shared Docker network, or the platform's documented host-network path. See the [Docker forwarding example](../examples/run-docker) for a complete service pair.

## Verify the installed binary

```sh
rstream --version
rstream --help
```

## First connection

Follow the [CLI workflow](002-cli-workflow.md#connect-the-cli) to choose a hosted project or a direct Engine context, then verify a first HTTP request.

## Build from source

To compile the CLI or develop the SDK, follow [Build from source](001-building-from-source.md). It lists the Go, Git and platform-tool requirements, gives platform-specific build instructions, and identifies the resulting binaries. Installing the SDK as a library does not install the CLI.

The [FIPS profile](017-fips-140-3-profile.md) is a separate restricted distribution with its own build requirements and cryptographic module boundary.
