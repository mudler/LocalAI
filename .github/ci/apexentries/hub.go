package main

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/.github/ci/galleryedit"
)

// IndexText is the gallery index seen as text: the entries it declares plus the
// exact lines each one occupies, which is what splicing a variants block into an
// entry the gallery already ships requires.
//
// It is a second, narrower read of the same file LoadExisting parses. The two
// answer different questions: LoadExisting answers "do these weights already
// exist anywhere", this one answers "where in the file does this entry live".
type IndexText struct {
	Lines   []string
	Entries []*indexEntry

	byName map[string]*indexEntry
}

// indexEntry is one entry of the index: its name, the variants it already
// declares, and its coordinates in the file.
type indexEntry struct {
	Name     string       `yaml:"name"`
	Variants []VariantRef `yaml:"variants"`

	Pos galleryedit.Entry `yaml:"-"`
}

// LoadIndexText reads the gallery index for editing.
func LoadIndexText(path string) (*IndexText, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseIndexText(string(raw))
}

// ParseIndexText pairs the decoded entries with the top level list items the
// text actually contains.
//
// If the two views disagree on how many entries there are then every line number
// a splice would compute is suspect, and the failure mode is writing a variants
// block into the wrong model. The parse refuses instead.
func ParseIndexText(text string) (*IndexText, error) {
	var entries []*indexEntry
	if err := yaml.Unmarshal([]byte(text), &entries); err != nil {
		return nil, fmt.Errorf("decoding gallery index: %w", err)
	}

	lines, starts := galleryedit.Scan(text)
	if len(starts) != len(entries) {
		return nil, fmt.Errorf("gallery index has %d decoded entries but %d top level list items; refusing to edit by line number",
			len(entries), len(starts))
	}

	ix := &IndexText{Lines: lines, Entries: entries, byName: map[string]*indexEntry{}}
	for i, e := range entries {
		if e == nil {
			return nil, fmt.Errorf("gallery index list item %d is empty; refusing to edit by line number", i)
		}
		end := len(lines)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		e.Pos = galleryedit.Entry{Name: e.Name, StartLine: starts[i], EndLine: end}

		// First occurrence wins, matching the gallery's own resolution.
		key := strings.ToLower(e.Name)
		if _, seen := ix.byName[key]; !seen {
			ix.byName[key] = e
		}
	}
	return ix, nil
}

// Find looks an entry up by name, case insensitively.
func (ix *IndexText) Find(name string) *indexEntry {
	return ix.byName[strings.ToLower(name)]
}

// ResolveHub returns the gallery name of a family's hub and whether the gallery
// already ships an entry under it.
//
// The hub is the BASE model entry, never a generated *-apex parent. Somebody
// looking for qwen3.6-35b-a3b has to find every build of those weights under
// that one name: the APEX imatrix rungs, the unsloth quant rungs and any
// speculative build. A separate qwen3.6-35b-a3b-apex hub competing with the base
// entry would split the family in two and leave whichever half the user did not
// search for invisible.
//
// Both candidates are tried for the same reason CounterpartCandidates tries
// both. The repo name and the published file stem disagree for several of these
// repos, and either one may be what the base entry was named after.
func ResolveHub(ix *IndexText, repoBase, stem string) (name string, exists bool) {
	candidates := CounterpartCandidates(repoBase, stem)
	for _, c := range candidates {
		if n := slug(c); ix.Find(n) != nil {
			return n, true
		}
	}
	// Nothing matched, so the family needs a hub of its own under the repo
	// derived name, which is the more reliable of the two.
	return slug(candidates[0]), false
}

// HubLabel is the human-cased base model name, for prose rather than lookup.
func HubLabel(repoBase, stem string) string {
	return CounterpartCandidates(repoBase, stem)[0]
}

// filterVariants drops the references a hub must not carry: itself, and anything
// it already lists.
//
// The self reference is not merely redundant. A hub that names itself makes the
// verifier resolve the reference back to the hub, see that the hub declares
// variants, and report a variant that declares variants of its own. It arises
// for real rather than in theory: an unsloth rung whose weights the gallery
// already ships under the base model name resolves, through Merge, straight back
// to the hub that is about to reference it.
func filterVariants(hub string, already []VariantRef, want []string) []string {
	seen := map[string]bool{strings.ToLower(hub): true}
	for _, v := range already {
		seen[strings.ToLower(v.Model)] = true
	}

	var out []string
	for _, w := range want {
		key := strings.ToLower(w)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, w)
	}
	return out
}
