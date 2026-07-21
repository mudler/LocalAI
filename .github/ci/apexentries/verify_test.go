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
    backend: llama-cpp
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
    backend: llama-cpp
    options:
      - spec_type:draft-mtp
  files:
    - filename: a.gguf
      sha256: aa
      uri: https://example.com/a.gguf
`))).To(ContainElement(ContainSubstring("not tagged mtp")))
	})

	// ds4 carries the MTP heads in the weights and names them with mtp_path, so
	// the rule holds there in a different vocabulary rather than not at all.
	It("reports a ds4 entry configuring mtp_path without the tag", func() {
		Expect(Verify(write(`
- name: ds4-shy
  overrides:
    backend: ds4
    options:
      - mtp_path:model-mtp.gguf
      - mtp_draft:2
  files:
    - filename: a.gguf
      sha256: aa
      uri: https://example.com/a.gguf
`))).To(ContainElement(ContainSubstring("not tagged mtp")))
	})

	It("reports a ds4 entry tagged mtp that configures no mtp_path", func() {
		Expect(Verify(write(`
- name: ds4-liar
  tags:
    - mtp
  overrides:
    backend: ds4
    options:
      - context_size:4096
  files:
    - filename: a.gguf
      sha256: aa
      uri: https://example.com/a.gguf
`))).To(ContainElement(ContainSubstring("tagged mtp")))
	})

	It("accepts a ds4 entry that both configures mtp_path and carries the tag", func() {
		Expect(Verify(write(`
- name: ds4-honest
  tags:
    - mtp
  overrides:
    backend: ds4
    options:
      - mtp_path:model-mtp.gguf
  files:
    - filename: a.gguf
      sha256: aa
      uri: https://example.com/a.gguf
`))).To(BeEmpty())
	})

	// sglang declares speculative_algorithm in the referenced gallery/*.yaml,
	// which Verify never reads, so it may not judge such an entry either way.
	It("says nothing about an sglang entry tagged mtp", func() {
		Expect(Verify(write(`
- name: sglang-mtp
  tags:
    - mtp
  overrides:
    backend: sglang
  files: []
`))).To(BeEmpty())
	})

	It("says nothing about the tag on an entry with no declared backend", func() {
		Expect(Verify(write(`
- name: templated
  tags:
    - mtp
  files:
    - filename: a.gguf
      sha256: aa
      uri: https://example.com/a.gguf
`))).To(BeEmpty())
	})

	// The flat-match branch in unsloth.go appends every match, so a repo
	// publishing both a plain and a UD Q8_0 renders one entry holding two full
	// models while model: points at only the first.
	It("reports an entry holding more than one non-shard weight file", func() {
		Expect(Verify(write(`
- name: greedy
  overrides:
    backend: llama-cpp
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
    backend: llama-cpp
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

	// Multi-component TTS and ASR engines legitimately ship an encoder, a
	// tokenizer, a vocoder and so on as one model, so the collision the weight
	// count catches does not exist for them.
	It("accepts a multi-component non-llama-cpp entry declaring five weights", func() {
		Expect(Verify(write(`
- name: multi
  overrides:
    backend: qwen3-tts-cpp
  files:
    - filename: talker.gguf
      sha256: aa
      uri: https://example.com/a.gguf
    - filename: tokenizer.gguf
      sha256: bb
      uri: https://example.com/b.gguf
    - filename: vocoder.gguf
      sha256: cc
      uri: https://example.com/c.gguf
    - filename: encoder.gguf
      sha256: dd
      uri: https://example.com/d.gguf
    - filename: vae.gguf
      sha256: ee
      uri: https://example.com/e.gguf
`))).To(BeEmpty())
	})

	It("says nothing about the weight count of an entry with no declared backend", func() {
		Expect(Verify(write(`
- name: templated-weights
  files:
    - filename: model-Q4_K_M.gguf
      sha256: aa
      uri: https://example.com/a.gguf
    - filename: model-mmproj-f16.gguf
      sha256: bb
      uri: https://example.com/b.gguf
`))).To(BeEmpty())
	})

	It("says nothing about an auxiliary metadata file carrying no sha256", func() {
		Expect(Verify(write(`
- name: aux
  files:
    - filename: a.gguf
      sha256: aa
      uri: https://example.com/a.gguf
    - filename: params.json
      sha256: ""
      uri: https://example.com/params.json
`))).To(BeEmpty())
	})

	// safetensors weights are downloaded and loaded exactly like GGUF weights,
	// so an unverified one is the same supply-chain hole.
	It("reports a safetensors weight carrying no sha256", func() {
		Expect(Verify(write(`
- name: vae
  files:
    - filename: wan_2.1_vae.safetensors
      sha256: ""
      uri: https://example.com/vae.safetensors
`))).To(ContainElement(ContainSubstring("no sha256")))
	})

	It("says nothing about a txt or md file carrying no sha256", func() {
		Expect(Verify(write(`
- name: docs
  files:
    - filename: notes.txt
      sha256: ""
      uri: https://example.com/notes.txt
    - filename: README.md
      sha256: ""
      uri: https://example.com/README.md
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

	// UD-Q8_0 is its own quant label and is not a wanted one. Reading it as a
	// publication of Q8_0 is the substring collision this diagnostic exists to
	// warn about, and subdirectory-sharded UD quants are the normal unsloth
	// layout for large repos, so the false positive would fire on every batch.
	It("does not read a subdirectory-sharded UD-Q8_0 as a published Q8_0", func() {
		files := []GGUFFile{
			{Name: "UD-Q8_0/Model-UD-Q8_0-00001-of-00002.gguf", SHA256: "aa"},
			{Name: "UD-Q8_0/Model-UD-Q8_0-00002-of-00002.gguf", SHA256: "bb"},
		}

		Expect(UnaccountedQuants(files, DiscoverUnslothQuants(files))).To(BeEmpty())
	})

	// A quant in its own subdirectory but not shard-numbered matches neither
	// branch of DiscoverUnslothQuants, so it is genuinely published and
	// genuinely undiscovered.
	It("reports a wanted quant published in its own subdirectory without shard numbering", func() {
		files := []GGUFFile{{Name: "Q8_0/Model-Q8_0.gguf", SHA256: "aa"}}

		Expect(UnaccountedQuants(files, DiscoverUnslothQuants(files))).
			To(ContainElement(ContainSubstring("quant Q8_0 is published upstream")))
	})

	// builds is empty on purpose: it isolates the file-to-quant match from
	// whatever DiscoverUnslothQuants would have made of the same file.
	It("matches the flat single-file layout", func() {
		files := []GGUFFile{{Name: "Model-Q8_0.gguf", SHA256: "aa"}}

		Expect(UnaccountedQuants(files, nil)).
			To(ConsistOf(ContainSubstring("quant Q8_0 is published upstream")))
	})
})
