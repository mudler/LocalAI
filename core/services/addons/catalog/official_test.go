// SPDX-License-Identifier: MIT
package catalog

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("official library default", func() {
	It("is a valid, signature-requiring source", func() {
		src := OfficialLibrary()
		Expect(validSource(src)).To(BeTrue())
		Expect(src.Enabled).To(BeTrue())
		Expect(src.Policy.RequireSignature).To(BeTrue())
		Expect(src.URL).To(HavePrefix("https://raw.githubusercontent.com/mudler/LocalAI/master/"))
	})
	It("is added when there is no sources.json and is written to it", func() {
		s := setup()
		added, e := s.SeedDefault(OfficialLibrary())
		Expect(e).NotTo(HaveOccurred())
		Expect(added).To(BeTrue())
		list, e := s.List(ctx, admin)
		Expect(e).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].ID).To(Equal(OfficialLibraryID))
		info, e := os.Stat(s.path)
		Expect(e).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0600)))
		reloaded, e := NewSources(s.path, checker, CredentialResolver{})
		Expect(e).NotTo(HaveOccurred())
		Expect(reloaded.sources).To(HaveKey(OfficialLibraryID))
	})
	It("keeps a sources.json the admin already has", func() {
		s := setup()
		save(s, "mine", "https://example.org/index.yaml")
		reloaded, e := NewSources(s.path, checker, CredentialResolver{})
		Expect(e).NotTo(HaveOccurred())
		added, e := reloaded.SeedDefault(OfficialLibrary())
		Expect(e).NotTo(HaveOccurred())
		Expect(added).To(BeFalse())
		Expect(reloaded.sources).To(HaveLen(1))
		Expect(reloaded.sources).To(HaveKey("mine"))
	})
	It("does not add the default again after the admin removed it", func() {
		s := setup()
		_, e := s.SeedDefault(OfficialLibrary())
		Expect(e).NotTo(HaveOccurred())
		Expect(s.Save(ctx, admin, Source{ID: OfficialLibraryID, Removed: true})).To(Succeed())
		reloaded, e := NewSources(s.path, checker, CredentialResolver{})
		Expect(e).NotTo(HaveOccurred())
		added, e := reloaded.SeedDefault(OfficialLibrary())
		Expect(e).NotTo(HaveOccurred())
		Expect(added).To(BeFalse())
		// The removal is remembered as a tombstone, and nothing is listed.
		Expect(reloaded.sources[OfficialLibraryID].IsTombstone()).To(BeTrue())
		list, e := reloaded.List(ctx, admin)
		Expect(e).NotTo(HaveOccurred())
		Expect(list).To(BeEmpty())
	})
	It("seeds only once per instance", func() {
		s := setup()
		_, e := s.SeedDefault(OfficialLibrary())
		Expect(e).NotTo(HaveOccurred())
		added, e := s.SeedDefault(OfficialLibrary())
		Expect(e).NotTo(HaveOccurred())
		Expect(added).To(BeFalse())
	})
})

var _ = Describe("official library index", func() {
	It("lists the seven first-party add-ons in the library format", func() {
		data, e := os.ReadFile("../../../../gallery/addons/index.yaml")
		Expect(e).NotTo(HaveOccurred())
		releases, e := DecodeIndex(data)
		Expect(e).NotTo(HaveOccurred())
		ids := []string{}
		for _, r := range releases {
			ids = append(ids, r.ID)
			// A development entry is mutable and has no digest; any other is pinned.
			if r.Mutable {
				Expect(r.Digest).To(BeEmpty(), r.ID)
				Expect(r.Channel).To(Equal("development"), r.ID)
				Expect(r.Artifact).To(HaveSuffix(":"+r.ID+"-development"), r.ID)
			} else {
				Expect(digestPattern.MatchString(r.Digest)).To(BeTrue(), r.ID)
			}
			Expect(versionPattern.MatchString(r.Version)).To(BeTrue(), r.ID)
			Expect(r.Artifact).To(HavePrefix("oci://"), r.ID)
		}
		Expect(ids).To(ConsistOf("fine-tuning", "quantization", "voice", "face", "studio", "talk", "tables"))
	})
})
