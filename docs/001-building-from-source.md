# Build the Go SDK and CLI from source

From a fresh checkout, build a local `rstream` executable and check that it
starts. This uses the source in the checkout. For a released executable, follow
[Install the CLI](000-installation.md).

| Tool | When you need it |
| --- | --- |
| Go | Use the minimum version in [`go.mod`](../go.mod) or a compatible newer toolchain. |
| Git and network access | Clone the repository and download its Go modules. |
| Make and a POSIX shell | Build on Linux or macOS with the Makefile. It also uses `find`, `awk` and `sed`. |
| C toolchain | Required for builds using cgo and for the race detector. On macOS, install the Xcode Command Line Tools with `xcode-select --install`. |

## Build on Linux or macOS

From a terminal with the tools above:

```sh
git clone https://github.com/rstreamlabs/rstream-go.git
cd rstream-go
go version
make rstream
```

On a fresh checkout, find the executable and check it runs:

```sh
cli="$(find out/cmd/rstream -type f -name rstream -path '*/release/bin/*')"
"$cli" --version
```

The executable reports the version built from the checkout. Store its full path
if you also have a released CLI installed. To connect to an Engine, follow the
[CLI workflow](002-cli-workflow.md#connect-the-cli) using the executable you built.

## Build on Windows

Install Go and Git, clone the repository, and run these commands from its root
in PowerShell:

```powershell
go run ./tools/icepatch -output out/ice-patch
go build -modfile=out/ice-patch/go.mod -o out/rstream.exe ./cmd/rstream
.\out\rstream.exe --version
```

The first command prepares an alternate Go module file with the selected ICE
dependency. Use that file for subsequent direct builds and tests. The direct
build omits the release metadata supplied by Make. See the
[build tool reference](../tools/icepatch/README.md) for its behavior and
consumer-module instructions.

## Build examples and test changes

| Command | Result |
| --- | --- |
| `make http-h1-server http-h1-client` | Build the first [HTTP example pair](../examples/README.md#serve-http). |
| `make examples` | Build the Go example catalog under `out/examples/`. |
| `make tests` | Run the package tests. |

See [Contributing](../CONTRIBUTING.md) for race detection, static checks and
repository practices. The [test guide](../test/README.md) covers Engine-backed
tests. The [FIPS profile](017-fips-140-3-profile.md) has its own build procedure.
