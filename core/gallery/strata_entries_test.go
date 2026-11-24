package gallery_test

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery"
)

// The strata backend reads a model directory and finds the files by the names
// in `options`. A name that no file of the entry provides installs cleanly and
// fails at load, so check that every name resolves to a file the entry
// downloads. There are two kinds of entry:
//   - a ready pack: the pack folder is downloaded with the GGUF side files;
//   - a GGUF set: only the GGUF files are downloaded, and the backend builds
//     the pack on the first load (the pack_gguf option);
//   - a Hy4-Flash set (family:hy4): the .strata build, the Engram file and the
//     vision file. There is no pack; the backend reads the tokenizer from the
//     build file. The three builds share one directory, so the Engram and
//     vision files are downloaded once;
//   - an Inkling 2 Mini set (family:inkling): a .strata build or a GGUF, the
//     vision file, the tokenizer files and, for a GGUF, the DSpark drafter. The
//     entries share one directory. There is no pack.
var _ = Describe("gallery/index.yaml strata entries", func() {
	strataEntries := func() []gallery.GalleryModel {
		entries, err := loadGalleryIndex()
		Expect(err).ToNot(HaveOccurred())
		var out []gallery.GalleryModel
		for _, e := range entries {
			if e.Overrides["backend"] == "strata" {
				out = append(out, e)
			}
		}
		return out
	}

	It("has at least one strata entry", func() {
		Expect(strataEntries()).ToNot(BeEmpty())
	})

	isHy4 := func(e gallery.GalleryModel) bool {
		for _, o := range optionList(e) {
			if o == "family:hy4" {
				return true
			}
		}
		return false
	}

	isInkling := func(e gallery.GalleryModel) bool {
		for _, o := range optionList(e) {
			if o == "family:inkling" {
				return true
			}
		}
		return false
	}

	isBuilt := func(e gallery.GalleryModel) bool {
		for _, o := range optionList(e) {
			if strings.HasPrefix(o, "pack_gguf:") {
				return true
			}
		}
		return false
	}

	It("pins every file to a commit and a SHA-256, inside the model directory", func() {
		sha := regexp.MustCompile(`^[0-9a-f]{64}$`)
		pinned := regexp.MustCompile(`^https://(huggingface\.co/[^/]+/[^/]+/resolve|raw\.githubusercontent\.com/[^/]+/[^/]+)/[0-9a-f]{40}/`)
		for _, e := range strataEntries() {
			dir := modelDir(e)
			if isHy4(e) {
				Expect(dir).To(Equal("strata/models/hy4-flash"), e.Name)
			} else if isInkling(e) {
				Expect(dir).To(Equal("strata/models/inkling-2-mini"), e.Name)
			} else {
				Expect(dir).To(Equal("strata/models/"+e.Name), e.Name)
			}
			Expect(e.AdditionalFiles).ToNot(BeEmpty(), e.Name)
			seen := map[string]bool{}
			for _, f := range e.AdditionalFiles {
				Expect(f.SHA256).To(MatchRegexp(sha.String()), e.Name+" "+f.Filename)
				Expect(f.URI).To(MatchRegexp(pinned.String()), "%s: %s is not pinned to a commit", e.Name, f.URI)
				inside := strings.HasPrefix(f.Filename, dir+"/")
				Expect(inside).To(BeTrue(), "%s: %s is outside the model directory %s", e.Name, f.Filename, dir)
				Expect(seen).ToNot(HaveKey(f.Filename), e.Name)
				seen[f.Filename] = true
			}
		}
	})

	It("downloads every file the options name", func() {
		fileOptions := map[string]bool{
			"native": true, "native_dense_gguf": true, "ple_gguf": true, "embd_gguf": true,
			"expert_profile": true, "mtp": true, "pack_gguf": true, "engram": true,
			"vision": true, "dspark": true, "tokenizer_json": true, "chat_template": true,
		}
		for _, e := range strataEntries() {
			dir := modelDir(e)
			have := downloaded(e)
			seen := map[string]bool{}
			for _, o := range optionList(e) {
				k, v, _ := strings.Cut(o, ":")
				seen[k] = true
				if fileOptions[k] {
					Expect(have).To(HaveKey(path.Clean(v)), "%s: option %s names %s, which the entry does not download", e.Name, k, v)
				}
			}
			Expect(seen).To(HaveKey("native"), e.Name)
			if isHy4(e) {
				// The vision file is named by a file option here, not a mmproj.
				Expect(seen).To(HaveKey("engram"), e.Name)
				Expect(seen).To(HaveKey("vision"), e.Name)
				for _, o := range optionList(e) {
					if v, ok := strings.CutPrefix(o, "vision:"); ok {
						Expect(have).To(HaveKey(path.Clean(v)), e.Name+" vision file")
					}
				}
				Expect(e.Overrides["known_input_modalities"]).To(ContainElement("image"), e.Name)
				Expect(e.Overrides).ToNot(HaveKey("mmproj"), e.Name)
			}
			if isInkling(e) {
				Expect(seen).To(HaveKey("vision"), e.Name)
				Expect(e.Overrides["known_input_modalities"]).To(ContainElement("image"), e.Name)
				Expect(e.Overrides).ToNot(HaveKey("mmproj"), e.Name)
			}
			if mm, ok := e.Overrides["mmproj"].(string); ok {
				Expect(have).To(HaveKey(strings.TrimPrefix(mm, dir+"/")), e.Name+" mmproj")
				Expect(e.Overrides["known_input_modalities"]).To(ContainElement("image"), e.Name)
			}
		}
	})

	It("downloads a complete ready pack, or every shard of a GGUF set the backend packs", func() {
		shard := regexp.MustCompile(`^(.*)-(\d{5})-of-(\d{5})\.gguf$`)
		for _, e := range strataEntries() {
			have := downloaded(e)
			if isHy4(e) {
				opts := map[string]string{}
				for _, o := range optionList(e) {
					k, v, _ := strings.Cut(o, ":")
					opts[k] = v
				}
				Expect(opts["native"]).To(MatchRegexp(`^Hy4-Flash-SQ-(64|96|128)\.strata$`), e.Name)
				Expect(opts["engram"]).To(Equal("Hy4-Flash-Engram-Q8.strata"), e.Name)
				Expect(have).To(HaveLen(3), e.Name)
				for rel := range have {
					Expect(rel).ToNot(HavePrefix("pack/"), e.Name)
					Expect(rel).To(HaveSuffix(".strata"), e.Name)
				}
				continue
			}
			if isInkling(e) {
				opts := map[string]string{}
				for _, o := range optionList(e) {
					k, v, _ := strings.Cut(o, ":")
					opts[k] = v
				}
				Expect(opts["native"]).To(MatchRegexp(`^Inkling-2-Mini-(SQ-(10|15|20)\.strata|UD-[A-Z0-9_]+\.gguf)$`), e.Name)
				Expect(opts["vision"]).To(Equal("Inkling-2-Mini-Vision.strata"), e.Name)
				// The tokenizer is made from tokenizer.json (a Strata build has the
				// chat template inside) and, for a GGUF, chat_template.jinja too.
				Expect(have).To(HaveKey("tokenizer.json"), e.Name)
				if strings.HasSuffix(opts["native"], ".gguf") {
					Expect(have).To(HaveKey("chat_template.jinja"), e.Name)
					Expect(opts["dspark"]).To(Equal("dspark-Inkling-2-Mini-Q8_0.gguf"), e.Name)
				} else {
					Expect(opts).ToNot(HaveKey("dspark"), e.Name)
				}
				for rel := range have {
					Expect(rel).ToNot(HavePrefix("pack/"), e.Name)
				}
				continue
			}
			if !isBuilt(e) {
				for _, rel := range []string{
					"pack/index.txt", "pack/native_experts.txt", "pack/dense.bin", "pack/experts.bin",
					"pack/tokenizer/vocab.json", "pack/tokenizer/merges.txt", "pack/tokenizer/token_type.json",
					"pack/tokenizer/tokenizer.json", "pack/tokenizer/chat_template.jinja",
				} {
					Expect(have).To(HaveKey(rel), "%s: pack file %s is missing", e.Name, rel)
				}
				continue
			}
			// A GGUF set: the backend makes the pack, so none is downloaded,
			// and the first shard must come with all of its siblings.
			for rel := range have {
				Expect(rel).ToNot(HavePrefix("pack/"), "%s: a built pack must not also be downloaded", e.Name)
			}
			opts := map[string]string{}
			for _, o := range optionList(e) {
				k, v, _ := strings.Cut(o, ":")
				opts[k] = v
			}
			first := opts["pack_gguf"]
			Expect(opts["native"]).To(Equal(first), e.Name)
			if m := shard.FindStringSubmatch(first); m != nil {
				Expect(m[2]).To(Equal("00001"), e.Name+": pack_gguf must be the first shard")
				n, _ := strconv.Atoi(m[3])
				for i := 1; i <= n; i++ {
					name := fmt.Sprintf("%s-%05d-of-%05d.gguf", m[1], i, n)
					Expect(have).To(HaveKey(name), "%s: shard %s is missing", e.Name, name)
				}
			}
			switch opts["pack_tool"] {
			case "", "iq_pack":
				Expect(opts).To(HaveKey("expert_profile"), e.Name)
			case "tokenizer":
				Expect(opts).ToNot(HaveKey("expert_profile"), e.Name)
			default:
				Fail(e.Name + ": unknown pack_tool " + opts["pack_tool"])
			}
		}
	})

	It("uses the model's own chat template, names its licence and its needs", func() {
		licenceWords := map[string]string{
			"qwen-community-1.0":        "Qwen Community License 1.0",
			"nvidia-open-model-license": "NVIDIA Open Model License",
			"apache-2.0":                "Apache License 2.0",
			"mit":                       "MIT License",
			"swift-open-license-1.0":    "Swift Open License 1.0",
			"tencent-hy-license":        "Tencent Hy License",
		}
		for _, e := range strataEntries() {
			tmpl, _ := e.Overrides["template"].(map[string]any)
			Expect(tmpl["use_tokenizer_template"]).To(BeTrue(), e.Name)
			Expect(e.License).ToNot(BeEmpty(), e.Name)
			word, known := licenceWords[e.License]
			Expect(known).To(BeTrue(), "%s: licence %s has no check", e.Name, e.License)
			Expect(e.Description).To(ContainSubstring(word), e.Name)
			Expect(e.Description).To(MatchRegexp(`about\s+\d+\s+GB\s+to\s+download`), e.Name)
			Expect(e.Description).To(MatchRegexp(`(?i)RAM`), e.Name)
			Expect(e.Description).To(MatchRegexp(`(?i)(conversion|quantization|Strata is by)`), e.Name+": credit the conversion")
			Expect(e.Tags).To(ContainElement("strata"), e.Name)
			Expect(e.Tags).To(ContainElement("experimental"), e.Name)
			Expect(e.URLs).To(ContainElement("https://github.com/Niko1221/Strata"), e.Name)
		}
	})

	It("has no DeepSeek V4.1 entry: Strata v0.2.15 removed that family", func() {
		for _, e := range strataEntries() {
			for _, o := range optionList(e) {
				Expect(o).ToNot(Equal("family:deepseek41"), e.Name)
			}
		}
	})

	It("lists every model Strata documents as running on this backend", func() {
		names := map[string]bool{}
		for _, e := range strataEntries() {
			names[e.Name] = true
		}
		for _, want := range []string{
			"qwen3.8-flash-next-nvfp4-gptq", "qwen3.8-flash-next-nvfp4-nvidia",
			"qwen3.8-flash-next-ista-q2_0", "qwen3.8-flash-next-ista-iq2_xs",
			"qwen3.8-flash-next-ista-iq3_xxs", "qwen3.8-flash-next-ista-iq3_s",
			"qwen3.8-flash-next-coder-iq1_m",
			"qwen3.8-flash-next-unsloth-ud-iq4_xs", "qwen3.8-flash-next-unsloth-ud-q4_k_xl",
			"qwen3.8-flash-next-unsloth-ud-q5_k_xl", "qwen3.8-flash-next-unsloth-ud-q6_k_xl",
			"qwen3.8-flash-next-unsloth-q8_0",
			"qwen3.8-flash-next-gyro-s", "qwen3.8-flash-next-gyro-m",
			"swift-1.5-qwen3.8-flash-next-iq2_xs", "swift-1.5-qwen3.8-flash-next-iq3_xxs",
			"swift-1.5-qwen3.8-flash-next-iq3_s",
			"qwen3.6-35b-a3b-ud-q4_k_xl-strata", "qwen3.6-35b-a3b-ud-q6_k_xl-strata", "qwen3.6-35b-a3b-q8_0-strata",
			"hy4-flash-sq-64-strata", "hy4-flash-sq-96-strata", "hy4-flash-sq-128-strata",
			"inkling-2-mini-sq-10-strata", "inkling-2-mini-sq-15-strata", "inkling-2-mini-sq-20-strata",
			"inkling-2-mini-ud-q8_k_xl-strata", "inkling-2-mini-ud-q4_k_xl-strata", "inkling-2-mini-ud-q4_k_m-strata",
			"inkling-2-mini-ud-iq4_xs-strata", "inkling-2-mini-ud-q3_k_xl-strata", "inkling-2-mini-ud-iq3_xxs-strata",
			"inkling-2-mini-ud-q2_k_xl-strata", "inkling-2-mini-ud-iq2_xxs-strata", "inkling-2-mini-ud-iq1_m-strata",
			"inkling-2-mini-ud-iq1_s-strata",
		} {
			Expect(names).To(HaveKey(want))
		}
	})
})

func optionList(e gallery.GalleryModel) []string {
	var out []string
	opts, _ := e.Overrides["options"].([]any)
	for _, o := range opts {
		out = append(out, o.(string))
	}
	return out
}

func modelDir(e gallery.GalleryModel) string {
	params, _ := e.Overrides["parameters"].(map[string]any)
	dir, _ := params["model"].(string)
	return dir
}

// downloaded is the set of file names the entry installs, relative to its
// model directory.
func downloaded(e gallery.GalleryModel) map[string]bool {
	dir := modelDir(e)
	have := map[string]bool{}
	for _, f := range e.AdditionalFiles {
		have[strings.TrimPrefix(f.Filename, dir+"/")] = true
	}
	return have
}
