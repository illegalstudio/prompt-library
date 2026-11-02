# plib

A small prompt library for your terminal. Run `plib`, pick a prompt, and paste it wherever you need it.

## Build and run

Requires Go 1.26.1+ and Git 2.28+.

```sh
make build
./plib
```

The first run asks for an optional Git remote. Leave it blank for local storage. To install the binary on your Go bin path, run `make install`.

## Interactive keys

Each prompt occupies one compact row with its title, scope and body preview. A blank line separates entries. On narrow terminals, `[G]` and `[P]` abbreviate global and project scope.

| Key | Action |
| --- | --- |
| `a` | Add a prompt |
| `s` or `/` | Search titles and bodies |
| `Enter` | Copy the selected prompt |
| `e` | Edit the selected prompt |
| `Tab` | Toggle current project/global prompts and every project |
| `Up` / `Down`, `j` / `k` | Select a prompt |
| `PageUp` / `PageDown` | Move through longer lists |
| `r` | Synchronize with Git |
| `q`, `Esc` | Quit |

In the editor, enter a title, press `Enter`, then write or paste a multiline body. `Enter` inserts newlines, `Ctrl+T` returns to the title, `Tab` toggles global/project scope, `Ctrl+S` saves, and `Esc` discards the draft. Prompts default to global scope. The initial list shows your saved prompts, with the most recently copied first; a new library starts empty.

`Tab` works in both the title and body, including when editing an existing prompt. Saving a scope change moves the same prompt between the global and project folders, preserving its ID and usage history. When editing another project's prompt from the all-projects view, its original project remains the project option. If no portable project remote is available, the editor explains why only global scope is available. Add a remote to the current project and restart plib to enable project scope; the library's optional sync remote is separate. A library has no built-in example prompts.

## Commands

```sh
plib init --local
plib init --remote git@github.com:you/prompts.git
plib add --title 'Code review' --body 'Review this change for correctness.'
printf 'Review this change.\nFocus on regressions.\n' | plib add --title Review --stdin --project
plib list
plib list migrations --all
plib list --json
plib show PROMPT_ID
plib copy PROMPT_ID
plib sync
plib remote
plib remote set git@github.com:you/prompts.git
plib remote remove
plib --version
```

IDs can be shortened to a unique prefix. `list`, `search`, and `ls` are aliases. `show` prints the exact body without updating usage. `copy` updates usage even if clipboard access fails and the body must be copied manually. `add` without flags opens the editor.

## Storage and projects

The default directory is `~/.plib`. Override it with `PLIB_HOME` or `--home PATH`. The directory is its own Git repository:

```text
~/.plib/
  .git/                         # Git history and machine-local setup state
  global/
    <id>.json
  projects/
    <repository>-<remote-hash>/
      <id>.json
      <id>.json.<time>.conflicted  # only when needed
```

Each JSON record holds an ID, title, exact multiline body, canonical project remote, creation/update dates, and last usage date. Separate prompt files keep unrelated edits independent. Data directories are created with mode 0700 and new prompt files with mode 0600 where the OS supports these permissions. Prompts are plaintext, including in Git history. Use a private remote for private prompts.

The current project is recognized through its `origin` remote, including from subdirectories and Git worktrees. If `origin` is absent, the only configured remote is used. SSH and HTTPS forms such as `git@github.com:acme/api.git` and `https://github.com/acme/api.git` share an identity. Credentials, transport, and SSH port are excluded; GitHub and Bitbucket paths are lowercased. Other hosts retain path case. SSH host aliases and renamed/moved remotes need the same canonical URL on each machine. Local filesystem remotes cannot identify a portable project. Outside a project with a portable remote, global prompts remain available and `--project` reports an error.

## Git synchronization and recovery

The tool uses your installed Git and existing SSH/HTTPS credentials. It never opens an authentication prompt inside the UI. Authenticate first with your normal Git tools. Automatic commits use the repository-local invocation identity `plib <plib@localhost>` and bypass signing and hooks for this dedicated data repository.

On startup, after saving or copying, and on `r`/`sync`, plib:

1. Finishes an interrupted merge and commits pending local changes.
2. Fetches and merges the remote default branch, including existing histories on another machine.
3. Resolves compatible prompt fields independently and takes the latest usage timestamp.
4. When content conflicts, keeps the local field and saves the remote record beside it as `*.conflicted`. If the local record is damaged and the remote record is valid, restores the valid record and archives the damaged one. Modify/delete conflicts keep the modified prompt and archive a copy. Concurrent scope changes keep the most recently updated copy, preserve the latest usage date, and archive the alternatives.
5. Commits the resolution and pushes without force, retrying up to three times if another machine pushes concurrently.

Recovery files are committed and synchronized. They are excluded from prompt lists. Inspect a `.conflicted` JSON record and use its content to edit or add a prompt, then remove the recovery file when no longer needed. `plib sync` commits that removal.

A failed fetch or push leaves local prompts available and local changes committed. The UI and ordinary commands report pending synchronization and continue; `plib sync` exits nonzero when synchronization fails. Permissions, disk errors and invalid repository states can still require manual repair. Network commands have bounded timeouts. Cross-process OS locks in a sibling `~/.plib.lock` file protect save/merge/push transactions, and saves use atomic file replacement. There is no background daemon: synchronization runs during tool usage.

## Clipboard

- macOS: `pbcopy`.
- Windows: native clipboard APIs.
- Linux and BSD: `wl-copy` on Wayland, `xclip` or `xsel` on X11.

If no supported clipboard is accessible, the interactive view shows the prompt with a manual-copy message. The `copy` command prints the full body. `show ID` is also available for terminal selection or redirection.

## Validation

```sh
make test
make vet
```

Tests use local temporary Git repositories and cover cross-machine bootstrap, independent histories, offline recovery, concurrent content edits, usage timestamps, modify/delete conflicts, interrupted merges, search, scope filtering and multiline input. CI runs tests, vet and builds on Linux, macOS and Windows for every push and pull request.

## Versions and releases

`make release` follows the sibling `ggw` and `ggg` workflow: propose the next semantic version, confirm, then push the branch and an annotated version tag atomically. It requires a clean checkout and an `origin` remote. The first proposed release is `v0.1.0`.

A tag triggers GitHub Actions and GoReleaser automatically. Releases include Linux, macOS and Windows binaries for amd64 and arm64, plus SHA-256 checksums. Each macOS binary is signed with Developer ID, verified, and submitted to Apple before publication. A rejected notarization stops the release. Bare command-line binaries do not support ticket stapling.

Configure these repository or organization secrets before the first release:

| Secret | Value |
| --- | --- |
| `APPLE_CERTIFICATE_P12` | Base64-encoded Developer ID Application certificate with its private key |
| `APPLE_CERTIFICATE_PASSWORD` | Password for that P12 file |
| `APPLE_SIGNING_IDENTITY` | Full Developer ID Application identity |
| `APPLE_ID` | Apple developer account email |
| `APPLE_TEAM_ID` | Apple developer team ID |
| `APPLE_APP_PASSWORD` | App-specific password for notarization |

`GITHUB_TOKEN` is supplied by GitHub Actions. The workflow fails if Apple credentials are missing, so an unsigned macOS release cannot be published accidentally. Signing material is imported into a temporary keychain and cleaned up even after failure.

This checkout initially has no remote. Publishing and actual Apple notarization require the GitHub repository and the secrets above. Local builds do not require Apple credentials.
