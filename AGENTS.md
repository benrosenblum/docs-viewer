# Docs viewer (Go)

## Overview

A read-only, Obsidian-style viewer for the Markdown documentation of a
repository. It is a local development tool: a developer runs it in a checkout
and reads the documents in a browser. It is never deployed.

- the **command** (`cmd/docs-viewer`): the flags, the foreground and
  background server, and the install of the pinned browser assets;
- the **handler** (`docsview`): the HTTP surface, the vault index, search,
  the link graph, live reload, and the git diff views;
- the **renderer** (`docsview/markdown`): Markdown with the Obsidian syntax
  to HTML, and the rendered diff of two versions of a note;
- the **diff** (`docsview/linediff`): the line and word diff.

Go 1.26, goldmark, chroma, yaml3. The browser side is hand-written
JavaScript and CSS with no build step. Mermaid, KaTeX, and Redoc are pinned
assets that the command downloads on the first run. A project uses the tool
as a binary that its Makefile installs at a pinned version
(`docs/USAGE.md`).

This repo is generic. It names no project that uses it: not in code,
comments, documents, tests, or commit messages.

## Code quality

- Write idiomatic Go. Follow [Effective Go](https://go.dev/doc/effective_go) and use `gofmt`.
- Keep the browser files plain: no framework, no bundler, no third-party code in `docsview/static`.
- Extract shared functions when behavior already exists. Do not copy the same logic into a second location.

## Documentation

- Document every change in human-readable Markdown under `docs/`. Update an existing document when it covers the change.
- Use [ASD-STE100 Simplified Technical English](https://www.asd-ste100.org/): short sentences, active voice, consistent technical terms, and clear instructions. Follow its writing rules and approved vocabulary.
- Explain what changed, why it changed, how to use it, and how it was checked, as applicable.
- Maintain `docs/README.md` as the documentation index. Add links to new documents and fix links when documents move.
- Add Mermaid diagrams when they help explain structure, behavior, or data flow. Use `flowchart TD` and keep a diagram narrow, so that the reading column shows it without a horizontal scroll.
- The documents are also the test content of the viewer. `make docs` shows them, and `TestRealDocs` renders each one. Keep `docs/MARKDOWN.md` as the page that uses each supported syntax.

## Commit messages

- Commit every logical unit of work.
- Start each commit subject with a lowercase tag in parentheses, such as `(fix)`, `(feat)`, `(docs)`, `(refactor)`, `(test)`, or `(chore)`.
- Follow the tag with a space and a short, clear summary. Example: `(fix) Keep the scroll position after a reload`.
- Do not add `Co-authored-by` trailers or other co-author attribution.

## Key references

Read before touching the matching area:

- `docs/README.md` — index of every document.
- `docs/ARCHITECTURE.md` — packages, the URL space, the index, live reload,
  security limits, pinned assets, the background mode.
- `docs/USAGE.md` — install, flags, subcommands, the files under `.local/`.
- `docs/THEMES.md` — the theme properties and how to write a theme. Read
  before any change to `viewer.css` or `theme.css`.
- `docs/MARKDOWN.md` — the supported Markdown and Obsidian syntax.
- `docs/NAVIGATION.md` — pages, the file tree, search, the graph, shortcuts.
- `docs/GIT-DIFF.md` — tree marks, diff mode, the rendered and the source
  diff, the git commands. Read before any change to `git.go` or the diff code.
- `docs/API-SPECS.md` — the spec vault and the API reference pages.
- `api/openapi.yaml` — the JSON routes of the viewer.

## Repository map

```
cmd/docs-viewer/   main.go (flags, run|start|stop|status|setup, the server),
                   assets.go + docs-assets.json (pinned browser assets),
                   background.go (lock, state file, detached start)
docsview/          docsview.go (Config, New, routes, static, theme, assets),
                   pages.go, vault.go (path admission, index, wikilinks),
                   search.go, graph.go, events.go (live reload),
                   git.go, diffpage.go, diffview.go (git diff views)
  markdown/        goldmark engine and the Obsidian extensions; RenderDiff
  linediff/        Myers line diff, hunks, pairs, word ranges
  static/          viewer.css (structure), theme.css (the built-in theme),
                   viewer.js, graph.js, theme.js, icons.svg
  templates/       layout.html (the one page template)
docs/              the documentation; also the viewer's own test vault
api/               openapi.yaml, the spec vault of this repo
```

## Layering rules (enforced by review)

- **`cmd/docs-viewer` → `docsview` → `docsview/markdown` → `docsview/linediff`.**
  No package imports one to its left. `linediff` uses the standard library
  only.
- **`markdown` knows no HTTP and no file system.** It reads other notes only
  through the `Resolver` interface. Source in, HTML and link data out.
- **`docsview` only reads.** It reads the repository through `os.Root`, and
  git through `gitRepo.run` with read-only commands. It never writes to the
  work tree, the index, or `.git`.
- **Only `cmd/docs-viewer` uses the network and writes files.** The asset
  download and everything below `.local/` are there. `docsview` takes a
  verified asset directory.
- **The page loads nothing from another host.** Browser libraries are pinned
  assets from `/_/assets/`. The content security policy allows this origin
  only.
- **Logging is `log/slog`** in the command only. Libraries return errors.

## Conventions

- **URL space**: each path below `/_/` belongs to the viewer (`/_/static/`,
  `/_/theme/`, `/_/assets/<version>/`, `/_/api/`, `/_/events`, `/_/search`,
  `/_/graph`). Each other path is a repository path. URLs are root-absolute.
- **Requests**: `GET` and `HEAD` only. Each response carries the content
  security policy, `nosniff`, and `no-store` (pinned assets are immutable).
- **Path admission**: `allowed` in `vault.go` rejects hidden entries,
  `node_modules`, and a first segment `_`. Use it for each new path input.
- **Git input**: a URL value never reaches git as an option. Check the base
  grammar first (`validBase`) and pass `--end-of-options`.
- **Limits** are constants next to their use (note size, source size,
  rendered diff size, history length, search results). Do not remove a
  limit; a too-large input gets a message in the page.
- **JSON routes** are in `api/openapi.yaml`. A change to a route or a
  response updates the spec in the same commit.
- **Browser storage** keys start with `docsview:`.

## Theme rules

- `viewer.css` holds structure and no theme value. Colors, fonts, and radii
  are custom properties that the theme sets.
- `theme.css` is the built-in theme. It sets each required property for
  `html[data-theme="light"]` and `html[data-theme="dark"]`.
- The page loads one theme stylesheet, then `viewer.css`. A custom theme
  (`-theme`) replaces the built-in theme.
- A new property is either required (set it in `theme.css`) or optional
  (give it a fallback in `viewer.css`: `var(--name, fallback)`). Add it to
  `docs/THEMES.md`. `TestThemeProperties` fails on a read with no source.
- The `--c-*` hues are complete colors. `viewer.css` mixes them with
  `color-mix`.

## Workflow

- `make build` compiles the command into `.local/bin/docs-viewer`.
- `make test`, `make vet`, `make fmt`. `make check` runs the three checks
  for a commit (`gofmt -l` must print nothing).
- The tests are hermetic: temporary directories, `httptest`, no network. The
  git tests need `git` on `PATH` and skip without it.
- `make docs` serves this repo's `docs/` and `api/` on `DOCS_ADDR`
  (`127.0.0.1:7900`). `make docs-start`, `docs-stop`, `docs-status`,
  `docs-logs` control a background server. `make docs-setup` installs the
  pinned assets (the first run needs the network).
- After a change to `static/` or `templates/`, build again and reload the
  page: the files are embedded in the binary. Look at the result in a
  browser in light and dark; a passed test does not show a visual fault.
- To change a pinned asset, edit `cmd/docs-viewer/docs-assets.json`: the
  version, the URL, and each sha256. Then run `make docs-setup`.
- A release is a `v*` tag. Projects pin a tag.

## Common pitfalls

- **`--no-optional-locks` is necessary.** Without it `git status` writes the
  index, the live reload sees a change, and the page reloads in a loop.
- **`start` executes the binary again** as a detached process. Build a
  binary; `go run` gives a temporary path that is gone after the command.
- **Vaults cannot overlap**, and the spec vault must be one of the vaults.
- **The theme directory is a confinement root.** A theme cannot import a
  file above the directory of its stylesheet.
- **The theme loads before `viewer.css`.** A theme rule that must win over a
  `viewer.css` rule needs a higher specificity.
- **`highlight.css` is not a file.** `New` generates it from chroma.
- **Asset paths are in three places**: `docs-assets.json`, `layout.html`,
  and `viewer.js`. Change them together.
- **A changed manifest gives a new bundle directory** (`bundle-<hash>`). The
  old directory stays until someone removes it.
- **The command is Unix only**: it uses `flock`, `fcntl` locks, and process
  sessions.
