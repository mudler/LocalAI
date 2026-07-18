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

// knownCapabilities mirrors the values SystemState.DetectedCapability() can
// actually report, which is what the candidate resolver compares against with
// a case-sensitive exact match. A capability outside this set can never match,
// so a typo would silently make a candidate unreachable on every host.
//
// Note "cpu" is deliberately absent: it exists only as a fallback key inside
// SystemState.Capability(capMap) for meta backends, and is never a value
// getSystemCapabilities() returns. A CPU-only host reports "default".
var knownCapabilities = map[string]bool{
	"default": true, "metal": true, "darwin-x86": true,
	"nvidia": true, "nvidia-cuda-12": true, "nvidia-cuda-13": true,
	"nvidia-l4t": true, "nvidia-l4t-cuda-12": true, "nvidia-l4t-cuda-13": true,
	"intel": true, "amd": true, "vulkan": true,
}

// metaViolation is one invariant breach found by a lint helper. Helpers return
// every breach they find instead of stopping at the first, so a single run
// names them all rather than forcing a fix-one-rerun-repeat cycle.
type metaViolation struct {
	Entry     string
	Candidate string
	Detail    string
}

func (v metaViolation) String() string {
	if v.Candidate == "" {
		return fmt.Sprintf("%s: %s", v.Entry, v.Detail)
	}
	return fmt.Sprintf("%s -> candidate %q: %s", v.Entry, v.Candidate, v.Detail)
}

func formatViolations(violations []metaViolation) string {
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

// checkMetaFallbackURL verifies that released LocalAI versions, which ignore
// the candidates key entirely, still install something sensible: the meta
// entry needs a url, and it must be the final candidate's url so old and new
// clients agree on the least demanding option.
func checkMetaFallbackURL(entries []gallery.GalleryModel) []metaViolation {
	byName := indexEntriesByName(entries)
	var violations []metaViolation
	for _, e := range entries {
		if !e.IsMeta() {
			continue
		}
		if e.URL == "" {
			violations = append(violations, metaViolation{Entry: e.Name, Detail: "needs a url fallback for older clients"})
		}
		if len(e.ConfigFile) > 0 {
			violations = append(violations, metaViolation{Entry: e.Name, Detail: "must not carry an inline config_file"})
		}
		if len(e.AdditionalFiles) > 0 {
			violations = append(violations, metaViolation{Entry: e.Name, Detail: "must not carry files"})
		}

		last := e.Candidates[len(e.Candidates)-1]
		fallback, ok := byName[last.Model]
		if !ok {
			// checkMetaReferences owns reporting the dangling name; there is
			// nothing to compare the url against here.
			continue
		}
		if e.URL != fallback.URL {
			violations = append(violations, metaViolation{
				Entry:     e.Name,
				Candidate: last.Model,
				Detail:    "meta url must equal its final candidate url so old and new clients agree",
			})
		}
	}
	return violations
}

// checkMetaReferences verifies every candidate names an existing, concrete
// entry. Resolution is a single pass, so a nested meta reference would install
// an entry that carries no files.
func checkMetaReferences(entries []gallery.GalleryModel) []metaViolation {
	byName := indexEntriesByName(entries)
	var violations []metaViolation
	for _, e := range entries {
		if !e.IsMeta() {
			continue
		}
		for _, c := range e.Candidates {
			target, ok := byName[c.Model]
			if !ok {
				violations = append(violations, metaViolation{Entry: e.Name, Candidate: c.Model, Detail: "references unknown model"})
				continue
			}
			if target.IsMeta() {
				violations = append(violations, metaViolation{Entry: e.Name, Candidate: c.Model, Detail: "references a meta entry; nesting is not allowed"})
			}
		}
	}
	return violations
}

// checkMetaConstraints verifies that only the final candidate is an
// unconstrained last resort. An earlier candidate without a floor would
// capture every host and make everything after it dead.
func checkMetaConstraints(entries []gallery.GalleryModel) []metaViolation {
	var violations []metaViolation
	for _, e := range entries {
		if !e.IsMeta() {
			continue
		}
		for i, c := range e.Candidates {
			_, declared, err := c.EffectiveMinVRAM()
			if err != nil {
				violations = append(violations, metaViolation{Entry: e.Name, Candidate: c.Model, Detail: "has a bad min_vram: " + err.Error()})
				continue
			}

			if i == len(e.Candidates)-1 {
				if declared {
					violations = append(violations, metaViolation{Entry: e.Name, Candidate: c.Model, Detail: "final candidate must be an unconstrained last resort"})
				}
				if c.Capability != "" {
					violations = append(violations, metaViolation{Entry: e.Name, Candidate: c.Model, Detail: "final candidate must not require a capability"})
				}
				continue
			}
			if !declared {
				violations = append(violations, metaViolation{Entry: e.Name, Candidate: c.Model, Detail: "needs a min_vram; the nightly job should have inferred one"})
			}
		}
	}
	return violations
}

// checkMetaCapabilities verifies candidates only name capabilities the system
// can actually report, since the resolver compares them exactly.
func checkMetaCapabilities(entries []gallery.GalleryModel) []metaViolation {
	var violations []metaViolation
	for _, e := range entries {
		if !e.IsMeta() {
			continue
		}
		for _, c := range e.Candidates {
			if c.Capability == "" {
				continue
			}
			if !knownCapabilities[c.Capability] {
				violations = append(violations, metaViolation{
					Entry:     e.Name,
					Candidate: c.Model,
					Detail:    fmt.Sprintf("uses unknown capability %q", c.Capability),
				})
			}
		}
	}
	return violations
}

// checkMetaOrdering verifies no candidate is shadowed by an earlier one.
// Selection is first-match over the authored order, so a candidate is dead
// whenever an earlier one matches every host this one would.
//
// Two shapes cause that. Within a single capability group the groups are
// mutually exclusive, so only a floor rising above an earlier floor is
// unreachable. A candidate with an EMPTY capability matches every host and so
// dominates ACROSS groups: every later candidate whose floor is at or above
// the running minimum unconditional floor is dead, whatever capability it asks
// for. Tracking that running minimum subsumes the same-group check for the
// empty capability.
func checkMetaOrdering(entries []gallery.GalleryModel) []metaViolation {
	var violations []metaViolation
	for _, e := range entries {
		if !e.IsMeta() {
			continue
		}
		previous := map[string]uint64{}
		var unconditionalFloor uint64
		haveUnconditional := false

		for _, c := range e.Candidates {
			floor, declared, err := c.EffectiveMinVRAM()
			if err != nil || !declared {
				// Parse errors and absent floors belong to checkMetaConstraints.
				continue
			}

			switch {
			case haveUnconditional && floor >= unconditionalFloor:
				violations = append(violations, metaViolation{
					Entry:     e.Name,
					Candidate: c.Model,
					Detail: fmt.Sprintf("is shadowed by an earlier candidate that matches any host at a %d byte floor, so it can never be reached",
						unconditionalFloor),
				})
			case c.Capability != "":
				if prior, seen := previous[c.Capability]; seen && floor > prior {
					violations = append(violations, metaViolation{
						Entry:     e.Name,
						Candidate: c.Model,
						Detail:    "raises the VRAM floor after a lower one in the same capability group, so it can never be reached",
					})
				}
			}

			if c.Capability == "" && (!haveUnconditional || floor < unconditionalFloor) {
				unconditionalFloor = floor
				haveUnconditional = true
			}
			previous[c.Capability] = floor
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

func concreteEntry(name, url string) gallery.GalleryModel {
	e := gallery.GalleryModel{}
	e.Name = name
	e.URL = url
	return e
}

func metaEntry(name, url string, candidates ...gallery.Candidate) gallery.GalleryModel {
	e := gallery.GalleryModel{Candidates: candidates}
	e.Name = name
	e.URL = url
	return e
}

// metaFixture builds a meta entry plus the concrete entries it references.
// Synthetic fixtures keep the invariant logic covered while gallery/index.yaml
// still holds zero meta entries; without them every index-driven spec below is
// a no-op that passes while checking nothing.
func metaFixture(meta gallery.GalleryModel, concrete ...gallery.GalleryModel) []gallery.GalleryModel {
	return append([]gallery.GalleryModel{meta}, concrete...)
}

var _ = Describe("meta entry lint helpers", func() {
	// A model entry is meta solely by carrying candidates. GalleryBackend.IsMeta()
	// has deliberately opposite semantics (it requires an EMPTY uri), so a
	// well-meaning alignment of the two would make every helper skip every
	// entry and pass silently. Assert the distinction directly.
	It("treats an entry with candidates as meta even though it has a url", func() {
		Expect(metaEntry("meta", "u://big", gallery.Candidate{Model: "big"}).IsMeta()).To(BeTrue())
		Expect(concreteEntry("big", "u://big").IsMeta()).To(BeFalse())
	})

	It("passes every invariant on a valid meta entry", func() {
		entries := metaFixture(
			metaEntry("meta", "u://small",
				gallery.Candidate{Model: "big", Capability: "nvidia", MinVRAM: "24GiB"},
				gallery.Candidate{Model: "mid", Capability: "nvidia", MinVRAM: "12GiB"},
				gallery.Candidate{Model: "metal-big", Capability: "metal", MinVRAM: "32GiB"},
				gallery.Candidate{Model: "small"},
			),
			concreteEntry("big", "u://big"),
			concreteEntry("mid", "u://mid"),
			concreteEntry("metal-big", "u://metal-big"),
			concreteEntry("small", "u://small"),
		)

		Expect(checkMetaFallbackURL(entries)).To(BeEmpty())
		Expect(checkMetaReferences(entries)).To(BeEmpty())
		Expect(checkMetaConstraints(entries)).To(BeEmpty())
		Expect(checkMetaCapabilities(entries)).To(BeEmpty())
		Expect(checkMetaOrdering(entries)).To(BeEmpty())
	})

	Describe("checkMetaOrdering", func() {
		It("flags a candidate shadowed by an earlier unconditional candidate", func() {
			// "a" matches any host at 8GiB, so the nvidia candidate at 20GiB is
			// dead: every nvidia host clearing 20GiB already cleared 8GiB.
			entries := metaFixture(
				metaEntry("meta", "u://c",
					gallery.Candidate{Model: "a", MinVRAM: "8GiB"},
					gallery.Candidate{Model: "b", Capability: "nvidia", MinVRAM: "20GiB"},
					gallery.Candidate{Model: "c"},
				),
				concreteEntry("a", "u://a"), concreteEntry("b", "u://b"), concreteEntry("c", "u://c"),
			)

			violations := checkMetaOrdering(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Candidate).To(Equal("b"))
			Expect(violations[0].Detail).To(ContainSubstring("shadowed by an earlier candidate that matches any host"))
		})

		It("flags a floor inversion inside one capability group", func() {
			entries := metaFixture(
				metaEntry("meta", "u://c",
					gallery.Candidate{Model: "a", Capability: "nvidia", MinVRAM: "8GiB"},
					gallery.Candidate{Model: "b", Capability: "nvidia", MinVRAM: "20GiB"},
					gallery.Candidate{Model: "c"},
				),
				concreteEntry("a", "u://a"), concreteEntry("b", "u://b"), concreteEntry("c", "u://c"),
			)

			violations := checkMetaOrdering(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Candidate).To(Equal("b"))
			Expect(violations[0].Detail).To(ContainSubstring("same capability group"))
		})

		It("keeps distinct capability groups independent", func() {
			// A high metal floor after a low nvidia floor is fine: no host
			// reports both capabilities.
			entries := metaFixture(
				metaEntry("meta", "u://c",
					gallery.Candidate{Model: "a", Capability: "nvidia", MinVRAM: "8GiB"},
					gallery.Candidate{Model: "b", Capability: "metal", MinVRAM: "32GiB"},
					gallery.Candidate{Model: "c"},
				),
				concreteEntry("a", "u://a"), concreteEntry("b", "u://b"), concreteEntry("c", "u://c"),
			)

			Expect(checkMetaOrdering(entries)).To(BeEmpty())
		})
	})

	Describe("checkMetaConstraints", func() {
		It("flags a non-final candidate with no floor", func() {
			entries := metaFixture(
				metaEntry("meta", "u://c",
					gallery.Candidate{Model: "a", Capability: "nvidia"},
					gallery.Candidate{Model: "c"},
				),
				concreteEntry("a", "u://a"), concreteEntry("c", "u://c"),
			)

			violations := checkMetaConstraints(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Candidate).To(Equal("a"))
			Expect(violations[0].Detail).To(ContainSubstring("needs a min_vram"))
		})

		It("flags a final candidate that declares a floor", func() {
			entries := metaFixture(
				metaEntry("meta", "u://c",
					gallery.Candidate{Model: "a", MinVRAM: "20GiB"},
					gallery.Candidate{Model: "c", MinVRAM: "8GiB"},
				),
				concreteEntry("a", "u://a"), concreteEntry("c", "u://c"),
			)

			violations := checkMetaConstraints(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Candidate).To(Equal("c"))
			Expect(violations[0].Detail).To(ContainSubstring("unconstrained last resort"))
		})

		It("flags a final candidate that requires a capability", func() {
			entries := metaFixture(
				metaEntry("meta", "u://c",
					gallery.Candidate{Model: "a", MinVRAM: "20GiB"},
					gallery.Candidate{Model: "c", Capability: "nvidia"},
				),
				concreteEntry("a", "u://a"), concreteEntry("c", "u://c"),
			)

			violations := checkMetaConstraints(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Candidate).To(Equal("c"))
			Expect(violations[0].Detail).To(ContainSubstring("must not require a capability"))
		})
	})

	Describe("checkMetaReferences", func() {
		It("flags a candidate naming an entry that does not exist", func() {
			entries := metaFixture(
				metaEntry("meta", "u://c",
					gallery.Candidate{Model: "ghost", MinVRAM: "20GiB"},
					gallery.Candidate{Model: "c"},
				),
				concreteEntry("c", "u://c"),
			)

			violations := checkMetaReferences(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Candidate).To(Equal("ghost"))
			Expect(violations[0].Detail).To(ContainSubstring("unknown model"))
		})

		It("flags a candidate naming another meta entry", func() {
			entries := metaFixture(
				metaEntry("meta", "u://c",
					gallery.Candidate{Model: "nested", MinVRAM: "20GiB"},
					gallery.Candidate{Model: "c"},
				),
				metaEntry("nested", "u://c", gallery.Candidate{Model: "c"}),
				concreteEntry("c", "u://c"),
			)

			violations := checkMetaReferences(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Candidate).To(Equal("nested"))
			Expect(violations[0].Detail).To(ContainSubstring("nesting is not allowed"))
		})
	})

	Describe("checkMetaFallbackURL", func() {
		It("flags a meta url that differs from its final candidate url", func() {
			entries := metaFixture(
				metaEntry("meta", "u://wrong",
					gallery.Candidate{Model: "a", MinVRAM: "20GiB"},
					gallery.Candidate{Model: "c"},
				),
				concreteEntry("a", "u://a"), concreteEntry("c", "u://c"),
			)

			violations := checkMetaFallbackURL(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Candidate).To(Equal("c"))
			Expect(violations[0].Detail).To(ContainSubstring("must equal its final candidate url"))
		})
	})

	Describe("checkMetaCapabilities", func() {
		It("flags a capability the system can never report", func() {
			entries := metaFixture(
				metaEntry("meta", "u://c",
					gallery.Candidate{Model: "a", Capability: "cuda", MinVRAM: "20GiB"},
					gallery.Candidate{Model: "c"},
				),
				concreteEntry("a", "u://a"), concreteEntry("c", "u://c"),
			)

			violations := checkMetaCapabilities(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Candidate).To(Equal("a"))
			Expect(violations[0].Detail).To(ContainSubstring(`unknown capability "cuda"`))
		})
	})
})

var _ = Describe("gallery/index.yaml meta entry invariants", Ordered, func() {
	var entries []gallery.GalleryModel

	BeforeAll(func() {
		var err error
		entries, err = loadGalleryIndex()
		Expect(err).ToNot(HaveOccurred())
		// A truncated or emptied index unmarshals cleanly and would make every
		// spec below vacuously pass.
		Expect(entries).ToNot(BeEmpty())
	})

	It("gives every meta entry a legacy url and no inline payload", func() {
		v := checkMetaFallbackURL(entries)
		Expect(v).To(BeEmpty(), formatViolations(v))
	})

	It("references only existing, non-meta entries", func() {
		v := checkMetaReferences(entries)
		Expect(v).To(BeEmpty(), formatViolations(v))
	})

	It("constrains every candidate except an unconstrained last resort", func() {
		v := checkMetaConstraints(entries)
		Expect(v).To(BeEmpty(), formatViolations(v))
	})

	It("uses only capabilities the system can report", func() {
		v := checkMetaCapabilities(entries)
		Expect(v).To(BeEmpty(), formatViolations(v))
	})

	It("orders candidates so no candidate is shadowed by an earlier one", func() {
		v := checkMetaOrdering(entries)
		Expect(v).To(BeEmpty(), formatViolations(v))
	})
})
