//go:build linux

package workspace

import (
	"context"
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

// Walk calls fn for every entry below Root, parents before children.
func (d DirSource) Walk(ctx context.Context, fn func(UpperEntry) error) error {
	return filepath.WalkDir(d.Root, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(d.Root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		e := UpperEntry{Path: filepath.ToSlash(rel), Mode: info.Mode()}
		switch m := info.Mode(); {
		case m.IsRegular():
			e.Kind = EntryFile
			path := p
			e.Open = func() (io.ReadCloser, error) { return os.Open(path) }
		case m.IsDir():
			e.Kind = EntryDir
			e.Xattrs = overlayXattrs(p)
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
