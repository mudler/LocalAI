package store

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// shortWriter writes half of what it is given and then fails, as a full disk
// does.
type shortWriter struct {
	*os.File
	truncatedTo int64
}

func (w *shortWriter) Write(p []byte) (int, error) {
	n, _ := w.File.Write(p[:len(p)/2])
	return n, errors.New("no space left on device")
}

func (w *shortWriter) Truncate(size int64) error {
	w.truncatedTo = size
	return w.File.Truncate(size)
}

var _ = ginkgo.Describe("appending to a transcript file", func() {
	ginkgo.It("cuts the file back to its old end after a failed append", func() {
		p := filepath.Join(ginkgo.GinkgoT().TempDir(), "chat.entries")
		Expect(os.WriteFile(p, []byte("{\"seq\":1}\n"), 0o600)).To(Succeed())
		f, err := os.OpenFile(p, os.O_APPEND|os.O_RDWR, 0o600)
		Expect(err).NotTo(HaveOccurred())
		defer f.Close()
		w := &shortWriter{File: f}
		Expect(appendTo(w, 10, []byte("{\"seq\":2}\n"))).To(HaveOccurred(), "the failed write was reported as a success")
		got, _ := os.ReadFile(p)
		Expect(string(got)).To(Equal("{\"seq\":1}\n"), "want the old line only")
		Expect(w.truncatedTo).To(BeEquivalentTo(10))
	})

	ginkgo.It("appendLines keeps earlier lines", func() {
		p := filepath.Join(ginkgo.GinkgoT().TempDir(), "chat.entries")
		for _, l := range []string{"a\n", "b\n"} {
			Expect(appendLines(p, []byte(l))).To(Succeed())
		}
		got, _ := os.ReadFile(p)
		Expect(string(got)).To(Equal("a\nb\n"))
	})
})
