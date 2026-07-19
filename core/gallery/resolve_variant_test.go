package gallery_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery"
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

	Describe("ranking by host backend preference", func() {
		// The token lists below are exactly what
		// SystemState.BackendPreferenceTokens reports for these hosts. They are
		// spelled out rather than read from the live machine so the specs pin
		// the intended behaviour on every CI runner.
		darwinMetal := []string{"mlx", "metal", "cpu"}
		nvidia := []string{"cuda", "vulkan", "cpu"}

		// A Mac runs both engines, so nothing is filtered here and preference is
		// the only thing that can decide.
		darwinRunsEverything := func(string) bool { return true }

		It("prefers an MLX build to a larger llama.cpp build on darwin", func() {
			options := []gallery.VariantOption{
				option("m-mlx-4bit", "mlx", gib(8)),
				option("m-gguf-q8", "llama-cpp", gib(24)),
				base("m-gguf-q4", gib(4)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
				AvailableMemory:   gib(64),
				BackendCompatible: darwinRunsEverything,
				BackendPreference: darwinMetal,
			}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-mlx-4bit"))
		})

		It("takes the larger llama.cpp build on the same host once preference is unknown", func() {
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

		It("prefers a CUDA build to a larger CPU-only build on nvidia", func() {
			options := []gallery.VariantOption{
				option("m-cuda-q4", "cuda12-llama-cpp", gib(8)),
				option("m-cpu-q8", "cpu-llama-cpp", gib(24)),
				base("m-base", gib(4)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
				AvailableMemory:   gib(64),
				BackendPreference: nvidia,
			}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-cuda-q4"))
		})

		It("still picks the largest fitting build among equally preferred backends", func() {
			// Preference must not flatten size ordering: with one runtime in
			// play there is nothing left for it to decide.
			options := []gallery.VariantOption{
				option("m-cuda-q4", "cuda12-llama-cpp", gib(6)),
				option("m-cuda-q8", "cuda12-llama-cpp", gib(12)),
				option("m-cuda-f16", "cuda12-llama-cpp", gib(48)),
				base("m-base", gib(2)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
				AvailableMemory:   gib(16),
				BackendPreference: nvidia,
			}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-cuda-q8"))
		})

		It("does not let a preferred backend rescue a build that does not fit", func() {
			// Fit is a filter and preference is only a ranking among survivors.
			// The CUDA build is both preferred and too large, so the CPU build
			// the host can actually hold has to win.
			options := []gallery.VariantOption{
				option("m-cuda-f16", "cuda12-llama-cpp", gib(48)),
				option("m-cpu-q4", "cpu-llama-cpp", gib(6)),
				base("m-base", gib(2)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
				AvailableMemory:   gib(16),
				BackendPreference: nvidia,
			}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-cpu-q4"))
			Expect(selection.Reasons).To(ContainElement(ContainSubstring("m-cuda-f16")))
		})

		It("orders unrecognised backends by size rather than dropping them", func() {
			// Neither engine name carries an nvidia token. Nothing is discarded
			// and nothing is arbitrarily favoured; the host falls back to the
			// size-only behaviour it had before preference existed.
			options := []gallery.VariantOption{
				option("m-vllm-awq", "vllm", gib(12)),
				option("m-sglang-fp8", "sglang", gib(6)),
				base("m-base", gib(2)),
			}

			selection, err := gallery.SelectVariant(options, gallery.ResolveEnv{
				AvailableMemory:   gib(16),
				BackendPreference: nvidia,
			}, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-vllm-awq"))
			Expect(selection.Reasons).To(BeEmpty())
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
				BackendPreference: darwinMetal,
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
				BackendPreference: darwinMetal,
			}, "m-gguf-q8")
			Expect(err).ToNot(HaveOccurred())
			Expect(selection.Option.Variant.Model).To(Equal("m-gguf-q8"))
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
