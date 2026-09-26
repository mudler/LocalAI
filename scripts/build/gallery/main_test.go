// SPDX-License-Identifier: MIT
package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGalleryPackage(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Gallery packaging") }

var _ = Describe("Gallery packaging", func() {
	It("packages both official indexes with every repository-local base available offline", func() {
		for _, source := range []string{"gallery", "backend"} {
			out := GinkgoT().TempDir()
			Expect(packageGallery("../../..", source, out)).To(Succeed())
			body, err := os.ReadFile(filepath.Join(out, "index.yaml"))
			Expect(err).ToNot(HaveOccurred())
			var entries []map[string]any
			Expect(yaml.Unmarshal(body, &entries)).To(Succeed())
			Expect(entries).ToNot(BeEmpty())
			for _, entry := range entries {
				url, _ := entry["url"].(string)
				Expect(url).ToNot(HavePrefix("github:mudler/LocalAI/"))
				if strings.HasPrefix(url, "gallery/") {
					Expect(filepath.Join(out, url)).To(BeAnExistingFile())
				}
			}
		}
	})
	It("bundles local base configs and preserves external URLs and YAML aliases", func() {
		root := GinkgoT().TempDir()
		Expect(os.MkdirAll(filepath.Join(root, "gallery"), 0755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(root, "gallery/base.yaml"), []byte("backend: llama-cpp\n"), 0644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(root, "gallery/index.yaml"), []byte("- &base\n  name: first\n  url: github:mudler/LocalAI/gallery/base.yaml@master\n- <<: *base\n  name: second\n- name: external\n  url: https://example.com/config.yaml\n"), 0644)).To(Succeed())
		out := filepath.Join(root, "out")
		Expect(packageGallery(root, "gallery", out)).To(Succeed())
		data, err := os.ReadFile(filepath.Join(out, "index.yaml"))
		Expect(err).ToNot(HaveOccurred())
		var entries []map[string]any
		Expect(yaml.Unmarshal(data, &entries)).To(Succeed())
		Expect(entries[0]["url"]).To(Equal("gallery/base.yaml"))
		Expect(entries[1]["url"]).To(Equal("gallery/base.yaml"))
		Expect(entries[2]["url"]).To(Equal("https://example.com/config.yaml"))
		body, err := os.ReadFile(filepath.Join(out, "gallery/base.yaml"))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(body)).To(Equal("backend: llama-cpp\n"))
	})
	It("fails if a referenced config is missing or escapes the repository", func() {
		for _, ref := range []string{"missing.yaml", "../../outside.yaml"} {
			root := GinkgoT().TempDir()
			Expect(os.Mkdir(filepath.Join(root, "gallery"), 0755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(root, "gallery/index.yaml"), []byte("- name: broken\n  url: github:mudler/LocalAI/gallery/"+ref+"@master\n"), 0644)).To(Succeed())
			Expect(packageGallery(root, "gallery", filepath.Join(root, "out"))).ToNot(Succeed())
		}
	})
	It("rejects symlink escapes when reading configs or writing the bundle", func() {
		for _, location := range []string{"source", "output"} {
			root, out, outside := GinkgoT().TempDir(), GinkgoT().TempDir(), GinkgoT().TempDir()
			for _, dir := range []string{filepath.Join(root, "gallery"), filepath.Join(out, "gallery")} {
				Expect(os.Mkdir(dir, 0700)).To(Succeed())
			}
			index := []byte("- name: test\n  url: github:mudler/LocalAI/gallery/base.yaml@master\n")
			Expect(os.WriteFile(filepath.Join(root, "gallery/index.yaml"), index, 0600)).To(Succeed())
			outsideFile := filepath.Join(outside, "base.yaml")
			Expect(os.WriteFile(outsideFile, []byte("outside"), 0600)).To(Succeed())
			link := filepath.Join(root, "gallery/base.yaml")
			if location == "output" {
				Expect(os.WriteFile(link, []byte("inside"), 0600)).To(Succeed())
				link = filepath.Join(out, "gallery/base.yaml")
			}
			Expect(os.Symlink(outsideFile, link)).To(Succeed())
			Expect(packageGallery(root, "gallery", out)).ToNot(Succeed(), location)
			data, err := os.ReadFile(outsideFile)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(data)).To(Equal("outside"))
		}
	})
})
