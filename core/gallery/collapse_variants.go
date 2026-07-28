package gallery

import (
	"os"
	"strings"
)

// VariantReferencedIDs reports the IDs of entries that another entry already
// offers as one of its variants, and which therefore need no row of their own
// in a deduplicated listing: they are reachable by installing the entry that
// references them.
//
// The returned keys are GalleryModel.ID() values, so an entry is identified by
// gallery and name rather than by name alone. Two galleries may legitimately
// ship an entry of the same name, and only the one actually referenced should
// disappear.
//
// This is the key set of VariantParents; see there for the resolution rules.
func VariantReferencedIDs(models []*GalleryModel) map[string]struct{} {
	parents := VariantParents(models)
	referenced := make(map[string]struct{}, len(parents))
	for id := range parents {
		referenced[id] = struct{}{}
	}
	return referenced
}

// VariantParents maps the ID of every entry another entry already offers as one
// of its variants to the entry that offers it. It is what a collapsed listing
// needs in order to answer a match on a hidden build with the row the user can
// actually act on: the parent.
//
// An entry that declares variants of its own is never reported, even when
// something else references it. That guard is what keeps a chain from hiding a
// row the user cannot otherwise reach: parents are always visible, so every
// hidden entry is guaranteed to have a visible entry that offers it, and no
// parent this returns is itself a key. Such a reference is an authoring error
// anyway, and variant resolution already refuses to install it, but the listing
// must stay coherent in the presence of a gallery that has one rather than
// silently swallowing entries.
//
// When two entries reference the same build, the first in gallery order wins,
// so the listing is deterministic for a gallery the linter would reject.
//
// This is a pure pass over metadata already in memory. It resolves nothing over
// the network and probes no weight files: whether an entry is referenced is
// answerable from the declared names alone.
func VariantParents(models []*GalleryModel) map[string]*GalleryModel {
	// FindGalleryElement resolves a reference by scanning every entry, which
	// would make this quadratic over the whole gallery. The index below is the
	// same lookup precomputed once, so the matching rules have to agree with
	// it: case-insensitive, path separators folded to "__", and a reference
	// carrying an "@" addressed against "gallery@name" instead of the bare name.
	byName := make(map[string]*GalleryModel, len(models))
	byQualified := make(map[string]*GalleryModel, len(models))
	for _, m := range models {
		if m == nil {
			continue
		}
		// First match wins, mirroring FindGalleryElement's scan order, so a
		// name colliding across galleries resolves the same way here as it
		// does at install time.
		name := variantLookupKey(m.Name)
		if _, seen := byName[name]; !seen {
			byName[name] = m
		}
		qualified := variantLookupKey(m.ID())
		if _, seen := byQualified[qualified]; !seen {
			byQualified[qualified] = m
		}
	}

	referenced := map[string]*GalleryModel{}
	for _, m := range models {
		if m == nil {
			continue
		}
		for _, v := range m.Variants {
			key := variantLookupKey(v.Model)
			target, ok := byQualified[key]
			if !strings.Contains(key, "@") {
				target, ok = byName[key]
			}
			// A dangling reference names nothing to hide. Reporting it is the
			// linter's job, not the listing's.
			if !ok || target == nil {
				continue
			}
			if target.HasVariants() {
				continue
			}
			// An entry naming itself would otherwise erase itself from the
			// gallery entirely.
			if target.ID() == m.ID() {
				continue
			}
			if _, claimed := referenced[target.ID()]; claimed {
				continue
			}
			referenced[target.ID()] = m
		}
	}

	return referenced
}

func variantLookupKey(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, string(os.PathSeparator), "__"))
}
