package gallery_test

import (
	"os"
	"path/filepath"

	. "github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("StorageIndex", func() {
	var base string
	var state *system.SystemState

	write := func(name, content string) {
		Expect(os.WriteFile(filepath.Join(base, name), []byte(content), 0o644)).To(Succeed())
	}

	// The sidecar fixture is 54 bytes; its size shows up in alpha's total
	// because deletion counts it too — the index mirrors what delete frees.
	const sidecar = "files:\n- filename: alpha.gguf\n- filename: shared.gguf\n"

	BeforeEach(func() {
		base = GinkgoT().TempDir()
		var err error
		state, err = system.GetSystemState(system.WithModelPath(base))
		Expect(err).ToNot(HaveOccurred())

		// alpha: own weight plus the shared file via its gallery sidecar.
		write("alpha.yaml", "name: alpha\nparameters:\n  model: alpha.gguf\n")
		write("._gallery_alpha.yaml", sidecar)
		// beta: a hand-written config (no sidecar) reusing alpha's file.
		write("beta.yaml", "name: beta\nparameters:\n  model: shared.gguf\n")

		write("alpha.gguf", "aaaa")      // 4 bytes
		write("shared.gguf", "ssssssss") // 8 bytes
		write("unreferenced.bin", "uu")  // orphan detection is out of scope
	})

	It("attributes files to every config referencing them, sidecar included", func() {
		idx, err := BuildStorageIndex(state)
		Expect(err).ToNot(HaveOccurred())

		byName := map[string]ModelStorage{}
		for _, m := range idx.Models {
			byName[m.Name] = m
		}
		Expect(byName).To(HaveLen(2))
		Expect(byName["alpha"].SizeBytes).To(Equal(int64(4 + 8 + len(sidecar))))
		Expect(byName["alpha"].SharedBytes).To(Equal(int64(8)))
		Expect(byName["alpha"].Missing).To(BeEmpty())
		Expect(byName["beta"].SizeBytes).To(Equal(int64(8)))
		Expect(byName["beta"].SharedBytes).To(Equal(int64(8)))
		// beta has no sidecar on disk: its absence is normal, not a
		// broken reference.
		Expect(byName["beta"].Missing).To(BeEmpty())

		byPath := map[string]StorageFile{}
		for _, f := range idx.Files {
			byPath[f.Path] = f
		}
		Expect(byPath["shared.gguf"].Models).To(Equal([]string{"alpha", "beta"}))
		Expect(byPath["alpha.gguf"].Models).To(Equal([]string{"alpha"}))
		Expect(byPath["._gallery_alpha.yaml"].SizeBytes).To(Equal(int64(len(sidecar))))
	})

	It("deduplicates shared files in the total and ignores unreferenced ones", func() {
		idx, err := BuildStorageIndex(state)
		Expect(err).ToNot(HaveOccurred())
		Expect(idx.TotalBytes).To(Equal(int64(4 + 8 + len(sidecar))))
		for _, f := range idx.Files {
			Expect(f.Path).ToNot(Equal("unreferenced.bin"))
		}
	})

	It("reports a reference without a file as missing, per model and per file", func() {
		write("gamma.yaml", "name: gamma\nparameters:\n  model: never-downloaded.gguf\n")
		idx, err := BuildStorageIndex(state)
		Expect(err).ToNot(HaveOccurred())

		byName := map[string]ModelStorage{}
		for _, m := range idx.Models {
			byName[m.Name] = m
		}
		Expect(byName["gamma"].Missing).To(Equal([]string{"never-downloaded.gguf"}))
		Expect(byName["gamma"].SizeBytes).To(BeZero())

		byPath := map[string]StorageFile{}
		for _, f := range idx.Files {
			byPath[f.Path] = f
		}
		Expect(byPath["never-downloaded.gguf"].Missing).To(BeTrue())
		Expect(byPath["never-downloaded.gguf"].SizeBytes).To(BeZero())
		Expect(byPath["never-downloaded.gguf"].Models).To(Equal([]string{"gamma"}))
	})

	It("reports a broken config instead of failing the whole index", func() {
		write("broken.yaml", "::: not yaml :::\n\t")
		idx, err := BuildStorageIndex(state)
		Expect(err).ToNot(HaveOccurred())
		Expect(idx.Errors).To(HaveLen(1))
		Expect(idx.Errors[0]).To(ContainSubstring("broken"))
		Expect(idx.Models).To(HaveLen(2))
	})
})
