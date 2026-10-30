//go:build linux

package gvisor

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ociimage"
)

// guestImage is an unpacked image in the cache.
type guestImage struct {
	Ref  string   // the reference it came from, with digest
	Root string   // the root folder, mounted read-only in the sandbox
	Env  []string // the image config environment
}

// maxUnpackBytes bounds the file content that one unpack writes to the cache.
var maxUnpackBytes int64 = 16 << 30

// maxUnpackEntries bounds the number of tar entries of one unpack, so a layer
// of millions of empty files, links or folders cannot use up the inodes.
var maxUnpackEntries = 5_000_000

// image returns the unpacked image for ref, unpacking it when the cache does
// not have it (ociimage.PrepareTar).
func (b *Backend) image(ctx context.Context, ref string) (guestImage, error) {
	if ref == "" {
		ref = b.opts.DefaultImage
	}
	if ref == "" {
		return guestImage{}, errors.New("gvisor: no image: set Spec.Image or Options.DefaultImage")
	}
	dir, env, err := ociimage.PrepareTar(ctx, b.opts.CacheDir, ref, unpack)
	if err != nil {
		return guestImage{}, fmt.Errorf("gvisor: %w", err)
	}
	return guestImage{Ref: ref, Root: filepath.Join(dir, "rootfs"), Env: env}, nil
}

// unpack writes the flattened layer tar r into dir/rootfs as the calling
// user. Every file belongs to that user, which the sandbox maps to uid 1000.
// Setuid and setgid bits are dropped, device nodes and fifos are skipped,
// symlinks and hard links are kept. All writes go through an os.Root, so an
// entry can never leave the folder, even through a symlink that an earlier
// entry made. os.OpenRoot, Root.OpenFile and Root.Mkdir need Go 1.24; the
// methods MkdirAll, RemoveAll, Chmod, Symlink and Link need Go 1.25 (the
// module requires go 1.25.0).
func unpack(r io.Reader, dir string) (err error) {
	rootDir := filepath.Join(dir, "rootfs")
	if err := os.Mkdir(rootDir, 0o755); err != nil {
		return err
	}
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	// A failed unpack leaves nothing behind that cannot be removed.
	defer func() {
		if err != nil {
			_ = ociimage.RemoveTree(rootDir)
		}
	}()
	type dirMode struct {
		name string
		mode fs.FileMode
	}
	var dirs []dirMode
	var total int64
	var entries int
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if entries++; entries > maxUnpackEntries {
			return fmt.Errorf("the image has more than %d entries", maxUnpackEntries)
		}
		if err := ociimage.CheckEntryName(h.Name); err != nil {
			return err
		}
		name := path.Clean(strings.TrimPrefix(h.Name, "./"))
		if name == "." {
			dirs = append(dirs, dirMode{".", entryMode(h)})
			continue
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(name, 0o700); err != nil {
				return err
			}
			dirs = append(dirs, dirMode{name, entryMode(h)})
		case tar.TypeReg:
			if err := root.MkdirAll(path.Dir(name), 0o755); err != nil {
				return err
			}
			if err := root.RemoveAll(name); err != nil {
				return err
			}
			f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return err
			}
			n, err := io.Copy(f, io.LimitReader(tr, maxUnpackBytes-total+1))
			total += n
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
			if total > maxUnpackBytes {
				return fmt.Errorf("the image is larger than %d bytes", maxUnpackBytes)
			}
			if err := root.Chmod(name, entryMode(h)); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := root.MkdirAll(path.Dir(name), 0o755); err != nil {
				return err
			}
			if err := root.RemoveAll(name); err != nil {
				return err
			}
			if err := root.Symlink(h.Linkname, name); err != nil {
				return err
			}
		case tar.TypeLink:
			if err := ociimage.CheckEntryName(h.Linkname); err != nil {
				return err
			}
			if err := root.MkdirAll(path.Dir(name), 0o755); err != nil {
				return err
			}
			if err := root.RemoveAll(name); err != nil {
				return err
			}
			if err := root.Link(path.Clean(strings.TrimPrefix(h.Linkname, "./")), name); err != nil {
				return err
			}
		default:
			// Device nodes and fifos: the sandbox has its own /dev, and an
			// unprivileged user cannot make device nodes.
		}
	}
	// The folders the host binds into the sandbox must exist: the root is
	// read-only, so runsc cannot make them.
	for _, d := range []string{"mnt/lower", "mnt/upper", "mnt/ctl"} {
		if err := root.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	// Set the folder modes last and deepest first, so a read-only folder in
	// the image did not block the writes into it.
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i].name) > len(dirs[j].name) })
	// A later entry may have replaced a folder by a file or a symlink; Chmod
	// follows symlinks inside the root, so only real folders are touched.
	for _, d := range dirs {
		st, err := root.Lstat(d.name)
		if err != nil {
			return err
		}
		if !st.IsDir() {
			continue
		}
		if err := root.Chmod(d.name, d.mode); err != nil {
			return err
		}
	}
	return nil
}

// entryMode is the mode of a tar entry without setuid and setgid.
func entryMode(h *tar.Header) fs.FileMode {
	m := fs.FileMode(h.Mode & 0o777)
	if h.Mode&0o1000 != 0 {
		m |= fs.ModeSticky
	}
	return m
}
