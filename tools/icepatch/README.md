# Verified ICE build patch

`icepatch` prepares a temporary source patch for `github.com/pion/ice/v4 v4.4.2`.
Updating STUN/TURN URLs while gathering candidates otherwise races with the
background gathering workers. Each gathering cycle must use a URL snapshot
captured on the agent event loop; continual gathering takes a fresh snapshot.

The patch is the `gather.go` change originally proposed in
[pion/ice#957](https://github.com/pion/ice/pull/957). It changes no public API.
A [new proposal against v4](https://github.com/pion/ice/pull/1049) includes
stronger race regressions. Keep this workaround until
a released upstream version contains and passes the same regression tests.

## Build this repository

The Makefile prepares and uses the patch for CLI builds, cross-compilation,
FIPS artifacts, test binaries and `make tests`. For individual Go commands:

```sh
make ice-patch
GOFLAGS="-modfile=$PWD/out/ice-patch/go.mod" go test -race ./...
go build -modfile=out/ice-patch/go.mod ./cmd/rstream
```

## Build a consuming module

Go does not inherit dependency `replace` directives. Installing a newer SDK
alone therefore cannot apply this patch to another application's binary.
The helper is available in this repository's source but is not yet included
in a published SDK release. Set `RSTREAM_GO_SOURCE` to the absolute path of
a reviewed checkout, then run the local helper from the consuming application's
module directory. Use the returned `go.mod` for **every** build and test of
the application. Git and Go are required on the build host.

```sh
go run "$RSTREAM_GO_SOURCE/tools/icepatch/main.go"
GOFLAGS="-modfile=$PWD/out/ice-patch/go.mod" go test -race ./...
go build -modfile=out/ice-patch/go.mod ./cmd/application
```

The [video samples](https://github.com/rstreamlabs/rstream-examples/tree/bc9339d7f1b4d4c81822e0018d4097e985839bdc/webrtc-video/ice-patch)
ship their own helper and `ice-patch.sh`; use those together when adopting a
sample. They require neither a separate SDK checkout nor a new SDK release.

Prepare before setting `GOFLAGS`, `GOOS` or `GOARCH` for the target. An active
Go workspace must be disabled with `GOWORK=off` for the consuming module.
The command supports exactly the reviewed ICE version and rejects an existing
ICE replacement. Other replacements in the consumer's module are preserved.
Keep `out/` ignored. After changing the module graph, remove `out/ice-patch`
and prepare again; never edit generated sources to resolve an error.

The command verifies the official module archive's identity and content hash,
the original source, the embedded patch, and the patched source. It copies the
module to `out/ice-patch/ice` and generates an alternate module file with a
relative local replacement. It does not edit the shared Go module cache or
the consumer's `go.mod` and `go.sum`. A changed or unexpected generated source
causes preparation to fail. The generated `provenance.json` records hashes.
There is no new runtime dependency or rstream Engine change.

Check which source was selected with:

```sh
go list -modfile=out/ice-patch/go.mod -m github.com/pion/ice/v4
go version -m ./application
```

Both should identify the local `out/ice-patch/ice` replacement. Preserve the
provenance file with build evidence. To retire the patch, upgrade the official
ICE dependency, run the URL-update race and media-restart regressions, then
remove the preparation step and alternate-module flag together.
