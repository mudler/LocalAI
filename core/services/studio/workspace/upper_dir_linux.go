//go:build linux

package workspace

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// DirSource reads an overlay upper layer from a folder on this machine.
type DirSource struct {
	Root string
}

// Walk calls fn for every entry below Root, parents before children. The
// guest writes the folder while the host reads it, so every access goes
// through an os.Root: a file or a folder swapped for a link that leaves Root
// is refused instead of followed. UpperEntry.Open is valid only while fn runs.
func (d DirSource) Walk(ctx context.Context, fn func(UpperEntry) error) error {
	root, err := os.OpenRoot(d.Root)
	if err != nil {
		return err
	}
	defer root.Close()
	return fs.WalkDir(root.FS(), ".", func(rel string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		info, err := root.Lstat(rel)
		if err != nil {
			return err
		}
		e := UpperEntry{Path: rel, Mode: info.Mode()}
		switch m := info.Mode(); {
		case m.IsRegular():
			e.Kind = EntryFile
			e.Open = func() (io.ReadCloser, error) { return openSame(root, rel, info) }
		case m.IsDir():
			e.Kind = EntryDir
			e.Xattrs = overlayXattrs(filepath.Join(d.Root, rel))
		case m&fs.ModeCharDevice != 0 && isWhiteout(info):
			e.Kind = EntryWhiteout
		case m&fs.ModeSymlink != 0:
			e.Kind = EntrySymlink
		default:
			e.Kind = EntryOther
		}
		return fn(e)
	})
}

// openSame opens rel below root and checks that it is still the file that
// was listed. A path swapped for another file after the listing is refused.
func openSame(root *os.Root, rel string, listed fs.FileInfo) (io.ReadCloser, error) {
	f, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	now, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !now.Mode().IsRegular() || !os.SameFile(listed, now) {
		_ = f.Close()
		return nil, fmt.Errorf("workspace: %s changed while it was read", rel)
	}
	return f, nil
}

func isWhiteout(info fs.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Rdev == 0
}

func overlayXattrs(p string) map[string][]byte {
	out := map[string][]byte{}
	buf := make([]byte, 64)
	for _, name := range []string{xattrOpaqueTrusted, xattrOpaqueUser} {
		if n, err := syscall.Getxattr(p, name, buf); err == nil {
			out[name] = append([]byte(nil), buf[:n]...)
		}
	}
	return out
}
