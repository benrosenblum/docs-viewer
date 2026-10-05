// Package markdown renders repository Markdown with the Obsidian syntax that
// the documentation viewer supports. Paths are clean, slash-separated, and
// relative to the repository root, for example "docs/ref/plan.md".
package markdown

// Resolver answers questions about other repository files during one render.
type Resolver interface {
	// ResolveWikilink finds the file that an Obsidian link target names, as
	// seen from the document at from. The target has no fragment or alias.
	ResolveWikilink(from, target string) (string, bool)
	// Exists reports whether path is a file or directory that the viewer serves.
	Exists(path string) bool
	// HeadingID returns the anchor of a heading in the Markdown file at path.
	HeadingID(path, heading string) (string, bool)
	// Source returns the Markdown source of an embedded note.
	Source(path string) ([]byte, error)
}

// Document is one rendered Markdown file.
type Document struct {
	// HTML is the rendered body. It excludes frontmatter properties.
	HTML string
	// Properties lists frontmatter keys in source order.
	Properties []Property
	// PropertiesError describes invalid frontmatter. The body still renders.
	PropertiesError string
	// Title is the frontmatter title, else the first level-1 heading, else "".
	Title   string
	Aliases []string
	// Tags lists frontmatter tags, then inline tags, without "#" or duplicates.
	Tags     []string
	Headings []Heading
	// Links lists internal links, wikilinks, and embeds in document order.
	Links   []Link
	Mermaid bool
	Math    bool
}

// Heading is one body heading and its anchor.
type Heading struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
	ID    string `json:"id"`
}

// Link is one internal reference from a document.
type Link struct {
	// Path is the resolved repository path, or "" when unresolved.
	Path string
	// Target is the link target as written, without fragment or alias.
	Target   string
	Fragment string
	Embed    bool
	Wikilink bool
	// Context is plain text around the link for backlink panes.
	Context string
}

// Property is one frontmatter key.
type Property struct {
	Key    string
	List   bool
	Values []PropertyValue
}

// PropertyValue is one displayed property value. Link values have an Href.
type PropertyValue struct {
	Text       string
	Href       string
	Path       string
	Link       bool
	Unresolved bool
}
