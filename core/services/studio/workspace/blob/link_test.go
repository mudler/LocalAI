package blob_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
)

func TestLinkTo(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Blob LinkTo Suite")
}

var _ = Describe("LinkTo into an open folder", func() {
	stores := func() map[string]blob.Store {
		fs, err := blob.NewFS(GinkgoT().TempDir())
		Expect(err).ToNot(HaveOccurred())
		objects, err := storage.NewFilesystemStore(GinkgoT().TempDir())
		Expect(err).ToNot(HaveOccurred())
		return map[string]blob.Store{"fs": fs, "objectstore": blob.NewObjectStore(objects, GinkgoT().TempDir())}
	}

	for _, name := range []string{"fs", "objectstore"} {
		It("cannot send the file outside the folder through a link ("+name+")", func() {
			s := stores()[name]
			ctx := context.Background()
			sha, _, err := s.Put(ctx, strings.NewReader("kept inside"), 0)
			Expect(err).ToNot(HaveOccurred())
			folder, outside := GinkgoT().TempDir(), GinkgoT().TempDir()
			Expect(os.Symlink(outside, filepath.Join(folder, "sub"))).To(Succeed())
			root, err := os.OpenRoot(folder)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = root.Close() })

			Expect(s.LinkTo(ctx, sha, root, "sub/x")).ToNot(Succeed(), "LinkTo followed a link out of the folder")
			for _, bad := range []string{"../x", "/abs", "a/../../x", ""} {
				Expect(s.LinkTo(ctx, sha, root, bad)).ToNot(Succeed(), "name %q", bad)
			}
			ents, err := os.ReadDir(outside)
			Expect(err).ToNot(HaveOccurred())
			Expect(ents).To(BeEmpty(), "a file reached the folder outside")

			Expect(os.Mkdir(filepath.Join(folder, "real"), 0o755)).To(Succeed())
			Expect(s.LinkTo(ctx, sha, root, "real/x")).To(Succeed())
			got, err := os.ReadFile(filepath.Join(folder, "real", "x"))
			Expect(err).ToNot(HaveOccurred())
			Expect(string(got)).To(Equal("kept inside"))
		})
	}
})
