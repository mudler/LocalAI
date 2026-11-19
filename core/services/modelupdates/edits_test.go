package modelupdates_test

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/services/modelupdates"
)

var _ = Describe("Edits of a model config", func() {
	installed := []byte("name: m\nbackend: llama-cpp\ncontext_size: 2048\nparameters:\n  model: m.gguf\n  temperature: 0.7\n")

	It("finds nothing when the config is the installed one", func() {
		edits, err := modelupdates.DiffEdits(installed, installed, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(edits).To(BeEmpty())
	})

	It("lists changed, added and removed keys by dotted path", func() {
		mine := []byte("name: m\nbackend: llama-cpp\nparameters:\n  model: m.gguf\n  temperature: 0.2\nf16: true\n")
		edits, err := modelupdates.DiffEdits(installed, mine, nil)
		Expect(err).ToNot(HaveOccurred())
		keys := []string{}
		for _, e := range edits {
			keys = append(keys, e.Key)
		}
		Expect(keys).To(Equal([]string{"context_size", "f16", "parameters.temperature"}))
	})

	It("never counts the operator state as an edit", func() {
		mine := append(append([]byte{}, installed...), []byte("pinned: true\ndisabled: true\n")...)
		edits, err := modelupdates.DiffEdits(installed, mine, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(edits).To(BeEmpty())
	})

	It("marks an edit the update also changes as a conflict", func() {
		mine := []byte("name: m\nbackend: llama-cpp\ncontext_size: 8192\nparameters:\n  model: m.gguf\n  temperature: 0.2\n")
		planned := []byte("name: m\nbackend: llama-cpp\ncontext_size: 4096\nparameters:\n  model: m2.gguf\n  temperature: 0.7\n")
		edits, err := modelupdates.DiffEdits(installed, mine, planned)
		Expect(err).ToNot(HaveOccurred())
		Expect(modelupdates.Conflicts(edits)).To(Equal([]string{"context_size"}))
	})

	Describe("MergeConfig", func() {
		mine := []byte("name: m\nbackend: llama-cpp\ncontext_size: 8192\nparameters:\n  model: m.gguf\n  temperature: 0.2\npinned: true\n")
		planned := []byte("name: m\nbackend: llama-cpp\ncontext_size: 4096\nparameters:\n  model: m2.gguf\n  temperature: 0.7\n")

		merged := func(choices map[string]string) map[string]any {
			out, err := modelupdates.MergeConfig(installed, mine, planned, choices)
			Expect(err).ToNot(HaveOccurred())
			m := map[string]any{}
			Expect(yaml.Unmarshal(out, &m)).To(Succeed())
			return m
		}

		It("refuses a conflicting key without a choice", func() {
			_, err := modelupdates.MergeConfig(installed, mine, planned, nil)
			var unresolved *modelupdates.ErrNeedsChoices
			Expect(errors.As(err, &unresolved)).To(BeTrue())
			Expect(unresolved.Keys).To(Equal([]string{"context_size"}))
			Expect(err.Error()).To(ContainSubstring("choose " + modelupdates.ChoiceMine + " or " + modelupdates.ChoiceGallery))
		})

		It("keeps mine, takes the gallery's new payload and the operator state", func() {
			m := merged(map[string]string{"context_size": modelupdates.ChoiceMine})
			Expect(m["context_size"]).To(Equal(8192))
			Expect(m["pinned"]).To(BeTrue())
			params := m["parameters"].(map[string]any)
			Expect(params["model"]).To(Equal("m2.gguf"))
			Expect(params["temperature"]).To(Equal(0.2), "an edit the update does not touch is kept")
		})

		It("takes the gallery value when asked", func() {
			m := merged(map[string]string{"context_size": modelupdates.ChoiceGallery})
			Expect(m["context_size"]).To(Equal(4096))
		})

		It("applies a deletion the person made", func() {
			deleted := []byte("name: m\nbackend: llama-cpp\ncontext_size: 2048\nparameters:\n  model: m.gguf\n")
			out, err := modelupdates.MergeConfig(installed, deleted, planned, map[string]string{"parameters.temperature": modelupdates.ChoiceMine})
			Expect(err).ToNot(HaveOccurred())
			m := map[string]any{}
			Expect(yaml.Unmarshal(out, &m)).To(Succeed())
			Expect(m["parameters"]).ToNot(HaveKey("temperature"))
		})
	})
})
