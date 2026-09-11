package worker

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("worker staging path containment", func() {
	DescribeTable("checks resolved directory boundaries",
		func(relative string, allowed bool) {
			root := GinkgoT().TempDir()
			models := filepath.Join(root, "models")
			sibling := filepath.Join(root, "models-other")
			Expect(os.Mkdir(models, 0o750)).To(Succeed())
			Expect(os.Mkdir(sibling, 0o750)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(models, "result.bin"), []byte("output"), 0o600)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(sibling, "private.bin"), []byte("private"), 0o600)).To(Succeed())
			Expect(os.Symlink(sibling, filepath.Join(models, "escape"))).To(Succeed())
			alias := filepath.Join(root, "alias")
			Expect(os.Symlink(root, alias)).To(Succeed())

			Expect(isPathAllowed(filepath.Join(alias, relative), []string{filepath.Join(alias, "models")})).To(Equal(allowed))
		},
		Entry("file beneath a symlinked root", "models/result.bin", true),
		Entry("the symlinked root itself", "models", true),
		Entry("a sibling sharing the directory prefix", "models-other/private.bin", false),
		Entry("a symlink escaping the allowed root", "models/escape/private.bin", false),
	)
})
