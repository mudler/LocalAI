package config_test

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

var _ = Describe("Gallery mirrors", func() {
	// Galleries are configured as a JSON list in LOCALAI_GALLERIES and edited
	// as raw JSON in the settings UI, so both directions must round-trip or a
	// user silently loses their mirrors the next time they save.
	It("round-trips through JSON", func() {
		const in = `[{"url":"https://primary.example/index.yaml","name":"localai",` +
			`"mirrors":["github:mudler/LocalAI/gallery/index.yaml@master","file:///srv/index.yaml"]}]`

		var galleries []config.Gallery
		Expect(json.Unmarshal([]byte(in), &galleries)).To(Succeed())
		Expect(galleries).To(HaveLen(1))

		// Order is load-bearing: mirrors are an ordered fallback chain, not a set.
		want := []string{"github:mudler/LocalAI/gallery/index.yaml@master", "file:///srv/index.yaml"}
		Expect(galleries[0].Mirrors).To(Equal(want))

		out, err := json.Marshal(galleries)
		Expect(err).ToNot(HaveOccurred())
		var again []config.Gallery
		Expect(json.Unmarshal(out, &again)).To(Succeed())
		Expect(again[0].Mirrors).To(Equal(want), "mirrors lost or reordered on round-trip: %s", out)
		Expect(again[0].URL).To(Equal("https://primary.example/index.yaml"))
		Expect(again[0].Name).To(Equal("localai"))
	})

	It("round-trips through YAML", func() {
		const in = "- url: https://primary.example/index.yaml\n" +
			"  name: localai\n" +
			"  mirrors:\n" +
			"    - github:mudler/LocalAI/gallery/index.yaml@master\n" +
			"    - https://fallback.example/index.yaml\n"

		var galleries []config.Gallery
		Expect(yaml.Unmarshal([]byte(in), &galleries)).To(Succeed())
		want := []string{"github:mudler/LocalAI/gallery/index.yaml@master", "https://fallback.example/index.yaml"}
		Expect(galleries[0].Mirrors).To(Equal(want))

		out, err := yaml.Marshal(galleries)
		Expect(err).ToNot(HaveOccurred())
		var again []config.Gallery
		Expect(yaml.Unmarshal(out, &again)).To(Succeed())
		Expect(again[0].Mirrors).To(Equal(want), "mirrors lost or reordered on YAML round-trip: %s", out)
	})

	// omitempty keeps existing configs byte-identical when they declare no
	// mirrors, so this change cannot churn anyone's stored settings.
	Context("a gallery without mirrors", func() {
		It("marshals to unchanged JSON", func() {
			out, err := json.Marshal(config.Gallery{URL: "https://x/index.yaml", Name: "n"})
			Expect(err).ToNot(HaveOccurred())
			Expect(string(out)).To(Equal(`{"url":"https://x/index.yaml","name":"n"}`), "want no mirrors key")
		})

		It("marshals to unchanged YAML", func() {
			y, err := yaml.Marshal(config.Gallery{URL: "https://x/index.yaml", Name: "localai"})
			Expect(err).ToNot(HaveOccurred())
			Expect(string(y)).To(Equal("url: https://x/index.yaml\nname: localai\n"), "want no mirrors key")
		})
	})
})

// The runtime settings registry diffs gallery lists to decide whether the
// persisted settings differ from the startup baseline. A Gallery carrying a
// slice is no longer comparable with ==, so that diff must still notice a
// change confined to the mirror list — otherwise editing mirrors in the
// settings UI would be dropped as a no-op.
var _ = Describe("GalleriesEqual", func() {
	base := []config.Gallery{{URL: "https://x/index.yaml", Name: "n"}}
	withMirror := []config.Gallery{{URL: "https://x/index.yaml", Name: "n", Mirrors: []string{"github:mudler/LocalAI/gallery/index.yaml@master"}}}

	It("reports lists that differ only by mirrors as unequal", func() {
		Expect(config.GalleriesEqual(base, withMirror)).To(BeFalse())
	})

	It("reports identical mirror-less lists as equal", func() {
		Expect(config.GalleriesEqual(base, []config.Gallery{{URL: "https://x/index.yaml", Name: "n"}})).To(BeTrue())
	})

	It("reports identical mirrored lists as equal", func() {
		Expect(config.GalleriesEqual(withMirror, []config.Gallery{
			{URL: "https://x/index.yaml", Name: "n", Mirrors: []string{"github:mudler/LocalAI/gallery/index.yaml@master"}},
		})).To(BeTrue())
	})

	// Reordering the fallback chain is a real change, not a no-op.
	It("does not ignore mirror ordering", func() {
		a := []config.Gallery{{URL: "u", Mirrors: []string{"m1", "m2"}}}
		b := []config.Gallery{{URL: "u", Mirrors: []string{"m2", "m1"}}}
		Expect(config.GalleriesEqual(a, b)).To(BeFalse())
	})

	// A nil mirror list and an empty one both mean "no mirrors".
	It("does not distinguish nil from empty mirrors", func() {
		Expect(config.GalleriesEqual(
			[]config.Gallery{{URL: "u"}},
			[]config.Gallery{{URL: "u", Mirrors: []string{}}})).To(BeTrue())
	})

	It("reports lists of different length as unequal", func() {
		Expect(config.GalleriesEqual(base, nil)).To(BeFalse())
	})

	// Equal replaced ==, so it has to keep covering every field == covered:
	// missing one would make an env/CLI-set gallery list look like the default.
	Context("comparing every field", func() {
		It("does not ignore URL", func() {
			Expect(config.GalleriesEqual(
				[]config.Gallery{{URL: "https://a/index.yaml", Name: "n"}},
				[]config.Gallery{{URL: "https://b/index.yaml", Name: "n"}})).To(BeFalse())
		})

		It("does not ignore Name", func() {
			Expect(config.GalleriesEqual(
				[]config.Gallery{{URL: "https://a/index.yaml", Name: "one"}},
				[]config.Gallery{{URL: "https://a/index.yaml", Name: "two"}})).To(BeFalse())
		})

		// The verification pointer must be compared by value, not identity.
		It("compares the verification block by value, not pointer identity", func() {
			v1 := &config.GalleryVerification{Issuer: "i"}
			v2 := &config.GalleryVerification{Issuer: "i"}
			Expect(config.GalleriesEqual(
				[]config.Gallery{{URL: "u", Verification: v1}},
				[]config.Gallery{{URL: "u", Verification: v2}})).To(BeTrue())
		})

		It("does not ignore a differing verification block", func() {
			Expect(config.GalleriesEqual(
				[]config.Gallery{{URL: "u", Verification: &config.GalleryVerification{Issuer: "i"}}},
				[]config.Gallery{{URL: "u", Verification: &config.GalleryVerification{Issuer: "other"}}})).To(BeFalse())
		})

		It("does not ignore a verification block appearing", func() {
			Expect(config.GalleriesEqual(
				[]config.Gallery{{URL: "u"}},
				[]config.Gallery{{URL: "u", Verification: &config.GalleryVerification{Issuer: "i"}}})).To(BeFalse())
		})

		// GalleryVerification has five string fields; a value comparison must
		// notice a change in any of them, not just the first.
		DescribeTable("notices a change in any verification field",
			func(mutate func(*config.GalleryVerification)) {
				full := config.GalleryVerification{
					Issuer: "i", IssuerRegex: "ir", Identity: "id", IdentityRegex: "idr", NotBefore: "2026-05-01T00:00:00Z",
				}
				other := full
				mutate(&other)
				Expect(config.GalleriesEqual(
					[]config.Gallery{{URL: "u", Verification: &full}},
					[]config.Gallery{{URL: "u", Verification: &other}})).To(BeFalse(),
					"GalleriesEqual ignored a verification change: %+v vs %+v", full, other)
			},
			Entry("issuer", func(v *config.GalleryVerification) { v.Issuer = "x" }),
			Entry("issuer regex", func(v *config.GalleryVerification) { v.IssuerRegex = "x" }),
			Entry("identity", func(v *config.GalleryVerification) { v.Identity = "x" }),
			Entry("identity regex", func(v *config.GalleryVerification) { v.IdentityRegex = "x" }),
			Entry("not before", func(v *config.GalleryVerification) { v.NotBefore = "2030-01-01T00:00:00Z" }),
		)
	})
})
