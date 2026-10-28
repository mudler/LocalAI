package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
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

func TestAFailedAppendCutsTheFileBackToItsOldEnd(t *testing.T) {
	p := filepath.Join(t.TempDir(), "chat.entries")
	if err := os.WriteFile(p, []byte("{\"seq\":1}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := &shortWriter{File: f}
	if err := appendTo(w, 10, []byte("{\"seq\":2}\n")); err == nil {
		t.Fatal("the failed write was reported as a success")
	}
	got, _ := os.ReadFile(p)
	if string(got) != "{\"seq\":1}\n" || w.truncatedTo != 10 {
		t.Fatalf("file = %q, truncated to %d; want the old line only", got, w.truncatedTo)
	}
}

func TestAppendLinesKeepsEarlierLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "chat.entries")
	for _, l := range []string{"a\n", "b\n"} {
		if err := appendLines(p, []byte(l)); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := os.ReadFile(p); string(got) != "a\nb\n" {
		t.Fatalf("file = %q", got)
	}
}
