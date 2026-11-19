# Build the Go SDK and CLI from source

Build this checkout to test a local change or run the SDK examples. For a packaged executable, follow [Install the CLI](000-installation.md). You can compile without an account. Running a tunnel requires an Engine connection and credentials.

| Prerequisite | When you need it |
| --- | --- |
| Go | Use the version declared in [`go.mod`](../go.mod) or a compatible newer toolchain. |
| Git | Clone the checkout and prepare its pinned build dependencies. |
| Network | Download modules on a fresh checkout. |
| Make and a POSIX shell | Use the targets below on macOS or Linux. They also use standard utilities such as `find`, `awk` and `sed`. |
| C toolchain | Build with cgo or run the race detector. On macOS install the Xcode Command Line Tools with `xcode-select --install`. |

## Build the CLI

From a terminal with the tools above, clone the repository and run:

```sh
git clone https://github.com/rstreamlabs/rstream-go.git
cd rstream-go
go version
make rstream
```

The build writes the executable below `out/cmd/rstream/<channel>/<version>/<os>/<arch>/release/bin/`. On a fresh checkout, `find out/cmd/rstream -type f -name rstream` locates it. Run that executable with `--help` to verify the build. For the [CLI walkthrough](002-cli-workflow.md), use the resulting executable explicitly or put its directory on your current shell's `PATH`.

On Windows with native PowerShell, follow the [direct Windows build](../tools/icepatch/README.md#build-the-cli-with-native-powershell). It produces `out\rstream.exe`.

## Build examples and test changes

| Command | Result |
| --- | --- |
| `make http-h1-server http-h1-client` | Build the first [HTTP example pair](../examples/README.md#serve-http). |
| `make examples` | Build the Go example catalog under `out/examples/`. |
| `make tests` | Run unit and package tests against the prepared dependency graph. |

See [Contributing](../CONTRIBUTING.md) for race detection, static checks and repository practices, and the [test guide](../test/README.md) for cases that need an Engine fixture. The [FIPS profile](017-fips-140-3-profile.md) has its own build procedure.
