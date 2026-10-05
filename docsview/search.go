package docsview

import (
	"bufio"
	"bytes"
	"cmp"
	"net/http"
	"slices"
	"strings"

	"github.com/benrosenblum/docs-viewer/docsview/markdown"
)

const (
	maxResults = 50
	maxMatches = 5
)

// query is a parsed search. Terms and phrases must all appear; filters
// restrict by tag, path, or file name. Matching ignores case.
type query struct {
	terms, tags, paths, files []string
}

func parseQuery(text string) query {
	var q query
	for _, token := range tokenize(text) {
		lower := strings.ToLower(token)
		switch {
		case strings.HasPrefix(lower, "tag:"):
			if tag := strings.TrimPrefix(strings.TrimPrefix(lower, "tag:"), "#"); tag != "" {
				q.tags = append(q.tags, tag)
			}
		case strings.HasPrefix(lower, "path:"):
			if value := strings.TrimPrefix(lower, "path:"); value != "" {
				q.paths = append(q.paths, value)
			}
		case strings.HasPrefix(lower, "file:"):
			if value := strings.TrimPrefix(lower, "file:"); value != "" {
				q.files = append(q.files, value)
			}
		default:
			q.terms = append(q.terms, lower)
		}
	}
	return q
}

// tokenize splits on spaces outside double quotes. A quoted phrase is one token.
func tokenize(text string) []string {
	var tokens []string
	var current strings.Builder
	quoted := false
	flush := func() {
		if token := strings.TrimSpace(current.String()); token != "" {
			tokens = append(tokens, token)
		}
		current.Reset()
	}
	for _, r := range text {
		switch {
		case r == '"':
			quoted = !quoted
			if !quoted {
				flush()
			}
		case !quoted && (r == ' ' || r == '\t' || r == '\n'):
			flush()
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return tokens
}

func (q query) empty() bool {
	return len(q.terms)+len(q.tags)+len(q.paths)+len(q.files) == 0
}

type searchResult struct {
	Query     string      `json:"query"`
	Terms     []string    `json:"terms"`
	Results   []searchHit `json:"results"`
	Total     int         `json:"total"`
	Truncated bool        `json:"truncated"`
}

type searchHit struct {
	Path    string        `json:"path"`
	Name    string        `json:"name"`
	Title   string        `json:"title"`
	URL     string        `json:"url"`
	Score   int           `json:"score"`
	Matches []searchMatch `json:"matches"`
}

type searchMatch struct {
	Line int    `json:"line"`
	Text string `json:"text"`
	URL  string `json:"url"`
}

func (idx *index) search(text string) searchResult {
	text = strings.TrimSpace(text)
	q := parseQuery(text)
	result := searchResult{Query: text, Terms: nonNil(q.terms), Results: []searchHit{}}
	if q.empty() {
		return result
	}
	for _, n := range idx.notes {
		if hit, ok := n.match(q); ok {
			result.Results = append(result.Results, hit)
		}
	}
	slices.SortFunc(result.Results, func(a, b searchHit) int {
		return cmp.Or(cmp.Compare(b.Score, a.Score), cmp.Compare(a.Path, b.Path))
	})
	result.Total = len(result.Results)
	if result.Total > maxResults {
		result.Results, result.Truncated = result.Results[:maxResults], true
	}
	return result
}

func (n *note) match(q query) (searchHit, bool) {
	lowerPath, lowerName := strings.ToLower(n.path), strings.ToLower(n.name)
	for _, p := range q.paths {
		if !strings.Contains(lowerPath, p) {
			return searchHit{}, false
		}
	}
	for _, f := range q.files {
		if !strings.Contains(lowerName, f) {
			return searchHit{}, false
		}
	}
	for _, tag := range q.tags {
		if !slices.ContainsFunc(n.doc.Tags, func(t string) bool {
			t = strings.ToLower(t)
			return t == tag || strings.HasPrefix(t, tag+"/")
		}) {
			return searchHit{}, false
		}
	}
	titles := strings.ToLower(strings.Join(append([]string{n.doc.Title}, n.doc.Aliases...), "\n"))
	body := bytes.ToLower(n.src)
	hit := searchHit{Path: n.path, Name: n.name, Title: n.doc.Title, URL: markdown.URL(n.path, ""), Score: 1, Matches: []searchMatch{}}
	for _, term := range q.terms {
		inName, inTitle, inBody := strings.Contains(lowerName, term), strings.Contains(titles, term), bytes.Contains(body, []byte(term))
		if !inName && !inTitle && !inBody {
			return searchHit{}, false
		}
		if inName {
			hit.Score += 20
		}
		if inTitle {
			hit.Score += 10
		}
		hit.Score += min(bytes.Count(body, []byte(term)), 20)
	}
	if len(q.terms) == 0 {
		return hit, true
	}
	lines := bufio.NewScanner(bytes.NewReader(n.src))
	lines.Buffer(make([]byte, 64<<10), maxNote)
	for number := 1; lines.Scan() && len(hit.Matches) < maxMatches; number++ {
		line := lines.Text()
		lower := strings.ToLower(line)
		for _, term := range q.terms {
			if strings.Contains(lower, term) {
				hit.Matches = append(hit.Matches, searchMatch{Line: number, Text: strings.TrimSpace(line), URL: hit.URL + "#:~:text=" + escapeFragmentText(term)})
				break
			}
		}
	}
	return hit, true
}

func (h *Handler) searchJSON(w http.ResponseWriter, r *http.Request, idx *index) {
	writeJSON(w, r, idx.search(r.URL.Query().Get("q")))
}
