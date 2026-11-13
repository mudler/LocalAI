// Package ext4image turns an OCI guest image into the files that a
// virtual-machine backend boots: root.ext4, a read-only ext4 root file system,
// and vmlinux, the kernel from /boot/vmlinux of the image.
//
// The conversion is pure Go. It needs no mke2fs, no loop device and no root
// rights, so it runs on every system that the add-on runs on. The Cloud
// Hypervisor backend and the Virtualization.framework backend share it.
package ext4image

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Microsoft/hcsshim/ext4/tar2ext4"
	v1 "github.com/google/go-containerregistry/pkg/v1"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ociimage"
)

// KernelPath is where the guest image keeps its kernel.
const KernelPath = "boot/vmlinux"

// DefaultMaxKernelBytes bounds the kernel that one conversion writes to the
// cache. A kernel of this build is 50 to 80 MB.
const DefaultMaxKernelBytes int64 = 1 << 30

// Image is a converted image in the cache.
type Image struct {
	Ref    string   // the reference it came from, with digest
	Root   string   // root.ext4
	Kernel string   // vmlinux
	Env    []string // the image config environment
}

// Load returns the converted image for ref, converting it when the cache does
// not have it. Two callers that convert the same digest at once share one
// conversion through a lock file (ociimage.Prepare). maxKernel is the largest
// kernel to accept, in bytes; zero means DefaultMaxKernelBytes. The backend
// that calls Load puts its own name in front of the error.
func Load(ctx context.Context, cacheDir, ref string, maxKernel int64) (Image, error) {
	dir, env, err := ociimage.Prepare(ctx, cacheDir, ref, func(img v1.Image, dir string) error {
		return Convert(img, dir, maxKernel)
	})
	if err != nil {
		return Image{}, err
	}
	return Image{Ref: ref, Root: filepath.Join(dir, "root.ext4"), Kernel: filepath.Join(dir, "vmlinux"), Env: env}, nil
}

// Convert flattens img into dir/root.ext4 and copies its kernel to
// dir/vmlinux. ociimage.Prepare writes env.json afterwards, which marks the
// folder complete. maxKernel is the largest kernel to accept, in bytes; zero
// means DefaultMaxKernelBytes.
func Convert(img v1.Image, dir string, maxKernel int64) error {
	if maxKernel <= 0 {
		maxKernel = DefaultMaxKernelBytes
	}
	rootTmp, err := os.CreateTemp(dir, "root-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(rootTmp.Name()) }()
	defer func() { _ = rootTmp.Close() }()
	kernTmp, err := os.CreateTemp(dir, "vmlinux-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(kernTmp.Name()) }()
	defer func() { _ = kernTmp.Close() }()

	flat := ociimage.Extract(img)
	defer func() { _ = flat.Close() }()
	pr, pw := io.Pipe()
	kernel := make(chan error, 1)
	go func() { kernel <- copyKernel(pr, kernTmp, maxKernel) }()
	err = tar2ext4.ConvertTarToExt4(io.TeeReader(flat, pw), rootTmp)
	if err == nil {
		_, err = io.Copy(pw, flat) // the rest of the stream, for the kernel reader
	}
	pw.CloseWithError(err)
	kerr := <-kernel
	if err != nil {
		return err
	}
	if kerr != nil {
		return kerr
	}
	for _, f := range []*os.File{rootTmp, kernTmp} {
		if err := f.Sync(); err != nil {
			return err
		}
	}
	if err := os.Rename(rootTmp.Name(), filepath.Join(dir, "root.ext4")); err != nil {
		return err
	}
	return os.Rename(kernTmp.Name(), filepath.Join(dir, "vmlinux"))
}

// copyKernel reads a tar stream, copies boot/vmlinux to out and drains the
// rest, so the writer of the stream never blocks.
func copyKernel(r io.Reader, out io.Writer, maxKernel int64) error {
	defer func() { _, _ = io.Copy(io.Discard, r) }()
	tr := tar.NewReader(r)
	found := false
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if err := ociimage.CheckEntryName(h.Name); err != nil {
			return err
		}
		if path.Clean(strings.TrimPrefix(h.Name, "./")) != KernelPath {
			continue
		}
		if h.Typeflag != tar.TypeReg {
			return fmt.Errorf("/%s is not a regular file", KernelPath)
		}
		n, err := io.Copy(out, io.LimitReader(tr, maxKernel+1))
		if err != nil {
			return err
		}
		if n > maxKernel {
			return fmt.Errorf("/%s is larger than %d bytes", KernelPath, maxKernel)
		}
		found = true
	}
	if !found {
		return fmt.Errorf("the image has no /%s", KernelPath)
	}
	return nil
}
