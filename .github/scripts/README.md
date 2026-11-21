# CLI release maintenance

CLI releases are built as immutable candidates and promoted separately after approval. Build workflows must not publish stable packages as a side effect of an ordinary source check.

## Stable promotion

[Build release candidates](../workflows/cross-compile.yml) creates the artifacts
for a stable tag. [Promote stable releases](../workflows/promote-release.yml)
verifies the successful candidate run, tag, commit and retained checksums before
requesting the `stable-release` approval. To rerun promotion, use the candidate
run ID. For an npm-only recovery, select `npm_only` with that same candidate.
The full promotion verifies the native package API downloads before publishing the npm wrapper. An npm-only recovery checks the public downloads selected by the installer against the retained native archives. An existing package version must match the retained archive.

When a client changes the wire protocol in `pb/rstream.proto`, verify against the
Engine version it requires. Release the Engine first if it must understand the
new client protocol. Confirm that the tagged CLI opens a control channel with
an authorized identity before stable promotion.

## Package source configuration

Release workflows obtain auxiliary package sources through repository secrets. This keeps non-public repository coordinates out of public source and logs.

| Secret | Use |
| --- | --- |
| `RELEASE_NPM_REPOSITORY` | npm distribution source, in `owner/name` format. |
| `RELEASE_WINGET_REPOSITORY` | Fallback WinGet manifest source, in `owner/name` format. |
| `GIT_TOKEN` | Authorized access to those release sources. |

Configure these in the release repository before using the affected build or promotion jobs. A missing source fails before checkout; it never selects the current repository as a fallback. Source configuration does not replace candidate integrity checks or the stable-release approval gate.

## WinGet community submission

The hosted fallback source and Microsoft’s community repository are separate distribution channels. After the stable Windows x64 and arm64 archives are published, prepare a matching manifest directory for `microsoft/winget-pkgs`.

Validate the version, archive checksums and ZIP contents, then submit the version directory as a pull request to the community repository. The public install command uses that channel:

```powershell
winget install --id rstream.rstream --exact --source winget
```

A published fallback manifest does not imply acceptance by Microsoft. See the [Windows installation guide](https://rstream.io/docs/getting-started/installation/windows) for end-user installation paths.
