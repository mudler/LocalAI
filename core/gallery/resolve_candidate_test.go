package gallery_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery"
)

var _ = Describe("Candidate.EffectiveMinVRAM", func() {
	It("reports no floor when neither authored nor inferred", func() {
		c := gallery.Candidate{Model: "x"}
		bytes, declared, err := c.EffectiveMinVRAM()
		Expect(err).ToNot(HaveOccurred())
		Expect(declared).To(BeFalse())
		Expect(bytes).To(Equal(uint64(0)))
	})

	It("uses the inferred value when nothing is authored", func() {
		c := gallery.Candidate{Model: "x", InferredMinVRAM: "6GiB"}
		bytes, declared, err := c.EffectiveMinVRAM()
		Expect(err).ToNot(HaveOccurred())
		Expect(declared).To(BeTrue())
		Expect(bytes).To(Equal(uint64(6 * 1024 * 1024 * 1024)))
	})

	It("lets an authored value win over an inferred one", func() {
		c := gallery.Candidate{Model: "x", MinVRAM: "20GiB", InferredMinVRAM: "6GiB"}
		bytes, declared, err := c.EffectiveMinVRAM()
		Expect(err).ToNot(HaveOccurred())
		Expect(declared).To(BeTrue())
		Expect(bytes).To(Equal(uint64(20 * 1024 * 1024 * 1024)))
	})

	It("errors on an unparseable floor rather than silently treating it as absent", func() {
		c := gallery.Candidate{Model: "x", MinVRAM: "twenty gigs"}
		_, _, err := c.EffectiveMinVRAM()
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("ResolveCandidate", func() {
	gib := func(n uint64) uint64 { return n * 1024 * 1024 * 1024 }

	list := []gallery.Candidate{
		{Model: "m-mlx", Capability: "metal", MinVRAM: "16GiB"},
		{Model: "m-vllm", Capability: "nvidia", MinVRAM: "20GiB"},
		{Model: "m-q8", MinVRAM: "12GiB"},
		{Model: "m-q4", MinVRAM: "6GiB"},
		{Model: "m-q4-cpu"},
	}

	It("picks the capability-matched candidate when VRAM allows", func() {
		got, err := gallery.ResolveCandidate(list, gallery.ResolveEnv{Capability: "nvidia", VRAM: gib(24)}, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(got.Model).To(Equal("m-vllm"))
	})

	It("skips a capability match whose VRAM floor does not fit", func() {
		got, err := gallery.ResolveCandidate(list, gallery.ResolveEnv{Capability: "nvidia", VRAM: gib(8)}, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(got.Model).To(Equal("m-q4"))
	})

	It("ignores candidates whose capability does not match the host", func() {
		got, err := gallery.ResolveCandidate(list, gallery.ResolveEnv{Capability: "metal", VRAM: gib(24)}, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(got.Model).To(Equal("m-mlx"))
	})

	It("falls through to the unconstrained last resort on a tiny host", func() {
		got, err := gallery.ResolveCandidate(list, gallery.ResolveEnv{Capability: "default", VRAM: gib(2)}, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(got.Model).To(Equal("m-q4-cpu"))
	})

	It("honors a pin even when the host would have chosen otherwise", func() {
		got, err := gallery.ResolveCandidate(list, gallery.ResolveEnv{Capability: "nvidia", VRAM: gib(24)}, "m-q8")
		Expect(err).ToNot(HaveOccurred())
		Expect(got.Model).To(Equal("m-q8"))
	})

	It("fails loudly when the pin names an absent candidate", func() {
		_, err := gallery.ResolveCandidate(list, gallery.ResolveEnv{Capability: "nvidia", VRAM: gib(24)}, "m-gone")
		Expect(err).To(MatchError(gallery.ErrPinNotFound))
		Expect(err.Error()).To(ContainSubstring("m-gone"))
	})

	It("reports the host state when nothing matches", func() {
		constrained := []gallery.Candidate{{Model: "big", MinVRAM: "80GiB"}}
		_, err := gallery.ResolveCandidate(constrained, gallery.ResolveEnv{Capability: "default", VRAM: gib(8)}, "")
		Expect(err).To(MatchError(gallery.ErrNoCandidateMatch))
		Expect(err.Error()).To(ContainSubstring("default"))
		Expect(err.Error()).To(ContainSubstring("big"))
	})

	It("propagates an unparseable floor instead of treating it as unconstrained", func() {
		bad := []gallery.Candidate{{Model: "bad", MinVRAM: "lots"}}
		_, err := gallery.ResolveCandidate(bad, gallery.ResolveEnv{Capability: "default", VRAM: gib(8)}, "")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("bad"))
	})

	It("rejects an empty candidate list", func() {
		_, err := gallery.ResolveCandidate(nil, gallery.ResolveEnv{Capability: "default", VRAM: gib(8)}, "")
		Expect(err).To(MatchError(gallery.ErrNoCandidateMatch))
	})
})
