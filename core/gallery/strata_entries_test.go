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
//     the pack on the first load (the pack_gguf option).
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

	isBuilt := func(e gallery.GalleryModel) bool {
		for _, o := range optionList(e) {
			if strings.HasPrefix(o, "pack_gguf:") {
				return true
			}
		}
		return false
	}

	isDeepSeek := func(e gallery.GalleryModel) bool {
		for _, o := range optionList(e) {
			if o == "family:deepseek41" {
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
			Expect(dir).To(Equal("strata/models/"+e.Name), e.Name)
			Expect(e.AdditionalFiles).ToNot(BeEmpty(), e.Name)
			seen := map[string]bool{}
			for _, f := range e.AdditionalFiles {
				Expect(f.SHA256).To(MatchRegexp(sha.String()), e.Name+" "+f.Filename)
				Expect(f.URI).To(MatchRegexp(pinned.String()), "%s: %s is not pinned to a commit", e.Name, f.URI)
				// The DeepSeek entries keep the files they share (the n-gram table, the
				// draft model, the metadata shard) in one folder, so a second set does
				// not download them again.
				inside := strings.HasPrefix(f.Filename, dir+"/")
				if isDeepSeek(e) && strings.HasPrefix(f.Filename, deepSeekSharedDir+"/") {
					inside = true
				}
				Expect(inside).To(BeTrue(), "%s: %s is outside the model directory %s", e.Name, f.Filename, dir)
				Expect(seen).ToNot(HaveKey(f.Filename), e.Name)
				seen[f.Filename] = true
			}
		}
	})

	It("downloads every file the options name", func() {
		fileOptions := map[string]bool{
			"native": true, "native_dense_gguf": true, "ple_gguf": true, "embd_gguf": true,
			"expert_profile": true, "mtp": true, "pack_gguf": true,
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
			if isDeepSeek(e) {
				continue // checked by its own spec below
			}
			Expect(seen).To(HaveKey("native"), e.Name)
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
			if isDeepSeek(e) {
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
			if isDeepSeek(e) {
				continue // checked by its own spec below
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

	It("builds the DeepSeek-V4.1 packs from files the entry downloads", func() {
		shard := regexp.MustCompile(`^(.*)-(\d{5})-of-(\d{5})\.gguf$`)
		type pin struct{ sha, uri string }
		shared := map[string]pin{}
		count := 0
		for _, e := range strataEntries() {
			if !isDeepSeek(e) {
				continue
			}
			count++
			dir := modelDir(e)
			have := downloaded(e)
			opts := map[string]string{}
			for _, o := range optionList(e) {
				k, v, _ := strings.Cut(o, ":")
				Expect(opts).ToNot(HaveKey(k), "%s: option %s is set twice", e.Name, k)
				opts[k] = v
			}
			for _, banned := range []string{"native", "ple_gguf", "mmproj", "vision_exe", "pack_tool"} {
				Expect(opts).ToNot(HaveKey(banned), "%s: %s is a Qwen option", e.Name, banned)
			}
			Expect(e.Overrides).ToNot(HaveKey("mmproj"), e.Name)
			Expect(opts).To(HaveKey("expert_profile"), e.Name)
			Expect(have).To(HaveKey(opts["expert_profile"]), e.Name)
			Expect(opts).To(HaveKey("ready_timeout"), e.Name)
			Expect(e.Overrides["context_size"]).To(BeNumerically(">=", 8192), e.Name)

			// pack_args names the model shards (in the model directory) and the
			// shared files (beside it), and every one of them is downloaded.
			args := strings.Fields(opts["pack_args"])
			flags := map[string][]string{}
			cur := ""
			for _, a := range args {
				if strings.HasPrefix(a, "--") {
					cur = a
					flags[cur] = nil
					continue
				}
				flags[cur] = append(flags[cur], a)
			}
			Expect(flags).To(HaveKey("--shards"), e.Name)
			Expect(flags["--shards"]).To(HaveLen(5), e.Name)
			Expect(flags["--shards"][0]).To(Equal(opts["pack_gguf"]), e.Name)
			m := shard.FindStringSubmatch(opts["pack_gguf"])
			Expect(m).ToNot(BeNil(), e.Name)
			Expect(m[2]).To(Equal("00001"), e.Name)
			Expect(m[3]).To(Equal("00006"), e.Name+": the sixth shard of the split is the n-gram table")
			for i, name := range flags["--shards"] {
				Expect(name).To(Equal(fmt.Sprintf("%s-%05d-of-00006.gguf", m[1], i+1)), e.Name)
				Expect(have).To(HaveKey(name), e.Name)
			}
			for _, flag := range []string{"--engram", "--engram-meta", "--dspark-gguf"} {
				Expect(flags[flag]).To(HaveLen(1), "%s: %s", e.Name, flag)
				rel := path.Clean(path.Join(dir, flags[flag][0]))
				found := false
				for _, f := range e.AdditionalFiles {
					if f.Filename == rel {
						found = true
						// The same shared file must be the same download in every entry.
						if prev, ok := shared[rel]; ok {
							Expect(pin{f.SHA256, f.URI}).To(Equal(prev), "%s: %s differs from another entry", e.Name, rel)
						}
						shared[rel] = pin{f.SHA256, f.URI}
					}
				}
				Expect(found).To(BeTrue(), "%s: %s names %s, which the entry does not download", e.Name, flag, rel)
			}
			// The geometry the ISTA-DAS files leave out, as Strata's setup states it.
			Expect(flags["--dspark-noise-token"]).To(Equal([]string{"128799"}), e.Name)
			Expect(flags["--dspark-markov-rank"]).To(Equal([]string{"256"}), e.Name)
			// Text only, DeepSeek's sampling for local use, its own template.
			Expect(e.Overrides["known_usecases"]).To(Equal([]any{"chat"}), e.Name)
			params, _ := e.Overrides["parameters"].(map[string]any)
			Expect(params["temperature"]).To(BeNumerically("==", 1.0), e.Name)
			Expect(params["top_p"]).To(BeNumerically("==", 1.0), e.Name)
			Expect(e.Description).To(ContainSubstring("ISTA-DAS"), e.Name)
			Expect(e.Description).To(MatchRegexp(`(?i)free disk`), e.Name)
			Expect(e.Description).To(ContainSubstring("CUDA 13"), e.Name)
		}
		Expect(count).To(Equal(4))
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
			"deepseek-v4.1-flash-ista-iq1_m", "deepseek-v4.1-flash-ista-iq2_xxs", "deepseek-v4.1-flash-ista-iq2_xs",
			"deepseek-v4.1-flash-ista-coder-iq2_xxs",
		} {
			Expect(names).To(HaveKey(want))
		}
	})
})

// deepSeekSharedDir holds the files every DeepSeek-V4.1 entry uses: the
// n-gram table, the draft model and the metadata shard.
const deepSeekSharedDir = "strata/models/deepseek-v4.1-flash-shared"

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
