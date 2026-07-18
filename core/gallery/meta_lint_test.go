package gallery_test

import (
	"os"
	"path/filepath"

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

var _ = Describe("gallery/index.yaml meta entry invariants", func() {
	var entries []gallery.GalleryModel
	var byName map[string]gallery.GalleryModel

	BeforeEach(func() {
		data, err := os.ReadFile(filepath.Join("..", "..", "gallery", "index.yaml"))
		Expect(err).ToNot(HaveOccurred())
		Expect(yaml.Unmarshal(data, &entries)).To(Succeed())

		byName = map[string]gallery.GalleryModel{}
		for _, e := range entries {
			byName[e.Name] = e
		}
	})

	It("gives every meta entry a legacy url and no inline payload", func() {
		for _, e := range entries {
			if !e.IsMeta() {
				continue
			}
			// Released LocalAI versions ignore the candidates key. Without a
			// url they would list the entry and install an empty model.
			Expect(e.URL).ToNot(BeEmpty(), "meta entry %q needs a url fallback for older clients", e.Name)
			Expect(e.ConfigFile).To(BeEmpty(), "meta entry %q must not carry an inline config_file", e.Name)
			Expect(e.AdditionalFiles).To(BeEmpty(), "meta entry %q must not carry files", e.Name)

			last := e.Candidates[len(e.Candidates)-1]
			fallback, ok := byName[last.Model]
			Expect(ok).To(BeTrue(), "meta entry %q final candidate %q not found", e.Name, last.Model)
			Expect(e.URL).To(Equal(fallback.URL),
				"meta entry %q url must equal its final candidate %q url so old and new clients agree", e.Name, last.Model)
		}
	})

	It("references only existing, non-meta entries", func() {
		for _, e := range entries {
			if !e.IsMeta() {
				continue
			}
			for _, c := range e.Candidates {
				target, ok := byName[c.Model]
				Expect(ok).To(BeTrue(), "meta entry %q references unknown model %q", e.Name, c.Model)
				Expect(target.IsMeta()).To(BeFalse(), "meta entry %q references meta entry %q; nesting is not allowed", e.Name, c.Model)
			}
		}
	})

	It("constrains every candidate except an unconstrained last resort", func() {
		for _, e := range entries {
			if !e.IsMeta() {
				continue
			}
			for i, c := range e.Candidates {
				_, declared, err := c.EffectiveMinVRAM()
				Expect(err).ToNot(HaveOccurred(), "meta entry %q candidate %q has a bad min_vram", e.Name, c.Model)

				if i == len(e.Candidates)-1 {
					Expect(declared).To(BeFalse(), "meta entry %q final candidate %q must be an unconstrained last resort", e.Name, c.Model)
					Expect(c.Capability).To(BeEmpty(), "meta entry %q final candidate %q must not require a capability", e.Name, c.Model)
					continue
				}
				Expect(declared).To(BeTrue(), "meta entry %q candidate %q needs a min_vram; the nightly job should have inferred one", e.Name, c.Model)
			}
		}
	})

	It("uses only capabilities the system can report", func() {
		for _, e := range entries {
			if !e.IsMeta() {
				continue
			}
			for _, c := range e.Candidates {
				if c.Capability == "" {
					continue
				}
				Expect(knownCapabilities).To(HaveKey(c.Capability),
					"meta entry %q candidate %q uses unknown capability %q", e.Name, c.Model, c.Capability)
			}
		}
	})

	It("orders candidates by descending VRAM within a capability group", func() {
		for _, e := range entries {
			if !e.IsMeta() {
				continue
			}
			previous := map[string]uint64{}
			for _, c := range e.Candidates {
				floor, declared, err := c.EffectiveMinVRAM()
				Expect(err).ToNot(HaveOccurred())
				if !declared {
					continue
				}
				if prior, seen := previous[c.Capability]; seen {
					Expect(floor).To(BeNumerically("<=", prior),
						"meta entry %q candidate %q raises the VRAM floor after a lower one in the same capability group, so it can never be reached", e.Name, c.Model)
				}
				previous[c.Capability] = floor
			}
		}
	})
})
