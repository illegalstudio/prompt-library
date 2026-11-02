# Prompt Library

Go CLI named `plib`, module `github.com/illegalstudio/prompt-library`.

- `cmd/plib`: entry point.
- `internal/cli`: Cobra commands and Charm Bubble Tea UI.
- `internal/store`: atomic per-prompt JSON storage and filtering.
- `internal/project`: canonical Git remote identities and project folder names.
- `internal/syncgit`: locked Git commit/fetch/merge/conflict recovery/push transactions.
- `internal/clipboard`: platform clipboard adapters.
- `internal/version`: version/commit values injected at build time.

Use standard Go formatting. Keep user-facing docs in README.md current. Never generate em dashes. Commits use lowercase Conventional Commits and no co-author trailers. Use gh for GitHub operations. Never change sibling repositories as part of this project.

Run builds and tests through the installed me-build-lock skill queue. Standard checks are `make test`, `make vet` and `make build`. Tests must use local temporary repositories and avoid touching real prompt libraries, remotes or clipboard content. Do not weaken the macOS signing/notarization gates to make a release succeed without Apple secrets.

Run Code Journal protocols for repository sessions. Use ggw if an isolated checkout is needed. Git synchronization changes must preserve both conflicting versions, close merges, commit resolutions and avoid force pushes. Local usability must survive network and authentication failures. All repository mutations must hold Repo.Locked. Do not hold a lock while waiting for user input.
