package main

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Verify", func() {
	write := func(body string) string {
		dir := GinkgoT().TempDir()
		p := filepath.Join(dir, "index.yaml")
		Expect(os.WriteFile(p, []byte(body), 0o600)).To(Succeed())
		return p
	}

	It("passes a sound index", func() {
		Expect(Verify(write(`
- name: parent
  variants:
    - model: child
  files:
    - filename: a.gguf
      sha256: aa
      uri: https://example.com/a.gguf
- name: child
  files:
    - filename: b.gguf
      sha256: bb
      uri: https://example.com/b.gguf
`))).To(BeEmpty())
	})

	It("reports a variant pointing at a missing entry", func() {
		Expect(Verify(write(`
- name: parent
  variants:
    - model: ghost
  files:
    - filename: a.gguf
      sha256: aa
      uri: https://example.com/a.gguf
`))).To(ContainElement(ContainSubstring("ghost")))
	})

	It("reports a variant that itself declares variants", func() {
		Expect(Verify(write(`
- name: parent
  variants:
    - model: child
  files:
    - filename: a.gguf
      sha256: aa
      uri: https://example.com/a.gguf
- name: child
  variants:
    - model: grandchild
  files:
    - filename: b.gguf
      sha256: bb
      uri: https://example.com/b.gguf
- name: grandchild
  files:
    - filename: c.gguf
      sha256: cc
      uri: https://example.com/c.gguf
`))).To(ContainElement(ContainSubstring("declares variants of its own")))
	})

	It("reports duplicate entry names", func() {
		Expect(Verify(write(`
- name: dup
  files:
    - filename: a.gguf
      sha256: aa
      uri: https://example.com/a.gguf
- name: dup
  files:
    - filename: b.gguf
      sha256: bb
      uri: https://example.com/b.gguf
`))).To(ContainElement(ContainSubstring("duplicate entry name")))
	})

	It("reports a file with no sha256", func() {
		Expect(Verify(write(`
- name: one
  files:
    - filename: a.gguf
      uri: https://example.com/a.gguf
`))).To(ContainElement(ContainSubstring("no sha256")))
	})

	It("reports an entry tagged dflash without a matching spec_type", func() {
		Expect(Verify(write(`
- name: liar
  tags:
    - dflash
  overrides:
    options:
      - use_jinja:true
  files:
    - filename: a.gguf
      sha256: aa
      uri: https://example.com/a.gguf
`))).To(ContainElement(ContainSubstring("tagged dflash")))
	})

	It("reports an entry configuring spec_type without the tag", func() {
		Expect(Verify(write(`
- name: shy
  overrides:
    options:
      - spec_type:draft-mtp
  files:
    - filename: a.gguf
      sha256: aa
      uri: https://example.com/a.gguf
`))).To(ContainElement(ContainSubstring("not tagged mtp")))
	})

	// The flat-match branch in unsloth.go appends every match, so a repo
	// publishing both a plain and a UD Q8_0 renders one entry holding two full
	// models while model: points at only the first.
	It("reports an entry holding more than one non-shard weight file", func() {
		Expect(Verify(write(`
- name: greedy
  overrides:
    options:
      - use_jinja:true
    parameters:
      model: llama-cpp/models/repo/Model-Q8_0.gguf
  files:
    - filename: llama-cpp/models/repo/Model-Q8_0.gguf
      sha256: aa
      uri: https://example.com/a.gguf
    - filename: llama-cpp/models/repo/Model-UD-Q8_0.gguf
      sha256: bb
      uri: https://example.com/b.gguf
`))).To(ContainElement(ContainSubstring("more than one weight file")))
	})

	It("accepts many shards alongside an mmproj and a drafter", func() {
		Expect(Verify(write(`
- name: sharded
  tags:
    - mtp
  overrides:
    options:
      - spec_type:draft-mtp
    mmproj: llama-cpp/mmproj/repo/mm.gguf
    draft_model: llama-cpp/models/repo/Model-draft.gguf
  files:
    - filename: llama-cpp/models/repo/Model-00001-of-00002.gguf
      sha256: aa
      uri: https://example.com/a.gguf
    - filename: llama-cpp/models/repo/Model-00002-of-00002.gguf
      sha256: bb
      uri: https://example.com/b.gguf
    - filename: llama-cpp/mmproj/repo/mm.gguf
      sha256: cc
      uri: https://example.com/c.gguf
    - filename: llama-cpp/models/repo/Model-draft.gguf
      sha256: dd
      uri: https://example.com/d.gguf
`))).To(BeEmpty())
	})
})

var _ = Describe("UnaccountedQuants", func() {
	// A quant published only as root-level shards matches neither branch in
	// DiscoverUnslothQuants, so without this diagnostic the build would vanish
	// from a batch run with nothing said about it.
	It("reports a wanted quant upstream publishes but discovery dropped", func() {
		files := []GGUFFile{
			{Name: "Model-UD-Q4_K_M-00001-of-00003.gguf", SHA256: "aa"},
			{Name: "Model-UD-Q4_K_M-00002-of-00003.gguf", SHA256: "bb"},
			{Name: "Model-UD-Q4_K_M-00003-of-00003.gguf", SHA256: "cc"},
		}

		Expect(UnaccountedQuants(files, DiscoverUnslothQuants(files))).
			To(ContainElement(ContainSubstring("UD-Q4_K_M")))
	})

	It("says nothing when every published wanted quant produced a build", func() {
		files := []GGUFFile{
			{Name: "Model-UD-Q4_K_M.gguf", SHA256: "aa"},
			{Name: "UD-Q6_K/Model-UD-Q6_K-00001-of-00002.gguf", SHA256: "bb"},
			{Name: "UD-Q6_K/Model-UD-Q6_K-00002-of-00002.gguf", SHA256: "cc"},
		}

		Expect(UnaccountedQuants(files, DiscoverUnslothQuants(files))).To(BeEmpty())
	})

	It("says nothing about a wanted quant the repo does not publish at all", func() {
		files := []GGUFFile{{Name: "Model-UD-Q4_K_M.gguf", SHA256: "aa"}}

		Expect(UnaccountedQuants(files, DiscoverUnslothQuants(files))).To(BeEmpty())
	})
})
