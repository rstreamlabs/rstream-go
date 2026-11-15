# Build the Go SDK and CLI from source

Use this path to run a modified CLI or work on the SDK itself. It builds the current checkout; [CLI installation](000-installation.md) installs a released binary instead. No rstream account or running engine is needed to compile the code. Connecting the resulting CLI or an example requires the corresponding credentials and endpoint.

## Prerequisites

| Tool | Requirement |
| --- | --- |
| Go | The version declared in `go.mod` or newer; this checkout declares Go 1.27.4. Check the selected toolchain with `go version`. |
| Git | Required to clone the repository and apply the verified ICE build input. |
| Network access | The first build downloads the locked Go modules unless they are already cached. |
| C toolchain | Required for `go test -race` and platform builds using cgo. On macOS, install the Xcode Command Line Tools with `xcode-select --install`. |
| Make and a POSIX shell | Required only for the Makefile targets. These also use standard Unix utilities such as `find`, `awk` and `sed`. |

Clone the repository and work from its root:

```sh
git clone https://github.com/rstreamlabs/rstream-go.git
cd rstream-go
go version
```

## Build a local CLI

Prepare the verified ICE source, then compile a binary at a fixed local path:

```sh
go run ./tools/icepatch -output out/ice-patch
go build -modfile=out/ice-patch/go.mod -o out/rstream ./cmd/rstream
./out/rstream --help
```

For native Windows PowerShell, use the same first command and an `.exe` output:

```powershell
go run ./tools/icepatch -output out/ice-patch
go build -modfile=out/ice-patch/go.mod -o out/rstream.exe ./cmd/rstream
.\out\rstream.exe --help
```

These commands build from this checkout without installing over another CLI. Use the output path when following the [CLI workflow](002-cli-workflow.md), or add that directory to the current process's `PATH`. The direct Go build does not set the release metadata supplied by the Makefile.

The ICE preparation creates an alternate module file under ignored `out/`; it does not modify `go.mod` or the shared module cache. Use that alternate file for subsequent builds and tests. The [ICE build guide](../tools/icepatch/README.md) explains its scope and how external applications adopt it.

## Use the Makefile

On a host with the Unix build tools, the Makefile prepares ICE and records version/platform metadata automatically:

```sh
make rstream
```

The binary is written below `out/cmd/rstream/<channel>/<version>/<os>/<arch>/release/bin/`. An untagged checkout uses the `dev` channel and its branch name as the version. `make` builds every command under `cmd/`; `make rstream` selects only the CLI. Native macOS builds enable cgo by default and need the C toolchain above.

| Target | Result |
| --- | --- |
| `make rstream` | CLI for the current host. |
| `make examples` | All sample binaries under `out/examples/`. |
| `make http-h1-server http-h1-client` | Only the selected example pair. |
| `make tests` | Repository tests with the prepared ICE input. |

## Develop and test

After preparation, run checks against the same source graph:

```sh
go test -modfile=out/ice-patch/go.mod ./...
go test -race -modfile=out/ice-patch/go.mod ./...
go vet -modfile=out/ice-patch/go.mod ./...
```

The race detector requires a supported host architecture and C compiler. Runtime fixtures that connect to an engine have additional prerequisites in the [test guide](../test/README.md); they are separate from compilation. [Contributing](../CONTRIBUTING.md) covers API, configuration, style and generated-code requirements. Cross-platform release packaging and FIPS builds follow their dedicated maintainer guides.
