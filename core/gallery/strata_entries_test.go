package gallery_test

import (
	"path"
	"regexp"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery"
)

// The strata backend reads a model directory (a Strata pack plus GGUF side
// files) and finds the side files by the names in `options`. A name that no
// file of the entry provides installs cleanly and fails at load, so check that
// every name resolves to a file the entry downloads.
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

	It("downloads a complete pack and every side file the options name", func() {
		sha := regexp.MustCompile(`^[0-9a-f]{64}$`)
		for _, e := range strataEntries() {
			params, _ := e.Overrides["parameters"].(map[string]any)
			dir, _ := params["model"].(string)
			Expect(dir).ToNot(BeEmpty(), e.Name)

			have := map[string]bool{}
			for _, f := range e.AdditionalFiles {
				Expect(f.SHA256).To(MatchRegexp(sha.String()), e.Name+" "+f.Filename)
				Expect(f.URI).To(HavePrefix("https://"), e.Name+" "+f.Filename)
				Expect(strings.HasPrefix(f.Filename, dir+"/")).To(BeTrue(),
					"%s: %s is outside the model directory %s", e.Name, f.Filename, dir)
				have[strings.TrimPrefix(f.Filename, dir+"/")] = true
			}
			for _, rel := range []string{
				"pack/index.txt", "pack/native_experts.txt", "pack/dense.bin", "pack/experts.bin",
				"pack/tokenizer/vocab.json", "pack/tokenizer/merges.txt", "pack/tokenizer/token_type.json",
				"pack/tokenizer/tokenizer.json", "pack/tokenizer/chat_template.jinja",
			} {
				Expect(have).To(HaveKey(rel), "%s: pack file %s is missing", e.Name, rel)
			}

			fileOptions := map[string]bool{
				"native": true, "native_dense_gguf": true, "ple_gguf": true, "embd_gguf": true,
				"expert_profile": true, "mtp": true,
			}
			opts, _ := e.Overrides["options"].([]any)
			seen := map[string]bool{}
			for _, o := range opts {
				k, v, _ := strings.Cut(o.(string), ":")
				seen[k] = true
				if fileOptions[k] {
					Expect(have).To(HaveKey(path.Clean(v)), "%s: option %s names %s, which the entry does not download", e.Name, k, v)
				}
			}
			Expect(seen).To(HaveKey("native"), e.Name)

			if mm, ok := e.Overrides["mmproj"].(string); ok {
				Expect(have).To(HaveKey(strings.TrimPrefix(mm, dir+"/")), e.Name+" mmproj")
				Expect(seen).To(HaveKey("native"), e.Name+": images need the native option")
			}
		}
	})

	It("uses the model's own chat template and declares a licence", func() {
		for _, e := range strataEntries() {
			tmpl, _ := e.Overrides["template"].(map[string]any)
			Expect(tmpl["use_tokenizer_template"]).To(BeTrue(), e.Name)
			Expect(e.License).ToNot(BeEmpty(), e.Name)
			Expect(e.Tags).To(ContainElement("strata"), e.Name)
		}
	})
})
