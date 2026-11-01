// SPDX-License-Identifier: MIT
package settings_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/LocalAI/addons/web/internal/settings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSettings(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Web add-on settings suite")
}

const key = "test-key-0123456789"

func ptr(s string) *string { return &s }

var _ = Describe("Store", func() {
	var dir string
	BeforeEach(func() { dir = GinkgoT().TempDir() })

	It("starts with every provider in order and only DuckDuckGo ready", func() {
		s, err := settings.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		v := s.View()
		Expect(v.Order).To(Equal([]string{"brave", "tavily", "google", "searxng", "duckduckgo"}))
		for _, p := range v.Providers {
			Expect(p.Ready).To(Equal(p.Name == "duckduckgo"), p.Name)
		}
	})

	It("saves a key in a private file and never shows it", func() {
		s, _ := settings.Open(dir)
		Expect(s.Apply(settings.Change{Keys: map[string]string{"brave": key}, Order: []string{"brave", "duckduckgo"}})).To(Succeed())
		fi, err := os.Stat(filepath.Join(dir, "settings.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(fi.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		raw, _ := json.Marshal(s.View())
		Expect(string(raw)).NotTo(ContainSubstring(key))
		Expect(s.View().Providers[0]).To(Equal(settings.ProviderView{Name: "brave", Keyed: true, KeySet: true, Ready: true}))

		again, err := settings.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		got, _ := again.Get()
		Expect(got.Keys["brave"]).To(Equal(key))
		Expect(got.Order).To(Equal([]string{"brave", "duckduckgo"}))
	})

	It("keeps a key when the page sends an empty one and removes it on clear", func() {
		s, _ := settings.Open(dir)
		Expect(s.Apply(settings.Change{Keys: map[string]string{"tavily": key}})).To(Succeed())
		Expect(s.Apply(settings.Change{Keys: map[string]string{"tavily": "  "}})).To(Succeed())
		got, _ := s.Get()
		Expect(got.Keys["tavily"]).To(Equal(key))
		Expect(s.Apply(settings.Change{Clear: []string{"tavily"}})).To(Succeed())
		got, _ = s.Get()
		Expect(got.Keys).To(BeEmpty())
	})

	It("needs both the key and the engine ID for Google, and an address for SearXNG", func() {
		s, _ := settings.Open(dir)
		Expect(s.Apply(settings.Change{Keys: map[string]string{"google": key}})).To(Succeed())
		Expect(s.View().Providers[2].Missing).To(Equal("no search engine ID"))
		Expect(s.Apply(settings.Change{GoogleEngine: ptr("abc:123"), SearXNGURL: ptr("https://searx.example.org")})).To(Succeed())
		v := s.View()
		Expect(v.Providers[2].Ready).To(BeTrue())
		Expect(v.Providers[3].Ready).To(BeTrue())
	})

	It("counts the revisions", func() {
		s, _ := settings.Open(dir)
		_, r0 := s.Get()
		Expect(s.Apply(settings.Change{Order: []string{"duckduckgo"}})).To(Succeed())
		_, r1 := s.Get()
		Expect(r1).To(Equal(r0 + 1))
	})

	DescribeTable("refuses a change and keeps the settings",
		func(c settings.Change) {
			s, _ := settings.Open(dir)
			before, _ := s.Get()
			err := s.Apply(c)
			Expect(errors.Is(err, settings.ErrInvalid)).To(BeTrue())
			Expect(err.Error()).NotTo(ContainSubstring("bad key with spaces"))
			after, _ := s.Get()
			Expect(after).To(Equal(before))
		},
		Entry("an empty order", settings.Change{Order: []string{}}),
		Entry("an unknown provider", settings.Change{Order: []string{"bing"}}),
		Entry("a repeated provider", settings.Change{Order: []string{"brave", "brave"}}),
		Entry("a key for a keyless provider", settings.Change{Keys: map[string]string{"duckduckgo": key}}),
		Entry("a malformed key", settings.Change{Keys: map[string]string{"brave": "bad key with spaces"}}),
		Entry("clearing a keyless provider", settings.Change{Clear: []string{"searxng"}}),
		Entry("a malformed engine ID", settings.Change{GoogleEngine: ptr("a b")}),
		Entry("a SearXNG address that is not http(s)", settings.Change{SearXNGURL: ptr("file:///etc")}),
		Entry("a SearXNG address with credentials", settings.Change{SearXNGURL: ptr("https://u:p@searx.example.org")}),
	)

	It("falls back to the defaults for a broken or invalid file and says so", func() {
		Expect(os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{"), 0o600)).To(Succeed())
		s, err := settings.Open(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.View().Note).To(ContainSubstring("could not be read"))
		Expect(os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"order":["bing"]}`), 0o600)).To(Succeed())
		s, _ = settings.Open(dir)
		Expect(s.View().Note).To(ContainSubstring("not valid"))
		Expect(s.View().Order).To(HaveLen(5))
	})

	It("needs a data folder", func() {
		_, err := settings.Open("")
		Expect(err).To(HaveOccurred())
	})
})
