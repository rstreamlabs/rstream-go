# CLI release maintenance

CLI releases are built as immutable candidates and promoted separately after approval. Build workflows must not publish stable packages as a side effect of an ordinary source check.

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
