// SPDX-License-Identifier: MIT
package packages

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var ErrPackage = errors.New("invalid or unverified addon package")

type fileWriter interface {
	io.Writer
	Close() error
	Sync() error
}
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(b)
}
func (i *Installer) create(p string, mode os.FileMode) (fileWriter, error) {
	if i.writeFile != nil {
		return i.writeFile(p, mode)
	}
	return os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode) // #nosec G304 -- path is under the add-on state root and is built from validated identifiers, never from a request
}

type extraction struct {
	bytes   int64
	written int64
	files   int
	seen    map[string]bool
}

func (i *Installer) extract(ctx context.Context, r io.Reader, dir string, x *extraction) error {
	bounded := &expandedReader{r: contextReader{ctx, r}, x: x, limit: i.byteLimit()}
	t := tar.NewReader(bounded)
	for {
		h, e := t.Next()
		if e == io.EOF {
			// tar stops at its end markers; trailing bytes still consume the budget.
			_, e = io.Copy(io.Discard, bounded)
			return e
		}
		if e != nil {
			return e
		}
		n := strings.TrimSuffix(h.Name, "/")
		if n == "" || n == "." || path.Clean(n) != n || strings.HasPrefix(n, "/") || strings.HasPrefix(n, "../") || n == ".." || strings.ContainsAny(n, "\\:\x00") || x.seen[n] {
			return ErrPackage
		}
		x.seen[n] = true
		x.files++
		if x.files > i.fileLimit() || h.Size < 0 || h.Size > i.byteLimit()-x.written {
			return ErrPackage
		}
		target := filepath.Join(dir, filepath.FromSlash(n))
		switch h.Typeflag {
		case tar.TypeDir:
			if e = os.MkdirAll(target, 0700); e != nil {
				return e
			}
		case tar.TypeReg, tar.TypeRegA:
			if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
				return e
			}
			mode := os.FileMode(0600)
			if h.Mode&0111 != 0 {
				mode = 0700
			}
			f, e := i.create(target, mode)
			if e != nil {
				return e
			}
			n, copyErr := io.CopyN(f, contextReader{ctx, t}, h.Size)
			x.written += n // tar.Reader synthesizes sparse holes absent from the input stream.
			e = copyErr
			if e == nil {
				e = f.Sync()
			}
			ce := f.Close()
			if e != nil {
				return e
			}
			if ce != nil {
				return ce
			}
		default:
			return ErrPackage
		}
	}
}

// Count the stream before tar.Reader can hide PAX/GNU metadata or padding.
// The shared counter also bounds the sum of all OCI layers.
type expandedReader struct {
	r     io.Reader
	x     *extraction
	limit int64
}

func (r *expandedReader) Read(p []byte) (int, error) {
	remaining := r.limit - r.x.bytes
	if remaining < 0 {
		return 0, ErrPackage
	}
	if int64(len(p)) > remaining+1 {
		p = p[:remaining+1]
	}
	n, e := r.r.Read(p)
	r.x.bytes += int64(n)
	if r.x.bytes > r.limit {
		return n, ErrPackage
	}
	return n, e
}
