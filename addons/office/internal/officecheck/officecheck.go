// Package officecheck inspects a generated .xlsx, .docx or .pptx file and
// reports structural problems without rendering it: a broken zip, missing
// content types, dangling relationships, malformed XML, missing required
// parts and a few format rules (cell references, style indexes, list
// numbering, slide lists). It uses only the standard library.
package officecheck

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Problem is one structural problem. Part is the zip entry where it was
// found, or empty when it concerns the whole file.
type Problem struct {
	Part    string
	Message string
}

func (p Problem) String() string {
	if p.Part == "" {
		return p.Message
	}
	return p.Part + ": " + p.Message
}

const (
	contentTypesPart = "[Content_Types].xml"
	rootRels         = "_rels/.rels"
	maxPartSize      = 256 << 20

	nsRels = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"

	maxCol = 16384
	maxRow = 1048576
)

type format int

const (
	fmtNone format = iota
	fmtXLSX
	fmtDOCX
	fmtPPTX
)

type checker struct {
	problems []Problem
	parts    map[string][]byte
	names    []string // sorted entry names
}

func (c *checker) add(part, format string, args ...any) {
	c.problems = append(c.problems, Problem{Part: part, Message: fmt.Sprintf(format, args...)})
}

// Check inspects data as an .xlsx, .docx or .pptx file and returns every
// structural problem found. It returns an empty slice for a clean file and
// never panics on malformed input.
func Check(data []byte) (problems []Problem) {
	c := &checker{parts: map[string][]byte{}}
	defer func() {
		if r := recover(); r != nil {
			c.add("", "internal error while checking: %v", r)
		}
		problems = c.problems
		if problems == nil {
			problems = []Problem{}
		}
	}()

	if !c.readZip(data) {
		return
	}
	f := c.checkContentTypes()
	c.checkRels()
	c.checkXML()
	switch f {
	case fmtXLSX:
		c.checkXLSX()
	case fmtDOCX:
		c.checkDOCX()
	case fmtPPTX:
		c.checkPPTX()
	}
	return
}

// readZip loads every entry. It reports false when nothing can be checked.
func (c *checker) readZip(data []byte) bool {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		c.add("", "not a valid zip file: %v", err)
		return false
	}
	for _, f := range zr.File {
		name := f.Name
		if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "\\") || hasDotDot(name) {
			c.add(name, "entry name is absolute or contains ..")
		}
		if strings.HasSuffix(name, "/") {
			continue
		}
		if _, dup := c.parts[name]; dup {
			c.add(name, "duplicate zip entry")
			continue
		}
		rc, err := f.Open()
		if err != nil {
			c.add(name, "cannot open zip entry: %v", err)
			continue
		}
		b, err := io.ReadAll(io.LimitReader(rc, maxPartSize+1))
		rc.Close()
		switch {
		case err != nil:
			c.add(name, "cannot read zip entry: %v", err)
		case len(b) > maxPartSize:
			c.add(name, "entry is larger than %d bytes", maxPartSize)
		default:
			c.parts[name] = b
		}
	}
	for n := range c.parts {
		c.names = append(c.names, n)
	}
	sort.Strings(c.names)
	return true
}

func hasDotDot(name string) bool {
	for _, seg := range strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return true
		}
	}
	return false
}

// attr returns the value of the attribute with the given local name.
func attr(se xml.StartElement, local string) (string, bool) {
	for _, a := range se.Attr {
		if a.Name.Local == local {
			return a.Value, true
		}
	}
	return "", false
}

// walk reads data token by token and calls fn for each token. It returns the
// parse error, or nil at a clean end of input.
func walk(data []byte, fn func(xml.Token)) error {
	d := xml.NewDecoder(bytes.NewReader(data))
	for {
		t, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		fn(t)
	}
}

// checkContentTypes parses [Content_Types].xml, checks that every part has a
// type and detects the format from the main part's type.
func (c *checker) checkContentTypes() format {
	raw, ok := c.parts[contentTypesPart]
	if !ok {
		c.add(contentTypesPart, "missing")
		return fmtNone
	}
	defaults := map[string]string{}
	overrides := map[string]string{}
	err := walk(raw, func(t xml.Token) {
		se, ok := t.(xml.StartElement)
		if !ok {
			return
		}
		ct, _ := attr(se, "ContentType")
		switch se.Name.Local {
		case "Default":
			if ext, ok := attr(se, "Extension"); ok {
				defaults[strings.ToLower(ext)] = ct
			}
		case "Override":
			if pn, ok := attr(se, "PartName"); ok {
				overrides[strings.TrimPrefix(pn, "/")] = ct
			}
		}
	})
	if err != nil {
		c.add(contentTypesPart, "not well-formed XML: %v", err)
		return fmtNone
	}
	for _, n := range c.names {
		if n == contentTypesPart || strings.HasPrefix(n, "_rels/") || strings.Contains(n, "/_rels/") {
			continue
		}
		if _, ok := overrides[n]; ok {
			continue
		}
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(n), "."))
		if _, ok := defaults[ext]; ok && ext != "" {
			continue
		}
		c.add(n, "no content type (no Override and no Default for its extension)")
	}
	for pn := range overrides {
		if _, ok := c.parts[pn]; !ok {
			c.add(contentTypesPart, "Override for %s, which is not in the package", pn)
		}
	}
	var f format
	for _, ct := range overrides {
		switch {
		case strings.Contains(ct, "spreadsheetml.sheet.main"):
			f = fmtXLSX
		case strings.Contains(ct, "wordprocessingml.document.main"):
			f = fmtDOCX
		case strings.Contains(ct, "presentationml.presentation.main"):
			f = fmtPPTX
		}
	}
	if f == fmtNone {
		c.add(contentTypesPart, "format not recognised: no workbook, document or presentation main part")
	}
	return f
}

type relationship struct {
	id, typ, target string
	external        bool
}

// parseRels reads a .rels part. It reports false when the part is missing or
// malformed (the malformed case is reported by checkXML).
func (c *checker) parseRels(name string) ([]relationship, bool) {
	raw, ok := c.parts[name]
	if !ok {
		return nil, false
	}
	var rels []relationship
	err := walk(raw, func(t xml.Token) {
		se, ok := t.(xml.StartElement)
		if !ok || se.Name.Local != "Relationship" {
			return
		}
		var r relationship
		r.id, _ = attr(se, "Id")
		r.typ, _ = attr(se, "Type")
		r.target, _ = attr(se, "Target")
		mode, _ := attr(se, "TargetMode")
		r.external = strings.EqualFold(mode, "External") || strings.HasPrefix(strings.ToLower(r.target), "http")
		rels = append(rels, r)
	})
	return rels, err == nil
}

// relsOwner returns the part that owns a .rels part, and its folder.
func relsOwner(rels string) (owner, base string) {
	dir, file := path.Split(rels) // dir ends in "_rels/"
	dir = strings.TrimSuffix(dir, "_rels/")
	if file == ".rels" {
		return "", dir
	}
	return dir + strings.TrimSuffix(file, ".rels"), strings.TrimSuffix(dir, "/")
}

// resolve turns a relationship target into a part name. It reports false
// when the target climbs out of the package root.
func resolve(base, target string) (string, bool) {
	if i := strings.IndexAny(target, "#?"); i >= 0 {
		target = target[:i]
	}
	if strings.HasPrefix(target, "/") {
		target = target[1:]
	} else {
		target = path.Join(base, target)
	}
	target = path.Clean(target)
	if target == ".." || strings.HasPrefix(target, "../") || target == "." {
		return "", false
	}
	return target, true
}

func (c *checker) checkRels() {
	for _, n := range c.names {
		if !strings.HasSuffix(n, ".rels") {
			continue
		}
		rels, ok := c.parseRels(n)
		if !ok {
			continue
		}
		owner, base := relsOwner(n)
		if owner != "" {
			if _, ok := c.parts[owner]; !ok {
				c.add(n, "relationships for %s, which is not in the package", owner)
			}
		}
		for _, r := range rels {
			if r.external {
				continue
			}
			target, ok := resolve(base, r.target)
			if !ok {
				c.add(n, "relationship %s target %q leaves the package", r.id, r.target)
				continue
			}
			if _, ok := c.parts[target]; !ok {
				c.add(n, "relationship %s target %q resolves to %s, which is missing", r.id, r.target, target)
			}
		}
	}
}

func (c *checker) checkXML() {
	for _, n := range c.names {
		if !strings.HasSuffix(n, ".xml") && !strings.HasSuffix(n, ".rels") {
			continue
		}
		if err := walk(c.parts[n], func(xml.Token) {}); err != nil {
			c.add(n, "not well-formed XML: %v", err)
		}
	}
}

// requireMain checks that _rels/.rels points an officeDocument relationship
// at main.
func (c *checker) requireMain(main string) {
	rels, ok := c.parseRels(rootRels)
	if !ok {
		if _, present := c.parts[rootRels]; !present {
			c.add(rootRels, "missing")
		}
		return
	}
	for _, r := range rels {
		if !strings.HasSuffix(r.typ, "/officeDocument") || r.external {
			continue
		}
		if t, ok := resolve("", r.target); ok && t == main {
			return
		}
	}
	c.add(rootRels, "no officeDocument relationship points at %s", main)
}

func (c *checker) require(name string) bool {
	if _, ok := c.parts[name]; !ok {
		c.add(name, "required part is missing")
		return false
	}
	return true
}

// partsMatching returns the sorted part names in dir that match
// prefixNNN.xml.
func (c *checker) partsMatching(dir, prefix string) []string {
	var out []string
	for _, n := range c.names {
		if path.Dir(n) != dir {
			continue
		}
		b := path.Base(n)
		if strings.HasPrefix(b, prefix) && strings.HasSuffix(b, ".xml") {
			out = append(out, n)
		}
	}
	return out
}

// ---- xlsx ----

var cellRefRe = regexp.MustCompile(`^([A-Z]+)([0-9]+)$`)

func validCellRef(ref string) bool {
	m := cellRefRe.FindStringSubmatch(ref)
	if m == nil || len(m[1]) > 3 {
		return false
	}
	col := 0
	for _, ch := range m[1] {
		col = col*26 + int(ch-'A') + 1
	}
	if col > maxCol {
		return false
	}
	if m[2][0] == '0' {
		return false
	}
	row, err := strconv.Atoi(m[2])
	return err == nil && row >= 1 && row <= maxRow
}

func (c *checker) checkXLSX() {
	c.requireMain("xl/workbook.xml")
	hasWB := c.require("xl/workbook.xml")
	sheets := c.partsMatching("xl/worksheets", "sheet")
	if len(sheets) == 0 {
		c.add("xl/worksheets", "no xl/worksheets/sheet*.xml part")
	}
	hasStyles := c.require("xl/styles.xml")

	if hasWB {
		c.checkSheetNames()
	}
	xfCount := -1
	if hasStyles {
		xfCount = countCellXfs(c.parts["xl/styles.xml"])
	}
	for _, s := range sheets {
		c.checkSheet(s, xfCount)
	}
}

func (c *checker) checkSheetNames() {
	_ = walk(c.parts["xl/workbook.xml"], func(t xml.Token) {
		se, ok := t.(xml.StartElement)
		if !ok || se.Name.Local != "sheet" {
			return
		}
		name, _ := attr(se, "name")
		if n := utf8.RuneCountInString(name); n > 31 {
			c.add("xl/workbook.xml", "sheet name %q is %d characters, the limit is 31", name, n)
		}
		if strings.ContainsAny(name, `[]:*?/\`) {
			c.add("xl/workbook.xml", "sheet name %q contains one of []:*?/\\", name)
		}
		if name == "" {
			c.add("xl/workbook.xml", "sheet name is empty")
		}
	})
}

// countCellXfs returns the number of xf entries in cellXfs, or -1 when
// styles.xml does not parse or has no cellXfs.
func countCellXfs(data []byte) int {
	depth, in, n := 0, -1, -1
	err := walk(data, func(t xml.Token) {
		switch t := t.(type) {
		case xml.StartElement:
			depth++
			if t.Name.Local == "cellXfs" && in < 0 {
				in, n = depth, 0
			} else if in >= 0 && depth == in+1 && t.Name.Local == "xf" {
				n++
			}
		case xml.EndElement:
			if depth == in {
				in = -2
			}
			depth--
		}
	})
	if err != nil {
		return -1
	}
	return n
}

func (c *checker) checkSheet(name string, xfCount int) {
	seen := map[string]bool{}
	inF := false
	var fText strings.Builder
	var fShared bool
	err := walk(c.parts[name], func(t xml.Token) {
		switch t := t.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "c":
				if ref, ok := attr(t, "r"); ok {
					if !validCellRef(ref) {
						c.add(name, "cell reference %q is not a valid A1 reference", ref)
					} else if seen[ref] {
						c.add(name, "duplicate cell %s", ref)
					}
					seen[ref] = true
				}
				if s, ok := attr(t, "s"); ok && xfCount >= 0 {
					ref, _ := attr(t, "r")
					idx, err := strconv.Atoi(s)
					if err != nil || idx < 0 || idx >= xfCount {
						c.add(name, "cell %s style index %q is outside the %d cellXfs in styles.xml", ref, s, xfCount)
					}
				}
			case "f":
				inF = true
				fText.Reset()
				_, hasSI := attr(t, "si")
				ft, _ := attr(t, "t")
				fShared = ft == "shared" && hasSI
			}
		case xml.CharData:
			if inF {
				fText.Write(t)
			}
		case xml.EndElement:
			if t.Name.Local == "f" && inF {
				inF = false
				if strings.TrimSpace(fText.String()) == "" && !fShared {
					c.add(name, "formula element <f> is empty")
				}
			}
		}
	})
	_ = err // malformed XML is reported by checkXML
}

// ---- docx ----

func (c *checker) checkDOCX() {
	c.requireMain("word/document.xml")
	if !c.require("word/document.xml") {
		return
	}
	body := 0
	used := map[string]bool{}
	_ = walk(c.parts["word/document.xml"], func(t xml.Token) {
		se, ok := t.(xml.StartElement)
		if !ok {
			return
		}
		switch se.Name.Local {
		case "p", "tbl":
			body++
		case "numId":
			if v, ok := attr(se, "val"); ok && v != "0" {
				used[v] = true
			}
		}
	})
	if body == 0 {
		c.add("word/document.xml", "body has no paragraph or table")
	}
	num, ok := c.parts["word/numbering.xml"]
	if !ok {
		return
	}
	defined := map[string]bool{}
	_ = walk(num, func(t xml.Token) {
		if se, ok := t.(xml.StartElement); ok && se.Name.Local == "num" {
			if v, ok := attr(se, "numId"); ok {
				defined[v] = true
			}
		}
	})
	ids := make([]string, 0, len(used))
	for id := range used {
		if !defined[id] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		c.add("word/document.xml", "numId %s is not defined in word/numbering.xml", id)
	}
}

// ---- pptx ----

func (c *checker) checkPPTX() {
	c.requireMain("ppt/presentation.xml")
	hasPres := c.require("ppt/presentation.xml")
	slides := c.partsMatching("ppt/slides", "slide")
	if len(slides) == 0 {
		c.add("ppt/slides", "no ppt/slides/slide*.xml part")
	}
	if !hasPres {
		return
	}
	var rids []string
	_ = walk(c.parts["ppt/presentation.xml"], func(t xml.Token) {
		se, ok := t.(xml.StartElement)
		if !ok || se.Name.Local != "sldId" {
			return
		}
		rid := ""
		for _, a := range se.Attr {
			if a.Name.Local == "id" && (a.Name.Space == nsRels || a.Name.Space == "r") {
				rid = a.Value
			}
		}
		rids = append(rids, rid)
	})
	if len(rids) != len(slides) {
		c.add("ppt/presentation.xml", "%d sldId entries but %d slide parts", len(rids), len(slides))
	}
	rels, ok := c.parseRels("ppt/_rels/presentation.xml.rels")
	if !ok {
		if len(rids) > 0 {
			c.add("ppt/_rels/presentation.xml.rels", "missing or unreadable, needed by the slide list")
		}
		return
	}
	byID := map[string]relationship{}
	for _, r := range rels {
		byID[r.id] = r
	}
	for _, rid := range rids {
		r, ok := byID[rid]
		if !ok {
			c.add("ppt/presentation.xml", "sldId r:id %q has no relationship", rid)
			continue
		}
		target, ok := resolve("ppt", r.target)
		if !ok {
			c.add("ppt/presentation.xml", "sldId r:id %q target %q leaves the package", rid, r.target)
		} else if _, ok := c.parts[target]; !ok {
			c.add("ppt/presentation.xml", "sldId r:id %q points at %s, which is missing", rid, target)
		}
	}
}
