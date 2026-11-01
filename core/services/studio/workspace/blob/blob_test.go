package blob_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing/iotest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
)

func newStores() map[string]blob.Store {
	GinkgoHelper()
	fs, err := blob.NewFS(GinkgoT().TempDir())
	Expect(err).ToNot(HaveOccurred())
	objects, err := storage.NewFilesystemStore(GinkgoT().TempDir())
	Expect(err).ToNot(HaveOccurred())
	return map[string]blob.Store{"fs": fs, "objectstore": blob.NewObjectStore(objects, GinkgoT().TempDir())}
}

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

var _ = Describe("Blob stores", func() {
	ctx := context.Background()

	for _, name := range []string{"fs", "objectstore"} {
		Describe(name, func() {
			var s blob.Store

			BeforeEach(func() {
				s = newStores()[name]
			})

			It("the same bytes are one blob named by their hash", func() {
				a, size, err := s.Put(ctx, strings.NewReader("hello"), 0)
				Expect(err).ToNot(HaveOccurred())
				Expect(a).To(Equal(sum("hello")))
				Expect(size).To(BeEquivalentTo(5))
				b, _, err := s.Put(ctx, strings.NewReader("hello"), 0)
				Expect(err).ToNot(HaveOccurred())
				Expect(b).To(Equal(a), "second Put: want the same hash")
				ok, err := s.Exists(ctx, a)
				Expect(err).ToNot(HaveOccurred())
				Expect(ok).To(BeTrue())
			})
			It("a stored blob reads back and seeks", func() {
				h, _, _ := s.Put(ctx, strings.NewReader("0123456789"), 0)
				r, err := s.Open(ctx, h)
				Expect(err).ToNot(HaveOccurred())
				defer func() { _ = r.Close() }()
				_, err = r.Seek(4, io.SeekStart)
				Expect(err).ToNot(HaveOccurred())
				got, _ := io.ReadAll(r)
				Expect(string(got)).To(Equal("456789"), "after Seek(4)")
			})
			It("a reader that fails mid-stream leaves no blob", func() {
				r := io.MultiReader(strings.NewReader("partial"), iotest.ErrReader(errors.New("cut off")))
				_, _, err := s.Put(ctx, r, 0)
				Expect(err).To(HaveOccurred(), "Put accepted a cut-off upload")
				ok, _ := s.Exists(ctx, sum("partial"))
				Expect(ok).To(BeFalse(), "a cut-off upload left a blob")
			})
			It("a stream over the limit is refused and leaves nothing", func() {
				big := bytes.Repeat([]byte("x"), 100)
				_, _, err := s.Put(ctx, bytes.NewReader(big), 10)
				Expect(err).To(MatchError(workspace.ErrTooLarge), "Put over the limit")
				ok, _ := s.Exists(ctx, sum(string(big)))
				Expect(ok).To(BeFalse(), "an over-limit upload left a blob")
			})
			It("a blob can be linked to a file in a folder, read-only", func() {
				h, _, _ := s.Put(ctx, strings.NewReader("linked bytes"), 0)
				folder := GinkgoT().TempDir()
				dst := filepath.Join(folder, "out.txt")
				root := openTestRoot(folder)
				Expect(s.LinkTo(ctx, h, root, "out.txt")).To(Succeed())
				got, err := os.ReadFile(dst)
				Expect(err).ToNot(HaveOccurred())
				Expect(string(got)).To(Equal("linked bytes"))
				st, _ := os.Stat(dst)
				Expect(st.Mode().Perm()&0o222).To(BeZero(), "the linked file is writable (%v): it would let a guest change the blob", st.Mode())
				Expect(s.LinkTo(ctx, h, root, "out.txt")).ToNot(Succeed(), "LinkTo replaced a file that exists")
				Expect(s.LinkTo(ctx, sum("never stored"), root, "x")).To(MatchError(workspace.ErrNotFound), "LinkTo of an unknown blob")
			})
			It("an unknown or malformed hash is not found", func() {
				for _, h := range []string{sum("never stored"), "../../etc/passwd", "", "zz"} {
					_, err := s.Open(ctx, h)
					Expect(err).To(MatchError(workspace.ErrNotFound), "Open(%q)", h)
					ok, err := s.Exists(ctx, h)
					Expect(err).ToNot(HaveOccurred(), "Exists(%q)", h)
					Expect(ok).To(BeFalse(), "Exists(%q)", h)
				}
			})
		})
	}
})

// openTestRoot opens dir as a root for the length of the spec.
func openTestRoot(dir string) *os.Root {
	GinkgoHelper()
	r, err := os.OpenRoot(dir)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { _ = r.Close() })
	return r
}
