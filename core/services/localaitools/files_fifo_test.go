//go:build unix

package localaitools_test

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	localaitools "github.com/mudler/LocalAI/core/services/localaitools"
)

var _ = Describe("DirFileStore with files that are not regular", func() {
	ctx := context.Background()

	It("does not block on a FIFO: it answers file not found", func() {
		dir := GinkgoT().TempDir()
		Expect(syscall.Mkfifo(filepath.Join(dir, "pipe.wav"), 0o600)).To(Succeed())
		done := make(chan error, 1)
		go func() {
			_, err := localaitools.DirFileStore{Dir: dir}.Read(ctx, "pipe.wav", 1<<20)
			done <- err
		}()
		select {
		case err := <-done:
			Expect(err).To(MatchError(localaitools.ErrFileNotFound))
		case <-time.After(5 * time.Second):
			Fail("Read blocked on a FIFO")
		}
	})

	It("still reads a regular file", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi"), 0o600)).To(Succeed())
		f, err := localaitools.DirFileStore{Dir: dir}.Read(ctx, "a.txt", 1<<20)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(f.Data)).To(Equal("hi"))
	})

	It("refuses to write a result over the cap", func() {
		defer localaitools.SetMaxResultBytes(8)()
		_, err := localaitools.DirFileStore{Dir: GinkgoT().TempDir()}.Write(ctx, "r.bin", make([]byte, 9), "")
		Expect(err).To(MatchError(localaitools.ErrFileTooLarge))
		_, err = localaitools.DirFileStore{Dir: GinkgoT().TempDir()}.Write(ctx, "r.bin", make([]byte, 8), "")
		Expect(err).NotTo(HaveOccurred())
	})
})
