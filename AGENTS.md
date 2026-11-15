# Repository guidance

## Shared conventions

Follow the existing architecture, naming and coding style. Preserve public contracts, authentication, certificate verification and project policy. Keep changes in the component that owns the behavior.

Update user documentation and examples when behavior changes. Run the relevant checks; place regression tests with the owning component. Keep secrets, execution logs, investigation notes and standalone upstream reproductions outside the repository. Do not link to private repositories from public source, documentation or PR descriptions.

## Repository-specific guidance

Follow `CODING_STYLE.md` and `CONTRIBUTING.md`. The SDK and CLI share configuration; keep CLI/YAML/Docker option normalization in the existing catalogs and preserve explicit false, zero and empty values. Use the verified ICE build preparation. Keep cancellation, deadlines and goroutine ownership explicit; preserve the WebTTY security and release-integrity requirements.
