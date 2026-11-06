// SPDX-License-Identifier: MIT
package packaging

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("feature level in the library index", func() {
	render := func(id string, edit func(string) string, extra ...string) []catalogRelease {
		GinkgoHelper()
		p := newProjectFor(id, both)
		path := filepath.Join(p.root, "addons", id, "manifest.json")
		b, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(path, []byte(edit(string(b))), 0o644)).To(Succeed())
		out := filepath.Join(GinkgoT().TempDir(), "catalog.yaml")
		args := append([]string{"catalog", "--config", p.config, "--root", p.root, "--template", "../../../gallery/addons/" + id + ".yaml.tmpl", "--from-manifest", "--repository", newRegistry().repo("addons"), "--channel", "development", "--owner-repo", "example/localai", "--signer-ref", "refs/heads/master", "--out", out}, extra...)
		_, err = run(args...)
		Expect(err).NotTo(HaveOccurred(), id)
		raw, err := os.ReadFile(out)
		Expect(err).NotTo(HaveOccurred())
		entries, err := readCatalogIndex(raw)
		Expect(err).NotTo(HaveOccurred(), id)
		Expect(entries).NotTo(BeEmpty())
		return entries
	}
	needsTwo := func(s string) string {
		return strings.Replace(s, `"schemaVersion": 1,`, `"schemaVersion": 1, "featureLevel": 2,`, 1)
	}
	It("writes nothing for a manifest at the base level, with or without the flag", func() {
		for _, extra := range [][]string{nil, {"--feature-level-in-index"}} {
			for _, e := range render("telegram", func(s string) string { return s }, extra...) {
				Expect(e.Requires.FeatureLevel).To(BeZero())
			}
		}
	})
	It("keeps the level out of the index by default, so a host that predates the field still reads it", func() {
		for _, e := range render("telegram", needsTwo) {
			Expect(e.Requires.FeatureLevel).To(BeZero())
		}
	})
	It("keeps the level of Tables out of the committed index shape, and writes it on request", func() {
		same := func(s string) string { return s }
		for _, e := range render("tables", same) {
			Expect(e.Requires.FeatureLevel).To(BeZero())
		}
		for _, e := range render("tables", same, "--feature-level-in-index") {
			Expect(e.Requires.FeatureLevel).To(Equal(uint64(2)))
		}
	})
	It("writes the level the manifest needs when asked to", func() {
		for _, e := range render("telegram", needsTwo, "--feature-level-in-index") {
			Expect(e.Requires.FeatureLevel).To(Equal(uint64(2)))
		}
	})
})
