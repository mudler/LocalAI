//go:build linux

package chv

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestCloneFileStaysSparse(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(256 << 20); err != nil {
		t.Fatal(err)
	}
	data := strings.Repeat("data", 1<<18) // 1 MiB
	if _, err := f.WriteAt([]byte(data), 100<<20); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := cloneFile(src, dst); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dst)
	if err != nil || st.Size() != 256<<20 {
		t.Fatalf("size = %v, %v", st, err)
	}
	if blocks := st.Sys().(*syscall.Stat_t).Blocks * 512; blocks > 16<<20 {
		t.Fatalf("the copy uses %d bytes on disk; it is not sparse", blocks)
	}
	g, _ := os.Open(dst)
	defer g.Close()
	buf := make([]byte, len(data))
	if _, err := g.ReadAt(buf, 100<<20); err != nil || string(buf) != data {
		t.Fatal("the data was not copied")
	}
	if _, err := g.ReadAt(buf[:4], 0); err != nil || string(buf[:4]) != "\x00\x00\x00\x00" {
		t.Fatal("the hole was not zero")
	}
}
