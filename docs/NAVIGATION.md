# Navigation

This document describes the pages of the viewer and the tools that find a
document: the file tree, the outline, the backlinks, the quick switcher,
search, and the graph. It also describes the lightbox, which shows a
diagram or an image across the full window.

## Terms

| Term | Meaning |
|------|---------|
| Vault | A documentation directory that `-vaults` names, such as `docs`. |
| Note | A Markdown file (`.md` or `.markdown`). |
| Repository path | The path of a file from the repository root, such as `docs/USAGE.md`. |

## Pages and URLs

A page URL is a repository path. For example, `/docs/USAGE.md` shows the
file `docs/USAGE.md`. Thus, a relative Markdown link between two files
works in the viewer and on a git host without a change.

| URL | Page |
|-----|------|
| `/` | The home note: `README.md` in the first vault. |
| `/docs/USAGE.md` | A note, rendered. |
| `/docs/` | A folder: the list of its files. |
| `/docs/USAGE.md?raw=1` | The file as plain text, without a change. |
| `/Makefile` | A file outside the vaults. See [Files outside the vaults](#files-outside-the-vaults). |
| `/_/search?q=theme` | The search results as a page. |
| `/_/graph` | The graph. |

Each path that starts with `/_/` belongs to the viewer.
[ARCHITECTURE.md](ARCHITECTURE.md#url-space) has the full list.

The viewer shows a file by its type:

| File | Page |
|------|------|
| A note | The rendered note, with its properties, outline, and backlinks. |
| An image or a PDF file | The file itself. |
| A YAML file in the spec vault | An API reference. See [API-SPECS.md](API-SPECS.md). |
| Other text | A source page: syntax colors, line numbers, the "Raw" link, and the "Copy" button. |
| A binary file | A source page with the size of the file and no content. |

On a source page, the fragment `#L10` goes to line 10. Select a line
number to get that link.

A link to another page loads without a full page load. The back and
forward buttons of the browser keep the scroll position.

### Files outside the vaults

A note can link to a file that is not in a vault, such as `/Makefile` or
`/cmd/app/main.go`. The viewer shows that file also:

- A Markdown file opens as a note. The header shows the label "Outside
  vault".
- Other text opens as a source page.
- The file tree, search, and the quick switcher do not list these files.

The viewer never shows these paths:

- A file or a folder with a name that starts with `.`, such as `.git` or
  `.env`.
- A `node_modules` folder.
- A path outside the repository.

A file that is too large shows a message and no content. The limits are
8 MiB for a note and 2 MiB for a source page.

## Screen layout

| Part | Contents |
|------|----------|
| Left sidebar | The name of the repository folder, which opens the home note. The buttons for the quick switcher, search, the graph, "Collapse all folders", and the theme. The file tree. |
| Header | The button for the left sidebar, the path of the page, the page controls, the live reload status, and the button for the right sidebar. |
| Content | The page. |
| Right sidebar | The outline and the linked mentions. |

Pull an edge of a sidebar to change its width. Double-click the edge to
get the initial width again. The header buttons hide and show the
sidebars. In a window narrower than 900 pixels, a sidebar opens above the
content.

The browser keeps the widths, the hidden sidebars, the open folders, and
the theme for this address.

## File tree

The top folders of the tree are the vaults, in the sequence of `-vaults`.
In each folder, the folders come first, then the files by name.

- A note shows its name without the extension.
- Another file shows its extension as a small label.
- The folder of the open file is open. Other folders keep their state.
- "Collapse all folders" closes each folder.
- A changed file has a letter after its name: `M`, `A`, or `U`. See
  [GIT-DIFF.md](GIT-DIFF.md#marks-in-the-file-tree).

## Outline and linked mentions

The outline lists the headings of the note. Select a heading to go to it.
While you scroll, the outline marks the heading at the top of the page.

Each heading in the page also shows a `#` link when the pointer is on it.
That link is the URL of the heading.

"Linked mentions" lists each note that links to the open file or embeds
it. These are the backlinks. Below each note, the list shows a maximum of
five text lines that contain the link. Select a line to open the note at
that text.

## Quick switcher

Press Ctrl+O to open the quick switcher. It finds a note by its name,
title, alias, or path.

| Query | Result |
|-------|--------|
| No text | The notes that you opened last, then the other notes. |
| `arch` | The notes that match these letters. The letters do not have to be adjacent. |
| `usage#make` | The headings that match `make` in the notes that match `usage`. |
| `#keys` | The headings that match `keys` in the open note. |

| Key | Function |
|-----|----------|
| Up arrow, Down arrow | Move in the list. Ctrl+P and Ctrl+N do the same. |
| Enter | Open the entry. |
| Ctrl+Enter | Open the entry in a new tab. |
| Escape | Close the list. |

The list shows a maximum of 50 entries.

## Search

Press Ctrl+Shift+F to open search. The results change while you type.
"Open results as a page" opens the same results at `/_/search`.

Search reads the notes of the vaults. It does not read other files.

| Query | Finds |
|-------|-------|
| `theme font` | Notes that contain the two terms. |
| `"live reload"` | Notes that contain the phrase. |
| `tag:#reference` | Notes with the tag `reference` or a tag below it, such as `reference/api`. The `#` is optional. |
| `path:docs/api/` | Notes with this text in their path. |
| `file:usage` | Notes with this text in their name. |

- You can use terms, phrases, and filters together. A note must agree with
  all of them.
- Search ignores letter case.
- A term can be in the name, the title, an alias, or the text of a note.
- A match in the name or in the title puts a note higher in the list.
- The list shows the first 50 notes, and a maximum of five text lines for
  each note.
- Select a text line to open the note at that term.

A tag in a note is a link. It opens the search for that tag.

## Graph

Press Ctrl+G, or select the graph button, to open the graph. Each note of
the vaults is a node. Each link or embed between two notes is a line.

| Control | Function |
|---------|----------|
| "Outside vault" | Shows or hides the Markdown files outside the vaults that a note links to. |
| "Unresolved links" | Shows or hides the wikilinks that find no file. |
| "Fit", or the key `0` | Shows the full graph. |

| Action | Result |
|--------|--------|
| Click a node | Opens the note. |
| Pull a node | Moves the node. |
| Pull the background | Moves the view. |
| Turn the mouse wheel, or press `+` and `-` | Changes the zoom. |
| Double-click the background | Increases the zoom. |
| Hold the pointer on a node | Shows the path of the note. |

When two notes have the same name, their labels also show the folder, for
example `guides/setup` and `reference/setup`. Files that are not notes,
such as images, are not nodes.

## Hover previews

Hold the pointer on an internal link. After a short time, a preview of the
target page shows near the link. A link to a heading shows the preview at
that heading.

- A link that finds no file has no preview.
- A preview always shows the normal page, also for a file with changes.
- Previews show only on a device with a mouse or a touchpad.
- Press Escape to close a preview.

## Lightbox

Click a diagram, an image, or a block of math in a page. The lightbox
opens and shows the item across the full window. Thus you can read a
diagram that is too wide for the reading column.

When the lightbox opens, the full item is in view. An item that is smaller
than the window shows at its usual size.

| Control | Function |
|---------|----------|
| The "Zoom out" button, or the key `-` | Decreases the zoom. |
| The "Zoom in" button, or the key `+` | Increases the zoom. |
| The "Fit to the window" button, or the key `0` | Shows the full item. |
| The "Close" button, or Escape | Closes the lightbox. |

| Action | Result |
|--------|--------|
| Turn the mouse wheel | Changes the zoom at the pointer. |
| Pull the item or the background | Moves the view. |

- An image that is a link opens the link, not the lightbox.
- Inline math, tables, and code blocks do not open the lightbox.
- The lightbox also opens from a hover preview and from the rendered
  changes of a file.
- The lightbox shows the item as it was at the time of the click. A live
  reload does not change an open lightbox.
- A change of the theme closes the lightbox.

## Keyboard shortcuts

| Key | Function |
|-----|----------|
| Ctrl+O | Open the quick switcher. |
| Ctrl+Shift+F | Open search. |
| Ctrl+G | Open the graph. |
| N | On a diff page, go to the next change. See [GIT-DIFF.md](GIT-DIFF.md#move-between-the-changes). |
| P | On a diff page, go to the previous change. |
| Escape | Close the open list, preview, or lightbox. In a narrow window, close the open sidebar. |

On macOS, you can use Cmd as an alternative to Ctrl. The keys N and P
have no effect while you type in a field.

## Light and dark

The theme button in the left sidebar changes between the light and the
dark theme.

- On the first visit, the viewer uses the setting of the operating system.
- After you select a theme, the browser keeps it for this address.
- Diagrams and API reference pages render again in the new theme.

[THEMES.md](THEMES.md) tells you how to change the colors and the fonts.

## Live reload

The page updates when a file changes. You do not reload the page.

- Save a file: the open page shows the new content and keeps its scroll
  position.
- Add, remove, or rename a file: the file tree and the linked mentions
  update.
- Change a file outside the vaults that a note links to or embeds: the
  page updates also.
- Commit, stage, or change the branch: the marks in the file tree update.

The dot in the header shows the connection. A short message, "Page
updated" or "Files updated", shows after each update. If the server stops,
the dot shows that the connection is lost. The browser then connects again
when the server runs.

[ARCHITECTURE.md](ARCHITECTURE.md#live-reload) describes how live reload
works.
