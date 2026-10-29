//go:build linux

package workspace_test

import (
	"context"
	"io"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// walkAndSwap writes the upper folder, then walks it. At the entry named
// swapAt, swap runs before the host opens the file, as a guest could do
// between the listing and the read. It returns what Open gave for each file.
func walkAndSwap(root, swapAt string, swap func()) (read map[string]string, refused map[string]bool) {
	GinkgoHelper()
	read, refused = map[string]string{}, map[string]bool{}
	err := workspace.DirSource{Root: root}.Walk(context.Background(), func(e workspace.UpperEntry) error {
		if e.Kind != workspace.EntryFile {
			return nil
		}
		if e.Path == swapAt {
			swap()
		}
		rc, err := e.Open()
		if err != nil {
			refused[e.Path] = true
			return nil
		}
		defer rc.Close()
		b, _ := io.ReadAll(rc)
		read[e.Path] = string(b)
		return nil
	})
	Expect(err).ToNot(HaveOccurred())
	return read, refused
}

func writeTree(root string, files map[string]string) {
	GinkgoHelper()
	for name, body := range files {
		p := filepath.Join(root, name)
		Expect(os.MkdirAll(filepath.Dir(p), 0o755)).To(Succeed())
		Expect(os.WriteFile(p, []byte(body), 0o644)).To(Succeed())
	}
}

var _ = Describe("DirSource", func() {
	// The guest owns the upper folder. A file that becomes a link to a host file
	// between the listing and the read must not be ingested.
	It("refuses a file swapped for a link", func() {
		secret := filepath.Join(GinkgoT().TempDir(), "secret")
		Expect(os.WriteFile(secret, []byte("host data"), 0o600)).To(Succeed())
		root := GinkgoT().TempDir()
		writeTree(root, map[string]string{"plain.txt": "guest data", "swapped.txt": "guest data"})

		read, refused := walkAndSwap(root, "swapped.txt", func() {
			p := filepath.Join(root, "swapped.txt")
			_ = os.Remove(p)
			Expect(os.Symlink(secret, p)).To(Succeed())
		})
		Expect(refused).To(HaveKeyWithValue("swapped.txt", true), "a file swapped for a link was opened: %q", read["swapped.txt"])
		Expect(read["plain.txt"]).To(Equal("guest data"))
	})

	// A folder swapped for a link to a host folder must not be followed either.
	It("refuses a folder swapped for a link", func() {
		host := GinkgoT().TempDir()
		writeTree(host, map[string]string{"a.txt": "host data"})
		root := GinkgoT().TempDir()
		writeTree(root, map[string]string{"sub/a.txt": "guest data"})

		read, refused := walkAndSwap(root, "sub/a.txt", func() {
			_ = os.RemoveAll(filepath.Join(root, "sub"))
			Expect(os.Symlink(host, filepath.Join(root, "sub"))).To(Succeed())
		})
		Expect(refused).To(HaveKeyWithValue("sub/a.txt", true), "a folder swapped for a link was followed: %q", read["sub/a.txt"])
	})
})
