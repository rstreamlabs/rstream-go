# Inspect tunnels from the terminal

Select a [CLI context](002-cli-workflow.md), then run `rstream ui` to watch clients,
tunnels and WebTTY servers. Run it in an interactive terminal. If an
expected resource is missing, check the active context and its Engine
permissions.

| Key | Action |
| --- | --- |
| `1`, `2` or `3` | View WebTTY, tunnels or clients. |
| `Tab` | Switch to the next inventory view. |
| `?` | Open the keyboard help. |
| `q` | Exit the inventory. |

## Switch context or project

Press `c` to open the picker. The **Current** and **Default** columns show which
context the UI is using and which context is saved for future commands.

| Key | Action |
| --- | --- |
| `1`, `2` or `Tab` | Select or switch between local contexts and remote projects. |
| `Enter` | Use the selection in the current UI session. |
| `d` | Use the selection and save it as the default context. |
| `/` | Search the active list. |
| `r` | Refresh projects from the active Control plane. |
| `Esc` | Return to the inventory. |

Local contexts are available without Control plane access. Project discovery
uses the active context's Control plane unless you select one with `--api-url`
or `RSTREAM_API_URL`. The UI can still list local contexts when the remote
project request fails.

`--context` and `RSTREAM_CONTEXT` select the initial context. You can switch
inside the UI. A saved default applies to future commands that have no explicit
context. If `RSTREAM_ENGINE` is set, the selected context must use that Engine.

## Open WebTTY

Select a WebTTY server and press Enter. The UI follows the same endpoint trust
and identity rules as the CLI. Configure any required known-server pin before
opening a protected session. Close the terminal with `Ctrl+g q` before switching
contexts. See [WebTTY configuration](webtty/000-server-configuration.md) for setup.
