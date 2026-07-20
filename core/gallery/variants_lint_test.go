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

// checkVariantTargetsInstallable verifies every entry referenced as a variant
// carries the payload an install needs, which is a non-empty url or a non-empty
// config_file.
//
// This mirrors the precondition InstallModelFromGallery's applyModel enforces:
// with neither field it has nothing to build a config from and fails with
// "invalid gallery model". Structural validity is not enough, because an entry
// can exist, declare no variants of its own, and still be uninstallable. That
// gap is how a grouping shipped whose every target failed on click: the parent
// resolved correctly and then routed the install into a dead entry.
//
// The message names the parent, the target and what is missing, because whoever
// hits this is reading a gallery entry and has no reason to know applyModel
// exists.
func checkVariantTargetsInstallable(entries []gallery.GalleryModel) []variantViolation {
	byName := indexEntriesByName(entries)
	var violations []variantViolation
	for _, e := range entries {
		if !e.HasVariants() {
			continue
		}
		for _, v := range e.Variants {
			target, ok := byName[v.Model]
			if !ok {
				// checkVariantReferences already reports the dangling name, and
				// reporting it twice buries two distinct rules under duplicates.
				continue
			}
			if len(target.URL) == 0 && len(target.ConfigFile) == 0 {
				violations = append(violations, variantViolation{
					Entry:   e.Name,
					Variant: v.Model,
					Detail: fmt.Sprintf("entry %q is not installable on its own: it declares neither url: nor config_file:, "+
						"so installing it fails with \"invalid gallery model\". Give it the url: its family uses "+
						"(commonly github:mudler/LocalAI/gallery/virtual.yaml@master) or an inline config_file:. "+
						"urls: (plural) is informational only and does not satisfy this", v.Model),
				})
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

func entryWithVariants(name, url string, variants ...gallery.Variant) gallery.GalleryModel {
	e := gallery.GalleryModel{Variants: variants}
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
		Expect(entryWithVariants("base", "u://base", gallery.Variant{Model: "big"}).HasVariants()).To(BeTrue())
		Expect(plainEntry("big", "u://big").HasVariants()).To(BeFalse())
	})

	It("passes every invariant on a valid entry", func() {
		// Authoring is nothing but a list of names, so a bare list of them must
		// be entirely valid.
		entries := variantFixture(
			entryWithVariants("base", "u://base",
				gallery.Variant{Model: "big"},
				gallery.Variant{Model: "mid"},
				gallery.Variant{Model: "metal-big"},
				gallery.Variant{Model: "small"},
			),
			plainEntry("big", "u://big"),
			plainEntry("mid", "u://mid"),
			plainEntry("metal-big", "u://metal-big"),
			plainEntry("small", "u://small"),
		)

		Expect(checkVariantReferences(entries)).To(BeEmpty())
		Expect(checkVariantTargetsInstallable(entries)).To(BeEmpty())
	})

	Describe("checkVariantTargetsInstallable", func() {
		// The defect this rule exists for: a target carrying everything except
		// the one field applyModel reads.
		uninstallable := func(name string) gallery.GalleryModel {
			e := gallery.GalleryModel{}
			e.Name = name
			// urls: is the informational HuggingFace link list, and the entry
			// that shipped broken had exactly this and nothing else. It must
			// not be mistaken for url:.
			e.URLs = []string{"https://huggingface.co/example/" + name}
			e.Overrides = map[string]any{"backend": "ds4"}
			return e
		}

		It("flags a target with neither url nor config_file", func() {
			entries := variantFixture(
				entryWithVariants("base", "u://base", gallery.Variant{Model: "dead"}),
				uninstallable("dead"),
			)

			violations := checkVariantTargetsInstallable(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Entry).To(Equal("base"))
			Expect(violations[0].Variant).To(Equal("dead"))
			Expect(violations[0].Detail).To(ContainSubstring("neither url: nor config_file:"))
			Expect(violations[0].Detail).To(ContainSubstring("invalid gallery model"))
		})

		It("accepts a target described by an inline config_file rather than a url", func() {
			target := gallery.GalleryModel{ConfigFile: map[string]any{"backend": "llama-cpp"}}
			target.Name = "inline"
			entries := variantFixture(
				entryWithVariants("base", "u://base", gallery.Variant{Model: "inline"}),
				target,
			)

			Expect(checkVariantTargetsInstallable(entries)).To(BeEmpty())
		})

		It("leaves an unknown target to checkVariantReferences rather than reporting it twice", func() {
			entries := variantFixture(
				entryWithVariants("base", "u://base", gallery.Variant{Model: "ghost"}),
			)

			Expect(checkVariantTargetsInstallable(entries)).To(BeEmpty())
			Expect(checkVariantReferences(entries)).To(HaveLen(1))
		})

		It("reports every breach in one pass rather than stopping at the first", func() {
			entries := variantFixture(
				entryWithVariants("base", "u://base",
					gallery.Variant{Model: "dead-a"},
					gallery.Variant{Model: "dead-b"},
				),
				uninstallable("dead-a"),
				uninstallable("dead-b"),
			)

			Expect(checkVariantTargetsInstallable(entries)).To(HaveLen(2))
		})
	})

	Describe("checkVariantReferences", func() {
		It("flags a variant naming an entry that does not exist", func() {
			entries := variantFixture(
				entryWithVariants("base", "u://base", gallery.Variant{Model: "ghost"}),
				plainEntry("a", "u://a"),
			)

			violations := checkVariantReferences(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Variant).To(Equal("ghost"))
			Expect(violations[0].Detail).To(ContainSubstring("unknown model"))
		})

		It("flags a variant naming an entry that declares variants itself", func() {
			entries := variantFixture(
				entryWithVariants("base", "u://base", gallery.Variant{Model: "nested"}),
				entryWithVariants("nested", "u://nested", gallery.Variant{Model: "a"}),
				plainEntry("a", "u://a"),
			)

			violations := checkVariantReferences(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Variant).To(Equal("nested"))
			Expect(violations[0].Detail).To(ContainSubstring("nesting is not allowed"))
		})

		It("reports every breach in one pass rather than stopping at the first", func() {
			entries := variantFixture(
				entryWithVariants("base", "u://base",
					gallery.Variant{Model: "ghost"},
					gallery.Variant{Model: "phantom"},
				),
			)

			Expect(checkVariantReferences(entries)).To(HaveLen(2))
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

	It("references only entries that are installable on their own", func() {
		v := checkVariantTargetsInstallable(entries)
		Expect(v).To(BeEmpty(), formatViolations(v))
	})
})

// The lint rules above check the catalog as text. This drives the real
// resolution path for the entry a user actually clicked and failed to install,
// so the fix is proven at the layer that broke and not only at the layer that
// should have caught it.
//
// It is a container of its own rather than another spec beside the lint rules
// because an Ordered container stops at its first failure: sharing one would
// let a lint breach skip these silently, which is precisely the kind of
// vacuously-green spec this file exists to avoid.
var _ = Describe("gallery/index.yaml deepseek-v4-flash resolution", Ordered, func() {
	var entries []gallery.GalleryModel
	var models []*gallery.GalleryModel
	var entry *gallery.GalleryModel

	BeforeAll(func() {
		var err error
		entries, err = loadGalleryIndex()
		Expect(err).ToNot(HaveOccurred())
		Expect(entries).ToNot(BeEmpty())
	})

	BeforeEach(func() {
		models = make([]*gallery.GalleryModel, 0, len(entries))
		for i := range entries {
			models = append(models, &entries[i])
		}
		entry = gallery.FindGalleryElement(models, "deepseek-v4-flash")
		Expect(entry).ToNot(BeNil())
		Expect(entry.HasVariants()).To(BeTrue())
	})

	// The unpinned pass covers the entry the user clicks. It does NOT prove the
	// variants are installable: with no probe wired every size is unknown and
	// the ranking ties, so the base wins and this only ever exercises the
	// parent's own url. The pin spec below is what covers the four targets.
	//
	// Note that a base selection reports Variant.Model as the ENTRY's name
	// rather than as empty, so asserting a non-empty Model here would look like
	// a check that a declared variant won while passing on the base every time.
	It("yields an installable entry for the entry itself", func() {
		env := gallery.ResolveEnv{
			AvailableMemory:   512 << 30,
			BackendCompatible: func(string) bool { return true },
		}

		resolved, selected, err := gallery.ResolveVariant(models, entry, env, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(len(resolved.URL) > 0 || len(resolved.ConfigFile) > 0).To(BeTrue(),
			"resolved entry %q (variant %q) has neither url nor config_file, so InstallModelFromGallery would fail with \"invalid gallery model\"",
			resolved.Name, selected.Model)
	})

	// Pinning reaches each target directly, which is what actually proves all
	// four are installable: every one of them is resolved, not just whichever
	// the ranking happens to prefer. This is the spec that fails on the
	// unfixed index.
	It("yields an installable entry for every declared variant pin", func() {
		env := gallery.ResolveEnv{
			AvailableMemory:   512 << 30,
			BackendCompatible: func(string) bool { return true },
		}

		for _, v := range entry.Variants {
			resolved, _, err := gallery.ResolveVariant(models, entry, env, v.Model)
			Expect(err).ToNot(HaveOccurred(), "pinning %q", v.Model)
			Expect(len(resolved.URL) > 0 || len(resolved.ConfigFile) > 0).To(BeTrue(),
				"variant %q has neither url nor config_file", v.Model)
		}
	})
})
