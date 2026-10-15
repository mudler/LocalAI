// SPDX-License-Identifier: MIT
package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.yaml.in/yaml/v2"
	"os"
	"path/filepath"
	"strings"
)

var _ = Describe("GEM-X distribution", func() {
	It("publishes every CI variant in the backend gallery", func() {
		root := "../../.."
		var matrix struct {
			Linux  []map[string]string `yaml:"include"`
			Darwin []map[string]string `yaml:"includeDarwin"`
		}
		data, err := os.ReadFile(filepath.Join(root, ".github/backend-matrix.yml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(yaml.Unmarshal(data, &matrix)).To(Succeed())
		var entries []struct {
			Name         string            `yaml:"name"`
			URI          string            `yaml:"uri"`
			Capabilities map[string]string `yaml:"capabilities"`
		}
		data, err = os.ReadFile(filepath.Join(root, "backend/index.yaml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(yaml.Unmarshal(data, &entries)).To(Succeed())
		images := map[string]bool{}
		names := map[string]bool{}
		var aliases map[string]string
		for _, e := range entries {
			images[e.URI] = true
			names[e.Name] = true
			if e.Name == "gemxcpp" {
				aliases = e.Capabilities
			}
		}
		count := 0
		for _, e := range append(matrix.Linux, matrix.Darwin...) {
			if e["backend"] != "gemxcpp" {
				continue
			}
			count++
			for _, version := range []string{"latest", "master"} {
				Expect(images["quay.io/go-skynet/local-ai-backends:"+version+e["tag-suffix"]]).To(BeTrue())
			}
		}
		Expect(count).To(Equal(5))
		Expect(aliases).NotTo(BeEmpty())
		for _, name := range aliases {
			Expect(names[name]).To(BeTrue())
		}
		Expect(strings.HasPrefix(aliases["metal"], "cpu-darwin")).To(BeTrue())
	})
})
