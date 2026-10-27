package chv

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestWatchConsole checks that the watcher reports a kernel panic in the
// console log and stays quiet without one.
func TestWatchConsole(t *testing.T) {
	dir := shortDir(t)
	path := filepath.Join(dir, "console.log")
	if err := os.WriteFile(path, []byte("boot ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	panicked := make(chan struct{}, 1)
	go watchConsole(path, done, 10*time.Millisecond, 0, func() { panicked <- struct{}{} }, nil)
	select {
	case <-panicked:
		t.Fatal("reported a panic without one in the log")
	case <-time.After(100 * time.Millisecond):
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("[  1.0] Kernel panic - not syncing: Attempted to kill init!\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	select {
	case <-panicked:
	case <-time.After(2 * time.Second):
		t.Fatal("a panic in the log was not reported")
	}
	close(done)
}

// TestWatchConsoleCap checks that a console log over the cap is reported once
// and that trimConsole then keeps only the last bytes of it.
func TestWatchConsoleCap(t *testing.T) {
	dir := shortDir(t)
	path := filepath.Join(dir, "console.log")
	body := append(bytes.Repeat([]byte("A"), 5000), []byte("TAIL-MARK")...)
	if err := os.WriteFile(path, body[:1000], 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	defer close(done)
	flooded := make(chan struct{}, 4)
	go watchConsole(path, done, 10*time.Millisecond, 2048, func() { t.Error("panic reported") }, func() { flooded <- struct{}{} })
	select {
	case <-flooded:
		t.Fatal("reported a flood under the cap")
	case <-time.After(100 * time.Millisecond):
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-flooded:
	case <-time.After(2 * time.Second):
		t.Fatal("a log over the cap was not reported")
	}
	if err := trimConsole(path, 100); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 100 || !bytes.HasSuffix(got, []byte("TAIL-MARK")) {
		t.Fatalf("trimmed log: %d bytes, %q", len(got), got)
	}
	// A log smaller than the keep size stays as it is.
	if err := trimConsole(path, 1000); err != nil {
		t.Fatal(err)
	}
	if got2, _ := os.ReadFile(path); !bytes.Equal(got, got2) {
		t.Fatal("a small log changed")
	}
}
