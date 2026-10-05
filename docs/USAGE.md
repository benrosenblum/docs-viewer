# Usage

This document tells you how to install the viewer, how to run it, and which
files it writes.

## Requirements

- Linux or macOS. The command uses file locks and process sessions, so it
  does not run on Windows.
- Go 1.26 or later, to install the command.
- A network connection for the first run. See [First run](#first-run).
- `git` on `PATH`, for the diff features. Without git, the viewer shows the
  documents and no diff. See [GIT-DIFF.md](GIT-DIFF.md).

## Install the command

```sh
go install github.com/benrosenblum/docs-viewer/cmd/docs-viewer@<version>
```

Replace `<version>` with a release tag. `@latest` installs the newest
release. For a project, pin one version in the Makefile. See
[Makefile example](#makefile-example).

## Run the viewer

1. Go to the root of the repository.
2. Run the command:

   ```sh
   docs-viewer
   ```

3. Open the URL that the command prints:

   ```text
   docs viewer: http://127.0.0.1:7000/
   ```

4. Press Ctrl+C to stop the viewer.

The working directory is the repository root. The viewer serves only files
below it. The directory needs no `go.mod`: the viewer works in a repository
of any language.

Each directory of `-vaults` must exist. If one is absent, the command stops
with status 1 and this message:

```text
2026/10/05 09:30:00 ERROR docs viewer failed command=run err="no documentation directory \"docs\"; run from the repository root"
```

## Flags

```text
docs-viewer [flags] [run|start|stop|status|setup]
```

| Flag | Default | Function |
|------|---------|----------|
| `-addr` | `127.0.0.1:7000` | The listen address. Port `0` selects a free port. |
| `-vaults` | `docs` | The documentation directories, with commas between them. The first directory holds the home note. |
| `-spec-vault` | none | The directory of `-vaults` that holds OpenAPI specs. See [API-SPECS.md](API-SPECS.md). |
| `-theme` | none | A theme stylesheet. Without it, the viewer uses the built-in theme. See [THEMES.md](THEMES.md). |

Put the flags before the subcommand. The command accepts one subcommand at
most.

These rules apply to `-vaults`:

- Each directory is a path relative to the repository root.
- A directory cannot be hidden (a name that starts with `.`).
- One directory cannot contain another.
- The home page is `README.md` in the first directory. If that file is
  absent, the home page is the file list of the first directory.

The viewer has no sign-in. Keep the loopback address of `-addr`, unless
each computer that can connect is permitted to read the repository.

Examples:

```sh
# docs/ only, on the default address.
docs-viewer

# Two vaults. The api/ directory holds OpenAPI specs.
docs-viewer -vaults docs,api -spec-vault api

# A free port and a project theme.
docs-viewer -addr 127.0.0.1:0 -theme ui/docs-theme.css
```

## Subcommands

| Subcommand | Function |
|------------|----------|
| `run` | Serves in the foreground. This is the default. |
| `start` | Serves in the background and writes the log to `.local/logs/docs.log`. |
| `stop` | Stops the server of this checkout. |
| `status` | Shows if a server runs in this checkout, and its URL. |
| `setup` | Installs the pinned browser assets and does nothing else. |

Each subcommand first makes sure that the directories of `-vaults` exist.
Thus, give the same `-vaults` value to each subcommand of a project.

## Run in the background

1. Start the server:

   ```sh
   docs-viewer -vaults docs,api -spec-vault api start
   ```

   The command installs the assets if necessary, starts the server, and
   waits until the server listens. Then it prints the URL:

   ```text
   docs viewer: http://127.0.0.1:7000/ (pid 41234, log .local/logs/docs.log)
   ```

2. To see the state, run `docs-viewer status`.
3. To read the log, run `tail -F .local/logs/docs.log`.
4. To stop the server, run `docs-viewer stop`.

`start` gives its flags to the background server. The background server
stays after you close the terminal.

One checkout has one server at a time:

- If a server runs, `start` prints its URL and starts no second server.
- If a server runs, `run` stops with the message `already running at <URL>`.
- `stop` and `status` print `docs viewer: not running` when no server runs.

`stop` sends `SIGTERM` to the server and waits for a maximum of 10 seconds.
If the server continues to run, `stop` sends `SIGKILL`.

If the server stops during the start, `start` prints the new lines of the
log. If the server does not listen after 30 seconds, `start` stops it and
gives an error.

Two checkouts of one repository can each run a server. Give each one a
different port, or use port `0`.

## Files under `.local/`

The command writes only below `.local/` in the repository root.

| Path | Contents |
|------|----------|
| `.local/tools/docs/bundle-<hash>/` | The pinned browser assets: `mermaid/`, `katex/`, and `redoc/`. |
| `.local/tools/docs/install.lock` | The lock of the asset install. |
| `.local/run/docs/lock` | The lock that a server holds while it runs. |
| `.local/run/docs/state.json` | The process ID and the URL of the server. |
| `.local/logs/docs.log` | The output of a background server. `start` adds to this file. |

Add `.local/` to the `.gitignore` file of the project:

```text
/.local/
```

The viewer does not show `.local/`, because the name starts with `.`.

You can delete `.local/` when no server runs. The subsequent run downloads
the assets again.

## First run

The first `run`, `start`, or `setup` downloads three archives from the npm
registry (`registry.npmjs.org`):

| Asset | Version | Function |
|-------|---------|----------|
| Mermaid | 11.17.2 | Renders diagrams. |
| KaTeX | 0.18.7 | Renders math. |
| Redoc | 2.5.4 | Renders API reference pages. |

The command does these steps:

1. It compares the SHA-256 value of each archive with the value in its
   manifest.
2. It takes only the necessary files from each archive, and the license
   files.
3. It compares the SHA-256 value of each file with the value in its
   manifest.
4. It moves the complete set to `.local/tools/docs/bundle-<hash>/`.

A wrong value stops the install. Then the directory gets no file.

Each subsequent run does the file check again and uses no network. The
browser loads these files only from the viewer, and never from another
host.

To install the assets before you go offline, run `docs-viewer setup`.

[ARCHITECTURE.md](ARCHITECTURE.md#pinned-assets) gives the format of the
manifest and the procedure to change a version.

## Makefile example

This example is for a project with a `docs/` and an `api/` directory. It
pins one version of the viewer and installs the binary into
`.local/tools/docs-viewer-<version>/`. A change of the version installs the
new binary on the subsequent `make docs`.

```make
DOCS_VIEWER_VERSION := v0.1.0
DOCS_VIEWER_DIR := .local/tools/docs-viewer-$(DOCS_VIEWER_VERSION)
DOCS_VIEWER := $(DOCS_VIEWER_DIR)/docs-viewer
DOCS_FLAGS := -addr 127.0.0.1:7000 -vaults docs,api -spec-vault api

$(DOCS_VIEWER):
	GOBIN=$(abspath $(DOCS_VIEWER_DIR)) go install github.com/benrosenblum/docs-viewer/cmd/docs-viewer@$(DOCS_VIEWER_VERSION)

.PHONY: docs docs-start docs-stop docs-status docs-logs

## docs: Show the documentation in a browser, with live reload
docs: $(DOCS_VIEWER)
	@$(DOCS_VIEWER) $(DOCS_FLAGS)

## docs-start, docs-stop, docs-status: Control a background viewer
docs-start docs-stop docs-status: $(DOCS_VIEWER)
	@$(DOCS_VIEWER) $(DOCS_FLAGS) $(@:docs-%=%)

## docs-logs: Follow the log of the background viewer
docs-logs:
	@tail -n 50 -F .local/logs/docs.log
```

| Target | Function |
|--------|----------|
| `make docs` | Runs the viewer in the foreground. |
| `make docs-start` | Starts the viewer in the background. |
| `make docs-stop` | Stops the background viewer. |
| `make docs-status` | Shows the state and the URL. |
| `make docs-logs` | Follows the log. |

Replace `v0.1.0` with the release that the project uses.
