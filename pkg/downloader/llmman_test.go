package downloader

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestLinkOrCopyTreeHardLinksADirectory(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "out")

	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "model.safetensors"), []byte("w"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := linkOrCopyTree(src, dst); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(dst, "sub", "model.safetensors"))
	if err != nil || string(got) != "w" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "config.json")); err != nil {
		t.Fatal(err)
	}

	// A model shared with llmman's store should cost its bytes once.
	var a, b syscall.Stat_t
	if err := syscall.Stat(filepath.Join(src, "config.json"), &a); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Stat(filepath.Join(dst, "config.json"), &b); err != nil {
		t.Fatal(err)
	}
	if a.Ino != b.Ino {
		t.Errorf("expected a hard link (same inode), got %d vs %d", a.Ino, b.Ino)
	}
}

func TestLinkOrCopyTreeHandlesASingleFilePayload(t *testing.T) {
	// A GGUF payload resolves to the file itself, not a directory.
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "model.gguf")
	if err := os.WriteFile(src, []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "nested", "model.gguf")

	if err := linkOrCopyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "gguf" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestLinkOrCopyTreeOverwritesAnExistingFile(t *testing.T) {
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "model.gguf")
	if err := os.WriteFile(src, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(dst, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := linkOrCopyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "new" {
		t.Fatalf("got %q", got)
	}
}

func TestLinkOrCopyTreeReportsAMissingSource(t *testing.T) {
	err := linkOrCopyTree(filepath.Join(t.TempDir(), "gone"), t.TempDir())
	if err == nil {
		t.Fatal("expected an error")
	}
}
