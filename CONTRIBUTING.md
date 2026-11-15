# Contributing

Thanks for considering a contribution to `rstream-go`.

## Before opening a change

For small fixes, opening a pull request directly is usually fine. For larger changes to the public API, CLI behavior, or transport model, start with an issue or discussion so the shape of the change can be reviewed before implementation.

Planned CLI and protocol work is tracked in the [roadmap](roadmap/).

## Development checks

Before sending a pull request, run the main local checks:

```bash
make ice-patch
export GOFLAGS="-modfile=$PWD/out/ice-patch/go.mod"
go test ./...
go test -race ./...
go vet ./...
```

The [verified ICE build procedure](tools/icepatch/README.md) explains the
preparation step and how consuming applications select the patched source.

If a change affects examples, documentation, or generated output, update the corresponding files in the same pull request.

Public pull requests do not automatically run the repository release/build workflows. Maintainers run CI after reviewing the change; include the local commands you ran in the PR description.

## CLI configuration changes

Tunnel options from `forward`, apply YAML and Docker labels must use
`cmd/rstream/internal/tunnelconfig` for shared normalization and validation.
WebTTY runtime options use the typed `webTTYBindings` catalog for CLI/YAML
correspondence. Preserve explicit false, zero and empty values when adapting an
input source. Validate the effective configuration before starting runtime I/O.

When adding an option, extend the source-equivalence tests and the option
coverage inventories in `tunnel_config_parity_test.go` or
`webtty_config_parity_test.go`. An option without an equivalent source must have
an explicit, justified exclusion in the inventory. Keep the CLI/YAML reference
tables current and reject unsupported spellings instead of adding compatibility
fallbacks.

## Stable releases

Follow [CLI release maintenance](docs/017-cli-releases.md) for candidate integrity, protocol compatibility, promotion and package-source configuration. A source check does not authorize publication.

## Style

Keep changes small, explicit, and idiomatic.

- follow the repository coding style in [CODING_STYLE.md](./CODING_STYLE.md)
- prefer focused pull requests over broad refactors
- keep public API changes intentional and documented
- update README or docs when user-facing behavior changes

## Generated code

Some files in this repository are generated. If you modify the corresponding source definitions, regenerate the derived files before opening the pull request.

## Security

WebTTY authentication or cryptographic changes need a security review and shared Go, JavaScript and C++ vectors for the supported suites. Preserve endpoint proofs, freshness, replay and downgrade rejection, and run the affected client/server combinations. Keep audit reports and release evidence outside the source tree.

Please do not disclose vulnerabilities in public issues. See [SECURITY.md](./SECURITY.md) for the reporting guidance used by this repository.
