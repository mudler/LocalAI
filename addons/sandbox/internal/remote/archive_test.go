package remote_test

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"io/fs"
	"runtime"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/remote"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// zeros is a file of n zero bytes that reports its size like an *os.File.
type zeros struct{ left int64 }

func (z *zeros) Read(p []byte) (int, error) {
	if z.left == 0 {
		return 0, io.EOF
	}
	n := min(int64(len(p)), z.left)
	clear(p[:n])
	z.left -= n
	return int(n), nil
}
func (*zeros) Close() error { return nil }
func (z *zeros) Stat() (fs.FileInfo, error) {
	return fakeInfo(z.left), nil
}

type fakeInfo int64

func (f fakeInfo) Name() string     { return "big" }
func (f fakeInfo) Size() int64      { return int64(f) }
func (fakeInfo) Mode() fs.FileMode  { return 0o644 }
func (fakeInfo) ModTime() time.Time { return time.Time{} }
func (fakeInfo) IsDir() bool        { return false }
func (fakeInfo) Sys() any           { return nil }

type oneFile struct{ size int64 }

func (o oneFile) Walk(_ context.Context, fn func(workspace.UpperEntry) error) error {
	return fn(workspace.UpperEntry{Path: "big.bin", Kind: workspace.EntryFile, Mode: 0o644,
		Open: func() (io.ReadCloser, error) { return &zeros{left: o.size}, nil }})
}

// A large file of the guest must not be held in memory to learn its size.
func TestTheArchiveStreamsAFileInsteadOfBufferingIt(t *testing.T) {
	const size = 96 << 20
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	var written int64
	_, _, err := remote.WriteArchive(context.Background(), oneFile{size}, countWriter{&written})
	if err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if written < size {
		t.Fatalf("archive holds %d bytes, want at least %d", written, size)
	}
	if got := after.TotalAlloc - before.TotalAlloc; got > size/2 {
		t.Fatalf("writing the archive allocated %d bytes for a %d byte file", got, size)
	}
}

type countWriter struct{ n *int64 }

func (c countWriter) Write(p []byte) (int, error) { *c.n += int64(len(p)); return len(p), nil }

// A file that changes size while it is read must fail the archive, not write a
// header that disagrees with the body.
func TestTheArchiveFailsWhenAFileChangesSizeWhileItIsRead(t *testing.T) {
	_, _, err := remote.WriteArchive(context.Background(), shrinking{}, io.Discard)
	if err == nil {
		t.Fatal("a short file was accepted")
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, tar.ErrWriteTooLong) && err.Error() == "" {
		t.Fatalf("unexpected error %v", err)
	}
}

type shrinking struct{}

func (shrinking) Walk(_ context.Context, fn func(workspace.UpperEntry) error) error {
	return fn(workspace.UpperEntry{Path: "f", Kind: workspace.EntryFile, Mode: 0o644,
		Open: func() (io.ReadCloser, error) { return &lying{zeros{left: 10}}, nil }})
}

// lying reports 100 bytes and delivers 10.
type lying struct{ zeros }

func (*lying) Stat() (fs.FileInfo, error) { return fakeInfo(100), nil }
