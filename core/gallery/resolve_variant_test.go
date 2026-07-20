package gallery_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"
)

var _ = Describe("VariantOption.EffectiveMemory", func() {
	It("reports no requirement when nothing was probed", func() {
		o := gallery.VariantOption{Variant: gallery.Variant{Model: "x"}}
		size, known := o.EffectiveMemory()
		Expect(known).To(BeFalse())
		Expect(size).To(Equal(uint64(0)))
	})

	It("uses the probed size", func() {
		o := gallery.VariantOption{Variant: gallery.Variant{Model: "x"}, ProbedMemory: 6 * 1024 * 1024 * 1024}
		size, known := o.EffectiveMemory()
		Expect(known).To(BeTrue())
		Expect(size).To(Equal(uint64(6 * 1024 * 1024 * 1024)))
	})

	It("treats a failed probe as unknown rather than as a zero requirement", func() {
		// A probe that could not reach the network reports 0. Reading that as
		// "needs nothing" would make an unreachable host look like the perfect
		// fit and hand the user the largest download on offer.
		o := gallery.VariantOption{Variant: gallery.Variant{Model: "x"}, ProbedMemory: 0}
		_, known := o.EffectiveMemory()
		Expect(known).To(BeFalse())
	})
})

var _ = Describe("SelectVariant", func() {
	gib := func(n uint64) uint64 { return n * 1024 * 1024 * 1024 }

	// Every size here is a probed one, because the probe is now the only source
	// a variant's footprint can come from. A zero stands for the probe having
	// been unable to tell, which is an unknown rather than a zero requirement.
	option := func(model, backend string, probed uint64) gallery.VariantOption {
		return gallery.VariantOption{
			Variant:      gallery.Variant{Model: model},
			Backend:      backend,
			ProbedMemory: probed,
		}
	}

	// The base is exempt from every filter, which the fallback specs below pin
	// down, but it is ranked against the variants like any other candidate, so
	// its size is load-bearing.
	base := func(model string, probed uint64) gallery.VariantOption {
		o := option(model, "llama-cpp", probed)
		o.IsBase = true
		return o
	}

	// linuxNvidia mirrors what SystemState.IsBackendCompatible does on a Linux
	// box with an NVIDIA card: Darwin-only engines are out, everything else runs.
	linuxNvidia := func(backend string) bool {
		return backend != "mlx" && backend != "mlx-vlm"
	}

	Describe("hardware filtering", func() {
		It("never selects a variant whose backend cannot run on this host", func() {
			// The MLX build is both the largest and the only thing that would
			// otherwise win, so nothing but the backend gate can reject it.
			options := []gallery.VariantOption{
				option("m-mlx-8bit", "mlx", gib(24)),
				option("m-gguf-q8", "llama-cpp", gib(12)),
				base("m-gguf-q4", gib(6)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
				AvailableMemory:   gib(80),
				BackendCompatible: linuxNvidia,
			}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-gguf-q8"))
		})

		It("selects the same variant on a host whose backend gate does admit it", func() {
			// The mirror image of the spec above, so the rejection is proven to
			// come from the host and not from something intrinsic to the entry.
			options := []gallery.VariantOption{
				option("m-mlx-8bit", "mlx", gib(24)),
				option("m-gguf-q8", "llama-cpp", gib(12)),
				base("m-gguf-q4", gib(6)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
				AvailableMemory:   gib(80),
				BackendCompatible: func(string) bool { return true },
			}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-mlx-8bit"))
		})

		It("treats every backend as runnable when the host cannot be inspected", func() {
			options := []gallery.VariantOption{option("m-mlx-8bit", "mlx", gib(24)), base("m-gguf-q4", gib(6))}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{AvailableMemory: gib(80)}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-mlx-8bit"))
		})
	})

	Describe("ranking", func() {
		It("picks the largest variant that fits, not the first authored", func() {
			// Authored smallest-first, so first-match would take m-q4 and any
			// ranking that ignores size would too.
			options := []gallery.VariantOption{
				option("m-q4", "llama-cpp", gib(6)),
				option("m-q8", "llama-cpp", gib(12)),
				option("m-f16", "llama-cpp", gib(24)),
				base("m-base", gib(2)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{AvailableMemory: gib(16)}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-q8"))
			Expect(selection.FellBackToBase).To(BeFalse())
		})

		It("picks the largest variant regardless of authored order", func() {
			// Same set, authored largest-first. Order must make no difference at
			// all, which is the entire point of dropping ordered first-match.
			options := []gallery.VariantOption{
				option("m-f16", "llama-cpp", gib(24)),
				option("m-q8", "llama-cpp", gib(12)),
				option("m-q4", "llama-cpp", gib(6)),
				base("m-base", gib(2)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{AvailableMemory: gib(16)}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-q8"))
		})

		It("prefers a known fit over a variant of unknown size", func() {
			// An unknown requirement is a guess. It survives the filter, because
			// nothing proves it does not fit, but it must never displace a
			// variant that is known to fit.
			options := []gallery.VariantOption{
				option("m-unknown", "llama-cpp", 0),
				option("m-q8", "llama-cpp", gib(12)),
				base("m-base", gib(2)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{AvailableMemory: gib(16)}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-q8"))
		})

		It("keeps a variant of unknown size rather than dropping it", func() {
			options := []gallery.VariantOption{
				option("m-unknown", "llama-cpp", 0),
				base("m-base", gib(2)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{AvailableMemory: gib(4)}, "")
			Expect(err).ToNot(HaveOccurred())
			// Surviving is observable through the rejection reasons: a dropped
			// variant is always accounted for there, and this one is not.
			Expect(selection.Reasons).ToNot(ContainElement(ContainSubstring("m-unknown")))
			// It survives, but it does not win: the base is a sized, guaranteed
			// payload and an unmeasurable variant is a guess.
			Expect(selection.Option.Variant.Model).To(Equal("m-base"))
			Expect(selection.FellBackToBase).To(BeFalse())
		})

		It("installs the base rather than an unsized variant on a host too small for either", func() {
			// The exact shape 241 of the current index entries have: a referenced
			// entry with no files and no size, whose probe can only answer
			// "unknown". Ranking it above the base would install an unmeasured
			// download on a machine with 2GiB, and would do so silently.
			options := []gallery.VariantOption{
				option("m-unknown", "llama-cpp", 0),
				base("m-base-q4", gib(4)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{AvailableMemory: gib(2)}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-base-q4"))
			Expect(selection.Option.IsBase).To(BeTrue())
		})

		It("selects the base when the base is the largest option that fits", func() {
			// A Q8 base offering a Q4 downgrade for small hosts is a natural
			// authoring shape. Treating the base as a last resort would install
			// the Q4 on every host large enough for the Q8 and permanently
			// downgrade the user.
			options := []gallery.VariantOption{
				option("m-q4", "llama-cpp", gib(4)),
				base("m-base-q8", gib(8)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{AvailableMemory: gib(16)}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-base-q8"))
			// The Q4 survived every filter, so this is the base winning on rank
			// and not the base being fallen back to.
			Expect(selection.FellBackToBase).To(BeFalse())
			Expect(selection.Reasons).To(BeEmpty())
		})

		It("selects a smaller variant when the base does not fit but the variant does", func() {
			// The mirror of the spec above: the base competes, it does not win by
			// default, so a host that cannot hold it must still take the downgrade
			// the entry offers for exactly that case.
			options := []gallery.VariantOption{
				option("m-q4", "llama-cpp", gib(4)),
				base("m-base-q8", gib(8)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{AvailableMemory: gib(6)}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-q4"))
		})

		It("reports why a probed size that does not fit was rejected", func() {
			// Ranking and filtering both run off the probe, so a rejection has to
			// be traceable back to the figure the probe returned.
			options := []gallery.VariantOption{
				option("m-q4", "llama-cpp", gib(6)),
				option("m-f16", "llama-cpp", gib(24)),
				option("m-q8", "llama-cpp", gib(12)),
				base("m-base", gib(2)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{AvailableMemory: gib(16)}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-q8"))
			Expect(selection.Reasons).To(ContainElement(ContainSubstring("m-f16")))
		})

		It("admits a variant needing exactly the memory available", func() {
			options := []gallery.VariantOption{option("m-q8", "llama-cpp", gib(12)), base("m-base", gib(2))}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{AvailableMemory: gib(12)}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-q8"))
		})
	})

	Describe("ranking by host engine preference", func() {
		// These are ENGINE NAMES, exactly what SystemState.EnginePreferenceTokens
		// reports for these hosts and exactly what a gallery entry's `backend:`
		// field holds. Build tags ("cuda", "rocm", "metal") belong to
		// BackendPreferenceTokens and would match no engine name here, which is
		// why every backend below is spelled as a real gallery engine.
		//
		// They are spelled out rather than read from the live machine so the
		// specs pin the intended behaviour on every CI runner.
		darwinMetal := []string{"mlx", "llama-cpp"}
		nvidia := []string{"vllm", "sglang", "llama-cpp"}

		// A Mac runs both engines, so nothing is filtered here and preference is
		// the only thing that can decide.
		darwinRunsEverything := func(string) bool { return true }

		It("prefers a vLLM build to a larger llama.cpp build on nvidia", func() {
			// The rule the engine table exists to express, and the one that was
			// silently inert while the ranker was fed build tags: no gallery
			// engine name contains "cuda", so every candidate scored equal and
			// the larger llama.cpp build won. Emptying the nvidia rule in
			// pkg/system fails this spec.
			options := []gallery.VariantOption{
				option("m-vllm-awq", "vllm", gib(8)),
				option("m-gguf-q8", "llama-cpp", gib(24)),
				base("m-gguf-q4", gib(4)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
				AvailableMemory:  gib(64),
				EnginePreference: nvidia,
			}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-vllm-awq"))
		})

		It("takes the larger llama.cpp build on the same nvidia host once preference is unknown", func() {
			// The mirror of the spec above, proving the vLLM win comes from the
			// preference list and not from anything intrinsic to the option set.
			// This is the state the whole feature was stuck in before the two
			// vocabularies were separated.
			options := []gallery.VariantOption{
				option("m-vllm-awq", "vllm", gib(8)),
				option("m-gguf-q8", "llama-cpp", gib(24)),
				base("m-gguf-q4", gib(4)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
				AvailableMemory: gib(64),
			}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-gguf-q8"))
		})

		It("ranks a vllm-omni build with vllm, since the token is a substring", func() {
			// Substring matching is deliberate: vllm-omni is a vLLM build and
			// must inherit vLLM's rank rather than fall through to unranked.
			options := []gallery.VariantOption{
				option("m-omni", "vllm-omni", gib(8)),
				option("m-gguf-q8", "llama-cpp", gib(24)),
				base("m-base", gib(4)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
				AvailableMemory:  gib(64),
				EnginePreference: nvidia,
			}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-omni"))
		})

		It("prefers an MLX build to a larger llama.cpp build on darwin", func() {
			options := []gallery.VariantOption{
				option("m-mlx-4bit", "mlx", gib(8)),
				option("m-gguf-q8", "llama-cpp", gib(24)),
				base("m-gguf-q4", gib(4)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
				AvailableMemory:   gib(64),
				BackendCompatible: darwinRunsEverything,
				EnginePreference:  darwinMetal,
			}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-mlx-4bit"))
		})

		It("takes the larger llama.cpp build on the same darwin host once preference is unknown", func() {
			// The mirror of the spec above, proving the MLX win comes from the
			// preference list and not from anything intrinsic to the option set.
			options := []gallery.VariantOption{
				option("m-mlx-4bit", "mlx", gib(8)),
				option("m-gguf-q8", "llama-cpp", gib(24)),
				base("m-gguf-q4", gib(4)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
				AvailableMemory:   gib(64),
				BackendCompatible: darwinRunsEverything,
			}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-gguf-q8"))
		})

		It("still picks the largest fitting build among equally preferred engines", func() {
			// Preference must not flatten size ordering: with one engine in
			// play there is nothing left for it to decide.
			options := []gallery.VariantOption{
				option("m-gguf-q4", "llama-cpp", gib(6)),
				option("m-gguf-q8", "llama-cpp", gib(12)),
				option("m-gguf-f16", "llama-cpp", gib(48)),
				base("m-base", gib(2)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
				AvailableMemory:  gib(16),
				EnginePreference: nvidia,
			}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-gguf-q8"))
		})

		It("does not let a preferred engine rescue a build that does not fit", func() {
			// Fit is a filter and preference is only a ranking among survivors.
			// The vLLM build is both preferred and too large, so the llama.cpp
			// build the host can actually hold has to win.
			options := []gallery.VariantOption{
				option("m-vllm-fp16", "vllm", gib(48)),
				option("m-gguf-q4", "llama-cpp", gib(6)),
				base("m-base", gib(2)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
				AvailableMemory:  gib(16),
				EnginePreference: nvidia,
			}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-gguf-q4"))
			Expect(selection.Reasons).To(ContainElement(ContainSubstring("m-vllm-fp16")))
		})

		It("orders engines absent from the table by size rather than dropping them", func() {
			// Neither engine appears in the nvidia rule. Nothing is discarded
			// and nothing is arbitrarily favoured; the host falls back to the
			// size-only behaviour it had before preference existed.
			//
			// The base is left unsized so it ranks in the tier below a proven
			// fit. It is a llama.cpp build, which the nvidia rule DOES rank, and
			// letting it compete in the same tier would prove preference works
			// rather than proving unlisted engines degrade to size.
			options := []gallery.VariantOption{
				option("m-diffusers", "diffusers", gib(12)),
				option("m-transformers", "transformers", gib(6)),
				base("m-base", 0),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
				AvailableMemory:  gib(16),
				EnginePreference: nvidia,
			}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-diffusers"))
			Expect(selection.Reasons).To(BeEmpty())
		})

		It("ranks sglang between vllm and llama-cpp on nvidia", func() {
			// A judgement call worth pinning: sglang is a GPU serving engine of
			// the same class as vllm, so it outranks the portable engine, but it
			// sits behind vllm.
			options := []gallery.VariantOption{
				option("m-vllm", "vllm", gib(4)),
				option("m-sglang", "sglang", gib(6)),
				option("m-gguf", "llama-cpp", gib(8)),
				base("m-base", gib(2)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
				AvailableMemory:  gib(16),
				EnginePreference: nvidia,
			}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-vllm"))

			withoutVLLM := []gallery.VariantOption{
				option("m-sglang", "sglang", gib(6)),
				option("m-gguf", "llama-cpp", gib(8)),
				base("m-base", gib(2)),
			}
			selection, err = gallery.SelectVariant(withoutVLLM, gallery.ResolveEnv{
				AvailableMemory:  gib(16),
				EnginePreference: nvidia,
			}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-sglang"))
		})

		It("still installs the base when nothing fits, whatever the host prefers", func() {
			options := []gallery.VariantOption{
				option("m-mlx-8bit", "mlx", gib(48)),
				option("m-gguf-q8", "llama-cpp", gib(24)),
				base("m-base", gib(64)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
				AvailableMemory:   gib(4),
				BackendCompatible: darwinRunsEverything,
				EnginePreference:  darwinMetal,
			}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-base"))
			Expect(selection.Option.IsBase).To(BeTrue())
			Expect(selection.FellBackToBase).To(BeTrue())
		})

		It("honors a pin for a less preferred backend", func() {
			// A pin is an operator override, so preference must not quietly
			// redirect it any more than the memory filter does.
			options := []gallery.VariantOption{
				option("m-mlx-4bit", "mlx", gib(8)),
				option("m-gguf-q8", "llama-cpp", gib(24)),
				base("m-base", gib(4)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
				AvailableMemory:   gib(64),
				BackendCompatible: darwinRunsEverything,
				EnginePreference:  darwinMetal,
			}, "m-gguf-q8")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-gguf-q8"))
		})
	})

	Describe("ranking by serving feature", func() {
		// These are SERVING FEATURES, exactly what
		// system.ServingFeaturePreferenceTokens reports, and a third vocabulary
		// after build tags and engine names. They are matched against a
		// variant's declared TAGS and against nothing else.
		//
		// Spelled out rather than read from pkg/system so these specs pin the
		// intended ordering rather than restating whatever the table says.
		features := []string{"dflash", "mtp"}
		nvidia := []string{"vllm", "sglang", "llama-cpp"}

		// A tag is the whole signal, so every candidate that is meant to carry
		// a feature declares it here. Candidates built with `option` carry no
		// tags and are therefore plain builds no matter what their name says,
		// which several specs below rely on.
		tagged := func(model string, probed uint64, tags ...string) gallery.VariantOption {
			o := option(model, "llama-cpp", probed)
			o.Tags = tags
			return o
		}

		It("prefers a speculative build to the plain build of the same weights", func() {
			// The rule the feature table exists to express. Both builds fit and
			// both run on the same engine, and the plain build is deliberately
			// the larger one, so without this axis size would take it and the
			// drafter pairing would never be installed. Emptying
			// ServingFeaturePreference fails this spec.
			options := []gallery.VariantOption{
				tagged("m-turbo-q4", gib(14), "llm", "dflash"),
				tagged("m-q8", gib(20), "llm"),
				base("m-q4", gib(6)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
				AvailableMemory:          gib(32),
				EnginePreference:         nvidia,
				ServingFeaturePreference: features,
			}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-turbo-q4"))
		})

		It("takes the larger plain build once the feature preference is unknown", func() {
			// The mirror of the spec above on the identical option set, proving
			// the DFlash win comes from the feature list rather than from
			// anything intrinsic to the set.
			options := []gallery.VariantOption{
				tagged("m-turbo-q4", gib(14), "llm", "dflash"),
				tagged("m-q8", gib(20), "llm"),
				base("m-q4", gib(6)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
				AvailableMemory:  gib(32),
				EnginePreference: nvidia,
			}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-q8"))
		})

		It("prefers dflash to mtp", func() {
			// Both are speculative pairings, so only the table's order can
			// separate them, and the MTP build is deliberately the larger one so
			// size cannot be what decides.
			options := []gallery.VariantOption{
				tagged("m-alpha-q8", gib(20), "llm", "mtp"),
				tagged("m-beta-q4", gib(14), "llm", "dflash"),
				base("m-q4", gib(6)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
				AvailableMemory:          gib(32),
				EnginePreference:         nvidia,
				ServingFeaturePreference: features,
			}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-beta-q4"))
		})

		It("does not let a preferred feature rescue a build that does not fit", func() {
			// A drafter pairing is strictly larger than the plain build, so this
			// is the ordinary case on a small host rather than a corner one. Fit
			// is a filter; the feature preference only ranks survivors.
			options := []gallery.VariantOption{
				tagged("m-turbo-f16", gib(48), "llm", "dflash"),
				tagged("m-q8", gib(12), "llm"),
				base("m-q4", gib(6)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
				AvailableMemory:          gib(16),
				EnginePreference:         nvidia,
				ServingFeaturePreference: features,
			}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-q8"))
			Expect(selection.Reasons).To(ContainElement(ContainSubstring("m-turbo-f16")))
		})

		It("lets the host engine preference outrank the serving feature", func() {
			// A serving feature makes the right engine faster; it does not make
			// a wrong engine right. On nvidia the plain vLLM build therefore
			// beats a DFlash llama.cpp build even though both fit and the
			// llama.cpp one is larger.
			options := []gallery.VariantOption{
				tagged("m-turbo-q8", gib(24), "llm", "dflash"),
				option("m-vllm-awq", "vllm", gib(8)),
				base("m-q4", gib(6)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
				AvailableMemory:          gib(64),
				EnginePreference:         nvidia,
				ServingFeaturePreference: features,
			}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-vllm-awq"))
		})

		It("still picks the largest fitting build among equally featured builds", func() {
			// Neither preference key may flatten size ordering: with one engine
			// and one feature in play there is nothing left for them to decide.
			options := []gallery.VariantOption{
				tagged("m-turbo-q4", gib(8), "llm", "dflash"),
				tagged("m-turbo-q8", gib(14), "llm", "dflash"),
				tagged("m-turbo-f16", gib(48), "llm", "dflash"),
				base("m-q4", gib(2)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
				AvailableMemory:          gib(16),
				EnginePreference:         nvidia,
				ServingFeaturePreference: features,
			}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-turbo-q8"))
		})

		It("leaves an unfeatured build ranked last rather than dropping it", func() {
			// Ranking never filters. With only plain builds on offer the axis
			// scores them uniformly and selection degrades to size alone.
			options := []gallery.VariantOption{
				option("m-q8", "llama-cpp", gib(12)),
				option("m-q4", "llama-cpp", gib(6)),
				base("m-q2", gib(3)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
				AvailableMemory:          gib(32),
				EnginePreference:         nvidia,
				ServingFeaturePreference: features,
			}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-q8"))
			Expect(selection.Reasons).To(BeEmpty())
		})

		Describe("reading the declared tags", func() {
			It("prefers a build whose tag declares the feature its name does not", func() {
				// The case tags exist for. "m-turbo-q8" carries MTP heads but
				// spells nothing in its name, which is the shape most of the
				// gallery's MTP entries had before they were tagged. It is also
				// the smaller build, so only the feature axis can lift it.
				options := []gallery.VariantOption{
					tagged("m-turbo-q8", gib(14), "llm", "gguf", "mtp"),
					tagged("m-plain-q8", gib(20), "llm", "gguf"),
					base("m-q4", gib(6)),
				}

				selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
					AvailableMemory:          gib(32),
					EnginePreference:         nvidia,
					ServingFeaturePreference: features,
				}, "")
				Expect(err).ToNot(HaveOccurred())
				Expect(selection.Option.Variant.Model).To(Equal("m-turbo-q8"))
			})

			It("does NOT prefer a build that only its name declares, with no tags at all", func() {
				// The inversion of the old name-fallback spec, and the
				// regression this change exists to guard. A name is
				// author-supplied free text and a naming convention is not a
				// contract: "m-nvfp4-mtp" is exactly the shape of the gallery's
				// NVFP4 entries, whose weights carry MTP heads while the entry
				// enables no speculative decoding at all, so ranking it as a
				// speculative build made it win the feature axis without being
				// any faster. With no tag it is a plain build and the larger
				// plain build takes it on size.
				options := []gallery.VariantOption{
					option("m-nvfp4-mtp", "llama-cpp", gib(14)),
					tagged("m-plain-q8", gib(20), "llm", "gguf"),
					base("m-q4", gib(6)),
				}

				selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
					AvailableMemory:          gib(32),
					EnginePreference:         nvidia,
					ServingFeaturePreference: features,
				}, "")
				Expect(err).ToNot(HaveOccurred())
				Expect(selection.Option.Variant.Model).To(Equal("m-plain-q8"))
			})

			It("lets a tagged build beat a larger one whose name says the same feature", func() {
				// The sharper form of the spec above: the name half is not
				// merely unnecessary, it is not consulted. The untagged
				// "m-dflash" would outrank the tagged MTP build on the old
				// name-first ordering, and is the larger build besides, so it
				// wins on either of the two ways the name could still be read.
				options := []gallery.VariantOption{
					option("m-dflash", "llama-cpp", gib(20)),
					tagged("m-turbo-q4", gib(14), "llm", "mtp"),
					base("m-q4", gib(6)),
				}

				selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
					AvailableMemory:          gib(32),
					EnginePreference:         nvidia,
					ServingFeaturePreference: features,
				}, "")
				Expect(err).ToNot(HaveOccurred())
				Expect(selection.Option.Variant.Model).To(Equal("m-turbo-q4"))
			})

			It("matches a tag regardless of the case it was written in", func() {
				// Gallery tags are author-supplied, so the same declaration
				// arrives in whatever case the author typed. A curator who
				// writes "MTP" has declared the feature just as plainly as one
				// who writes "mtp", and the sole signal must not turn on that.
				options := []gallery.VariantOption{
					tagged("m-turbo-q8", gib(14), "LLM", "MTP"),
					tagged("m-plain-q8", gib(20), "llm", "gguf"),
					base("m-q4", gib(6)),
				}

				selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
					AvailableMemory:          gib(32),
					EnginePreference:         nvidia,
					ServingFeaturePreference: features,
				}, "")
				Expect(err).ToNot(HaveOccurred())
				Expect(selection.Option.Variant.Model).To(Equal("m-turbo-q8"))
			})

			It("prefers dflash to mtp when both are declared by tag", func() {
				// The table's order has to survive on the tag path with both
				// features spelled out explicitly, and the MTP build is
				// deliberately the larger one so size cannot be what decides.
				options := []gallery.VariantOption{
					tagged("m-alpha-q8", gib(20), "llm", "mtp"),
					tagged("m-beta-q8", gib(14), "llm", "dflash"),
					base("m-q4", gib(6)),
				}

				selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
					AvailableMemory:          gib(32),
					EnginePreference:         nvidia,
					ServingFeaturePreference: features,
				}, "")
				Expect(err).ToNot(HaveOccurred())
				Expect(selection.Option.Variant.Model).To(Equal("m-beta-q8"))
			})

			It("does not read a feature out of an unrelated tag", func() {
				// Tags are compared whole, so a tag that merely contains a
				// feature token declares nothing. "multimodal" contains no
				// feature; "smtp" does contain "mtp" as a substring and must
				// not count either. The mail model is the larger build, so a
				// false positive would hand it the win.
				options := []gallery.VariantOption{
					tagged("m-mail-q8", gib(24), "llm", "multimodal", "smtp"),
					tagged("m-turbo-q4", gib(14), "llm", "mtp"),
					base("m-q4", gib(6)),
				}

				selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
					AvailableMemory:          gib(64),
					EnginePreference:         nvidia,
					ServingFeaturePreference: features,
				}, "")
				Expect(err).ToNot(HaveOccurred())
				Expect(selection.Option.Variant.Model).To(Equal("m-turbo-q4"))
			})

			It("does not let a tag rescue a build that does not fit", func() {
				// Fit still outranks the feature axis, whichever signal
				// declared it.
				options := []gallery.VariantOption{
					tagged("m-turbo-f16", gib(48), "llm", "mtp"),
					tagged("m-plain-q8", gib(12), "llm"),
					base("m-q4", gib(6)),
				}

				selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
					AvailableMemory:          gib(16),
					EnginePreference:         nvidia,
					ServingFeaturePreference: features,
				}, "")
				Expect(err).ToNot(HaveOccurred())
				Expect(selection.Option.Variant.Model).To(Equal("m-plain-q8"))
				Expect(selection.Reasons).To(ContainElement(ContainSubstring("m-turbo-f16")))
			})

			It("lets the host engine preference outrank a tagged serving feature", func() {
				// Engine beats feature no matter which signal carried the
				// feature: a tag does not make a wrong engine right.
				options := []gallery.VariantOption{
					tagged("m-turbo-q8", gib(24), "llm", "mtp"),
					option("m-vllm-awq", "vllm", gib(8)),
					base("m-q4", gib(6)),
				}

				selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
					AvailableMemory:          gib(64),
					EnginePreference:         nvidia,
					ServingFeaturePreference: features,
				}, "")
				Expect(err).ToNot(HaveOccurred())
				Expect(selection.Option.Variant.Model).To(Equal("m-vllm-awq"))
			})

			It("ignores tags entirely when no feature preference is configured", func() {
				// The axis stops discriminating with an empty list, tags or no
				// tags, and selection degrades to size alone as it always did.
				options := []gallery.VariantOption{
					tagged("m-turbo-q8", gib(14), "llm", "mtp"),
					tagged("m-plain-q8", gib(20), "llm"),
					base("m-q4", gib(6)),
				}

				selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
					AvailableMemory:  gib(32),
					EnginePreference: nvidia,
				}, "")
				Expect(err).ToNot(HaveOccurred())
				Expect(selection.Option.Variant.Model).To(Equal("m-plain-q8"))
			})
		})
	})

	Describe("falling back to the base", func() {
		It("selects the base when nothing else fits", func() {
			options := []gallery.VariantOption{
				option("m-q8", "llama-cpp", gib(12)),
				option("m-f16", "llama-cpp", gib(24)),
				base("m-base", gib(2)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{AvailableMemory: gib(4)}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-base"))
			Expect(selection.Option.IsBase).To(BeTrue())
			Expect(selection.FellBackToBase).To(BeTrue())
			Expect(selection.Reasons).To(HaveLen(2))
		})

		It("selects the base even when the base does not fit either", func() {
			// The base is exempt from the memory filter, not merely favoured by
			// it: there is nothing below it, so refusing here would make an entry
			// every older client installs fine uninstallable on newer ones.
			options := []gallery.VariantOption{option("m-q8", "llama-cpp", gib(12)), base("m-base", gib(2))}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{AvailableMemory: 0}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-base"))
			Expect(selection.Option.IsBase).To(BeTrue())
			Expect(selection.FellBackToBase).To(BeTrue())
		})

		It("prefers the base to an unsized variant even when the base itself is unsized", func() {
			// Neither can be shown to fit, so nothing separates them on size. The
			// base is still the payload the entry is guaranteed to install.
			options := []gallery.VariantOption{
				option("m-unknown", "llama-cpp", 0),
				base("m-base", 0),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{AvailableMemory: gib(8)}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-base"))
		})

		It("explains why each variant was rejected", func() {
			options := []gallery.VariantOption{
				option("m-mlx", "mlx", gib(8)),
				option("m-f16", "llama-cpp", gib(24)),
				base("m-base", gib(2)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
				AvailableMemory:   gib(4),
				BackendCompatible: linuxNvidia,
			}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Reasons).To(ContainElement(ContainSubstring("cannot run on this system")))
			Expect(selection.Reasons).To(ContainElement(ContainSubstring("24.0GiB")))
		})

		It("errors when the caller supplies no base at all", func() {
			options := []gallery.VariantOption{option("m-f16", "llama-cpp", gib(24))}

			_, err := gallery.SelectVariant(options, gallery.ResolveEnv{AvailableMemory: gib(4)}, "")
			Expect(err).To(MatchError(gallery.ErrNoVariantMatch))
		})
	})

	Describe("explicit selection", func() {
		It("honors a pin the hardware would never have chosen", func() {
			options := []gallery.VariantOption{
				option("m-mlx", "mlx", gib(64)),
				base("m-base", gib(2)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
				AvailableMemory:   gib(4),
				BackendCompatible: linuxNvidia,
			}, "m-mlx")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-mlx"))
		})

		It("honors a pin naming the base, declining an upgrade that fits", func() {
			options := []gallery.VariantOption{option("m-f16", "llama-cpp", gib(8)), base("m-base", gib(2))}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{AvailableMemory: gib(64)}, "m-base")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-base"))
		})

		It("matches a pin case-insensitively", func() {
			options := []gallery.VariantOption{option("m-f16", "llama-cpp", gib(8)), base("m-base", gib(2))}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{AvailableMemory: gib(64)}, "M-F16")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-f16"))
		})

		It("fails loudly when the pin names nothing in the list", func() {
			options := []gallery.VariantOption{option("m-f16", "llama-cpp", gib(8)), base("m-base", gib(2))}

			_, err := gallery.SelectVariant(options, gallery.ResolveEnv{AvailableMemory: gib(64)}, "m-gone")
			Expect(err).To(MatchError(gallery.ErrPinNotFound))
			Expect(err.Error()).To(ContainSubstring("m-gone"))
		})
	})
})

var _ = Describe("HostResolveEnv engine preference wiring", func() {
	// The specs above feed SelectVariant a hand-written token list, which proves
	// the ranker but not that the host actually reaches the engine table. These
	// drive the REAL table through the REAL wiring, so emptying
	// engineNamePreferenceRules in pkg/system, or reverting this field to
	// BackendPreferenceTokens, fails here.
	var origEnv, origRunFileEnv string
	const capabilityEnv = "LOCALAI_FORCE_META_BACKEND_CAPABILITY"
	const capabilityRunFileEnv = "LOCALAI_FORCE_META_BACKEND_CAPABILITY_RUN_FILE"
	// What getSystemCapabilities reports for a host with no usable accelerator.
	const noGPUCapability = "default"

	BeforeEach(func() {
		origEnv = os.Getenv(capabilityEnv)
		origRunFileEnv = os.Getenv(capabilityRunFileEnv)
	})

	AfterEach(func() {
		if origEnv != "" {
			Expect(os.Setenv(capabilityEnv, origEnv)).To(Succeed())
		} else {
			Expect(os.Unsetenv(capabilityEnv)).To(Succeed())
		}
		if origRunFileEnv != "" {
			Expect(os.Setenv(capabilityRunFileEnv, origRunFileEnv)).To(Succeed())
		} else {
			Expect(os.Unsetenv(capabilityRunFileEnv)).To(Succeed())
		}
	})

	envFor := func(capability string) gallery.ResolveEnv {
		GinkgoHelper()
		Expect(os.Setenv(capabilityEnv, capability)).To(Succeed())
		return gallery.HostResolveEnv(context.Background(), &system.SystemState{})
	}

	It("hands the ranker engine names an NVIDIA host's gallery entries can match", func() {
		preference := envFor("nvidia-cuda-12").EnginePreference
		Expect(preference).To(Equal([]string{"vllm", "sglang", "llama-cpp"}))
	})

	It("installs the vLLM build over a larger llama.cpp one on a real NVIDIA host", func() {
		// End to end on the live table: the exact behaviour that was silently
		// dead while the ranker was fed build tags.
		env := envFor("nvidia-cuda-12")
		env.AvailableMemory = 64 * 1024 * 1024 * 1024

		options := []gallery.VariantOption{
			{Variant: gallery.Variant{Model: "m-vllm"}, Backend: "vllm", ProbedMemory: 8 * 1024 * 1024 * 1024},
			{Variant: gallery.Variant{Model: "m-gguf"}, Backend: "llama-cpp", ProbedMemory: 24 * 1024 * 1024 * 1024},
		}

		selection, err := gallery.SelectVariant(options, env, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(selection.Option.Variant.Model).To(Equal("m-vllm"))
	})

	It("installs the MLX build over a larger llama.cpp one on a real darwin host", func() {
		env := envFor("metal")
		env.AvailableMemory = 64 * 1024 * 1024 * 1024
		// The real gate rejects mlx off darwin, and this spec is about ranking.
		env.BackendCompatible = func(string) bool { return true }

		options := []gallery.VariantOption{
			{Variant: gallery.Variant{Model: "m-mlx"}, Backend: "mlx", ProbedMemory: 8 * 1024 * 1024 * 1024},
			{Variant: gallery.Variant{Model: "m-gguf"}, Backend: "llama-cpp", ProbedMemory: 24 * 1024 * 1024 * 1024},
		}

		selection, err := gallery.SelectVariant(options, env, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(selection.Option.Variant.Model).To(Equal("m-mlx"))
	})

	It("installs the llama.cpp build over a larger vLLM one on a host with no GPU", func() {
		// The hole this rule closes. IsBackendCompatible keys on the engine
		// name, and "vllm" carries no darwin/cuda/rocm/sycl token, so a vLLM
		// build is NOT filtered out here. Emptying the default rule in
		// pkg/system puts the larger vLLM build back on a CPU-only box.
		env := envFor(noGPUCapability)
		env.AvailableMemory = 64 * 1024 * 1024 * 1024

		options := []gallery.VariantOption{
			{Variant: gallery.Variant{Model: "m-vllm"}, Backend: "vllm", ProbedMemory: 24 * 1024 * 1024 * 1024},
			{Variant: gallery.Variant{Model: "m-gguf"}, Backend: "llama-cpp", ProbedMemory: 8 * 1024 * 1024 * 1024},
		}

		selection, err := gallery.SelectVariant(options, env, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(selection.Option.Variant.Model).To(Equal("m-gguf"))
	})

	It("installs the llama.cpp build over a larger vLLM one on an intel mac", func() {
		env := envFor("darwin-x86")
		env.AvailableMemory = 64 * 1024 * 1024 * 1024

		options := []gallery.VariantOption{
			{Variant: gallery.Variant{Model: "m-vllm"}, Backend: "vllm", ProbedMemory: 24 * 1024 * 1024 * 1024},
			{Variant: gallery.Variant{Model: "m-gguf"}, Backend: "llama-cpp", ProbedMemory: 8 * 1024 * 1024 * 1024},
		}

		selection, err := gallery.SelectVariant(options, env, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(selection.Option.Variant.Model).To(Equal("m-gguf"))
	})

	It("still installs a vLLM build on a host with no GPU when it is the only one offered", func() {
		// Preference ORDERS survivors, it never filters them. Demoting vLLM must
		// not make a model published only as a vLLM build uninstallable.
		env := envFor(noGPUCapability)
		env.AvailableMemory = 64 * 1024 * 1024 * 1024

		options := []gallery.VariantOption{
			{Variant: gallery.Variant{Model: "m-vllm"}, Backend: "vllm", ProbedMemory: 8 * 1024 * 1024 * 1024},
		}

		selection, err := gallery.SelectVariant(options, env, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(selection.Option.Variant.Model).To(Equal("m-vllm"))
	})

	It("prefers llama.cpp on a GPU host with too little VRAM to serve from", func() {
		// This host has a GPU, yet getSystemCapabilities reports "default"
		// because it is under the 4 GiB floor. Driven through the real detector
		// rather than a forced capability, so that mapping is exercised too.
		if runtime.GOOS == "darwin" {
			Skip("darwin reports metal or darwin-x86 before the VRAM floor is consulted")
		}
		Expect(os.Unsetenv(capabilityEnv)).To(Succeed())
		// A capability run file on the machine would override detection.
		Expect(os.Setenv(capabilityRunFileEnv, filepath.Join(GinkgoT().TempDir(), "absent"))).To(Succeed())

		state := &system.SystemState{GPUVendor: system.Nvidia, VRAM: 2 * 1024 * 1024 * 1024}
		Expect(state.DetectedCapability()).To(Equal(noGPUCapability))

		env := gallery.HostResolveEnv(context.Background(), state)
		env.AvailableMemory = 64 * 1024 * 1024 * 1024

		options := []gallery.VariantOption{
			{Variant: gallery.Variant{Model: "m-vllm"}, Backend: "vllm", ProbedMemory: 24 * 1024 * 1024 * 1024},
			{Variant: gallery.Variant{Model: "m-gguf"}, Backend: "llama-cpp", ProbedMemory: 8 * 1024 * 1024 * 1024},
		}

		selection, err := gallery.SelectVariant(options, env, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(selection.Option.Variant.Model).To(Equal("m-gguf"))
	})

	It("carries no build tag into the ranker, whatever the host", func() {
		// The wiring-level lock. BackendPreferenceTokens returns build tags for
		// every capability, so if it is ever wired back into this field, one of
		// these tags shows up here.
		for _, capability := range []string{"nvidia-cuda-12", "amd", "intel", "metal", "vulkan", "default"} {
			Expect(envFor(capability).EnginePreference).ToNot(
				ContainElements("cuda", "rocm", "hip", "sycl", "metal", "cpu", "darwin-x86"),
				"capability %q leaked a build tag into the variant ranker", capability)
		}
	})
})
