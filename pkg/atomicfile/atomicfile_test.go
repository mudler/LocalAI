package atomicfile_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/atomicfile"
)

func TestAtomicfile(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Atomicfile Suite")
}

var _ = Describe("Write", func() {
	It("creates a file and replaces it with the permission asked", func() {
		path := filepath.Join(GinkgoT().TempDir(), "state.json")
		Expect(atomicfile.Write(path, []byte("one"), 0o600)).To(Succeed())
		Expect(atomicfile.Write(path, []byte("two"), 0o640)).To(Succeed())
		got, err := os.ReadFile(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(got)).To(Equal("two"))
		st, err := os.Stat(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(st.Mode().Perm()).To(Equal(os.FileMode(0o640)))
	})

	It("leaves no temporary file behind", func() {
		dir := GinkgoT().TempDir()
		Expect(atomicfile.Write(filepath.Join(dir, "a.json"), []byte("x"), 0o600)).To(Succeed())
		ents, err := os.ReadDir(dir)
		Expect(err).ToNot(HaveOccurred())
		Expect(ents).To(HaveLen(1))
	})

	It("keeps the old file and removes the temporary one when the folder is missing or the target is a folder", func() {
		dir := GinkgoT().TempDir()
		Expect(atomicfile.Write(filepath.Join(dir, "missing", "a.json"), []byte("x"), 0o600)).ToNot(Succeed())
		target := filepath.Join(dir, "target")
		Expect(os.Mkdir(target, 0o700)).To(Succeed())
		Expect(atomicfile.Write(target, []byte("x"), 0o600)).ToNot(Succeed())
		ents, _ := os.ReadDir(dir)
		Expect(ents).To(HaveLen(1), "only the folder remains")
	})

	It("tells that the content is in place when only the sync of the folder fails", func() {
		path := filepath.Join(GinkgoT().TempDir(), "a.json")
		renamed, err := atomicfile.WriteWith(path, []byte("new"), 0o600, atomicfile.Options{
			SyncDir: func(*os.File) error { return errors.New("disk detached") },
		})
		Expect(err).To(MatchError(ContainSubstring("disk detached")))
		Expect(renamed).To(BeTrue())
		got, _ := os.ReadFile(path)
		Expect(string(got)).To(Equal("new"))
	})
})
