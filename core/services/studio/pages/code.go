package pages

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The code of a page is more than its index.html: the UI puts the scripts,
// style sheets and ES modules that the page names from its own folder into the
// page before it runs (pageAssets.js in the React UI). The consent to a shared
// page must cover all of them, or the author could change app.js after the
// viewer allowed the page and run new code without asking again.
//
// codeSet finds the same files as the UI's inliner: <script src> (classic and
// module), <link rel="stylesheet" href>, the relative @import of every style
// sheet, inline <style> and style attribute, and the relative imports of every
// module, recursively. The rules below follow pageAssets.js line by line. The
// UI also refuses to put a text file into a shared page that is not in this
// set, so if the two ever disagree a file fails to load instead of running
// outside the consent.

const (
	// maxCodeFiles bounds the files of one page's code. A page with more is
	// hashed with what was found and a marker, and the rest do not load.
	maxCodeFiles = 256
	maxCSSDepth  = 8
	// refCacheSize bounds the cache of the references found in a file.
	refCacheSize = 4096
)

// codeFile is one file of a page's code: its path inside the page's folder and
// its content blob, or missing when the page names a file that is not there
// (adding it later changes the code too).
type codeFile struct {
	path    string
	blob    string
	size    int64
	missing bool
}

// fileRefs is what one file names. For index.html: classic scripts, module
// roots and style sheets (each to load at depth 1). For a module: the modules
// it imports. For a style sheet: the style sheets it imports.
type fileRefs struct {
	scripts []string
	modules []string
	styles  []string
}

// refCache keeps the references of a file by the hash of its content, so a
// listing of many shared pages reads each file once while it stays the same.
// It is a per-instance cache: a content hash names the same text on every
// frontend, so two frontends with different caches compute the same set.
type refCache struct {
	mu sync.Mutex
	m  map[string]fileRefs
}

func (c *refCache) get(key string) (fileRefs, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.m[key]
	return r, ok
}

func (c *refCache) put(key string, r fileRefs) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) >= refCacheSize {
		c.m = make(map[string]fileRefs, 64)
	}
	c.m[key] = r
}

// codeHash is the hash of what a page runs: index.html, page.json (the title
// and icon the trust sheet shows) and the files of code. The data files of the
// page are not in it: they change at every save, and the page's code decides
// what to do with them.
func codeHash(index, meta workspace.Node, code []codeFile) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "index.html\x00%s\x00%d\x00page.json\x00%s\x00%d", index.Blob, index.Size, meta.Blob, meta.Size)
	for _, f := range code {
		if f.missing {
			_, _ = fmt.Fprintf(h, "\x00code\x00%s\x00missing", f.path)
			continue
		}
		_, _ = fmt.Fprintf(h, "\x00code\x00%s\x00%s\x00%d", f.path, f.blob, f.size)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// codeSet returns the files of code of the page at l, sorted by path, and
// whether the page names more than maxCodeFiles.
func (s *Service) codeSet(ctx context.Context, l loc, index workspace.Node) ([]codeFile, bool) {
	root, ok := s.refsOf(ctx, l, index, "index")
	if !ok {
		return nil, false
	}
	type job struct {
		path  string
		kind  string // script, module or style
		depth int
	}
	seen := map[string]bool{}
	var queue []job
	add := func(p, kind string, depth int) {
		key := kind + ":" + p
		if seen[key] {
			return
		}
		seen[key] = true
		queue = append(queue, job{p, kind, depth})
	}
	for _, p := range root.scripts {
		add(p, "script", 0)
	}
	for _, p := range root.modules {
		add(p, "module", 0)
	}
	for _, p := range root.styles {
		add(p, "style", 1)
	}
	files := map[string]codeFile{}
	over := false
	// Breadth first, so a style sheet is first met at its smallest depth.
	for i := 0; i < len(queue); i++ {
		j := queue[i]
		f, ok := files[j.path]
		if !ok {
			if len(files) == maxCodeFiles {
				over = true
				break
			}
			f = s.codeNode(ctx, l, j.path)
			files[j.path] = f
		}
		if f.missing || j.kind == "script" {
			continue
		}
		n := workspace.Node{Path: l.path(j.path), Kind: workspace.KindFile, Blob: f.blob, Size: f.size}
		refs, ok := s.refsOf(ctx, l, n, j.kind)
		if !ok {
			continue
		}
		if j.kind == "module" {
			for _, p := range refs.modules {
				add(p, "module", 0)
			}
			continue
		}
		if j.depth < maxCSSDepth {
			for _, p := range refs.styles {
				add(p, "style", j.depth+1)
			}
		}
	}
	out := make([]codeFile, 0, len(files))
	for _, f := range files {
		out = append(out, f)
	}
	slices.SortFunc(out, func(a, b codeFile) int { return strings.Compare(a.path, b.path) })
	return out, over
}

func (s *Service) codeNode(ctx context.Context, l loc, rel string) codeFile {
	if _, err := CleanPath(rel); err != nil {
		return codeFile{path: rel, missing: true}
	}
	n, err := s.f.Store().Stat(ctx, l.a, l.path(rel))
	if err != nil || n.Kind != workspace.KindFile {
		return codeFile{path: rel, missing: true}
	}
	return codeFile{path: rel, blob: n.Blob, size: n.Size}
}

// refsOf returns what the file n names, read as kind (index, module or
// style). A file the UI could not read (too large, not text) names nothing.
func (s *Service) refsOf(ctx context.Context, l loc, n workspace.Node, kind string) (fileRefs, bool) {
	rel := strings.TrimPrefix(n.Path, l.folder+"/")
	key := kind + "\x00" + dirOf(rel) + "\x00" + n.Blob
	if r, ok := s.refs.get(key); ok {
		return r, true
	}
	if n.Size > MaxFileSize {
		return fileRefs{}, false
	}
	rd, got, err := s.f.Store().Open(ctx, l.a, n.Path)
	if err != nil {
		return fileRefs{}, false
	}
	defer func() { _ = rd.Close() }()
	body, err := io.ReadAll(io.LimitReader(rd, MaxFileSize+1))
	if err != nil || len(body) > MaxFileSize || !utf8.Valid(body) || got.Blob != n.Blob {
		return fileRefs{}, false
	}
	text := string(body)
	var r fileRefs
	switch kind {
	case "index":
		r = htmlRefs(text)
	case "module":
		r.modules = moduleImports(text, dirOf(rel))
	case "style":
		r.styles = cssImports(text, dirOf(rel))
	}
	s.refs.put(key, r)
	return r, true
}

func dirOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return ""
}

// ---------- resolvePagePath ----------

var (
	schemeRE     = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*:`)
	badCharsRE   = regexp.MustCompile("[\\\\:\x00-\x1f\x7f]")
	relativeSpec = regexp.MustCompile(`^\.{1,2}/`)
)

// resolveRef is resolvePagePath of pageAssets.js: the path of the page's
// folder that ref names, relative to the folder from, or "" when it names no
// file of the folder (a remote URL, an empty one, or one that is refused).
func resolveRef(ref, from string) string {
	s := strings.TrimSpace(ref)
	if s == "" || s[0] == '#' || s[0] == '?' {
		return ""
	}
	if schemeRE.MatchString(s) || strings.HasPrefix(s, "//") || strings.HasPrefix(s, `\\`) || strings.HasPrefix(s, `/\`) || strings.HasPrefix(s, `\/`) {
		return ""
	}
	if s[0] == '/' || s[0] == '\\' {
		return ""
	}
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	dec, err := url.PathUnescape(s)
	if err != nil || !utf8.ValidString(dec) {
		return ""
	}
	if badCharsRE.MatchString(dec) || strings.HasPrefix(dec, "/") {
		return ""
	}
	var out []string
	if from != "" {
		out = strings.Split(from, "/")
	}
	for _, seg := range strings.Split(dec, "/") {
		switch seg {
		case "":
			return ""
		case ".":
			continue
		case "..":
			if len(out) == 0 {
				return ""
			}
			out = out[:len(out)-1]
			continue
		}
		out = append(out, seg)
	}
	p := strings.Join(out, "/")
	if p == "" || utf8.RuneCountInString(p) > maxPathBytes || len(out) > maxDepth {
		return ""
	}
	return p
}

// ---------- the tags of the markup ----------

var rawText = map[string]bool{"script": true, "style": true, "textarea": true, "title": true, "xmp": true, "iframe": true, "noembed": true, "noframes": true, "noscript": true}

var entityRE = regexp.MustCompile(`(?i)&(#x[0-9a-f]+|#[0-9]+|[a-z]+);?`)
var entities = map[string]string{"amp": "&", "lt": "<", "gt": ">", "quot": `"`, "apos": "'", "nbsp": " "}

func decodeEntities(s string) string {
	return entityRE.ReplaceAllStringFunc(s, func(m string) string {
		e := entityRE.FindStringSubmatch(m)[1]
		if e[0] == '#' {
			var n int64
			var err error
			if len(e) > 1 && (e[1] == 'x' || e[1] == 'X') {
				n, err = strconv.ParseInt(e[2:], 16, 64)
			} else {
				n, err = strconv.ParseInt(e[1:], 10, 64)
			}
			if err != nil || n <= 0 || n > 0x10ffff {
				return m
			}
			return string(rune(n))
		}
		if v, ok := entities[strings.ToLower(e)]; ok {
			return v
		}
		return m
	})
}

type tagAttr struct {
	name, value string
	hasValue    bool
}

type htmlTag struct {
	name   string
	attrs  []tagAttr
	inner  string
	hasRaw bool
}

func (t htmlTag) attr(name string) (string, bool) {
	for _, a := range t.attrs {
		if a.name == name {
			return a.value, true
		}
	}
	return "", false
}

func isSpace(c byte) bool { return c == ' ' || c == '\n' || c == '\t' || c == '\r' || c == '\f' }

func isASCIILetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

// scanTags is scanTags of pageAssets.js: the start tags of the markup with
// their attributes, and the text of the raw text elements.
func scanTags(text string) []htmlTag {
	n := len(text)
	var tags []htmlTag
	i := 0
	for i < n {
		lt := strings.IndexByte(text[i:], '<')
		if lt < 0 {
			break
		}
		lt += i
		if strings.HasPrefix(text[lt:], "<!--") {
			e := strings.Index(text[lt+4:], "-->")
			if e < 0 {
				i = n
			} else {
				i = lt + 4 + e + 3
			}
			continue
		}
		var c byte
		if lt+1 < n {
			c = text[lt+1]
		}
		if c == '!' || c == '?' || c == '/' {
			from := min(lt+2, n)
			e := strings.IndexByte(text[from:], '>')
			if e < 0 {
				i = n
			} else {
				i = from + e + 1
			}
			continue
		}
		if !isASCIILetter(c) {
			i = lt + 1
			continue
		}
		j := lt + 1
		for j < n && !isSpace(text[j]) && text[j] != '/' && text[j] != '>' {
			j++
		}
		tag := htmlTag{name: strings.ToLower(text[lt+1 : j])}
		for j < n {
			for j < n && (isSpace(text[j]) || text[j] == '/') {
				j++
			}
			if j >= n {
				break
			}
			if text[j] == '>' {
				j++
				break
			}
			k := j + 1
			for k < n && !isSpace(text[k]) && text[k] != '/' && text[k] != '>' && text[k] != '=' {
				k++
			}
			an := strings.ToLower(text[j:min(k, n)])
			j = k
			for j < n && isSpace(text[j]) {
				j++
			}
			if j >= n || text[j] != '=' {
				tag.attrs = append(tag.attrs, tagAttr{name: an})
				continue
			}
			j++
			for j < n && isSpace(text[j]) {
				j++
			}
			if j < n && (text[j] == '"' || text[j] == '\'') {
				q := text[j]
				end := n
				if e := strings.IndexByte(text[j+1:], q); e >= 0 {
					end = j + 1 + e
				}
				tag.attrs = append(tag.attrs, tagAttr{name: an, value: decodeEntities(text[j+1 : end]), hasValue: true})
				j = min(n, end+1)
			} else {
				e := j
				for e < n && !isSpace(text[e]) && text[e] != '>' {
					e++
				}
				tag.attrs = append(tag.attrs, tagAttr{name: an, value: decodeEntities(text[j:e]), hasValue: true})
				j = e
			}
		}
		if rawText[tag.name] {
			innerEnd, closeAt := n, n
			if m := closeTag(text, tag.name, j); m >= 0 {
				innerEnd = m
				if gt := strings.IndexByte(text[m:], '>'); gt >= 0 {
					closeAt = m + gt + 1
				}
			}
			tag.inner, tag.hasRaw = text[min(j, n):innerEnd], true
			i = closeAt
		} else {
			i = j
		}
		tags = append(tags, tag)
	}
	return tags
}

// closeTag finds </name, in any case, followed by white space, / or > or the
// end, from offset at.
func closeTag(text, name string, at int) int {
	for at < len(text) {
		k := strings.Index(text[at:], "</")
		if k < 0 {
			return -1
		}
		k += at
		e := k + 2 + len(name)
		if e <= len(text) && strings.EqualFold(text[k+2:e], name) &&
			(e == len(text) || isSpace(text[e]) || text[e] == '/' || text[e] == '>') {
			return k
		}
		at = k + 1
	}
	return -1
}

var jsTypeRE = regexp.MustCompile(`^(|module|text/javascript|application/javascript|text/ecmascript|application/ecmascript)$`)
var urlOrImportRE = regexp.MustCompile(`(?i)url\(|@import`)

// htmlRefs is what index.html names: the files that the inliner reads as text.
func htmlRefs(text string) fileRefs {
	var r fileRefs
	for _, tag := range scanTags(text) {
		switch tag.name {
		case "script":
			typ, _ := tag.attr("type")
			typ = strings.ToLower(strings.TrimSpace(typ))
			if !jsTypeRE.MatchString(typ) {
				continue
			}
			src, hasSrc := tag.attr("src")
			if typ == "module" {
				if hasSrc {
					if p := resolveRef(src, ""); p != "" {
						r.modules = append(r.modules, p)
					}
				} else if tag.hasRaw {
					r.modules = append(r.modules, moduleImports(tag.inner, "")...)
				}
				continue
			}
			if hasSrc {
				if p := resolveRef(src, ""); p != "" {
					r.scripts = append(r.scripts, p)
				}
			}
			continue
		case "link":
			rel, _ := tag.attr("rel")
			rels := strings.Fields(strings.ToLower(rel))
			if !slices.Contains(rels, "stylesheet") || slices.Contains(rels, "alternate") {
				continue
			}
			if href, ok := tag.attr("href"); ok {
				if p := resolveRef(href, ""); p != "" {
					r.styles = append(r.styles, p)
				}
			}
			continue
		case "style":
			if tag.hasRaw && urlOrImportRE.MatchString(tag.inner) {
				r.styles = append(r.styles, cssImports(tag.inner, "")...)
			}
		}
		for _, a := range tag.attrs {
			if a.hasValue && a.name == "style" {
				r.styles = append(r.styles, cssImports(a.value, "")...)
			}
		}
	}
	return r
}

// The import specifiers of a module, as rewriteImports of pageAssets.js reads
// them. RE2 has no back references, so each quote has its own branch.
var (
	staticImportRE  = regexp.MustCompile(`(?:\bimport\s*(?:[\w$*{}\s,]+?\s*from\s*)?|\bexport\s*(?:\*\s*(?:as\s+[\w$]+\s*)?|\{[^}]*\}\s*)from\s*)(?:'([^'"\n\r]+)'|"([^'"\n\r]+)")`)
	dynamicImportRE = regexp.MustCompile("\\bimport\\s*\\(\\s*(?:'([^'\"`$\\n\\r]+)'|\"([^'\"`$\\n\\r]+)\"|`([^'\"`$\\n\\r]+)`)\\s*\\)")
)

func moduleImports(code, dir string) []string {
	var out []string
	take := func(spec string) {
		if !relativeSpec.MatchString(spec) {
			return // a package name, a remote URL or an absolute path: never a file
		}
		if p := resolveRef(spec, dir); p != "" {
			out = append(out, p)
		}
	}
	for _, m := range staticImportRE.FindAllStringSubmatch(code, -1) {
		take(m[1] + m[2])
	}
	for _, m := range dynamicImportRE.FindAllStringSubmatch(code, -1) {
		take(m[1] + m[2] + m[3])
	}
	return out
}

var cssImportRE = regexp.MustCompile(`(?i)@import\s+(?:url\(\s*(?:"([^"]*)"|'([^']*)'|([^)\s]*))\s*\)|"([^"]*)"|'([^']*)')\s*([^;]*);`)

// cssImports is the relative @import of a style sheet, relative to dir.
func cssImports(css, dir string) []string {
	var out []string
	for _, m := range cssImportRE.FindAllStringSubmatch(css, -1) {
		if p := resolveRef(m[1]+m[2]+m[3]+m[4]+m[5], dir); p != "" {
			out = append(out, p)
		}
	}
	return out
}
