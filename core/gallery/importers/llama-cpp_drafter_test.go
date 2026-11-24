package importers_test

import (
	"encoding/json"
	"fmt"
	"strings"

	. "github.com/mudler/LocalAI/core/gallery/importers"
	hfapi "github.com/mudler/LocalAI/pkg/huggingface-api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The listing is modeled on unsloth/Inkling-2-Mini-GGUF: ten quants at the
// root, a folder of non-GGUF files, three projectors and a DSpark drafter in
// two builds. The drafter sits after the models, as an alphabetical listing
// puts it, so the old "last group wins" fallback would pick it.
var _ = Describe("LlamaCPPImporter speculative drafters", func() {
	const repoBase = "https://huggingface.co/acme/Inkling-GGUF/resolve/main/"

	hfFile := func(path string) hfapi.ModelFile {
		return hfapi.ModelFile{Path: path, SHA256: "sha-" + path, URL: repoBase + path}
	}
	files := func(paths ...string) []hfapi.ModelFile {
		out := make([]hfapi.ModelFile, 0, len(paths))
		for _, p := range paths {
			out = append(out, hfFile(p))
		}
		return out
	}
	imp := func(prefs string, listing []hfapi.ModelFile) (string, []string) {
		d := Details{
			URI:         "https://huggingface.co/acme/Inkling-GGUF",
			HuggingFace: &hfapi.ModelDetails{ModelID: "acme/Inkling-GGUF", Files: listing},
		}
		if prefs != "" {
			d.Preferences = json.RawMessage(prefs)
		}
		cfg, err := (&LlamaCPPImporter{}).Import(d)
		Expect(err).ToNot(HaveOccurred())
		names := make([]string, 0, len(cfg.Files))
		for _, f := range cfg.Files {
			names = append(names, f.Filename)
		}
		return cfg.ConfigFile, names
	}

	inkling := func() []hfapi.ModelFile {
		return files(
			"README.md",
			"Inkling-2-Mini-UD-IQ2_M.gguf",
			"Inkling-2-Mini-UD-Q2_K_XL.gguf",
			"Inkling-2-Mini-UD-Q3_K_M.gguf",
			"Inkling-2-Mini-UD-Q3_K_XL.gguf",
			"Inkling-2-Mini-UD-Q4_K_M.gguf",
			"Inkling-2-Mini-UD-Q4_K_XL.gguf",
			"Inkling-2-Mini-UD-Q5_K_M.gguf",
			"Inkling-2-Mini-UD-Q6_K.gguf",
			"Inkling-2-Mini-UD-Q8_K_XL.gguf",
			"Inkling-2-Mini-BF16.gguf",
			"Mainline_Support/Inkling-2-Mini-UD-Q4_K_M.gguf_file",
			"Mainline_Support/Inkling-2-Mini-UD-Q8_K_XL.gguf_file",
			"dspark-Inkling-2-Mini-Q8_0.gguf",
			"dspark/dspark-Inkling-2-Mini-BF16.gguf",
			"mmproj-BF16.gguf",
			"mmproj-F16.gguf",
			"mmproj-F32.gguf",
		)
	}

	const dir = "llama-cpp/models/"

	It("pairs the Q8_0 DSpark drafter with the default model and the F16 projector", func() {
		cfg, names := imp(`{}`, inkling())

		name := "Inkling-2-Mini-UD-Q4_K_M"
		Expect(names).To(Equal([]string{
			dir + name + "/Inkling-2-Mini-UD-Q4_K_M.gguf",
			"llama-cpp/mmproj/" + name + "/mmproj-F16.gguf",
			dir + name + "/dspark-Inkling-2-Mini-Q8_0.gguf",
		}))
		Expect(cfg).To(ContainSubstring("model: " + dir + name + "/Inkling-2-Mini-UD-Q4_K_M.gguf"))
		Expect(cfg).To(ContainSubstring("mmproj: llama-cpp/mmproj/" + name + "/mmproj-F16.gguf"))
		Expect(cfg).To(ContainSubstring("draft_model: " + dir + name + "/dspark-Inkling-2-Mini-Q8_0.gguf"))
		Expect(cfg).To(ContainSubstring("spec_type:draft-dspark"))
		Expect(cfg).To(ContainSubstring("spec_n_max:3"))
		Expect(cfg).To(ContainSubstring("use_jinja:true"))
	})

	It("never picks the drafter as the model when the preference is q8_0", func() {
		// No model file has a whole `q8_0` token (the Q8 quant is Q8_K_XL), so
		// the fallback runs. The root dspark Q8_0 file must not win it.
		cfg, names := imp(`{"quantizations":"q8_0"}`, inkling())

		Expect(names[0]).To(HaveSuffix("/Inkling-2-Mini-BF16.gguf"))
		Expect(cfg).To(ContainSubstring("model: " + names[0]))
		Expect(cfg).ToNot(MatchRegexp(`(?m)^model: .*dspark`))
	})

	It("does not fall back to a drafter when no preference matches", func() {
		cfg, names := imp(`{"quantizations":"no-such-quant"}`, inkling())

		Expect(names[0]).To(HaveSuffix("/Inkling-2-Mini-BF16.gguf"))
		Expect(cfg).ToNot(MatchRegexp(`(?m)^model: .*(dspark|mmproj)`))
	})

	It("picks the drafter quantization from drafter_quantizations", func() {
		cfg, names := imp(`{"drafter_quantizations":"bf16"}`, inkling())

		Expect(names).To(ContainElement(dir + "Inkling-2-Mini-UD-Q4_K_M/dspark-Inkling-2-Mini-BF16.gguf"))
		Expect(cfg).To(ContainSubstring("draft_model: " + dir + "Inkling-2-Mini-UD-Q4_K_M/dspark-Inkling-2-Mini-BF16.gguf"))
	})

	DescribeTable("the drafter switch",
		func(prefs string) {
			cfg, names := imp(prefs, inkling())

			Expect(names).To(HaveLen(2), fmt.Sprintf("%v", names))
			for _, n := range names {
				Expect(n).ToNot(ContainSubstring("dspark"))
			}
			Expect(cfg).ToNot(ContainSubstring("draft_model"))
			Expect(cfg).ToNot(ContainSubstring("spec_type"))
			Expect(cfg).ToNot(ContainSubstring("spec_n_max"))
		},
		Entry("none", `{"drafter_quantizations":"none"}`),
		Entry("off", `{"drafter_quantizations":"off"}`),
		Entry("false", `{"drafter_quantizations":"false"}`),
		Entry("JSON false", `{"drafter_quantizations":false}`),
	)

	DescribeTable("the projector choice",
		func(projectors []string, want string) {
			listing := files("model-Q4_K_M.gguf")
			listing = append(listing, files(projectors...)...)
			cfg, _ := imp(`{"name":"vl"}`, listing)

			Expect(cfg).To(ContainSubstring("mmproj: llama-cpp/mmproj/vl/" + want))
		},
		Entry("F16 over BF16 and F32", []string{"mmproj-F32.gguf", "mmproj-BF16.gguf", "mmproj-F16.gguf"}, "mmproj-F16.gguf"),
		Entry("fp16 spelling is matched", []string{"mmproj-F32.gguf", "mmproj-model-fp16.gguf"}, "mmproj-model-fp16.gguf"),
		Entry("BF16 over F32 when there is no F16", []string{"mmproj-F32.gguf", "mmproj-BF16.gguf"}, "mmproj-BF16.gguf"),
		Entry("F32 only when nothing smaller exists", []string{"mmproj-F32.gguf"}, "mmproj-F32.gguf"),
	)

	It("keeps a user projector preference ahead of the defaults", func() {
		cfg, _ := imp(`{"name":"vl","mmproj_quantizations":"f32"}`,
			files("model-Q4_K_M.gguf", "mmproj-F16.gguf", "mmproj-F32.gguf"))

		Expect(cfg).To(ContainSubstring("mmproj: llama-cpp/mmproj/vl/mmproj-F32.gguf"))
	})

	DescribeTable("other drafter kinds",
		func(drafter, specType string, extra ...string) {
			cfg, names := imp(`{"name":"m"}`, files("m-Q4_K_M.gguf", drafter))

			Expect(names).To(Equal([]string{dir + "m/m-Q4_K_M.gguf", dir + "m/" + drafter}))
			Expect(cfg).To(ContainSubstring("draft_model: " + dir + "m/" + drafter))
			Expect(cfg).To(ContainSubstring("spec_type:" + specType))
			for _, e := range extra {
				Expect(cfg).To(ContainSubstring(e))
			}
		},
		Entry("dflash", "dflash-m-Q8_0.gguf", "draft-dflash"),
		Entry("eagle3", "eagle3-m-Q8_0.gguf", "draft-eagle3"),
		Entry("mtp", "mtp-m-Q8_0.gguf", "draft-mtp", "spec_n_max:6", "spec_p_min:0.75"),
	)

	It("leaves a repository without a drafter unchanged", func() {
		cfg, names := imp(`{"name":"plain"}`, files("m-Q4_K_M.gguf", "m-Q8_0.gguf"))

		Expect(names).To(Equal([]string{dir + "plain/m-Q4_K_M.gguf"}))
		Expect(cfg).ToNot(ContainSubstring("draft_model"))
		Expect(cfg).ToNot(ContainSubstring("spec_type"))
	})

	It("does not treat an MTP-bearing model as a drafter", func() {
		// Capitalised MTP- in a main file name is not llama.cpp's sidecar
		// keyword; the heads are embedded and the existing detection owns it.
		cfg, names := imp(`{"name":"mtp"}`, files("Qwen-27B-MTP-Q4_K_M.gguf"))

		Expect(names).To(Equal([]string{dir + "mtp/Qwen-27B-MTP-Q4_K_M.gguf"}))
		Expect(cfg).ToNot(ContainSubstring("draft_model"))
	})

	It("does not import an imatrix file as the model", func() {
		cfg, names := imp(`{"name":"im"}`, files("m-Q4_K_M.gguf", "imatrix_unsloth.gguf"))

		Expect(names).To(Equal([]string{dir + "im/m-Q4_K_M.gguf"}))
		Expect(cfg).ToNot(ContainSubstring("imatrix"))
	})

	It("skips a repository that ships only a drafter", func() {
		cfg, names := imp(`{"name":"d"}`, files("dspark-x-Q8_0.gguf"))

		Expect(names).To(BeEmpty())
		Expect(strings.Contains(cfg, "draft_model")).To(BeFalse())
	})

	It("does not write the drafter for the llama.cpp forks", func() {
		cfg, names := imp(`{"name":"m","backend":"turboquant"}`, files("m-Q4_K_M.gguf", "dspark-m-Q8_0.gguf"))

		Expect(names).To(Equal([]string{dir + "m/m-Q4_K_M.gguf"}))
		Expect(cfg).ToNot(ContainSubstring("draft_model"))
	})
})
