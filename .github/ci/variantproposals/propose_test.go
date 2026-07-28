package main

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// entryYAML writes one gallery entry with a single weight file, which is the
// shape almost every real entry has. Specs that need something else write the
// YAML out by hand.
func entryYAML(name, repo, filename, sha string) string {
	return fmt.Sprintf(`- name: %s
  url: github:mudler/LocalAI/gallery/virtual.yaml@master
  overrides:
    parameters:
      model: %s
  files:
    - filename: %s
      uri: huggingface://%s/%s
      sha256: %s
`, name, filename, filename, repo, filename, sha)
}

func indexOf(entries ...string) *Index {
	ix, err := ParseIndex(strings.Join(entries, ""))
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return ix
}

// familyNames flattens a result into "parent <- variant, variant" strings, the
// form the specs assert against.
func familyNames(r *Result) []string {
	out := make([]string, 0, len(r.Families))
	for _, f := range r.Families {
		names := make([]string, 0, len(f.Proposals))
		for _, p := range f.Proposals {
			names = append(names, p.Variant)
		}
		out = append(out, f.Parent+" <- "+strings.Join(names, ", "))
	}
	return out
}

func refusalReasons(r *Result) string {
	var b strings.Builder
	for _, ref := range r.Refusals {
		b.WriteString(strings.Join(ref.Members, " + ") + ": " + ref.Reason + "\n")
	}
	return b.String()
}

func suppressionReasons(r *Result) string {
	var b strings.Builder
	for _, s := range r.Suppressed {
		b.WriteString(s.String() + "\n")
	}
	return b.String()
}

var _ = Describe("Propose", func() {
	Describe("the grouping signals", func() {
		It("groups entries whose names differ only by a quantization marker", func() {
			ix := indexOf(
				entryYAML("foo-model", "acme/foo-GGUF", "foo-model-Q4_K_M.gguf", "aa"),
				entryYAML("foo-model-q8_0", "acme/foo-GGUF", "foo-model-Q8_0.gguf", "bb"),
			)
			r := Propose(ix, nil)
			Expect(familyNames(r)).To(ConsistOf("foo-model <- foo-model-q8_0"))
			Expect(r.Families[0].Proposals[0].Evidence.Signals).To(ContainElement(SignalName))
			Expect(r.Families[0].Proposals[0].Evidence.SharedStem).To(Equal("foo-model"))
			Expect(r.Families[0].Proposals[0].Evidence.QuantTokens).To(ContainElements("q4_k_m", "q8_0"))
		})

		It("groups entries that use the colon config-suffix convention", func() {
			ix := indexOf(
				entryYAML("bar-model", "acme/bar-GGUF", "bar-model-Q4_K_M.gguf", "aa"),
				entryYAML("bar-model:grammar-functioncall", "acme/bar-GGUF", "bar-model-Q4_K_M-grammar.gguf", "bb"),
			)
			r := Propose(ix, nil)
			Expect(familyNames(r)).To(ConsistOf("bar-model <- bar-model:grammar-functioncall"))
			Expect(r.Families[0].Proposals[0].Evidence.Signals).To(ContainElement(SignalConfigSuffix))
		})

		It("groups entries whose own weight file is the same file at another quantization", func() {
			// The names share no stem, so only the filename signal can link
			// these two.
			ix := indexOf(
				entryYAML("omni-cpp", "Serveurperso/Omni-GGUF", "omnivoice-base-Q8_0.gguf", "aa"),
				entryYAML("omni-cpp-hq", "Serveurperso/Omni-GGUF", "omnivoice-base-BF16.gguf", "bb"),
			)
			r := Propose(ix, nil)
			Expect(familyNames(r)).To(ConsistOf("omni-cpp <- omni-cpp-hq"))
			ev := r.Families[0].Proposals[0].Evidence
			Expect(ev.Signals).To(ConsistOf(SignalWeightFile))
			Expect(ev.SharedFile).To(Equal("omnivoice-base"))
			Expect(ev.SharedRepo).To(Equal("serveurperso/omni-gguf"))
		})

		It("does not let a shared auxiliary file link unrelated models", func() {
			// Both entries ship the same text encoder. That is a packaging
			// convention, not evidence of shared weights: this is how an
			// earlier sweep linked four wan-2.1 entries to each other.
			ix := indexOf(`- name: wan-2.1-t2v
  url: u
  files:
    - filename: wan-2.1-t2v-Q4_K_M.gguf
      uri: huggingface://acme/wan/wan-2.1-t2v-Q4_K_M.gguf
      sha256: aa
    - filename: umt5-xxl-encoder-Q8_0.gguf
      uri: huggingface://acme/wan/umt5-xxl-encoder-Q8_0.gguf
      sha256: cc
`, `- name: z-image-turbo
  url: u
  files:
    - filename: z-image-turbo-Q4_K_M.gguf
      uri: huggingface://acme/wan/z-image-turbo-Q4_K_M.gguf
      sha256: bb
    - filename: umt5-xxl-encoder-Q8_0.gguf
      uri: huggingface://acme/wan/umt5-xxl-encoder-Q8_0.gguf
      sha256: cc
`)
			r := Propose(ix, nil)
			Expect(familyNames(r)).To(BeEmpty())
		})

		It("does not treat a shared filename in two different repos as evidence", func() {
			// A finetune republished under the base model's filename is the
			// most common way this signal misfires.
			ix := indexOf(
				entryYAML("llama-3.2-3b-instruct", "hugging-quants/Llama-3.2-3B-Instruct-GGUF", "llama-3.2-3b-instruct-q4_k_m.gguf", "aa"),
				entryYAML("llama-3.2-3b-shiro-roleplay", "someone/Shiro-GGUF", "Llama-3.2-3B-Instruct.Q8_0.gguf", "bb"),
			)
			r := Propose(ix, nil)
			Expect(familyNames(r)).To(BeEmpty())
		})
	})

	Describe("what must never be proposed", func() {
		It("does not group different parameter sizes that share a prefix", func() {
			ix := indexOf(
				entryYAML("qwen3-tts-cpp-0.6b-base", "Serveurperso/Qwen3-TTS-GGUF", "qwen3-tts-talker-Q4_K_M.gguf", "aa"),
				entryYAML("qwen3-tts-cpp-1.7b-base", "Serveurperso/Qwen3-TTS-GGUF", "qwen3-tts-talker-Q8_0.gguf", "bb"),
			)
			r := Propose(ix, nil)
			Expect(familyNames(r)).To(BeEmpty())
			Expect(suppressionReasons(r)).To(ContainSubstring("different parameter sizes"))
		})

		It("does not group the Gemma effective sizes", func() {
			ix := indexOf(
				entryYAML("gemma-4-e2b-it", "google/gemma-GGUF", "gemma-4-it-Q4_K_M.gguf", "aa"),
				entryYAML("gemma-4-e4b-it", "google/gemma-GGUF", "gemma-4-it-Q8_0.gguf", "bb"),
			)
			r := Propose(ix, nil)
			Expect(familyNames(r)).To(BeEmpty())
			Expect(suppressionReasons(r)).To(ContainSubstring("different parameter sizes"))
		})

		It("does not group entries with a byte-identical install payload", func() {
			// whisper-1 exists so OpenAI-compatible clients can send that name.
			// Folding it under whisper-base would hide the name they send.
			payload := `  url: github:mudler/LocalAI/gallery/whisper-base.yaml@master
  overrides:
    parameters:
      model: ggml-whisper-base.bin
  files:
    - filename: ggml-whisper-base.bin
      uri: huggingface://ggerganov/whisper.cpp/ggml-base.bin
      sha256: aa
`
			ix := indexOf("- name: whisper-base\n"+payload, "- name: whisper-1\n"+payload)
			r := Propose(ix, nil)
			Expect(familyNames(r)).To(BeEmpty())
			Expect(r.AliasSkipped).To(HaveLen(1))
			Expect(r.AliasSkipped[0].Reason).To(ContainSubstring("aliases"))
		})

		DescribeTable("declines the categories the ledger records",
			func(nameA, nameB string, ledgerYAML string) {
				ix := indexOf(
					entryYAML(nameA, "acme/repo", "shared-weights-Q4_K_M.gguf", "aa"),
					entryYAML(nameB, "acme/repo", "shared-weights-Q8_0.gguf", "bb"),
				)
				ledger, err := ParseLedger([]byte(ledgerYAML))
				Expect(err).ToNot(HaveOccurred())

				// Without the ledger these would be proposed, which is what
				// makes the ledger load bearing rather than decorative.
				Expect(familyNames(Propose(ix, nil))).ToNot(BeEmpty())

				r := Propose(ix, ledger)
				Expect(familyNames(r)).To(BeEmpty())
				Expect(r.Suppressed).To(HaveLen(1))
			},
			Entry("a finetune", "base-model", "base-model-abliterated",
				"tokens:\n  - {token: abliterated, reason: finetune}\n"),
			Entry("a distill", "base-model", "base-model-distilled",
				"tokens:\n  - {token: distilled, reason: distilled}\n"),
			Entry("English-only versus multilingual ASR", "whisper-small", "whisper-small-en",
				"pairs:\n  - {parent: whisper-small, variant: whisper-small-en, reason: English-only versus multilingual}\n"),
			Entry("two products sharing a prefix", "vibevoice-cpp", "vibevoice-cpp-asr",
				"pairs:\n  - {parent: vibevoice-cpp, variant: vibevoice-cpp-asr, reason: different products}\n"),
			Entry("a per-language release", "kokoros-de", "kokoros-ja",
				"groups:\n  - {members: [kokoros, kokoros-de, kokoros-ja], reason: different languages}\n"),
		)

		It("reports the ledger's reason so its effect stays visible", func() {
			ix := indexOf(
				entryYAML("base-model", "acme/repo", "shared-weights-Q4_K_M.gguf", "aa"),
				entryYAML("base-model-heretic", "acme/repo", "shared-weights-Q8_0.gguf", "bb"),
			)
			ledger, err := ParseLedger([]byte("tokens:\n  - {token: heretic, reason: \"finetune, not a re-quantization\"}\n"))
			Expect(err).ToNot(HaveOccurred())
			r := Propose(ix, ledger)
			Expect(suppressionReasons(r)).To(ContainSubstring("finetune, not a re-quantization"))
			Expect(suppressionReasons(r)).To(ContainSubstring(`token "heretic"`))
		})
	})

	Describe("parent selection", func() {
		It("picks the bare-named entry when one exists", func() {
			ix := indexOf(
				entryYAML("base-model-q8_0", "acme/repo", "base-model-Q8_0.gguf", "aa"),
				entryYAML("base-model", "acme/repo", "base-model-Q4_K_M.gguf", "bb"),
				entryYAML("base-model-f16", "acme/repo", "base-model-f16.gguf", "cc"),
			)
			r := Propose(ix, nil)
			Expect(familyNames(r)).To(ConsistOf("base-model <- base-model-f16, base-model-q8_0"))
		})

		It("picks the smallest build when no entry is bare-named", func() {
			ix := indexOf(
				entryYAML("ced-base-f16", "acme/repo", "ced-base-f16.gguf", "aa"),
				entryYAML("ced-base-q8", "acme/repo", "ced-base-Q8_0.gguf", "bb"),
			)
			r := Propose(ix, nil)
			Expect(familyNames(r)).To(ConsistOf("ced-base-q8 <- ced-base-f16"))
		})

		It("judges the smallest build by the quantization in the model filename, not the name", func() {
			// The names carry no marker at all; only the filenames say which
			// build is which.
			ix := indexOf(
				entryYAML("thing-hq", "acme/repo", "thing-weights-BF16.gguf", "aa"),
				entryYAML("thing-lite", "acme/repo", "thing-weights-Q4_K_M.gguf", "bb"),
			)
			r := Propose(ix, nil)
			Expect(familyNames(r)).To(ConsistOf("thing-lite <- thing-hq"))
		})
	})

	Describe("the rules a proposal has to respect", func() {
		It("refuses to nest: a target that already offers variants of its own", func() {
			ix := indexOf(
				entryYAML("nest-model", "acme/repo", "nest-model-Q4_K_M.gguf", "aa"),
				`- name: nest-model-q8_0
  url: u
  variants:
    - model: nest-model-q8_0-mtp
  overrides:
    parameters:
      model: nest-model-Q8_0.gguf
  files:
    - filename: nest-model-Q8_0.gguf
      uri: huggingface://acme/repo/nest-model-Q8_0.gguf
      sha256: bb
`,
				entryYAML("nest-model-q8_0-mtp", "other/repo", "nest-model-mtp.gguf", "cc"),
			)
			r := Propose(ix, nil)
			Expect(familyNames(r)).To(BeEmpty())
			Expect(refusalReasons(r)).To(ContainSubstring("already offers variants of its own"))
		})

		It("refuses to nest: a parent that is already somebody else's variant", func() {
			ix := indexOf(
				`- name: outer
  url: u
  variants:
    - model: middle
  overrides:
    parameters:
      model: outer-Q4_K_M.gguf
  files:
    - filename: outer-Q4_K_M.gguf
      uri: huggingface://acme/repo/outer-Q4_K_M.gguf
      sha256: aa
`,
				entryYAML("middle", "acme/other", "middle-Q4_K_M.gguf", "bb"),
				entryYAML("middle-q8_0", "acme/other", "middle-Q8_0.gguf", "cc"),
			)
			r := Propose(ix, nil)
			Expect(familyNames(r)).To(BeEmpty())
			Expect(refusalReasons(r)).To(ContainSubstring("would nest variants"))
		})

		It("refuses to let two parents claim one target", func() {
			ix := indexOf(
				`- name: claimant
  url: u
  variants:
    - model: contested-q8_0
  overrides:
    parameters:
      model: claimant-Q4_K_M.gguf
  files:
    - filename: claimant-Q4_K_M.gguf
      uri: huggingface://acme/repo/claimant-Q4_K_M.gguf
      sha256: aa
`,
				entryYAML("contested", "acme/other", "contested-Q4_K_M.gguf", "bb"),
				entryYAML("contested-q8_0", "acme/other", "contested-Q8_0.gguf", "cc"),
			)
			r := Propose(ix, nil)
			Expect(familyNames(r)).To(BeEmpty())
			Expect(refusalReasons(r)).To(ContainSubstring("already a variant of"))
		})

		It("refuses a target that is not independently installable", func() {
			ix := indexOf(
				entryYAML("stub-model", "acme/repo", "stub-model-Q4_K_M.gguf", "aa"),
				"- name: stub-model-q8_0\n  description: a stanza nobody finished\n",
			)
			r := Propose(ix, nil)
			Expect(familyNames(r)).To(BeEmpty())
			Expect(refusalReasons(r)).To(ContainSubstring("not independently installable"))
		})

		It("refuses a family whose parent defines a merge anchor, naming the entries that would inherit", func() {
			ix := indexOf(
				`- &anchored
  name: anchored-model
  url: u
  overrides:
    parameters:
      model: anchored-Q4_K_M.gguf
  files:
    - filename: anchored-Q4_K_M.gguf
      uri: huggingface://acme/repo/anchored-Q4_K_M.gguf
      sha256: aa
`,
				`- !!merge <<: *anchored
  name: anchored-child
  variants: []
  overrides:
    parameters:
      model: unrelated-child-Q4_K_M.gguf
  files:
    - filename: unrelated-child-Q4_K_M.gguf
      uri: huggingface://other/repo/unrelated-child-Q4_K_M.gguf
      sha256: cc
`,
				entryYAML("anchored-model-q8_0", "acme/repo", "anchored-Q8_0.gguf", "bb"),
			)
			r := Propose(ix, nil)
			Expect(familyNames(r)).To(BeEmpty())
			Expect(refusalReasons(r)).To(ContainSubstring("defines YAML anchor &anchored"))
			Expect(refusalReasons(r)).To(ContainSubstring("anchored-child"))
			Expect(refusalReasons(r)).To(ContainSubstring("variants: []"))
		})

		It("refuses an entry whose name is not unique in the gallery", func() {
			ix := indexOf(
				entryYAML("twin", "acme/repo", "twin-Q4_K_M.gguf", "aa"),
				entryYAML("twin", "acme/repo", "twin-Q4_K_M.gguf", "aa"),
				entryYAML("twin-q8_0", "acme/repo", "twin-Q8_0.gguf", "bb"),
			)
			r := Propose(ix, nil)
			Expect(familyNames(r)).To(BeEmpty())
			Expect(refusalReasons(r)).To(ContainSubstring("appears more than once"))
		})

		It("says nothing about a pair that is already grouped", func() {
			ix := indexOf(
				`- name: settled
  url: u
  variants:
    - model: settled-q8_0
  overrides:
    parameters:
      model: settled-Q4_K_M.gguf
  files:
    - filename: settled-Q4_K_M.gguf
      uri: huggingface://acme/repo/settled-Q4_K_M.gguf
      sha256: aa
`,
				entryYAML("settled-q8_0", "acme/repo", "settled-Q8_0.gguf", "bb"),
			)
			r := Propose(ix, nil)
			Expect(r.HasProposals()).To(BeFalse())
			Expect(r.Refusals).To(BeEmpty())
			Expect(r.Suppressed).To(BeEmpty())
		})

		It("adds only the missing members to a family that already exists", func() {
			ix := indexOf(
				`- name: partial
  url: u
  variants:
    - model: partial-q8_0
  overrides:
    parameters:
      model: partial-Q4_K_M.gguf
  files:
    - filename: partial-Q4_K_M.gguf
      uri: huggingface://acme/repo/partial-Q4_K_M.gguf
      sha256: aa
`,
				entryYAML("partial-q8_0", "acme/repo", "partial-Q8_0.gguf", "bb"),
				entryYAML("partial-f16", "acme/repo", "partial-f16.gguf", "cc"),
			)
			r := Propose(ix, nil)
			Expect(familyNames(r)).To(ConsistOf("partial <- partial-f16"))
		})
	})

	It("does not modify the index it was given", func() {
		text := entryYAML("foo-model", "acme/foo-GGUF", "foo-model-Q4_K_M.gguf", "aa") +
			entryYAML("foo-model-q8_0", "acme/foo-GGUF", "foo-model-Q8_0.gguf", "bb")
		ix, err := ParseIndex(text)
		Expect(err).ToNot(HaveOccurred())
		before := strings.Join(ix.Lines, "\n")
		Propose(ix, nil)
		Expect(strings.Join(ix.Lines, "\n")).To(Equal(before))
	})
})
