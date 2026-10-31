package workspace

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"sync/atomic"
)

// The limits of an upper layer read from an archive, the same as the ones the
// backends keep when they read a layer themselves.
const archiveMaxDepth = 512

// archiveMaxEntries is a variable so that a test can lower it.
var archiveMaxEntries = 1000000

// ArchiveSource reads an upper layer from a tar stream. A sandbox that cannot
// hand the host a folder, for example because it keeps the layer on a disk
// image or in another process, sends the layer this way.
//
// Each member is one entry. A regular file, folder and symbolic link are what
// they say; a character device 0/0 is a whiteout (a delete); any other kind is
// EntryOther. The overlay attributes `trusted.overlay.*` and `user.overlay.*`
// are read from PAX records named `SCHILY.xattr.<name>`; other attributes are
// dropped. A name that is empty, absolute, holds `..` or a NUL, a path deeper
// than 512 levels or more than 1,000,000 entries fail the whole read.
//
// The stream is read once: Walk opens a file's content only while it calls fn,
// and the second Walk is an error.
func ArchiveSource(r io.Reader) UpperSource { return &archiveSource{r: r} }

type archiveSource struct {
	r    io.Reader
	used atomic.Bool
}

func (a *archiveSource) Walk(ctx context.Context, fn func(UpperEntry) error) error {
	if !a.used.CompareAndSwap(false, true) {
		return errors.New("workspace: an archive can be read once")
	}
	tr := tar.NewReader(a.r)
	for n := 0; ; n++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("workspace: read the archive: %w", err)
		}
		if n >= archiveMaxEntries {
			return fmt.Errorf("%w: the archive has more than %d entries", ErrInvalid, archiveMaxEntries)
		}
		p, err := archivePath(h.Name)
		if err != nil {
			return err
		}
		e := UpperEntry{Path: p, Mode: h.FileInfo().Mode()}
		switch h.Typeflag {
		case tar.TypeReg:
			e.Kind = EntryFile
			e.Open = func() (io.ReadCloser, error) { return io.NopCloser(tr), nil }
		case tar.TypeDir:
			e.Kind = EntryDir
		case tar.TypeSymlink:
			e.Kind = EntrySymlink
		case tar.TypeChar:
			if h.Devmajor == 0 && h.Devminor == 0 {
				e.Kind = EntryWhiteout
			} else {
				e.Kind = EntryOther
			}
		default:
			e.Kind = EntryOther
		}
		for k, v := range h.PAXRecords {
			name, ok := strings.CutPrefix(k, "SCHILY.xattr.")
			if ok && (strings.HasPrefix(name, "trusted.overlay.") || strings.HasPrefix(name, "user.overlay.")) {
				if e.Xattrs == nil {
					e.Xattrs = map[string][]byte{}
				}
				e.Xattrs[name] = []byte(v)
			}
		}
		if e.Kind == EntryDir {
			e.Mode |= fs.ModeDir
		}
		if err := fn(e); err != nil {
			return err
		}
	}
}

// archivePath cleans a member name into a relative slash path, or refuses it.
func archivePath(name string) (string, error) {
	if name == "" || strings.HasPrefix(name, "/") || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("%w: the archive has an entry named %q", ErrInvalid, name)
	}
	var segs []string
	for s := range strings.SplitSeq(strings.TrimSuffix(name, "/"), "/") {
		switch s {
		case "", ".":
			continue
		case "..":
			return "", fmt.Errorf("%w: the archive has an entry named %q", ErrInvalid, name)
		}
		segs = append(segs, s)
	}
	if len(segs) == 0 {
		return "", fmt.Errorf("%w: the archive has an entry named %q", ErrInvalid, name)
	}
	if len(segs) > archiveMaxDepth {
		return "", fmt.Errorf("%w: the archive has a path deeper than %d levels", ErrInvalid, archiveMaxDepth)
	}
	return strings.Join(segs, "/"), nil
}
