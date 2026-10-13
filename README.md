# arbr

A TUI for managing git worktrees across projects

Allows you to create, delete and navigate to / between git worktrees from a central UI. Automatically parses git projects in a configured project directory & allows hiding / unhiding irrelevant projects.

## Install

### Via install script (recommended)

Downloads the latest release binary to `~/.local/bin`, clears Gatekeeper quarantine and adds `~/.local/bin` to your PATH if it's not already:

```sh
curl -fsSL https://raw.githubusercontent.com/fipppi/arbr/main/install.sh | bash
```

### Via pre-built binary

Grab the [latest release](https://github.com/fipppi/arbr/releases/latest) for Apple Silicon:

```sh
mkdir -p ~/.local/bin # create ~/.local/bin if it doesn't exist
curl -fsSL -o ~/.local/bin/arbr https://github.com/fipppi/arbr/releases/latest/download/arbr # download the release to ~/.local/bin
chmod +x ~/.local/bin/arbr # make it executable

# add ~/.local/bin to your PATH if it's not already there (zsh - ymmv)
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc && source ~/.zshrc

# macOS Gatekeeper may block the binary from running as it's unsigned - remove it from quarantine if you receive an error while running it:
xattr -d com.apple.quarantine ~/.local/bin/arbr
```

### Via `go install`

`go install github.com/fipppi/arbr@latest`

### Build from source

`git clone https://github.com/fipppi/arbr && cd arbr && go build .`

### Shell wrapper

```sh
eval "$(command arbr init)" # add to ~/.zshrc to allow arbr to change your working directory
```

Once installed, `U` inside the TUI downloads the latest GitHub release and replaces the
running binary in place.

## Keybinds

| view      | key            | action                               |
| --------- | -------------- | ------------------------------------ |
| projects  | `↑↓` / jk      | move                                 |
|           | `enter`        | open a project's worktrees           |
|           | `/`            | fuzzy-filter projects                |
|           | `x`            | hide / unhide project                |
|           | `u`            | undo the last hide                   |
|           | `.`            | toggle showing hidden projects       |
|           | `r`            | rescan                               |
|           | `esc`          | clear active filter                  |
| worktrees | `enter`        | cd to worktree and exit arbr         |
|           | `n`            | create a new worktree                |
|           | `d`            | remove worktree                      |
|           | `p`            | prune stale worktree entries         |
|           | `P`            | remove worktrees with no activity    |
|           |                | in the last month                    |
|           | `*`            | pin / unpin worktree (protected from |
|           |                | delete, prune, and sweep)            |
|           | `s`            | toggle sorting by modified / created |
|           | `esc`          | back to project grid                 |
| anywhere  | `U`            | self-update to the latest GitHub     |
|           |                | release (restart to apply)           |
|           | `q` / `ctrl+c` | quit                                 |

Config is stored in `~/.config/arbr/config.json` (or `$XDG_CONFIG_HOME/arbr/config.json`).

New worktrees branch off the repo's default branch (`origin/HEAD`, and fall back to local `main`/`master`).

To use a different base for a project, add it to the config:

```json
{
  "default_branch": {
    "~/git/backend": "develop"
  }
}
```

New worktrees can also automatically sync gitignored local files (eg .env files) on creation. These need to be explicitly
defined in the config file for each project.

```json
{
  "sync_files": {
    "~/git/backend": [".npmrc", "apps/web/.env.local", "packages/i18n/.env"]
  }
}
```

Pinned worktrees (`*`, stored under `pinned` in the config) are never
touched by `delete` or `prune` commands. If a pinned worktree's directory goes missing,
prune temporarily locks it so git's prune can't take it either.
