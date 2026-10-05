package markdown

import (
	"bytes"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
)

// schemePattern matches URLs that name a scheme, such as "https:" or "mailto:".
var schemePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)

// destination is a resolved standard link or image destination.
type destination struct {
	// external and anchor destinations stay as written.
	external bool
	anchor   bool
	// target is the path as written, without query or fragment.
	target   string
	file     string
	fragment string
	outside  bool
}

// resolveDest classifies dest and resolves relative paths against this
// document; a leading "/" means the repository root.
func (st *renderState) resolveDest(raw []byte) destination {
	dest := string(unescapeText(raw))
	switch {
	case schemePattern.MatchString(dest) || strings.HasPrefix(dest, "//"):
		return destination{external: true}
	case dest == "" || strings.HasPrefix(dest, "#"):
		return destination{anchor: true}
	}
	target, fragment, _ := strings.Cut(dest, "#")
	target, _, _ = strings.Cut(target, "?")
	d := destination{target: target, fragment: percentDecode(fragment)}
	p := percentDecode(target)
	if p == "" {
		d.file = st.from
		return d
	}
	if strings.HasPrefix(p, "/") {
		d.file = path.Clean(strings.TrimLeft(p, "/"))
	} else {
		d.file = path.Join(path.Dir(st.from), p)
	}
	d.outside = escapesRoot(d.file)
	return d
}

func percentDecode(s string) string {
	if v, err := url.PathUnescape(s); err == nil {
		return v
	}
	return s
}

// escapesRoot reports whether a cleaned relative path leaves the repository.
func escapesRoot(p string) bool {
	return p == ".." || strings.HasPrefix(p, "../")
}

// resolveLink rewrites an internal link to its viewer URL and marks
// external links. Same-page anchors stay as written.
func (st *renderState) resolveLink(n *ast.Link, source []byte) {
	d := st.resolveDest(n.Destination)
	switch {
	case d.external:
		setExternal(n)
		return
	case d.anchor:
		return
	}
	file := d.file
	exists := !d.outside && st.resolver.Exists(file)
	class := "internal-link"
	if !exists {
		class += " is-unresolved"
	}
	if d.outside {
		file = ""
		n.Destination = util.URLEscape(n.Destination, true)
	} else {
		n.Destination = []byte(URL(file, d.fragment))
	}
	n.SetAttributeString("class", []byte(class))
	n.SetAttributeString("data-path", []byte(file))
	st.links = append(st.links, Link{
		Path:     resolvedPath(file, exists),
		Target:   d.target,
		Fragment: d.fragment,
		Context:  linkContext(n, source),
	})
}

// resolveImage makes a relative image source absolute so it works in any
// page that shows it.
func (st *renderState) resolveImage(n *ast.Image, source []byte) {
	d := st.resolveDest(n.Destination)
	if d.external || d.anchor {
		return
	}
	file := ""
	if !d.outside {
		file = d.file
		n.Destination = []byte(URL(file, ""))
	}
	exists := file != "" && st.resolver.Exists(file)
	if !exists {
		n.SetAttributeString("class", []byte("is-unresolved"))
	}
	st.links = append(st.links, Link{
		Path:    resolvedPath(file, exists),
		Target:  d.target,
		Embed:   true,
		Context: linkContext(n, source),
	})
}

func resolvedPath(file string, exists bool) string {
	if exists {
		return file
	}
	return ""
}

// setExternal makes a link open in a new tab without referrer.
func setExternal(n ast.Node) {
	n.SetAttributeString("class", []byte("external-link"))
	n.SetAttributeString("target", []byte("_blank"))
	n.SetAttributeString("rel", []byte("noopener noreferrer"))
}

// renderLink writes standard links. Internal destinations are already
// escaped by URL, so only HTML escaping applies; goldmark's URL escaping
// would rewrite fragment characters such as "^".
func renderLink(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.Link)
	if !entering {
		_, _ = w.WriteString("</a>")
		return ast.WalkContinue, nil
	}
	dest := n.Destination
	if _, internal := n.AttributeString("data-path"); !internal {
		dest = util.URLEscape(dest, true)
	}
	openLink(w, n, dest, n.Title)
	return ast.WalkContinue, nil
}

func renderAutoLink(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.AutoLink)
	dest := n.URL(source)
	if n.AutoLinkType == ast.AutoLinkEmail && !bytes.HasPrefix(bytes.ToLower(dest), []byte("mailto:")) {
		dest = append([]byte("mailto:"), dest...)
	}
	openLink(w, n, util.URLEscape(dest, false), nil)
	_, _ = w.Write(util.EscapeHTML(n.Label(source)))
	_, _ = w.WriteString("</a>")
	return ast.WalkContinue, nil
}

// openLink writes an <a> start tag with class first, like wikilinks. Only
// resolveLink and setExternal set link attributes, all as []byte.
func openLink(w util.BufWriter, n ast.Node, href, title []byte) {
	_, _ = w.WriteString("<a")
	if class, ok := n.AttributeString("class"); ok {
		writeAttribute(w, "class", class.([]byte))
	}
	writeAttribute(w, "href", href)
	if title != nil {
		_, _ = w.WriteString(` title="`)
		html.DefaultWriter.Write(w, title)
		_ = w.WriteByte('"')
	}
	for _, a := range n.Attributes() {
		if string(a.Name) != "class" {
			writeAttribute(w, string(a.Name), a.Value.([]byte))
		}
	}
	_ = w.WriteByte('>')
}

// writeAttribute writes ` name="value"` with value HTML-escaped.
func writeAttribute(w util.BufWriter, name string, value []byte) {
	_, _ = w.WriteString(" " + name + `="`)
	_, _ = w.Write(util.EscapeHTML(value))
	_ = w.WriteByte('"')
}
