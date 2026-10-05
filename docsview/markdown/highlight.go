package markdown

import (
	"html"
	"regexp"
	"strings"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// noPre lets Highlight write its own <pre> while chroma still emits line
// spans, which PreventSurroundingPre would drop.
type noPre struct{}

func (noPre) Start(bool, string) string { return "" }
func (noPre) End(bool) string           { return "" }

var (
	codeFormatter   = chromahtml.New(chromahtml.WithClasses(true), chromahtml.WithPreWrapper(noPre{}))
	sourceFormatter = chromahtml.New(
		chromahtml.WithClasses(true),
		chromahtml.WithPreWrapper(noPre{}),
		chromahtml.WithLineNumbers(true),
		chromahtml.WithLinkableLineNumbers(true, "L"),
	)
	// classStyle only satisfies Format; class output does not depend on it.
	classStyle = styles.Get("github")
)

// Highlight renders code as class-based chroma HTML in
// <pre class="code-block chroma" data-lang="...">. An empty lang picks a
// lexer from filename. With lineNumbers the lines get "L1" anchors.
func Highlight(code []byte, lang, filename string, lineNumbers bool) string {
	lexer, name := pickLexer(lang, filename)
	formatter, class := codeFormatter, "code-block chroma"
	if lineNumbers {
		formatter, class = sourceFormatter, "code-block chroma source"
	}
	var b strings.Builder
	b.WriteString(`<pre class="` + class + `" data-lang="` + html.EscapeString(name) + `"><code>`)
	tokens, err := chroma.Coalesce(lexer).Tokenise(nil, string(code))
	if err != nil {
		tokens, _ = lexers.Fallback.Tokenise(nil, string(code))
	}
	var body strings.Builder
	if formatter.Format(&body, classStyle, tokens) != nil {
		body.Reset()
		body.WriteString(html.EscapeString(string(code)))
	}
	b.WriteString(body.String())
	b.WriteString("</code></pre>")
	return b.String()
}

// pickLexer returns the lexer for lang, else for filename, else plain text,
// with the language name for data-lang ("" for plain text fallbacks).
func pickLexer(lang, filename string) (chroma.Lexer, string) {
	if lang != "" {
		if l := lexers.Get(lang); l != nil {
			return l, strings.ToLower(lang)
		}
		return lexers.Fallback, ""
	}
	if filename != "" {
		if l := lexers.Match(filename); l != nil {
			return l, lexerName(l)
		}
	}
	return lexers.Fallback, ""
}

func lexerName(l chroma.Lexer) string {
	if c := l.Config(); len(c.Aliases) > 0 {
		return c.Aliases[0]
	} else if c != nil {
		return strings.ToLower(c.Name)
	}
	return ""
}

// cssRule matches one "/* Token */ selector { declarations }" line of chroma CSS.
var (
	cssRule         = regexp.MustCompile(`^(?:/\*.*?\*/ )?(.+?) \{ (.*) \}$`)
	cssBackground   = regexp.MustCompile(`background-color:[^;]*;?\s*`)
	lineNumberColor = map[string]string{"light": "#8c959f", "dark": "#6e7681"}
)

// HighlightCSS returns chroma CSS for the light and dark viewer themes,
// scoped by html[data-theme]. Code block backgrounds are left to the page.
func HighlightCSS() string {
	var b strings.Builder
	for _, theme := range []struct{ name, style string }{{"light", "github"}, {"dark", "github-dark"}} {
		var css strings.Builder
		_ = sourceFormatter.WriteCSS(&css, styles.Get(theme.style))
		scope := `html[data-theme="` + theme.name + `"] `
		for _, line := range strings.Split(css.String(), "\n") {
			m := cssRule.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			selector, decls := m[1], m[2]
			switch selector {
			case ".bg":
				continue
			case ".chroma":
				decls = strings.TrimSpace(cssBackground.ReplaceAllString(decls, ""))
			}
			if decls != "" {
				b.WriteString(scope + selector + " { " + decls + " }\n")
			}
		}
		b.WriteString(scope + ".chroma .ln, " + scope + ".chroma .ln a { color: " + lineNumberColor[theme.name] + "; }\n")
	}
	return b.String()
}

// SourceSpan is one token of a highlighted source line. An empty Class is
// plain text.
type SourceSpan struct{ Class, Text string }

// SourceLine is one highlighted source line without its line break.
type SourceLine []SourceSpan

// HighlightLines highlights code as a whole and then splits the tokens into
// lines, so that a multi-line string or comment keeps its class on each
// line. The line texts joined with "\n" give code again: when a lexer
// changes the text, the lines are plain text. The second result is the
// language name for data-lang, as in Highlight.
func HighlightLines(code []byte, lang, filename string) ([]SourceLine, string) {
	lexer, name := pickLexer(lang, filename)
	text := string(code)
	var tokens []chroma.Token
	if it, err := chroma.Coalesce(lexer).Tokenise(&chroma.TokeniseOptions{State: "root"}, text); err == nil {
		tokens = it.Tokens()
	}
	var joined strings.Builder
	for _, t := range tokens {
		joined.WriteString(t.Value)
	}
	if joined.String() != text {
		tokens = []chroma.Token{{Type: chroma.Text, Value: text}}
	}
	var lines []SourceLine
	for _, tokenLine := range chroma.SplitTokensIntoLines(tokens) {
		var line SourceLine
		for _, t := range tokenLine {
			value := strings.TrimSuffix(t.Value, "\n")
			if value == "" {
				continue
			}
			class := tokenClass(t.Type)
			if n := len(line); n > 0 && line[n-1].Class == class {
				line[n-1].Text += value
				continue
			}
			line = append(line, SourceSpan{class, value})
		}
		lines = append(lines, line)
	}
	return lines, name
}

// tokenClass returns the chroma class of a token type, as the chroma HTML
// formatter does.
func tokenClass(t chroma.TokenType) string {
	for t != 0 {
		if class, ok := chroma.StandardTypes[t]; ok {
			return class
		}
		t = t.Parent()
	}
	return chroma.StandardTypes[t]
}

// Text returns the text of the line.
func (l SourceLine) Text() string {
	var b strings.Builder
	for _, s := range l {
		b.WriteString(s.Text)
	}
	return b.String()
}

// HTML renders the line as chroma spans. Each mark is a byte range of the
// line text, in order. The marked text is wrapped in
// <tag class="diff-word"> inside the token spans, so the colors stay; a
// mark that crosses tokens gives one wrap in each token.
func (l SourceLine) HTML(tag string, marks [][2]int) string {
	var b strings.Builder
	pos, m := 0, 0
	for _, s := range l {
		start, end := pos, pos+len(s.Text)
		pos = end
		if s.Class != "" {
			b.WriteString(`<span class="` + s.Class + `">`)
		}
		for at := start; at < end; {
			for m < len(marks) && marks[m][1] <= at {
				m++
			}
			if m < len(marks) && marks[m][0] <= at {
				stop := min(end, marks[m][1])
				b.WriteString("<" + tag + ` class="diff-word">` + html.EscapeString(s.Text[at-start:stop-start]) + "</" + tag + ">")
				at = stop
				continue
			}
			stop := end
			if m < len(marks) {
				stop = min(end, marks[m][0])
			}
			b.WriteString(html.EscapeString(s.Text[at-start : stop-start]))
			at = stop
		}
		if s.Class != "" {
			b.WriteString("</span>")
		}
	}
	return b.String()
}
