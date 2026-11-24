// Package galleryedit splices variant references into the LocalAI gallery index
// as TEXT.
//
// Re-serialising the index through a YAML marshaller would reflow 40,000 lines,
// drop the anchors and merge keys the gallery relies on, and produce a diff no
// reviewer could read, which makes a pull request worthless even when the
// content inside it is right. Every generator that adds variants to an entry the
// gallery already ships therefore edits lines, and they share this package so
// that two of them cannot drift apart on where a variants block belongs.
package galleryedit

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var (
	entryStart   = regexp.MustCompile(`^-(?: |$)`)
	inlineName   = regexp.MustCompile(`^- (?:&\S+ )?name:`)
	keyName      = regexp.MustCompile(`^  name:`)
	keyVariants  = regexp.MustCompile(`^  variants:\s*(.*)$`)
	variantItem  = regexp.MustCompile(`^    - `)
	unsafeInName = regexp.MustCompile(`[:#{}\[\],&*?|>'"%@` + "`" + `]|^\s|\s$`)
)

// layout says where the variants key and its items sit in an entry. The
// gallery keeps them at the top of the entry, the model library keeps them in
// the model body.
type layout struct {
	keyVariants *regexp.Regexp
	variantItem *regexp.Regexp
	keyLine     string
	itemPrefix  string
	// afterName puts a new variants key right after the name line. Without
	// it a new key goes after the last line of the entry, where the library
	// keeps the end of the model body.
	afterName bool
}

var galleryLayout = layout{
	keyVariants: keyVariants,
	variantItem: variantItem,
	keyLine:     "  variants:",
	itemPrefix:  "    - model: ",
	afterName:   true,
}

var libraryLayout = layout{
	keyVariants: regexp.MustCompile(`^    variants:\s*(.*)$`),
	variantItem: regexp.MustCompile(`^      - `),
	keyLine:     "    variants:",
	itemPrefix:  "      - model: ",
}

// Entry is the positional view of one gallery entry: what it is called and
// which lines it occupies. Nothing about what the entry MEANS belongs here, so
// each caller keeps its own semantic decode and only hands over the coordinates.
type Entry struct {
	Name string
	// StartLine and EndLine bound the entry, zero based and half open.
	StartLine int
	EndLine   int
}

// Insert is one entry's pending variants addition. The caller owns the contents
// of Variants: this package neither orders nor deduplicates them, because the
// right order and the right dedup rule differ between generators.
type Insert struct {
	Entry    Entry
	Variants []string
}

// Scan splits index text into lines and reports the line each top level list
// item begins on.
func Scan(text string) (lines []string, starts []int) {
	lines = strings.Split(text, "\n")
	for i, line := range lines {
		if entryStart.MatchString(line) {
			starts = append(starts, i)
		}
	}
	return lines, starts
}

// Apply splices every insert into the gallery index lines and returns the new
// text.
func Apply(lines []string, inserts []Insert) ([]string, error) {
	return apply(lines, inserts, galleryLayout)
}

// ApplyLibrary does the same for the model library, gallery/models/index.yaml,
// where an entry keeps its variants under the model: body.
func ApplyLibrary(lines []string, inserts []Insert) ([]string, error) {
	return apply(lines, inserts, libraryLayout)
}

func apply(lines []string, inserts []Insert, l layout) ([]string, error) {
	type edit struct {
		at     int
		remove int
		insert []string
	}
	var edits []edit

	for _, in := range inserts {
		if len(in.Variants) == 0 {
			continue
		}

		items := make([]string, 0, len(in.Variants))
		for _, v := range in.Variants {
			items = append(items, l.itemPrefix+QuoteName(v))
		}

		at, remove, err := insertionPoint(lines, in.Entry, l)
		if err != nil {
			return nil, err
		}
		block := items
		if remove > 0 || !hasVariantsKey(lines, in.Entry, l) {
			block = append([]string{l.keyLine}, items...)
		}
		edits = append(edits, edit{at: at, remove: remove, insert: block})
	}

	// Applying from the bottom up keeps every line number computed against the
	// original text valid while earlier edits are still pending.
	sort.Slice(edits, func(i, j int) bool { return edits[i].at > edits[j].at })

	out := append([]string(nil), lines...)
	for _, e := range edits {
		tail := append([]string(nil), out[e.at+e.remove:]...)
		out = append(out[:e.at], append(append([]string(nil), e.insert...), tail...)...)
	}
	return out, nil
}

func hasVariantsKey(lines []string, e Entry, l layout) bool {
	for i := e.StartLine; i < e.EndLine; i++ {
		if l.keyVariants.MatchString(lines[i]) {
			return true
		}
	}
	return false
}

// insertionPoint reports where new variant items belong, and how many existing
// lines the insertion replaces.
//
// An entry with no variants key gets one right after its name, which is where
// the hand-written families put it. An entry with an empty "variants: []" has
// that line replaced by a block. An entry with a block gets its items appended.
func insertionPoint(lines []string, e Entry, l layout) (at int, remove int, err error) {
	for i := e.StartLine; i < e.EndLine; i++ {
		m := l.keyVariants.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		if strings.TrimSpace(m[1]) == "[]" {
			return i, 1, nil
		}
		if strings.TrimSpace(m[1]) != "" {
			return 0, 0, fmt.Errorf("entry %q writes its variants inline (%q); this job only edits block lists", e.Name, strings.TrimSpace(m[1]))
		}
		last := i
		for j := i + 1; j < e.EndLine && l.variantItem.MatchString(lines[j]); j++ {
			last = j
		}
		return last + 1, 0, nil
	}

	if !l.afterName {
		last := e.EndLine - 1
		for last > e.StartLine && strings.TrimSpace(lines[last]) == "" {
			last--
		}
		return last + 1, 0, nil
	}
	if inlineName.MatchString(lines[e.StartLine]) {
		return e.StartLine + 1, 0, nil
	}
	for i := e.StartLine; i < e.EndLine; i++ {
		if keyName.MatchString(lines[i]) {
			return i + 1, 0, nil
		}
	}
	return 0, 0, fmt.Errorf("entry %q has no name line to anchor the insertion to", e.Name)
}

// QuoteName quotes a variant reference when the name would otherwise change
// meaning as bare YAML. Config-suffixed names carry a ":" and always need it.
func QuoteName(name string) string {
	if unsafeInName.MatchString(name) {
		return `"` + strings.ReplaceAll(name, `"`, `\"`) + `"`
	}
	return name
}
