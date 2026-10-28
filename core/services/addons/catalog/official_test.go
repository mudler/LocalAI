// SPDX-License-Identifier: MIT
package catalog

import (
	"os"
	"path/filepath"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
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
	It("lists the ten first-party add-ons, one entry for each platform, in the library format", func() {
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
		Expect(slices.Compact(slices.Sorted(slices.Values(ids)))).To(ConsistOf("fine-tuning", "quantization", "voice", "face", "studio", "talk", "telegram", "slack", "tables", "office", "sandbox"))
	})
})

var _ = Describe("official library of a release build", func() {
	DescribeTable("reads the index and the signer of its own tag",
		func(version, wantURL, wantRef string) {
			src := officialLibraryFor(version)
			Expect(validSource(src)).To(BeTrue())
			Expect(src.URL).To(Equal(wantURL))
			Expect(src.Policy.RequireSignature).To(BeTrue())
			Expect(src.Policy.Identity).To(Equal("https://github.com/mudler/LocalAI/.github/workflows/addons-package.yml@" + wantRef))
		},
		Entry("a release tag", "v4.9.0",
			"https://github.com/mudler/LocalAI/releases/download/v4.9.0/addons-index.yaml", "refs/tags/v4.9.0"),
		Entry("a release candidate", "v4.9.0-rc.1",
			"https://github.com/mudler/LocalAI/releases/download/v4.9.0-rc.1/addons-index.yaml", "refs/tags/v4.9.0-rc.1"),
		Entry("a source build", "", OfficialLibraryURL, "refs/heads/master"),
		Entry("a commit after a tag", "v4.9.0-14-gabc1234", OfficialLibraryURL, "refs/heads/master"),
		Entry("a bare commit", "abc1234", OfficialLibraryURL, "refs/heads/master"),
		Entry("a version that could escape the URL", "v1.2.3/../../x", OfficialLibraryURL, "refs/heads/master"),
	)
	It("moves a stored official library to the index of the new build", func() {
		s := setup()
		_, e := s.SeedDefaults(officialLibraryFor("v4.9.0"))
		Expect(e).NotTo(HaveOccurred())
		_, e = s.SeedDefaults(officialLibraryFor("v4.10.0"))
		Expect(e).NotTo(HaveOccurred())
		reloaded, e := NewSources(s.path, checker, CredentialResolver{})
		Expect(e).NotTo(HaveOccurred())
		Expect(reloaded.sources[OfficialLibraryID].URL).To(Equal(officialLibraryFor("v4.10.0").URL))
		Expect(reloaded.sources[OfficialLibraryID].Policy).To(Equal(officialLibraryFor("v4.10.0").Policy))
	})
	It("leaves an official library the admin pointed at another URL", func() {
		s := setup()
		_, e := s.SeedDefaults(officialLibraryFor("v4.9.0"))
		Expect(e).NotTo(HaveOccurred())
		mine := s.sources[OfficialLibraryID]
		mine.URL = "https://example.org/addons.yaml"
		Expect(s.Save(ctx, admin, mine)).To(Succeed())
		_, e = s.SeedDefaults(officialLibraryFor("v4.10.0"))
		Expect(e).NotTo(HaveOccurred())
		Expect(s.sources[OfficialLibraryID].URL).To(Equal("https://example.org/addons.yaml"))
	})
})

var _ = Describe("official library index platforms", func() {
	It("has an entry for every enabled platform of every add-on", func() {
		data, e := os.ReadFile("../../../../gallery/addons/index.yaml")
		Expect(e).NotTo(HaveOccurred())
		releases, e := DecodeIndex(data)
		Expect(e).NotTo(HaveOccurred())
		have := map[string]bool{}
		for _, r := range releases {
			have[r.ID+" "+r.OS+"/"+r.Arch] = true
		}
		files, e := filepath.Glob("../../../../addons/*/package/platforms.yaml")
		Expect(e).NotTo(HaveOccurred())
		Expect(files).NotTo(BeEmpty())
		want := map[string]bool{}
		for _, f := range files {
			raw, e := os.ReadFile(f)
			Expect(e).NotTo(HaveOccurred())
			var cfg struct {
				Addon     string `yaml:"addon"`
				Platforms []struct {
					Platform string `yaml:"platform"`
					Enabled  bool   `yaml:"enabled"`
				} `yaml:"platforms"`
			}
			Expect(yaml.Unmarshal(raw, &cfg)).To(Succeed(), f)
			for _, p := range cfg.Platforms {
				if p.Enabled {
					want[cfg.Addon+" "+p.Platform] = true
				}
			}
		}
		Expect(have).To(Equal(want), "regenerate with scripts/addons/catalog.sh")
	})
})
