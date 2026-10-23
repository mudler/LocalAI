package utils_test

import (
	"os"
	"path/filepath"

	. "github.com/mudler/LocalAI/pkg/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ModelNameTaken", func() {
	var dir string

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
	})

	It("is free when nothing in the folder has the name", func() {
		Expect(ModelNameTaken(dir, "my-model")).To(BeFalse())
	})

	It("is taken by a config file with the name", func() {
		Expect(os.WriteFile(filepath.Join(dir, "my-model.yaml"), []byte("name: x"), 0600)).To(Succeed())
		Expect(ModelNameTaken(dir, "my-model")).To(BeTrue())
	})

	It("is taken by a directory with the name, with no config file", func() {
		Expect(os.Mkdir(filepath.Join(dir, "my-model"), 0750)).To(Succeed())
		Expect(ModelNameTaken(dir, "my-model")).To(BeTrue())
	})

	It("is taken by a plain file with the name", func() {
		Expect(os.WriteFile(filepath.Join(dir, "my-model"), []byte("x"), 0600)).To(Succeed())
		Expect(ModelNameTaken(dir, "my-model")).To(BeTrue())
	})

	It("is taken by a file that has the name with another extension", func() {
		Expect(os.WriteFile(filepath.Join(dir, "my-model.gguf"), []byte("x"), 0600)).To(Succeed())
		Expect(ModelNameTaken(dir, "my-model")).To(BeTrue())
	})

	It("is taken when the name cannot be matched, so the guard refuses", func() {
		Expect(ModelNameTaken(dir, "fo[")).To(BeTrue())
	})

	It("is not taken by a longer name that starts with it", func() {
		Expect(os.Mkdir(filepath.Join(dir, "my-model-2"), 0750)).To(Succeed())
		Expect(ModelNameTaken(dir, "my-model")).To(BeFalse())
	})
})
