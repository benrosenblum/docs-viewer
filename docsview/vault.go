package docsview

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/benrosenblum/docs-viewer/docsview/markdown"
)

const (
	maxNote   = 8 << 20
	maxSource = 2 << 20
)

// rawTypes are served as bytes instead of a rendered page.
var rawTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
	".webp": "image/webp", ".svg": "image/svg+xml", ".avif": "image/avif", ".bmp": "image/bmp",
	".ico": "image/x-icon", ".pdf": "application/pdf",
}

func isNote(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	return ext == ".md" || ext == ".markdown"
}

func noteName(name string) string {
	if isNote(name) {
		return strings.TrimSuffix(name, path.Ext(name))
	}
	return name
}

// labels names each path after its note name. When several paths share a
// name, as OpenSpec's spec.md and tasks.md files do, their labels add the
// parent folder, such as "api-framework/spec".
func labels(paths []string) map[string]string {
	count := map[string]int{}
	for _, p := range paths {
		count[strings.ToLower(noteName(path.Base(p)))]++
	}
	result := make(map[string]string, len(paths))
	for _, p := range paths {
		name := noteName(path.Base(p))
		if dir := path.Dir(p); count[strings.ToLower(name)] > 1 && dir != "." {
			name = path.Base(dir) + "/" + name
		}
		result[p] = name
	}
	return result
}

// allowed reports whether a clean repository path is inside the viewer
// surface. Hidden entries, dependency trees, and the reserved "_" prefix are
// excluded. os.Root confines the remaining paths to the repository.
func allowed(p string) bool {
	if p == "." {
		return true
	}
	if !fs.ValidPath(p) {
		return false
	}
	for i, segment := range strings.Split(p, "/") {
		if strings.HasPrefix(segment, ".") || segment == "node_modules" || (i == 0 && segment == "_") {
			return false
		}
	}
	return true
}

// within reports whether the clean path p is dir or is below it.
func within(p, dir string) bool { return p == dir || strings.HasPrefix(p, dir+"/") }

// vaultOf returns the vault directory that contains p, or "" for a path
// outside the vault.
func (h *Handler) vaultOf(p string) string {
	for _, vault := range h.vaults {
		if within(p, vault) {
			return vault
		}
	}
	return ""
}

// visible reports whether p is allowed and, after symbolic links, whether its
// target is allowed too. os.Root keeps links inside the repository, but a
// link with a visible name could still lead to a hidden file.
func (h *Handler) visible(p string) bool {
	if !allowed(p) {
		return false
	}
	target, err := filepath.EvalSymlinks(filepath.Join(h.rootPath, filepath.FromSlash(p)))
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(h.rootPath, target)
	return err == nil && allowed(filepath.ToSlash(rel))
}

// entryLine describes the state of one path for a snapshot version.
func entryLine(p string, dir bool, size int64, mod time.Time) string {
	return fmt.Sprintf("%s\x00%t\x00%d\x00%d\n", p, dir, size, mod.UnixNano())
}

// entry is one vault file or directory.
type entry struct {
	path, name string
	dir        bool
	size       int64
	mod        time.Time
	children   []*entry
}

// scan lists the vault directories under a tree root whose children are the
// vault directories in configuration order. The returned version changes
// with any file addition, removal, size change, or modification time change.
func (h *Handler) scan() (*entry, map[string]*entry, string, error) {
	root := &entry{path: ".", name: h.name, dir: true}
	entries := map[string]*entry{}
	hash := sha256.New()
	for _, vault := range h.vaults {
		top := &entry{path: vault, name: vault, dir: true}
		root.children = append(root.children, top)
		entries[vault] = top
		if err := h.walk(vault, entries, hash); err != nil {
			return nil, nil, "", err
		}
	}
	for _, e := range entries {
		slices.SortFunc(e.children, compareEntries)
	}
	return root, entries, hex.EncodeToString(hash.Sum(nil))[:16], nil
}

// walk adds the entries below one vault directory and writes their state
// lines to hash.
func (h *Handler) walk(vault string, entries map[string]*entry, hash io.Writer) error {
	fsys := h.root.FS()
	return fs.WalkDir(fsys, vault, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == vault {
			return nil
		}
		if !allowed(p) || d.Type()&fs.ModeSymlink != 0 && !h.visible(p) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		info, err := fs.Stat(fsys, p) // Follows links that stay inside the root.
		if err != nil {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 && info.IsDir() {
			return nil // Avoid link cycles; linked directories are not listed.
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return nil
		}
		e := &entry{path: p, name: d.Name(), dir: info.IsDir(), size: info.Size(), mod: info.ModTime()}
		parent := entries[path.Dir(p)]
		if parent == nil {
			return nil
		}
		parent.children = append(parent.children, e)
		entries[p] = e
		_, _ = io.WriteString(hash, entryLine(p, e.dir, e.size, e.mod))
		return nil
	})
}

// compareEntries sorts folders first, then names without regard to case.
func compareEntries(a, b *entry) int {
	if a.dir != b.dir {
		if a.dir {
			return -1
		}
		return 1
	}
	return cmp.Or(cmp.Compare(strings.ToLower(a.name), strings.ToLower(b.name)), cmp.Compare(a.name, b.name))
}

// note is one rendered vault Markdown file.
type note struct {
	path, name string
	src        []byte
	headings   []markdown.Heading
	doc        *markdown.Document
}

// index is an immutable snapshot of the vault. Every vault note is rendered
// once per snapshot, which provides backlinks, search text, and the graph.
type index struct {
	version string   // The vault scan version and the outside file version.
	outside []string // Sorted files outside the vault that vault notes use.
	tree    *entry
	entries map[string]*entry
	lower   map[string]string   // Lower-case vault file path to path.
	bases   map[string][]string // Lower-case base name to vault file paths.
	exts    map[string]bool     // Lower-case vault file extensions.
	notes   []*note
	byPath  map[string]*note
	labels  map[string]string // Note path to its backlink label.
}

// current returns the snapshot for the vault's present state.
func (h *Handler) current() (*index, error) {
	tree, entries, scanned, err := h.scan()
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	// Vault notes can link to or embed files outside the vault, so a change
	// to those files also makes a new snapshot.
	if h.idx != nil && h.idx.version == scanned+outsideVersion(h.idx.outside, h.statLine) {
		return h.idx, nil
	}
	idx := &index{tree: tree, entries: entries, lower: map[string]string{}, bases: map[string][]string{}, exts: map[string]bool{}, byPath: map[string]*note{}}
	for p, e := range entries {
		if e.dir {
			continue
		}
		idx.lower[strings.ToLower(p)] = p
		base := strings.ToLower(e.name)
		idx.bases[base] = append(idx.bases[base], p)
		idx.exts[strings.ToLower(path.Ext(base))] = true
		if !isNote(e.name) {
			continue
		}
		src, err := h.read(p, maxNote)
		if err != nil {
			continue // Removed or replaced during the scan; the next scan sees it.
		}
		n := &note{path: p, name: noteName(e.name), src: src, headings: markdown.Headings(src)}
		idx.notes = append(idx.notes, n)
		idx.byPath[p] = n
	}
	slices.SortFunc(idx.notes, func(a, b *note) int { return cmp.Compare(a.path, b.path) })
	idx.labels = labels(slices.Collect(maps.Keys(idx.byPath)))
	for _, paths := range idx.bases {
		slices.Sort(paths)
	}
	r := &resolver{h: h, idx: idx, outside: map[string]string{}}
	for _, n := range idx.notes {
		doc, err := markdown.Render(n.src, n.path, r)
		if err != nil {
			doc = &markdown.Document{HTML: "<p class=\"render-error\">" + escapeText(err.Error()) + "</p>"}
		}
		n.doc = doc
	}
	idx.outside = slices.Sorted(maps.Keys(r.outside))
	idx.version = scanned + outsideVersion(idx.outside, func(p string) string { return r.outside[p] })
	h.idx = idx
	return idx, nil
}

// outsideVersion hashes the state lines of the given paths.
func outsideVersion(paths []string, line func(string) string) string {
	hash := sha256.New()
	for _, p := range paths {
		_, _ = io.WriteString(hash, line(p))
	}
	return hex.EncodeToString(hash.Sum(nil))[:16]
}

// statLine returns the current state line of a path.
func (h *Handler) statLine(p string) string {
	info, err := h.stat(p)
	if err != nil {
		return p + "\x00missing\n"
	}
	return entryLine(p, info.IsDir(), info.Size(), info.ModTime())
}

// read returns a regular file's bytes through the confined repository root.
func (h *Handler) read(p string, limit int64) ([]byte, error) {
	if !h.visible(p) {
		return nil, fs.ErrNotExist
	}
	f, err := h.root.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fs.ErrNotExist
	}
	if info.Size() > limit {
		return nil, errTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errTooLarge
	}
	return data, nil
}

var errTooLarge = errors.New("file is too large to display")

// stat returns information about an allowed repository path.
func (h *Handler) stat(p string) (fs.FileInfo, error) {
	if !h.visible(p) {
		return nil, fs.ErrNotExist
	}
	return h.root.Stat(p)
}

// resolver answers renderer questions from one snapshot.
type resolver struct {
	h   *Handler
	idx *index
	// outside, when set, records the state line of each file outside the
	// vault that rendering used.
	outside map[string]string
}

func (r *resolver) use(p string) {
	if _, ok := r.outside[p]; !ok && r.outside != nil {
		r.outside[p] = r.h.statLine(p)
	}
}

// ResolveWikilink follows Obsidian's rules: a path relative to the linking
// file, a path in a vault directory, a unique path suffix, then a base name,
// preferring the linking file's folder and then the shortest path. Vault
// paths try the linking file's vault directory first, then the others in
// configuration order, then the repository root, as in [[openspec/x]].
func (r *resolver) ResolveWikilink(from, target string) (string, bool) {
	target = strings.TrimPrefix(strings.TrimSpace(target), "/")
	if target == "" {
		return "", false
	}
	candidates := []string{target}
	if ext := path.Ext(target); ext == "" || !r.knownExtension(ext) {
		candidates = []string{target + ".md", target}
	}
	relativeOnly := strings.HasPrefix(target, "./") || strings.HasPrefix(target, "../")
	vaults := append(slices.Clone(r.h.vaults), ".")
	if own := r.h.vaultOf(from); own != "" {
		vaults = append([]string{own}, vaults...)
	}
	for _, c := range candidates {
		if relative := path.Join(path.Dir(from), c); allowed(relative) && r.isFile(relative) {
			return relative, true
		}
		if relativeOnly {
			continue
		}
		for _, vault := range vaults {
			if p, ok := r.idx.lower[strings.ToLower(path.Join(vault, c))]; ok {
				return p, true
			}
		}
		if strings.Contains(c, "/") {
			suffix := "/" + strings.ToLower(c)
			var matches []string
			for lower, p := range r.idx.lower {
				if strings.HasSuffix(lower, suffix) {
					matches = append(matches, p)
				}
			}
			if p, ok := closest(from, matches); ok {
				return p, true
			}
			continue
		}
		if p, ok := closest(from, r.idx.bases[strings.ToLower(c)]); ok {
			return p, true
		}
	}
	return "", false
}

func (r *resolver) knownExtension(ext string) bool {
	ext = strings.ToLower(ext)
	_, raw := rawTypes[ext]
	return raw || r.idx.exts[ext] || ext == ".md" || ext == ".markdown"
}

func (r *resolver) isFile(p string) bool {
	if e, ok := r.idx.entries[p]; ok {
		return !e.dir
	}
	r.use(p)
	info, err := r.h.stat(p)
	return err == nil && info.Mode().IsRegular()
}

// closest picks a match in from's folder, else the shortest path.
func closest(from string, matches []string) (string, bool) {
	if len(matches) == 0 {
		return "", false
	}
	dir := path.Dir(from)
	best := ""
	for _, p := range matches {
		if path.Dir(p) == dir {
			return p, true
		}
		if best == "" || len(p) < len(best) || len(p) == len(best) && p < best {
			best = p
		}
	}
	return best, true
}

func (r *resolver) Exists(p string) bool {
	if _, ok := r.idx.entries[p]; ok {
		return true
	}
	r.use(p)
	_, err := r.h.stat(p)
	return err == nil
}

func (r *resolver) HeadingID(p, heading string) (string, bool) {
	if i := strings.LastIndex(heading, "#"); i >= 0 {
		heading = heading[i+1:]
	}
	var headings []markdown.Heading
	if n, ok := r.idx.byPath[p]; ok {
		headings = n.headings
	} else {
		r.use(p)
		headings = r.h.outsideHeadings(p)
	}
	for _, match := range []func(string) string{normalizeHeading, strippedHeading} {
		want := match(heading)
		for _, h := range headings {
			if match(h.Text) == want {
				return h.ID, true
			}
		}
	}
	return "", false
}

func (r *resolver) Source(p string) ([]byte, error) {
	if !isNote(p) {
		return nil, fmt.Errorf("%s is not a Markdown file", p)
	}
	if n, ok := r.idx.byPath[p]; ok {
		return n.src, nil
	}
	r.use(p)
	return r.h.read(p, maxNote)
}

func normalizeHeading(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

// strippedHeading removes characters that Obsidian drops from heading links.
func strippedHeading(s string) string {
	return normalizeHeading(strings.Map(func(r rune) rune {
		if strings.ContainsRune("#|^:%[]\\", r) {
			return -1
		}
		return r
	}, s))
}

// headingCache holds outline parses of Markdown files outside the vault.
type headingCache struct {
	mu      sync.Mutex
	entries map[string]cachedHeadings
}

type cachedHeadings struct {
	size     int64
	mod      time.Time
	headings []markdown.Heading
}

func (h *Handler) outsideHeadings(p string) []markdown.Heading {
	info, err := h.stat(p)
	if err != nil || !isNote(p) {
		return nil
	}
	h.headings.mu.Lock()
	cached, ok := h.headings.entries[p]
	h.headings.mu.Unlock()
	if ok && cached.size == info.Size() && cached.mod.Equal(info.ModTime()) {
		return cached.headings
	}
	src, err := h.read(p, maxNote)
	if err != nil {
		return nil
	}
	headings := markdown.Headings(src)
	h.headings.mu.Lock()
	h.headings.entries[p] = cachedHeadings{info.Size(), info.ModTime(), headings}
	h.headings.mu.Unlock()
	return headings
}

// backlink groups the references from one note to the viewed file.
type backlink struct {
	Path, Name string
	URL        string
	Contexts   []backlinkContext
}

type backlinkContext struct{ Text, URL string }

func (idx *index) backlinks(p string) []backlink {
	var result []backlink
	for _, n := range idx.notes {
		if n.path == p {
			continue
		}
		var group *backlink
		seen := map[string]bool{}
		for _, link := range n.doc.Links {
			if link.Path != p {
				continue
			}
			if group == nil {
				result = append(result, backlink{Path: n.path, Name: idx.labels[n.path], URL: markdown.URL(n.path, "")})
				group = &result[len(result)-1]
			}
			if link.Context == "" || seen[link.Context] || len(group.Contexts) == 5 {
				continue
			}
			seen[link.Context] = true
			group.Contexts = append(group.Contexts, backlinkContext{link.Context, markdown.URL(n.path, "") + textFragment(link.Context)})
		}
	}
	return result
}

// textFragment scrolls to the start of a context with a browser text fragment.
func textFragment(context string) string {
	words := strings.Fields(context)
	if len(words) > 6 {
		words = words[:6]
	}
	if len(words) == 0 {
		return ""
	}
	return "#:~:text=" + escapeFragmentText(strings.Join(words, " "))
}
