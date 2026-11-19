# Interactive CLI inventory

Run `rstream ui` after selecting a context with the [CLI workflow](002-cli-workflow.md). It watches visible clients, tunnels and WebTTY servers in one terminal. Engine permissions determine which categories and fields are available; an empty result alone does not prove a permission denial.

## Switch context or project


`rstream ui` can change the runtime context without restarting the process.
Press `c` from the inventory to open the context and project picker:

- `1` and `2` select the separate context and project views; `Tab` toggles them.
- `Enter` switches only for the current UI process and does not write config.
- `d` switches and saves the selected context as the default.
- `/` searches the active view.
- `r` refreshes projects from the active Control plane API.
- `Esc` returns to the inventory without changing context.

Each table has one row per choice and explicit `CURRENT` and `DEFAULT` columns.
The detail panel shows the selected target properties. Selecting a remote
project without a local context builds an in-memory context for a session-only
switch. A persistent selection creates or refreshes the project context with
the same engine and TURN metadata as `rstream project use`, then writes the
config selected by `--config`, `RSTREAM_CONFIG`, or the default config path.

Local contexts do not depend on Control plane access. They remain available
when the current context is unlinked, when no Control plane API is defined, or
when the selected token cannot list projects. Remote project discovery runs in
the background and reports its error in the picker without interrupting the
current Engine inventory. By default, projects are loaded only for the Control
plane API linked to the active context. `--api-url` or `RSTREAM_API_URL` pins
that scope; unlinked local contexts remain available.

`--context` and `RSTREAM_CONTEXT` select the initial runtime. Interactive
selection can override them for the running UI, but saving another default does
not override `RSTREAM_CONTEXT` in future processes, so the UI displays a
warning. `RSTREAM_ENGINE` remains authoritative and prevents switching to a
context configured for another engine. An active WebTTY session must be closed
with `Ctrl+g q` before switching so a remote terminal is never disconnected
implicitly.


## Open WebTTY

Select a WebTTY server and press Enter. The UI resolves the same identity, known-server pin and workspace-device grants as the CLI. It never creates a trust-on-first-use entry for authenticated E2E. A missing known-server pin fails before sending terminal content. See [WebTTY](009-webtty.md) for enrollment, OS users and supported transports.
