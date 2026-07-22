package main

import (
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/.github/ci/galleryedit"
)

// ApplyFamilies writes the proposed variant lists into the index text.
//
// The line editing itself lives in galleryedit, shared with the apexentries
// generator. Both jobs add variants to entries the gallery already ships, and a
// second answer to "where does a variants block go" would drift from this one;
// see that package for why the edit is textual rather than a YAML round trip.
func ApplyFamilies(ix *Index, families []Family) ([]string, error) {
	byName, _ := ix.ByName()

	var inserts []galleryedit.Insert
	for _, f := range families {
		entry, ok := byName[strings.ToLower(f.Parent)]
		if !ok {
			return nil, fmt.Errorf("parent %q is not in the index", f.Parent)
		}

		variants := make([]string, 0, len(f.Proposals))
		for _, p := range f.Proposals {
			variants = append(variants, p.Variant)
		}

		inserts = append(inserts, galleryedit.Insert{
			Entry: galleryedit.Entry{
				Name:      entry.Name,
				StartLine: entry.StartLine,
				EndLine:   entry.EndLine,
			},
			Variants: variants,
		})
	}

	return galleryedit.Apply(ix.Lines, inserts)
}
