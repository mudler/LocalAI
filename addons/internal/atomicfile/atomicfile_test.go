// SPDX-License-Identifier: MIT
package atomicfile

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// failingReader gives some bytes and then fails, as a source that breaks off
// does.
type failingReader struct{ done bool }

func (r *failingReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, errors.New("the source broke off")
	}
	r.done = true
	return copy(p, "half"), nil
}

var _ = Describe("atomicfile", func() {
	var dir, path string

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		path = filepath.Join(dir, "state.json")
	})

	entries := func() []string {
		es, err := os.ReadDir(dir)
		Expect(err).NotTo(HaveOccurred())
		var names []string
		for _, e := range es {
			names = append(names, e.Name())
		}
		return names
	}

	It("writes a new file with its mode and leaves no temporary file", func() {
		Expect(Write(path, []byte("one"), 0o640)).To(Succeed())
		Expect(os.ReadFile(path)).To(Equal([]byte("one")))
		fi, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(fi.Mode().Perm()).To(Equal(os.FileMode(0o640)))
		Expect(entries()).To(Equal([]string{"state.json"}))
	})

	It("replaces the content of a file", func() {
		Expect(Write(path, []byte("one"), 0o600)).To(Succeed())
		Expect(WriteFrom(path, strings.NewReader("two"), 0o755)).To(Succeed())
		Expect(os.ReadFile(path)).To(Equal([]byte("two")))
		fi, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(fi.Mode().Perm()).To(Equal(os.FileMode(0o755)))
	})

	It("keeps the old file when the source fails", func() {
		Expect(Write(path, []byte("one"), 0o600)).To(Succeed())
		var r io.Reader = &failingReader{}
		Expect(WriteFrom(path, r, 0o600)).To(MatchError("the source broke off"))
		Expect(os.ReadFile(path)).To(Equal([]byte("one")))
		Expect(entries()).To(Equal([]string{"state.json"}))
	})

	It("fails when the folder does not exist", func() {
		Expect(Write(filepath.Join(dir, "missing", "state.json"), []byte("one"), 0o600)).NotTo(Succeed())
		Expect(entries()).To(BeEmpty())
	})
})
