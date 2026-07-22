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

// checkEntriesInstallSomething verifies every entry would actually put
// something on disk.
//
// It replaces an earlier rule that demanded a url: or a config_file: from every
// variant target. That demand no longer holds: applyModel now treats an entry
// declaring neither as an empty base config, which is precisely what the many
// entries pointing at gallery/virtual.yaml were already getting, minus the
// fetch. Requiring one of the two fields would now reject perfectly good
// authoring.
//
// What survives is the weaker invariant the relaxation left exposed. An entry
// with no base config, no overrides: and no files: names nothing to download
// and nothing to configure, so installing it yields an empty model directory.
// That is an authoring mistake, and it is worth catching in the catalog rather
// than on someone's machine.
//
// The rule covers every entry, not only variant targets, because the hazard has
// nothing to do with variants: it is a half-written stanza, and a parent entry
// can be one just as easily as a target.
// entryInstallsSomething restates applyModel's acceptance rule in the terms an
// author reads: a base config, or a payload to lay over an empty one.
func entryInstallsSomething(e gallery.GalleryModel) bool {
	return len(e.URL) > 0 || len(e.ConfigFile) > 0 || len(e.Overrides) > 0 || len(e.AdditionalFiles) > 0
}

func checkEntriesInstallSomething(entries []gallery.GalleryModel) []variantViolation {
	var violations []variantViolation
	for _, e := range entries {
		if entryInstallsSomething(e) {
			continue
		}
		violations = append(violations, variantViolation{
			Entry: e.Name,
			Detail: fmt.Sprintf("entry %q installs nothing: it declares no url:, no config_file:, no overrides: and no files:, "+
				"so installing it would leave an empty model directory. Give it the payload it is missing. "+
				"Note that urls: (plural) is the informational link list and is not a payload", e.Name),
		})
	}
	return violations
}

// checkNoDuplicateEntryNames verifies no two entries share a name.
//
// FindGalleryElement resolves a reference by scanning and returning the first
// match, so a second entry carrying an already-used name is unreachable: it can
// never be installed, never be selected as a variant target, and never be
// corrected, because every edit to it goes to a copy nobody reads. A reference
// to such a name is also ambiguous to a reader and to any tooling that reasons
// over the catalog, which is the harm even when the two copies happen to agree
// today.
func checkNoDuplicateEntryNames(entries []gallery.GalleryModel) []variantViolation {
	seen := make(map[string]struct{}, len(entries))
	var violations []variantViolation
	for _, e := range entries {
		if _, dup := seen[e.Name]; dup {
			violations = append(violations, variantViolation{
				Entry: e.Name,
				Detail: fmt.Sprintf("entry %q is declared more than once: FindGalleryElement returns the first match, "+
					"so only the first declaration is reachable and this one is dead weight. "+
					"Remove the extra copy, or rename it if the two are meant to be different models", e.Name),
			})
			continue
		}
		seen[e.Name] = struct{}{}
	}
	return violations
}

// checkSingleVariantClaim verifies no entry is offered as a variant by more
// than one parent.
//
// VariantParents hides a claimed build from a collapsed listing and answers
// matches on it with the parent instead. With two parents claiming the same
// build it picks the first in gallery order, which is deterministic but
// arbitrary: the user is shown one of two equally valid rows, and which one
// depends on authoring order rather than on anything about the models. That
// resolution exists so the listing stays coherent for a catalog carrying the
// mistake; this is the rule that keeps the catalog from carrying it.
func checkSingleVariantClaim(entries []gallery.GalleryModel) []variantViolation {
	byName := indexEntriesByName(entries)
	// Only the first claim per parent counts, so a parent listing the same
	// target twice is a redundancy inside one stanza and not two parents
	// fighting over a build.
	claimedBy := make(map[string]string, len(entries))
	var violations []variantViolation
	for _, e := range entries {
		for _, v := range e.Variants {
			// A dangling reference and a nested one are checkVariantReferences'
			// business, and neither claims anything VariantParents would hide.
			target, ok := byName[v.Model]
			if !ok || target.HasVariants() || v.Model == e.Name {
				continue
			}
			first, claimed := claimedBy[v.Model]
			if !claimed {
				claimedBy[v.Model] = e.Name
				continue
			}
			if first == e.Name {
				continue
			}
			violations = append(violations, variantViolation{
				Entry:   e.Name,
				Variant: v.Model,
				Detail: fmt.Sprintf("already offered as a variant by %q; a build may have only one parent, "+
					"otherwise which row a collapsed listing shows for it depends on authoring order", first),
			})
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
		Expect(checkEntriesInstallSomething(entries)).To(BeEmpty())
		Expect(checkNoDuplicateEntryNames(entries)).To(BeEmpty())
		Expect(checkSingleVariantClaim(entries)).To(BeEmpty())
	})

	Describe("checkNoDuplicateEntryNames", func() {
		It("flags a second entry carrying an already-used name", func() {
			entries := []gallery.GalleryModel{
				plainEntry("twin", "u://first"),
				plainEntry("other", "u://other"),
				plainEntry("twin", "u://second"),
			}

			violations := checkNoDuplicateEntryNames(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Entry).To(Equal("twin"))
			Expect(violations[0].Detail).To(ContainSubstring("declared more than once"))
			Expect(violations[0].Detail).To(ContainSubstring("only the first declaration is reachable"))
		})

		// The copies in gallery/index.yaml were identical, so a rule that only
		// fired on differing copies would have let every one of them through.
		It("flags a duplicate whose fields match the first copy exactly", func() {
			entries := []gallery.GalleryModel{
				plainEntry("twin", "u://same"),
				plainEntry("twin", "u://same"),
			}

			Expect(checkNoDuplicateEntryNames(entries)).To(HaveLen(1))
		})

		It("reports every breach in one pass rather than stopping at the first", func() {
			entries := []gallery.GalleryModel{
				plainEntry("a", "u://a"),
				plainEntry("b", "u://b"),
				plainEntry("a", "u://a2"),
				plainEntry("b", "u://b2"),
			}

			Expect(checkNoDuplicateEntryNames(entries)).To(HaveLen(2))
		})

		// A name used three times costs two removals, so all the copies past
		// the first have to be named rather than only the second.
		It("flags every copy past the first", func() {
			entries := []gallery.GalleryModel{
				plainEntry("a", "u://1"),
				plainEntry("a", "u://2"),
				plainEntry("a", "u://3"),
			}

			Expect(checkNoDuplicateEntryNames(entries)).To(HaveLen(2))
		})
	})

	Describe("checkSingleVariantClaim", func() {
		It("flags a build offered as a variant by two parents", func() {
			entries := variantFixture(
				entryWithVariants("base-a", "u://a", gallery.Variant{Model: "shared"}),
				entryWithVariants("base-b", "u://b", gallery.Variant{Model: "shared"}),
				plainEntry("shared", "u://shared"),
			)

			violations := checkSingleVariantClaim(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Entry).To(Equal("base-b"))
			Expect(violations[0].Variant).To(Equal("shared"))
			Expect(violations[0].Detail).To(ContainSubstring(`already offered as a variant by "base-a"`))
		})

		It("accepts two parents offering different builds", func() {
			entries := variantFixture(
				entryWithVariants("base-a", "u://a", gallery.Variant{Model: "one"}),
				entryWithVariants("base-b", "u://b", gallery.Variant{Model: "two"}),
				plainEntry("one", "u://one"),
				plainEntry("two", "u://two"),
			)

			Expect(checkSingleVariantClaim(entries)).To(BeEmpty())
		})

		// VariantParents takes the first claim per parent, so a repeat inside
		// one stanza hides nothing extra and is not this rule's business.
		It("does not treat one parent listing a build twice as two claims", func() {
			entries := variantFixture(
				entryWithVariants("base", "u://base",
					gallery.Variant{Model: "dup"},
					gallery.Variant{Model: "dup"},
				),
				plainEntry("dup", "u://dup"),
			)

			Expect(checkSingleVariantClaim(entries)).To(BeEmpty())
		})

		// VariantParents skips these too, so flagging them here would report
		// the same authoring mistake twice under two different names.
		It("leaves dangling and nested references to checkVariantReferences", func() {
			entries := variantFixture(
				entryWithVariants("base-a", "u://a",
					gallery.Variant{Model: "ghost"},
					gallery.Variant{Model: "nested"},
				),
				entryWithVariants("base-b", "u://b",
					gallery.Variant{Model: "ghost"},
					gallery.Variant{Model: "nested"},
				),
				entryWithVariants("nested", "u://nested", gallery.Variant{Model: "leaf"}),
				plainEntry("leaf", "u://leaf"),
			)

			Expect(checkSingleVariantClaim(entries)).To(BeEmpty())
		})
	})

	Describe("checkEntriesInstallSomething", func() {
		// The shape the rule exists for: a stanza that got as far as a name and
		// stopped.
		empty := func(name string) gallery.GalleryModel {
			e := gallery.GalleryModel{}
			e.Name = name
			// urls: is the informational HuggingFace link list. It reads like a
			// payload and is not one, so a stub carrying only this must still be
			// flagged.
			e.URLs = []string{"https://huggingface.co/example/" + name}
			return e
		}

		It("flags an entry with no url, no config_file, no overrides and no files", func() {
			entries := variantFixture(
				entryWithVariants("base", "u://base", gallery.Variant{Model: "dead"}),
				empty("dead"),
			)

			violations := checkEntriesInstallSomething(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Entry).To(Equal("dead"))
			Expect(violations[0].Detail).To(ContainSubstring("installs nothing"))
		})

		It("accepts an entry carrying only overrides, which applyModel installs on an empty base", func() {
			target := gallery.GalleryModel{Overrides: map[string]any{"backend": "ds4"}}
			target.Name = "overrides-only"
			entries := variantFixture(
				entryWithVariants("base", "u://base", gallery.Variant{Model: "overrides-only"}),
				target,
			)

			Expect(checkEntriesInstallSomething(entries)).To(BeEmpty())
		})

		It("accepts an entry carrying only files", func() {
			target := gallery.GalleryModel{}
			target.Name = "files-only"
			target.AdditionalFiles = []gallery.File{{Filename: "weights.gguf", URI: "u://weights"}}
			entries := variantFixture(
				entryWithVariants("base", "u://base", gallery.Variant{Model: "files-only"}),
				target,
			)

			Expect(checkEntriesInstallSomething(entries)).To(BeEmpty())
		})

		It("accepts an entry described by an inline config_file rather than a url", func() {
			target := gallery.GalleryModel{ConfigFile: map[string]any{"backend": "llama-cpp"}}
			target.Name = "inline"
			entries := variantFixture(
				entryWithVariants("base", "u://base", gallery.Variant{Model: "inline"}),
				target,
			)

			Expect(checkEntriesInstallSomething(entries)).To(BeEmpty())
		})

		It("reports every breach in one pass rather than stopping at the first", func() {
			entries := variantFixture(
				entryWithVariants("base", "u://base",
					gallery.Variant{Model: "dead-a"},
					gallery.Variant{Model: "dead-b"},
				),
				empty("dead-a"),
				empty("dead-b"),
			)

			Expect(checkEntriesInstallSomething(entries)).To(HaveLen(2))
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

	// Gallery-wide, not variants-only. The rule this replaced was scoped to
	// variant targets because the index carried nine unrelated entries it would
	// have failed on. Those nine declared overrides and files but no url, and
	// they install cleanly on an empty base now, so there is nothing left to
	// exempt and the gate covers the whole catalog in one step.
	It("contains no entry that would install nothing", func() {
		v := checkEntriesInstallSomething(entries)
		Expect(v).To(BeEmpty(), formatViolations(v))
	})

	It("declares every entry name exactly once", func() {
		v := checkNoDuplicateEntryNames(entries)
		Expect(v).To(BeEmpty(), formatViolations(v))
	})

	It("gives every variant build a single parent", func() {
		v := checkSingleVariantClaim(entries)
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
		Expect(entryInstallsSomething(*resolved)).To(BeTrue(),
			"resolved entry %q (variant %q) carries no payload, so InstallModelFromGallery would refuse it",
			resolved.Name, selected.Model)
	})

	// Pinning reaches each target directly, which is what actually proves all
	// four are installable: every one of them is resolved, not just whichever
	// the ranking happens to prefer.
	//
	// None of the four declares a url: any more. They describe themselves
	// entirely through overrides: and files:, which applyModel lays over an
	// empty base, so this also pins that dropping the urls left them
	// installable.
	It("yields an installable entry for every declared variant pin", func() {
		env := gallery.ResolveEnv{
			AvailableMemory:   512 << 30,
			BackendCompatible: func(string) bool { return true },
		}

		for _, v := range entry.Variants {
			resolved, _, err := gallery.ResolveVariant(models, entry, env, v.Model)
			Expect(err).ToNot(HaveOccurred(), "pinning %q", v.Model)
			Expect(resolved.URL).To(BeEmpty(), "variant %q should reach the empty-base path, not a fetch", v.Model)
			Expect(entryInstallsSomething(*resolved)).To(BeTrue(), "variant %q carries no payload", v.Model)
		}
	})
})
