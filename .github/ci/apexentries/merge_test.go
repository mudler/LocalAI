package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Merge", func() {
	It("drops a generated entry whose weight URI already exists and reports the existing name", func() {
		existing := &ExistingIndex{
			ByName: map[string]int{"qwen3.6-35b-a3b-apex": 0},
			ByURI: map[string]string{
				"https://huggingface.co/mudler/X-APEX-GGUF/resolve/main/X-APEX-I-Quality.gguf": "qwen3.6-35b-a3b-apex",
			},
		}
		gen := []GalleryEntry{{
			Name:  "x-apex-i-quality",
			Files: []EntryFile{{URI: "https://huggingface.co/mudler/X-APEX-GGUF/resolve/main/X-APEX-I-Quality.gguf"}},
		}}

		add, reused := Merge(existing, gen)

		Expect(add).To(BeEmpty())
		Expect(reused).To(HaveKeyWithValue("x-apex-i-quality", "qwen3.6-35b-a3b-apex"))
	})

	It("keeps a generated entry whose weights are new", func() {
		existing := &ExistingIndex{ByName: map[string]int{}, ByURI: map[string]string{}}
		gen := []GalleryEntry{{
			Name:  "x-apex-i-mini",
			Files: []EntryFile{{URI: "https://huggingface.co/mudler/X-APEX-GGUF/resolve/main/X-APEX-I-Mini.gguf"}},
		}}

		add, reused := Merge(existing, gen)

		Expect(add).To(HaveLen(1))
		Expect(reused).To(BeEmpty())
	})

	It("refuses to add an entry whose name collides with an existing one", func() {
		existing := &ExistingIndex{
			ByName: map[string]int{"x-apex-i-mini": 0},
			ByURI:  map[string]string{},
		}
		gen := []GalleryEntry{{
			Name:  "x-apex-i-mini",
			Files: []EntryFile{{URI: "https://huggingface.co/mudler/X-APEX-GGUF/resolve/main/other.gguf"}},
		}}

		add, reused := Merge(existing, gen)

		Expect(add).To(BeEmpty())
		Expect(reused).To(HaveKeyWithValue("x-apex-i-mini", "x-apex-i-mini"))
	})

	// The gallery records most of its URIs in huggingface:// shorthand while
	// render.go only ever emits the resolve/main form, so without
	// canonicalization the majority of the file is invisible to the dedup.
	It("matches a generated https URI against the shorthand form recorded in the gallery", func() {
		existing := &ExistingIndex{
			ByName: map[string]int{"foo-gguf-q8-0": 0},
			ByURI: map[string]string{
				"huggingface://unsloth/Foo-GGUF/Foo-Q8_0.gguf": "foo-gguf-q8-0",
			},
		}
		gen := []GalleryEntry{{
			Name:  "foo-apex-q8-0",
			Files: []EntryFile{{URI: "https://huggingface.co/unsloth/Foo-GGUF/resolve/main/Foo-Q8_0.gguf"}},
		}}

		add, reused := Merge(existing, gen)

		Expect(add).To(BeEmpty())
		Expect(reused).To(HaveKeyWithValue("foo-apex-q8-0", "foo-gguf-q8-0"))
	})

	It("matches a generated shorthand URI against the https form recorded in the gallery", func() {
		existing := &ExistingIndex{
			ByName: map[string]int{"foo-gguf-q8-0": 0},
			ByURI: map[string]string{
				"https://huggingface.co/unsloth/Foo-GGUF/resolve/main/Foo-Q8_0.gguf": "foo-gguf-q8-0",
			},
		}
		gen := []GalleryEntry{{
			Name:  "foo-apex-q8-0",
			Files: []EntryFile{{URI: "huggingface://unsloth/Foo-GGUF/Foo-Q8_0.gguf"}},
		}}

		add, reused := Merge(existing, gen)

		Expect(add).To(BeEmpty())
		Expect(reused).To(HaveKeyWithValue("foo-apex-q8-0", "foo-gguf-q8-0"))
	})

	// Sharded quants live under a subdirectory, so the file path carries slashes
	// of its own and only the first two segments are the repo.
	It("matches across both forms when the file path has a subdirectory", func() {
		existing := &ExistingIndex{
			ByName: map[string]int{"model-ud-q4-k-m": 0},
			ByURI: map[string]string{
				"huggingface://unsloth/Model-GGUF/UD-Q4_K_M/Model-UD-Q4_K_M-00001-of-00002.gguf": "model-ud-q4-k-m",
			},
		}
		gen := []GalleryEntry{{
			Name:  "model-apex-ud-q4-k-m",
			Files: []EntryFile{{URI: "https://huggingface.co/unsloth/Model-GGUF/resolve/main/UD-Q4_K_M/Model-UD-Q4_K_M-00001-of-00002.gguf"}},
		}}

		add, reused := Merge(existing, gen)

		Expect(add).To(BeEmpty())
		Expect(reused).To(HaveKeyWithValue("model-apex-ud-q4-k-m", "model-ud-q4-k-m"))
	})

	// Several APEX repos share one base model, so the same unsloth rungs are
	// generated more than once in a single batch.
	It("adds only the first of two generated entries sharing a name", func() {
		existing := &ExistingIndex{ByName: map[string]int{}, ByURI: map[string]string{}}
		gen := []GalleryEntry{
			{
				Name:  "shared-rung-q8-0",
				Files: []EntryFile{{URI: "https://huggingface.co/unsloth/Shared-GGUF/resolve/main/Shared-Q8_0.gguf"}},
			},
			{
				Name:  "shared-rung-q8-0",
				Files: []EntryFile{{URI: "https://huggingface.co/unsloth/Other-GGUF/resolve/main/Other-Q8_0.gguf"}},
			},
		}

		add, reused := Merge(existing, gen)

		Expect(add).To(HaveLen(1))
		Expect(add[0].Files[0].URI).To(Equal("https://huggingface.co/unsloth/Shared-GGUF/resolve/main/Shared-Q8_0.gguf"))
		Expect(reused).To(HaveKeyWithValue("shared-rung-q8-0", "shared-rung-q8-0"))
	})

	It("adds only the first of two generated entries sharing a primary URI", func() {
		existing := &ExistingIndex{ByName: map[string]int{}, ByURI: map[string]string{}}
		gen := []GalleryEntry{
			{
				Name:  "shared-rung-from-apex",
				Files: []EntryFile{{URI: "https://huggingface.co/unsloth/Shared-GGUF/resolve/main/Shared-Q8_0.gguf"}},
			},
			{
				Name:  "shared-rung-from-apex-mtp",
				Files: []EntryFile{{URI: "huggingface://unsloth/Shared-GGUF/Shared-Q8_0.gguf"}},
			},
		}

		add, reused := Merge(existing, gen)

		Expect(add).To(HaveLen(1))
		Expect(add[0].Name).To(Equal("shared-rung-from-apex"))
		Expect(reused).To(HaveKeyWithValue("shared-rung-from-apex-mtp", "shared-rung-from-apex"))
	})

	// Anything that is not a HuggingFace URI must survive untouched, so an
	// unrecognised scheme still dedups against the very same string.
	It("leaves a URI in neither recognised form alone and still dedups it exactly", func() {
		existing := &ExistingIndex{
			ByName: map[string]int{"mirrored-model": 0},
			ByURI: map[string]string{
				"https://mirror.example.com/weights/Model-Q8_0.gguf": "mirrored-model",
			},
		}
		gen := []GalleryEntry{
			{
				Name:  "mirrored-apex",
				Files: []EntryFile{{URI: "https://mirror.example.com/weights/Model-Q8_0.gguf"}},
			},
			{
				Name:  "elsewhere-apex",
				Files: []EntryFile{{URI: "https://mirror.example.com/weights/Other-Q8_0.gguf"}},
			},
		}

		add, reused := Merge(existing, gen)

		Expect(add).To(HaveLen(1))
		Expect(add[0].Name).To(Equal("elsewhere-apex"))
		Expect(reused).To(HaveKeyWithValue("mirrored-apex", "mirrored-model"))
	})
})
