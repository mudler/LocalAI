package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var (
	inlineName   = regexp.MustCompile(`^- (?:&\S+ )?name:`)
	keyName      = regexp.MustCompile(`^  name:`)
	keyVariants  = regexp.MustCompile(`^  variants:\s*(.*)$`)
	variantItem  = regexp.MustCompile(`^    - `)
	unsafeInName = regexp.MustCompile(`[:#{}\[\],&*?|>'"%@` + "`" + `]|^\s|\s$`)
)

// ApplyFamilies writes the proposed variant lists into the index text.
//
// The edit is textual on purpose. Re-serialising the index through a YAML
// marshaller would reflow 40,000 lines, drop the anchors and merge keys the
// gallery relies on, and produce a diff no reviewer could read, which would
// make the pull request worthless even when the proposals inside it are right.
func ApplyFamilies(ix *Index, families []Family) ([]string, error) {
	byName, _ := ix.ByName()

	type edit struct {
		at      int
		remove  int
		insert  []string
		ordinal int
	}
	var edits []edit

	for _, f := range families {
		entry, ok := byName[strings.ToLower(f.Parent)]
		if !ok {
			return nil, fmt.Errorf("parent %q is not in the index", f.Parent)
		}
		items := make([]string, 0, len(f.Proposals))
		for _, p := range f.Proposals {
			items = append(items, "    - model: "+quoteName(p.Variant))
		}

		at, remove, err := insertionPoint(ix, entry)
		if err != nil {
			return nil, err
		}
		insert := items
		if remove > 0 || !hasVariantsKey(ix, entry) {
			insert = append([]string{"  variants:"}, items...)
		}
		edits = append(edits, edit{at: at, remove: remove, insert: insert, ordinal: entry.Index})
	}

	// Applying from the bottom up keeps every line number computed against the
	// original text valid while earlier edits are still pending.
	sort.Slice(edits, func(i, j int) bool { return edits[i].at > edits[j].at })

	lines := append([]string(nil), ix.Lines...)
	for _, e := range edits {
		tail := append([]string(nil), lines[e.at+e.remove:]...)
		lines = append(lines[:e.at], append(append([]string(nil), e.insert...), tail...)...)
	}
	return lines, nil
}

func hasVariantsKey(ix *Index, e *GalleryEntry) bool {
	for i := e.StartLine; i < e.EndLine; i++ {
		if keyVariants.MatchString(ix.Lines[i]) {
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
func insertionPoint(ix *Index, e *GalleryEntry) (at int, remove int, err error) {
	for i := e.StartLine; i < e.EndLine; i++ {
		m := keyVariants.FindStringSubmatch(ix.Lines[i])
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
		for j := i + 1; j < e.EndLine && variantItem.MatchString(ix.Lines[j]); j++ {
			last = j
		}
		return last + 1, 0, nil
	}

	if inlineName.MatchString(ix.Lines[e.StartLine]) {
		return e.StartLine + 1, 0, nil
	}
	for i := e.StartLine; i < e.EndLine; i++ {
		if keyName.MatchString(ix.Lines[i]) {
			return i + 1, 0, nil
		}
	}
	return 0, 0, fmt.Errorf("entry %q has no name line to anchor the insertion to", e.Name)
}

// quoteName quotes a variant reference when the name would otherwise change
// meaning as bare YAML. Config-suffixed names carry a ":" and always need it.
func quoteName(name string) string {
	if unsafeInName.MatchString(name) {
		return `"` + strings.ReplaceAll(name, `"`, `\"`) + `"`
	}
	return name
}
