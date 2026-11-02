// SPDX-License-Identifier: MIT
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestViewer(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Memory viewer") }

var _ = Describe("Generated viewer", func() {
	It("generates deterministic self-contained executions of every trace", func() {
		first, err := generate()
		Expect(err).NotTo(HaveOccurred())
		second, err := generate()
		Expect(err).NotTo(HaveOccurred())
		Expect(second).To(Equal(first))
		for _, field := range []string{"Before", "Event", "After", "Effects", "Rule", "Guard", "Ignored"} {
			Expect(string(first)).To(ContainSubstring(`"` + field + `"`))
		}
		Expect(string(first)).To(ContainSubstring("crash after backend commit before queue success"))
		Expect(string(first)).NotTo(ContainSubstring("<script src="))
	})
	It("checks fresh output and rejects stale or missing output without overwriting it", func() {
		dir, err := os.MkdirTemp("", "memory-viewer-")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.RemoveAll, dir)
		path := filepath.Join(dir, "viewer.html")
		var stderr bytes.Buffer
		Expect(run([]string{"--check", "--output", path}, &stderr)).NotTo(Succeed())
		Expect(run([]string{"--output", path}, &stderr)).To(Succeed())
		info, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()&0077).To(BeZero(), "new output must be private to its owner")
		original, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(run([]string{"--check", "--output", path}, &stderr)).To(Succeed())
		Expect(os.WriteFile(path, []byte("stale"), 0600)).To(Succeed())
		Expect(run([]string{"--check", "--output", path}, &stderr)).To(MatchError(ContainSubstring("stale viewer")))
		stale, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(stale)).To(Equal("stale"))
		Expect(run([]string{"--output", path}, &stderr)).To(Succeed())
		regenerated, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(regenerated).To(Equal(original))
	})
	It("rejects unknown flags and positional arguments", func() {
		var stderr bytes.Buffer
		Expect(run([]string{"--unknown"}, &stderr)).NotTo(Succeed())
		Expect(run([]string{"unexpected"}, &stderr)).NotTo(Succeed())
	})
})
