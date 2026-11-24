// SPDX-License-Identifier: MIT
package gallery_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery"
)

// MiniCPM-OCR only runs as a multimodal model: the 4x projector is required,
// and the entry must keep thinking disabled so the transcription lands in the
// response content instead of the reasoning channel. The DSpark build adds a
// second model file that must be named as the draft, not as a variant's
// primary weights.
var _ = Describe("gallery/index.yaml MiniCPM-OCR entries", func() {
	const (
		modelDir  = "llama-cpp/models/minicpm-ocr-1.0/"
		mmproj    = modelDir + "mmproj-model-f16-4x.gguf"
		mmprojSHA = "ceed82261b3b1380c3165f8b37c10117a50093be1362cbfeeb692fa036444682"
		q8        = modelDir + "MiniCPM-OCR-1_0-Q8_0.gguf"
		q8SHA     = "791486a476bdacb48acb3f31aa3a18dbb5be87e723a4074bcd13a37d61c852e2"
		q4        = modelDir + "MiniCPM-OCR-1_0-Q4_K_M.gguf"
		q4SHA     = "c80a703400633f298cc2fddf6d493a541be36918c0801c0b1bf3f14c5703ce49"
		dspark    = modelDir + "dspark-MiniCPM-OCR-1_0-Q8_0.gguf"
		dsparkSHA = "fd8f64bb62cf5a5c6b4c0999bfa58f64453690c9f75f1124ab613dfb44a822da"
		ocrBase   = "https://huggingface.co/openbmb/MiniCPM-OCR-1.0-gguf/resolve/9ba8c0db38ec333bdd83d44a88d25c6f1d748ba4/"
		dsparkURL = "https://huggingface.co/openbmb/MiniCPM-OCR-1.0-DSpark-gguf/resolve/265c4dc398c8d5c0ac56a4dd155a5a97b2037226/"
	)

	byName := func() map[string]gallery.GalleryModel {
		entries, err := loadGalleryIndex()
		Expect(err).ToNot(HaveOccurred())
		return indexEntriesByName(entries)
	}
	fileByName := func(e gallery.GalleryModel, name string) (gallery.File, bool) {
		for _, f := range e.AdditionalFiles {
			if f.Filename == name {
				return f, true
			}
		}
		return gallery.File{}, false
	}

	It("declares the Q8, Q4_K_M and DSpark builds as one selectable family", func() {
		entries := byName()
		base, ok := entries["minicpm-ocr-1.0-q8"]
		Expect(ok).To(BeTrue())

		variants := make([]string, 0, len(base.Variants))
		for _, v := range base.Variants {
			variants = append(variants, v.Model)
		}
		Expect(variants).To(ConsistOf("minicpm-ocr-1.0-q4km", "minicpm-ocr-1.0-dspark"))

		for _, name := range []string{"minicpm-ocr-1.0-q8", "minicpm-ocr-1.0-q4km", "minicpm-ocr-1.0-dspark"} {
			e, ok := entries[name]
			Expect(ok).To(BeTrue(), name)
			Expect(e.License).To(Equal("apache-2.0"), name)
			Expect(e.Overrides).To(HaveKeyWithValue("backend", "llama-cpp"), name)
			Expect(e.Overrides).To(HaveKeyWithValue("context_size", 8192), name)
			Expect(e.Overrides).To(HaveKeyWithValue("mmproj", mmproj), name)
			Expect(e.Overrides["known_usecases"]).To(ConsistOf("chat", "vision"), name)

			tmpl, ok := e.Overrides["template"].(map[string]any)
			Expect(ok).To(BeTrue(), name)
			Expect(tmpl).To(HaveKeyWithValue("use_tokenizer_template", true), name)
		}
	})

	It("keeps thinking disabled and selects the expected weights", func() {
		entries := byName()

		q8Entry := entries["minicpm-ocr-1.0-q8"]
		Expect(q8Entry.Overrides["options"]).To(ConsistOf("use_jinja:true", "enable_reasoning:0", "parallel:1"))
		Expect(q8Entry.Overrides["parameters"]).To(HaveKeyWithValue("model", q8))

		q4Entry := entries["minicpm-ocr-1.0-q4km"]
		Expect(q4Entry.Overrides["options"]).To(ConsistOf("use_jinja:true", "enable_reasoning:0", "parallel:1"))
		Expect(q4Entry.Overrides["parameters"]).To(HaveKeyWithValue("model", q4))

		dsparkEntry := entries["minicpm-ocr-1.0-dspark"]
		Expect(dsparkEntry.Overrides["options"]).To(ConsistOf(
			"use_jinja:true", "enable_reasoning:0", "parallel:1", "spec_type:draft-dspark", "spec_n_max:15"))
		Expect(dsparkEntry.Overrides).To(HaveKeyWithValue("draft_model", dspark))
		Expect(dsparkEntry.Overrides["parameters"]).To(HaveKeyWithValue("model", q8))
	})

	It("pins every weight and projector to the published sha256", func() {
		entries := byName()
		want := map[string]map[string]string{
			"minicpm-ocr-1.0-q8": {
				q8:     q8SHA,
				mmproj: mmprojSHA,
			},
			"minicpm-ocr-1.0-q4km": {
				q4:     q4SHA,
				mmproj: mmprojSHA,
			},
			"minicpm-ocr-1.0-dspark": {
				q8:     q8SHA,
				dspark: dsparkSHA,
				mmproj: mmprojSHA,
			},
		}
		for name, files := range want {
			e := entries[name]
			Expect(e.AdditionalFiles).To(HaveLen(len(files)), name)
			for filename, sha := range files {
				f, ok := fileByName(e, filename)
				Expect(ok).To(BeTrue(), name+": "+filename)
				Expect(f.SHA256).To(Equal(sha), name+": "+filename)
			}
		}

		q8File, _ := fileByName(entries["minicpm-ocr-1.0-q8"], q8)
		Expect(q8File.URI).To(Equal(ocrBase + "MiniCPM-OCR-1_0-Q8_0.gguf"))
		mmprojFile, _ := fileByName(entries["minicpm-ocr-1.0-q8"], mmproj)
		Expect(mmprojFile.URI).To(Equal(ocrBase + "mmproj-model-f16-4x.gguf"))
		q4File, _ := fileByName(entries["minicpm-ocr-1.0-q4km"], q4)
		Expect(q4File.URI).To(Equal(ocrBase + "MiniCPM-OCR-1_0-Q4_K_M.gguf"))
		dsparkFile, _ := fileByName(entries["minicpm-ocr-1.0-dspark"], dspark)
		Expect(dsparkFile.URI).To(Equal(dsparkURL + "dspark-MiniCPM-OCR-1_0-Q8_0.gguf"))
	})
})
