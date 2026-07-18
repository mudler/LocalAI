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

// candidateViolation is one invariant breach found by a lint helper. Helpers
// return every breach they find instead of stopping at the first, so a single
// run names them all rather than forcing a fix-one-rerun-repeat cycle.
type candidateViolation struct {
	Entry     string
	Candidate string
	Detail    string
}

func (v candidateViolation) String() string {
	if v.Candidate == "" {
		return fmt.Sprintf("%s: %s", v.Entry, v.Detail)
	}
	return fmt.Sprintf("%s -> candidate %q: %s", v.Entry, v.Candidate, v.Detail)
}

func formatViolations(violations []candidateViolation) string {
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

// baseCandidate mirrors the implicit last-resort candidate the resolver
// synthesizes from the entry itself, so lint measures the same floor the
// installer will.
func baseCandidate(e gallery.GalleryModel) gallery.Candidate {
	return gallery.Candidate{Model: e.Name, Capability: e.Capability, MinVRAM: e.MinVRAM}
}

// checkCandidateReferences verifies every candidate names an existing entry
// that declares no candidates of its own. Resolution is a single pass, so a
// nested reference would silently ignore the inner list.
func checkCandidateReferences(entries []gallery.GalleryModel) []candidateViolation {
	byName := indexEntriesByName(entries)
	var violations []candidateViolation
	for _, e := range entries {
		if !e.HasCandidates() {
			continue
		}
		for _, c := range e.Candidates {
			target, ok := byName[c.Model]
			if !ok {
				violations = append(violations, candidateViolation{Entry: e.Name, Candidate: c.Model, Detail: "references unknown model"})
				continue
			}
			if target.HasCandidates() {
				violations = append(violations, candidateViolation{Entry: e.Name, Candidate: c.Model, Detail: "references an entry that declares candidates of its own; nesting is not allowed"})
			}
		}
	}
	return violations
}

// checkCandidateFloors verifies every declared candidate carries a parseable
// VRAM floor. A candidate is an UPGRADE over the entry, so it must say what it
// costs; a floorless one would capture every host and make the entry's own
// payload unreachable.
func checkCandidateFloors(entries []gallery.GalleryModel) []candidateViolation {
	var violations []candidateViolation
	for _, e := range entries {
		if !e.HasCandidates() {
			continue
		}
		for _, c := range e.Candidates {
			_, declared, err := c.EffectiveMinVRAM()
			switch {
			case err != nil:
				violations = append(violations, candidateViolation{Entry: e.Name, Candidate: c.Model, Detail: "has a bad min_vram: " + err.Error()})
			case !declared:
				violations = append(violations, candidateViolation{Entry: e.Name, Candidate: c.Model, Detail: "needs a min_vram; the nightly job should have inferred one"})
			}
		}
	}
	return violations
}

// checkBaseFloor verifies the entry's own floor sits strictly below every
// candidate's. The entry is the last element of its own candidate list, so a
// base floor at or above a candidate's makes that candidate dead: any host
// clearing it already cleared the base, and first-match never gets that far.
func checkBaseFloor(entries []gallery.GalleryModel) []candidateViolation {
	var violations []candidateViolation
	for _, e := range entries {
		if !e.HasCandidates() {
			continue
		}
		base := baseCandidate(e)
		baseFloor, _, err := base.EffectiveMinVRAM()
		if err != nil {
			violations = append(violations, candidateViolation{Entry: e.Name, Detail: "has a bad min_vram: " + err.Error()})
			continue
		}
		for _, c := range e.Candidates {
			floor, declared, cerr := c.EffectiveMinVRAM()
			if cerr != nil || !declared {
				// checkCandidateFloors owns reporting those.
				continue
			}
			if baseFloor >= floor {
				violations = append(violations, candidateViolation{
					Entry:     e.Name,
					Candidate: c.Model,
					Detail: fmt.Sprintf("sits at or below the entry's own %d byte floor, so it can never be reached",
						baseFloor),
				})
			}
		}
	}
	return violations
}

// checkCandidateCapabilities verifies entries and candidates only name
// capabilities the system can actually report, since the resolver compares
// them exactly.
func checkCandidateCapabilities(entries []gallery.GalleryModel) []candidateViolation {
	var violations []candidateViolation
	for _, e := range entries {
		if !e.HasCandidates() {
			continue
		}
		if e.Capability != "" && !knownCapabilities[e.Capability] {
			violations = append(violations, candidateViolation{
				Entry:  e.Name,
				Detail: fmt.Sprintf("uses unknown capability %q", e.Capability),
			})
		}
		for _, c := range e.Candidates {
			if c.Capability == "" {
				continue
			}
			if !knownCapabilities[c.Capability] {
				violations = append(violations, candidateViolation{
					Entry:     e.Name,
					Candidate: c.Model,
					Detail:    fmt.Sprintf("uses unknown capability %q", c.Capability),
				})
			}
		}
	}
	return violations
}

// checkCandidateOrdering verifies no candidate is shadowed by an earlier one.
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
func checkCandidateOrdering(entries []gallery.GalleryModel) []candidateViolation {
	var violations []candidateViolation
	for _, e := range entries {
		if !e.HasCandidates() {
			continue
		}
		previous := map[string]uint64{}
		var unconditionalFloor uint64
		haveUnconditional := false

		for _, c := range e.Candidates {
			floor, declared, err := c.EffectiveMinVRAM()
			if err != nil || !declared {
				// Parse errors and absent floors belong to checkCandidateFloors.
				continue
			}

			switch {
			case haveUnconditional && floor >= unconditionalFloor:
				violations = append(violations, candidateViolation{
					Entry:     e.Name,
					Candidate: c.Model,
					Detail: fmt.Sprintf("is shadowed by an earlier candidate that matches any host at a %d byte floor, so it can never be reached",
						unconditionalFloor),
				})
			case c.Capability != "":
				if prior, seen := previous[c.Capability]; seen && floor > prior {
					violations = append(violations, candidateViolation{
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

func plainEntry(name, url string) gallery.GalleryModel {
	e := gallery.GalleryModel{}
	e.Name = name
	e.URL = url
	return e
}

func entryWithCandidates(name, url, minVRAM string, candidates ...gallery.Candidate) gallery.GalleryModel {
	e := gallery.GalleryModel{Candidates: candidates, MinVRAM: minVRAM}
	e.Name = name
	e.URL = url
	return e
}

// candidateFixture builds an entry with candidates plus the entries it
// references. Synthetic fixtures keep the invariant logic covered however few
// entries in gallery/index.yaml declare candidates; without them every
// index-driven spec below is a no-op that passes while checking nothing.
func candidateFixture(base gallery.GalleryModel, referenced ...gallery.GalleryModel) []gallery.GalleryModel {
	return append([]gallery.GalleryModel{base}, referenced...)
}

var _ = Describe("gallery candidate lint helpers", func() {
	// An entry declares candidates solely by carrying the key. GalleryBackend.IsMeta()
	// has deliberately different semantics (it requires an EMPTY uri), so a
	// well-meaning alignment of the two would make every helper skip every
	// entry and pass silently. Assert the distinction directly.
	It("treats an entry with candidates as such even though it has a url", func() {
		Expect(entryWithCandidates("base", "u://base", "2GiB", gallery.Candidate{Model: "big"}).HasCandidates()).To(BeTrue())
		Expect(plainEntry("big", "u://big").HasCandidates()).To(BeFalse())
	})

	It("passes every invariant on a valid entry", func() {
		entries := candidateFixture(
			entryWithCandidates("base", "u://base", "4GiB",
				gallery.Candidate{Model: "big", Capability: "nvidia", MinVRAM: "24GiB"},
				gallery.Candidate{Model: "mid", Capability: "nvidia", MinVRAM: "12GiB"},
				gallery.Candidate{Model: "metal-big", Capability: "metal", MinVRAM: "32GiB"},
				gallery.Candidate{Model: "small", MinVRAM: "8GiB"},
			),
			plainEntry("big", "u://big"),
			plainEntry("mid", "u://mid"),
			plainEntry("metal-big", "u://metal-big"),
			plainEntry("small", "u://small"),
		)

		Expect(checkCandidateReferences(entries)).To(BeEmpty())
		Expect(checkCandidateFloors(entries)).To(BeEmpty())
		Expect(checkBaseFloor(entries)).To(BeEmpty())
		Expect(checkCandidateCapabilities(entries)).To(BeEmpty())
		Expect(checkCandidateOrdering(entries)).To(BeEmpty())
	})

	It("passes every invariant on an entry that declares no floor of its own", func() {
		// A floorless entry is the weakest possible base, below every
		// candidate, which is exactly the position the base should hold.
		entries := candidateFixture(
			entryWithCandidates("base", "u://base", "",
				gallery.Candidate{Model: "big", MinVRAM: "24GiB"},
			),
			plainEntry("big", "u://big"),
		)

		Expect(checkCandidateFloors(entries)).To(BeEmpty())
		Expect(checkBaseFloor(entries)).To(BeEmpty())
		Expect(checkCandidateOrdering(entries)).To(BeEmpty())
	})

	Describe("checkBaseFloor", func() {
		It("flags a candidate whose floor sits below the entry's own", func() {
			entries := candidateFixture(
				entryWithCandidates("base", "u://base", "20GiB",
					gallery.Candidate{Model: "big", MinVRAM: "8GiB"},
				),
				plainEntry("big", "u://big"),
			)

			violations := checkBaseFloor(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Candidate).To(Equal("big"))
			Expect(violations[0].Detail).To(ContainSubstring("at or below the entry's own"))
		})

		It("flags a candidate whose floor merely equals the entry's own", func() {
			// Equal floors make the candidate unreachable just as surely: every
			// host clearing it clears the base, and the base is checked first
			// only in the sense that first-match never reaches this candidate.
			entries := candidateFixture(
				entryWithCandidates("base", "u://base", "8GiB",
					gallery.Candidate{Model: "big", MinVRAM: "8GiB"},
				),
				plainEntry("big", "u://big"),
			)

			violations := checkBaseFloor(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Candidate).To(Equal("big"))
		})

		It("flags an entry whose own min_vram cannot be parsed", func() {
			entries := candidateFixture(
				entryWithCandidates("base", "u://base", "eight gigs",
					gallery.Candidate{Model: "big", MinVRAM: "24GiB"},
				),
				plainEntry("big", "u://big"),
			)

			violations := checkBaseFloor(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Entry).To(Equal("base"))
			Expect(violations[0].Candidate).To(BeEmpty())
			Expect(violations[0].Detail).To(ContainSubstring("bad min_vram"))
		})
	})

	Describe("checkCandidateOrdering", func() {
		It("flags a candidate shadowed by an earlier unconditional candidate", func() {
			// "a" matches any host at 8GiB, so the nvidia candidate at 20GiB is
			// dead: every nvidia host clearing 20GiB already cleared 8GiB.
			entries := candidateFixture(
				entryWithCandidates("base", "u://base", "2GiB",
					gallery.Candidate{Model: "a", MinVRAM: "8GiB"},
					gallery.Candidate{Model: "b", Capability: "nvidia", MinVRAM: "20GiB"},
				),
				plainEntry("a", "u://a"), plainEntry("b", "u://b"),
			)

			violations := checkCandidateOrdering(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Candidate).To(Equal("b"))
			Expect(violations[0].Detail).To(ContainSubstring("shadowed by an earlier candidate that matches any host"))
		})

		It("flags a floor inversion inside one capability group", func() {
			entries := candidateFixture(
				entryWithCandidates("base", "u://base", "2GiB",
					gallery.Candidate{Model: "a", Capability: "nvidia", MinVRAM: "8GiB"},
					gallery.Candidate{Model: "b", Capability: "nvidia", MinVRAM: "20GiB"},
				),
				plainEntry("a", "u://a"), plainEntry("b", "u://b"),
			)

			violations := checkCandidateOrdering(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Candidate).To(Equal("b"))
			Expect(violations[0].Detail).To(ContainSubstring("same capability group"))
		})

		It("keeps distinct capability groups independent", func() {
			// A high metal floor after a low nvidia floor is fine: no host
			// reports both capabilities.
			entries := candidateFixture(
				entryWithCandidates("base", "u://base", "2GiB",
					gallery.Candidate{Model: "a", Capability: "nvidia", MinVRAM: "8GiB"},
					gallery.Candidate{Model: "b", Capability: "metal", MinVRAM: "32GiB"},
				),
				plainEntry("a", "u://a"), plainEntry("b", "u://b"),
			)

			Expect(checkCandidateOrdering(entries)).To(BeEmpty())
		})
	})

	Describe("checkCandidateFloors", func() {
		It("flags a candidate with no floor", func() {
			entries := candidateFixture(
				entryWithCandidates("base", "u://base", "2GiB",
					gallery.Candidate{Model: "a", Capability: "nvidia"},
				),
				plainEntry("a", "u://a"),
			)

			violations := checkCandidateFloors(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Candidate).To(Equal("a"))
			Expect(violations[0].Detail).To(ContainSubstring("needs a min_vram"))
		})

		It("flags a candidate whose floor cannot be parsed", func() {
			entries := candidateFixture(
				entryWithCandidates("base", "u://base", "2GiB",
					gallery.Candidate{Model: "a", MinVRAM: "lots"},
				),
				plainEntry("a", "u://a"),
			)

			violations := checkCandidateFloors(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Candidate).To(Equal("a"))
			Expect(violations[0].Detail).To(ContainSubstring("bad min_vram"))
		})
	})

	Describe("checkCandidateReferences", func() {
		It("flags a candidate naming an entry that does not exist", func() {
			entries := candidateFixture(
				entryWithCandidates("base", "u://base", "2GiB",
					gallery.Candidate{Model: "ghost", MinVRAM: "20GiB"},
				),
				plainEntry("a", "u://a"),
			)

			violations := checkCandidateReferences(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Candidate).To(Equal("ghost"))
			Expect(violations[0].Detail).To(ContainSubstring("unknown model"))
		})

		It("flags a candidate naming an entry that declares candidates itself", func() {
			entries := candidateFixture(
				entryWithCandidates("base", "u://base", "2GiB",
					gallery.Candidate{Model: "nested", MinVRAM: "20GiB"},
				),
				entryWithCandidates("nested", "u://nested", "4GiB", gallery.Candidate{Model: "a", MinVRAM: "30GiB"}),
				plainEntry("a", "u://a"),
			)

			violations := checkCandidateReferences(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Candidate).To(Equal("nested"))
			Expect(violations[0].Detail).To(ContainSubstring("nesting is not allowed"))
		})
	})

	Describe("checkCandidateCapabilities", func() {
		It("flags a capability the system can never report", func() {
			entries := candidateFixture(
				entryWithCandidates("base", "u://base", "2GiB",
					gallery.Candidate{Model: "a", Capability: "cuda", MinVRAM: "20GiB"},
				),
				plainEntry("a", "u://a"),
			)

			violations := checkCandidateCapabilities(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Candidate).To(Equal("a"))
			Expect(violations[0].Detail).To(ContainSubstring(`unknown capability "cuda"`))
		})

		It("flags an unknown capability on the entry itself", func() {
			entry := entryWithCandidates("base", "u://base", "2GiB", gallery.Candidate{Model: "a", MinVRAM: "20GiB"})
			entry.Capability = "NVIDIA"
			entries := candidateFixture(entry, plainEntry("a", "u://a"))

			violations := checkCandidateCapabilities(entries)
			Expect(violations).To(HaveLen(1))
			Expect(violations[0].Entry).To(Equal("base"))
			Expect(violations[0].Candidate).To(BeEmpty())
			Expect(violations[0].Detail).To(ContainSubstring(`unknown capability "NVIDIA"`))
		})
	})
})

var _ = Describe("gallery/index.yaml candidate invariants", Ordered, func() {
	var entries []gallery.GalleryModel

	BeforeAll(func() {
		var err error
		entries, err = loadGalleryIndex()
		Expect(err).ToNot(HaveOccurred())
		// A truncated or emptied index unmarshals cleanly and would make every
		// spec below vacuously pass.
		Expect(entries).ToNot(BeEmpty())
	})

	It("references only existing entries that declare no candidates themselves", func() {
		v := checkCandidateReferences(entries)
		Expect(v).To(BeEmpty(), formatViolations(v))
	})

	It("gives every candidate a VRAM floor", func() {
		v := checkCandidateFloors(entries)
		Expect(v).To(BeEmpty(), formatViolations(v))
	})

	It("keeps every entry's own floor below its candidates'", func() {
		v := checkBaseFloor(entries)
		Expect(v).To(BeEmpty(), formatViolations(v))
	})

	It("uses only capabilities the system can report", func() {
		v := checkCandidateCapabilities(entries)
		Expect(v).To(BeEmpty(), formatViolations(v))
	})

	It("orders candidates so no candidate is shadowed by an earlier one", func() {
		v := checkCandidateOrdering(entries)
		Expect(v).To(BeEmpty(), formatViolations(v))
	})
})
