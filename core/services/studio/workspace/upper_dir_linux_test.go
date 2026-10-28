//go:build linux

package workspace_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// walkAndSwap writes the upper folder, then walks it. At the entry named
// swapAt, swap runs before the host opens the file, as a guest could do
// between the listing and the read. It returns what Open gave for each file.
func walkAndSwap(t *testing.T, root, swapAt string, swap func()) (read map[string]string, refused map[string]bool) {
	t.Helper()
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
	if err != nil {
		t.Fatal(err)
	}
	return read, refused
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// The guest owns the upper folder. A file that becomes a link to a host file
// between the listing and the read must not be ingested.
func TestDirSourceRefusesAFileSwappedForALink(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("host data"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeTree(t, root, map[string]string{"plain.txt": "guest data", "swapped.txt": "guest data"})

	read, refused := walkAndSwap(t, root, "swapped.txt", func() {
		p := filepath.Join(root, "swapped.txt")
		_ = os.Remove(p)
		if err := os.Symlink(secret, p); err != nil {
			t.Fatal(err)
		}
	})
	if !refused["swapped.txt"] {
		t.Fatalf("a file swapped for a link was opened: %q", read["swapped.txt"])
	}
	if read["plain.txt"] != "guest data" {
		t.Fatalf("plain file = %q", read["plain.txt"])
	}
}

// A folder swapped for a link to a host folder must not be followed either.
func TestDirSourceRefusesAFolderSwappedForALink(t *testing.T) {
	host := t.TempDir()
	writeTree(t, host, map[string]string{"a.txt": "host data"})
	root := t.TempDir()
	writeTree(t, root, map[string]string{"sub/a.txt": "guest data"})

	read, refused := walkAndSwap(t, root, "sub/a.txt", func() {
		_ = os.RemoveAll(filepath.Join(root, "sub"))
		if err := os.Symlink(host, filepath.Join(root, "sub")); err != nil {
			t.Fatal(err)
		}
	})
	if !refused["sub/a.txt"] {
		t.Fatalf("a folder swapped for a link was followed: %q", read["sub/a.txt"])
	}
}
