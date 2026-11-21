// SPDX-License-Identifier: MIT
package packaging

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const hostOpsFlag = "--host-operations-in-index"

var _ = Describe("host operations in the library index", func() {
	renderIndex := func(id string, extra ...string) []byte {
		GinkgoHelper()
		p := newProjectFor(id, both)
		out := filepath.Join(GinkgoT().TempDir(), "catalog.yaml")
		_, err := run(append([]string{"catalog", "--config", p.config, "--root", p.root, "--template", "../../../gallery/addons/" + id + ".yaml.tmpl", "--from-manifest", "--repository", newRegistry().repo("addons"), "--channel", "development", "--owner-repo", "example/localai", "--signer-ref", "refs/heads/master", "--out", out}, extra...)...)
		Expect(err).NotTo(HaveOccurred(), id)
		raw, err := os.ReadFile(out)
		Expect(err).NotTo(HaveOccurred())
		return raw
	}
	manifestOps := func(id string) *contract.HostOperations {
		GinkgoHelper()
		data, err := os.ReadFile(filepath.Join("../../..", "addons", id, "manifest.json"))
		Expect(err).NotTo(HaveOccurred())
		m, err := contract.ParseManifest(data)
		Expect(err).NotTo(HaveOccurred(), id)
		want := &contract.HostOperations{}
		if ops := m.Capabilities.HostOperations; ops != nil {
			want.Required = slices.Sorted(slices.Values(ops.Required))
			want.Optional = slices.Sorted(slices.Values(ops.Optional))
		}
		return want
	}

	It("records the sorted host operations of every manifest on its entries when asked to", func() {
		for _, id := range firstPartyAddons {
			want := manifestOps(id)
			entries, err := readCatalogIndex(renderIndex(id, hostOpsFlag))
			Expect(err).NotTo(HaveOccurred(), id)
			Expect(entries).NotTo(BeEmpty(), id)
			for _, e := range entries {
				Expect(e.HostOperations).NotTo(BeNil(), id)
				Expect(*e.HostOperations).To(Equal(*want), id)
			}
		}
	})
	It("reads the rendered hint through the library reader of the host", func() {
		for _, id := range []string{"sandbox", "web", "tables"} {
			want := manifestOps(id)
			releases, err := catalog.DecodeIndex(renderIndex(id, hostOpsFlag))
			Expect(err).NotTo(HaveOccurred(), id)
			Expect(releases).NotTo(BeEmpty(), id)
			for _, r := range releases {
				Expect(r.HostOperations).NotTo(BeNil(), id)
				Expect(*r.HostOperations).To(Equal(*want), id)
			}
		}
	})
	It("renders an empty declaration for a manifest that requests none, and sorts the names", func() {
		Expect(hostOperationsFieldsYAML(nil, "  ")).To(Equal("  hostOperations: {}\n"))
		Expect(hostOperationsFieldsYAML(&contract.HostOperations{}, "")).To(Equal("hostOperations: {}\n"))
		got := hostOperationsFieldsYAML(&contract.HostOperations{Required: []string{"b.x", "a.y"}, Optional: []string{"d.z", "c.z"}}, "  ")
		Expect(got).To(Equal("  hostOperations:\n    required:\n      - \"a.y\"\n      - \"b.x\"\n    optional:\n      - \"c.z\"\n      - \"d.z\"\n"))
		Expect(hostOperationsFieldsYAML(&contract.HostOperations{Optional: []string{"c.z"}}, "")).To(Equal("hostOperations:\n  optional:\n    - \"c.z\"\n"))
	})
	It("reads an index that predates the hint, with no hint rather than an empty one", func() {
		raw := regexp.MustCompile(`(?m)^  hostOperations:.*\n(    .*\n)*`).ReplaceAll(renderIndex("sandbox", hostOpsFlag), nil)
		Expect(string(raw)).NotTo(ContainSubstring("hostOperations"))
		releases, err := catalog.DecodeIndex(raw)
		Expect(err).NotTo(HaveOccurred())
		Expect(releases).NotTo(BeEmpty())
		for _, r := range releases {
			Expect(r.HostOperations).To(BeNil())
		}
		entries, err := readCatalogIndex(raw)
		Expect(err).NotTo(HaveOccurred())
		for _, e := range entries {
			Expect(e.HostOperations).To(BeNil())
		}
	})
	It("keeps the host operations out of the index by default, so a host that predates the field still reads it", func() {
		for _, id := range []string{"sandbox", "tables"} {
			raw := renderIndex(id)
			Expect(string(raw)).NotTo(ContainSubstring("hostOperations"), id)
			entries, err := readCatalogIndex(raw)
			Expect(err).NotTo(HaveOccurred(), id)
			for _, e := range entries {
				Expect(e.HostOperations).To(BeNil(), id)
			}
		}
	})
	It("carries the hint of its manifest on every entry of the committed development index", func() {
		raw, err := os.ReadFile("../../../gallery/addons/index.yaml")
		Expect(err).NotTo(HaveOccurred())
		entries, err := readCatalogIndex(raw)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).NotTo(BeEmpty())
		for _, e := range entries {
			Expect(e.HostOperations).NotTo(BeNil(), e.ID)
			Expect(*e.HostOperations).To(Equal(*manifestOps(e.ID)), e.ID)
		}
	})
	It("adds the host operations to an existing index only when asked to, and removes them again", func() {
		raw, err := os.ReadFile("../../../gallery/addons/index.yaml")
		Expect(err).NotTo(HaveOccurred())
		index := filepath.Join(GinkgoT().TempDir(), "index.yaml")
		Expect(os.WriteFile(index, raw, 0o600)).To(Succeed())
		_, err = run("catalog-tools", "--index", index, "--addons", "../../../addons", hostOpsFlag)
		Expect(err).NotTo(HaveOccurred())
		with, err := os.ReadFile(index)
		Expect(err).NotTo(HaveOccurred())
		entries, err := readCatalogIndex(with)
		Expect(err).NotTo(HaveOccurred())
		for _, e := range entries {
			Expect(e.HostOperations).NotTo(BeNil(), e.ID)
			Expect(*e.HostOperations).To(Equal(*manifestOps(e.ID)), e.ID)
		}
		_, err = run("catalog-tools", "--index", index, "--addons", "../../../addons", hostOpsFlag)
		Expect(err).NotTo(HaveOccurred())
		twice, _ := os.ReadFile(index)
		Expect(string(twice)).To(Equal(string(with)))
		_, err = run("catalog-tools", "--index", index, "--addons", "../../../addons")
		Expect(err).NotTo(HaveOccurred())
		without, _ := os.ReadFile(index)
		stripped := regexp.MustCompile(`(?m)^  hostOperations:.*\n(    .*\n)*`).ReplaceAll(raw, nil)
		Expect(string(stripped)).NotTo(ContainSubstring("hostOperations"))
		Expect(string(without)).To(Equal(string(stripped)), "without the flag the hint is removed and nothing else changes")
	})
})
