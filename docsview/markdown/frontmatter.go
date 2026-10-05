package markdown

import (
	"bytes"
	"regexp"
	"strings"

	yaml "github.com/oasdiff/yaml3"
)

var (
	utf8BOM = []byte("\xef\xbb\xbf")
	// propertyWikilink matches a property value that is exactly one wikilink.
	propertyWikilink = regexp.MustCompile(`^\[\[([^\[\]]+)\]\]$`)
	tagSeparators    = regexp.MustCompile(`[\s,]+`)
)

// splitFrontmatter separates YAML frontmatter, which starts with a "---"
// first line and ends at the next "---" or "..." line, from the body.
func splitFrontmatter(src []byte) (frontmatter, body []byte, ok bool) {
	src = bytes.TrimPrefix(src, utf8BOM)
	first, rest, found := bytes.Cut(src, []byte("\n"))
	if !found || !isFence(first, "---") {
		return nil, src, false
	}
	start := len(src) - len(rest)
	for pos := start; pos < len(src); {
		line, next, _ := bytes.Cut(src[pos:], []byte("\n"))
		if isFence(line, "---") || isFence(line, "...") {
			return src[start:pos], next, true
		}
		pos += len(line) + 1
	}
	return nil, src, false
}

func isFence(line []byte, fence string) bool {
	return string(bytes.TrimRight(line, " \t\r")) == fence
}

// readProperties fills the frontmatter fields of d and returns the links
// that property values make.
func (st *renderState) readProperties(frontmatter []byte, d *Document) []Link {
	var root yaml.Node
	if err := yaml.Unmarshal(frontmatter, &root); err != nil {
		d.PropertiesError = err.Error()
		return nil
	}
	if len(root.Content) == 0 {
		return nil
	}
	m := root.Content[0]
	if m.Kind != yaml.MappingNode {
		if m.Tag != "!!null" {
			d.PropertiesError = "frontmatter is not a mapping of properties"
		}
		return nil
	}
	var links []Link
	for i := 0; i+1 < len(m.Content); i += 2 {
		key, value := m.Content[i].Value, dealias(m.Content[i+1])
		p := Property{Key: key}
		switch value.Kind {
		case yaml.SequenceNode:
			p.List = true
			for _, item := range value.Content {
				p.Values = append(p.Values, st.propertyValue(dealias(item), key, &links))
			}
		case yaml.MappingNode:
			p.Values = []PropertyValue{{Text: compactYAML(value)}}
		default:
			if value.Tag != "!!null" {
				p.Values = []PropertyValue{st.propertyValue(value, key, &links)}
			}
		}
		switch strings.ToLower(key) {
		case "title":
			if value.Kind == yaml.ScalarNode {
				d.Title = value.Value
			}
		case "aliases", "alias":
			for _, v := range p.Values {
				if v.Text != "" {
					d.Aliases = append(d.Aliases, v.Text)
				}
			}
		case "tags", "tag":
			p.Values = tagValues(p.Values)
			for _, v := range p.Values {
				d.Tags = append(d.Tags, v.Text)
			}
		}
		d.Properties = append(d.Properties, p)
	}
	return links
}

// propertyValue converts one YAML value. A string that is exactly a
// wikilink becomes a link resolved like a body wikilink.
func (st *renderState) propertyValue(n *yaml.Node, key string, links *[]Link) PropertyValue {
	if n.Kind != yaml.ScalarNode {
		return PropertyValue{Text: compactYAML(n)}
	}
	if n.Tag == "!!null" {
		return PropertyValue{}
	}
	m := propertyWikilink.FindStringSubmatch(strings.TrimSpace(n.Value))
	if n.Tag != "!!str" || m == nil {
		return PropertyValue{Text: n.Value}
	}
	ref := parseWikiRef(m[1])
	file, fragment, ok := st.resolveRef(ref)
	*links = append(*links, Link{
		Path:     file,
		Target:   ref.target,
		Fragment: fragment,
		Wikilink: true,
		Context:  key + ": " + ref.text(),
	})
	return PropertyValue{
		Text:       ref.text(),
		Href:       st.hrefFor(ref, file, fragment, ok),
		Path:       file,
		Link:       true,
		Unresolved: !ok,
	}
}

// tagValues splits tag values on commas and spaces, drops "#", and links
// each tag to its search page.
func tagValues(values []PropertyValue) []PropertyValue {
	var tags []PropertyValue
	for _, v := range values {
		for _, t := range tagSeparators.Split(v.Text, -1) {
			if t = strings.TrimPrefix(t, "#"); t != "" {
				tags = append(tags, PropertyValue{Text: t, Href: tagURL(t)})
			}
		}
	}
	return tags
}

func dealias(n *yaml.Node) *yaml.Node {
	for n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	return n
}

// compactYAML renders a nested value on one line in YAML flow style.
func compactYAML(n *yaml.Node) string {
	setFlow(n)
	out, err := yaml.Marshal(n)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func setFlow(n *yaml.Node) {
	if n.Kind == yaml.MappingNode || n.Kind == yaml.SequenceNode {
		n.Style |= yaml.FlowStyle
	}
	for _, c := range n.Content {
		setFlow(c)
	}
}
