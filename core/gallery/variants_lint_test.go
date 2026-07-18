package gallery_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/gallery"
)

// variantViolation is one invariant breach found by a lint helper. Helpers
// return every breach they find instead of stopping at the first, so a single
// run names them all rather than forcing a fix-one-rerun-repeat cycle.
type variantViolation struct {
	Entry   string
	Variant string
	Detail  string
}

func (v variantViolation) String() string {
	if v.Variant == "" {
		return fmt.Sprintf("%s: %s", v.Entry, v.Detail)
	}
	return fmt.Sprintf("%s -> variant %q: %s", v.Entry, v.Variant, v.Detail)
}

func formatViolations(violations []variantViolation) string {
	lines := make([]string, 0, len(violations))
	for _, v := range violations {
		lines = append(lines, "  "+v.String())
	}
	return "\n" + strings.Join(lines, "\n")
}

func indexEntriesByName(entries []gallery.GalleryModel) map[string]gallery.GalleryModel {
	byName := make(map[string]gallery.GalleryModel, len(entries))
	for _, e := range entries {
		byName[e.Name] = e
	}
	return byName
}

// checkVariantReferences verifies every variant names an existing entry that
// declares no variants of its own. Selection is a single pass, so a nested
// reference would silently ignore the inner list.
//
// This is the one structural rule left. The rules about authored order, the
// hardware vocabulary and floor relationships stopped describing real hazards
// once selection became a filter plus a ranking: an author writes names, and
// there is no longer anything about hardware they can get wrong.
func checkVariantReferences(entries []gallery.GalleryModel) []variantViolation {
	byName := indexEntriesByName(entries)
	var violations []variantViolation
	for _, e := range entries {
		if !e.HasVariants() {
			continue
		}
		for _, v := range e.Variants {
			target, ok := byName[v.Model]
			if !ok {
				violations = append(violations, variantViolation{Entry: e.Name, Variant: v.Model, Detail: "references unknown model"})
				continue
			}
			if target.HasVariants() {
				violations = append(violations, variantViolation{Entry: e.Name, Variant: v.Model, Detail: "references an entry that declares variants of its own; nesting is not allowed"})
			}
		}
	}
	return violations
}

// checkVariantMemory verifies every declared memory figure parses, on the
// variants and on the entry itself.
//
// Absence is fine and deliberately not flagged: an unknown requirement is a
// legitimate state that selection handles by ranking the variant last. An
// UNPARSEABLE one is different, because it makes selection fail outright for
// the whole entry rather than degrade.
func checkVariantMemory(entries []gallery.GalleryModel) []variantViolation {
	var violations []variantViolation
	for _, e := range entries {
		if !e.HasVariants() {
			continue
		}
		base := gallery.Variant{Model: e.Name, MinMemory: e.MinMemory}
		if _, _, err := base.EffectiveMinMemory(); err != nil {
			violations = append(violations, variantViolation{Entry: e.Name, Detail: "has a bad min_memory: " + err.Error()})
		}
		for _, v := range e.Variants {
			if _, _, err := v.EffectiveMinMemory(); err != nil {
				violations = append(violations, variantViolation{Entry: e.Name, Variant: v.Model, Detail: "has a bad min_memory: " + err.Error()})
			}
		}
	}
	return violations
}

// loadGalleryIndex parses gallery/index.yaml once for the whole suite. The
// index carries well over a thousand entries, so re-parsing it per spec is
// pure overhead.
var loadGalleryIndex = sync.OnceValues(func() ([]gallery.GalleryModel, error) {
	data, err := os.ReadFile(filepath.Join("..", "..", "gallery", "index.yaml"))
	if err != nil {
		return nil, err
	}
	var entries []gallery.GalleryModel
	if err := yaml.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	return entries, nil
})

func plainEntry(name, url string) gallery.GalleryModel {
	e := gallery.GalleryModel{}
	e.Name = name
	e.URL = url
	return e
}

func entryWithVariants(name, url, minMemory string, variants ...gallery.Variant) gallery.GalleryModel {
	e := gallery.GalleryModel{Variants: variants, MinMemory: minMemory}
	e.Name = name
	e.URL = url
	return e
}

// variantFixture builds an entry with variants plus the entries it references.
// Synthetic fixtures keep the invariant logic covered however few entries in
// gallery/index.yaml declare variants; without them every index-driven spec
// below is a no-op that passes while checking nothing.
func variantFixture(base gallery.GalleryModel, referenced ...gallery.GalleryModel) []gallery.GalleryModel {
	return append([]gallery.GalleryModel{base}, referenced...)
}

var _ = Describe("gallery variant lint helpers", func() {
	// An entry declares variants solely by carrying the key. GalleryBackend.IsMeta()
	// has deliberately different semantics (it requires an EMPTY uri), so a
	// well-meaning alignment of the two would make every helper skip every
	// entry and pass silently. Assert the distinction directly.
	It("treats an entry with variants as such even though it has a url", func() {
		Expect(entryWithVariants("base", "u://base", "2GiB", gallery.Variant{Model: "big"}).HasVariants()).To(BeTrue())
		Expect(plainEntry("big", "u://big").HasVariants()).To(BeFalse())
	})

	It("passes every invariant on a valid entry", func() {
		entries := variantFixture(
			entryWithVariants("base", "u://base", "4GiB",
				gallery.Variant{Model: "big", MinMemory: "24GiB"},
				gallery.Variant{Model: "mid", MinMemory: "12GiB"},
				gallery.Variant{Model: "metal-big", MinMemory: "32GiB"},
				gallery.Variant{Model: "small"},
			),
			plainEntry("big", "u://big"),
			plainEntry("mid", "u://mid"),
			plainEntry("metal-big", "u://metal-big"),
			plainEntry("small", "u://small"),
		)

		Expect(checkVariantReferences(entries)).To(BeEmpty())
		Expect(checkVariantMemory(entries)).To(BeEmpty())
	})

	It("passes every invariant on an entry that declares no memory at all", func() {
		// Authoring is meant to be nothing but a list of names, so an entry and
		// its variants carrying no figures must be entirely valid.
		entries := variantFixture(
			entryWithVariants("base", "u://base", "", gallery.Variant{Model: "big"}),
			plainEntry("big", "u://big"),
		)

		Expect(checkVariantReferences(entries)).To(BeEmpty())
		Expect(checkVariantMemory(entries)).To(BeEmpty())
	})

	Describe("checkVariantReferences", func() {
		It("flags a variant naming an entry that does not exist", func() {
			entries := variantFixture(
				entryWithVariants("base", "u://base", "2GiB", gallery.Variant{Model: "ghost"}),
				plainEntry("a", "u://a"),
			)

			violations := checkVariantReferences(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Variant).To(Equal("ghost"))
			Expect(violations[0].Detail).To(ContainSubstring("unknown model"))
		})

		It("flags a variant naming an entry that declares variants itself", func() {
			entries := variantFixture(
				entryWithVariants("base", "u://base", "2GiB", gallery.Variant{Model: "nested"}),
				entryWithVariants("nested", "u://nested", "4GiB", gallery.Variant{Model: "a"}),
				plainEntry("a", "u://a"),
			)

			violations := checkVariantReferences(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Variant).To(Equal("nested"))
			Expect(violations[0].Detail).To(ContainSubstring("nesting is not allowed"))
		})

		It("reports every breach in one pass rather than stopping at the first", func() {
			entries := variantFixture(
				entryWithVariants("base", "u://base", "2GiB",
					gallery.Variant{Model: "ghost"},
					gallery.Variant{Model: "phantom"},
				),
			)

			Expect(checkVariantReferences(entries)).To(HaveLen(2))
		})
	})

	Describe("checkVariantMemory", func() {
		It("flags a variant whose memory figure cannot be parsed", func() {
			entries := variantFixture(
				entryWithVariants("base", "u://base", "2GiB", gallery.Variant{Model: "a", MinMemory: "lots"}),
				plainEntry("a", "u://a"),
			)

			violations := checkVariantMemory(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Variant).To(Equal("a"))
			Expect(violations[0].Detail).To(ContainSubstring("bad min_memory"))
		})

		It("flags an entry whose own memory figure cannot be parsed", func() {
			entries := variantFixture(
				entryWithVariants("base", "u://base", "eight gigs", gallery.Variant{Model: "a"}),
				plainEntry("a", "u://a"),
			)

			violations := checkVariantMemory(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Entry).To(Equal("base"))
			Expect(violations[0].Variant).To(BeEmpty())
			Expect(violations[0].Detail).To(ContainSubstring("bad min_memory"))
		})

		It("flags an unparseable denormalized figure just as an authored one", func() {
			// The nightly job writes inferred_min_memory, so a bug there breaks
			// selection exactly as a bad hand-authored value does.
			entries := variantFixture(
				entryWithVariants("base", "u://base", "", gallery.Variant{Model: "a", InferredMinMemory: "8 gigabytes"}),
				plainEntry("a", "u://a"),
			)

			violations := checkVariantMemory(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Variant).To(Equal("a"))
		})
	})
})

var _ = Describe("gallery/index.yaml variant invariants", Ordered, func() {
	var entries []gallery.GalleryModel

	BeforeAll(func() {
		var err error
		entries, err = loadGalleryIndex()
		Expect(err).ToNot(HaveOccurred())
		// A truncated or emptied index unmarshals cleanly and would make every
		// spec below vacuously pass.
		Expect(entries).ToNot(BeEmpty())
	})

	It("references only existing entries that declare no variants themselves", func() {
		v := checkVariantReferences(entries)
		Expect(v).To(BeEmpty(), formatViolations(v))
	})

	It("declares only parseable memory figures", func() {
		v := checkVariantMemory(entries)
		Expect(v).To(BeEmpty(), formatViolations(v))
	})
})
