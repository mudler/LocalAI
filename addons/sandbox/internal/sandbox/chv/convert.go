package chv

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

// layoutPrefix marks a reference to an image in a local OCI layout folder.
const layoutPrefix = ociimage.LayoutPrefix

// kernelPath is where the guest image keeps its kernel.
const kernelPath = "boot/vmlinux"

// maxKernelBytes bounds the kernel that one conversion writes to the cache.
// A kernel of this build is 50 to 80 MB.
var maxKernelBytes int64 = 1 << 30

// guestImage is a converted image in the cache.
type guestImage struct {
	Ref    string   // the reference it came from, with digest
	Root   string   // root.ext4
	Kernel string   // vmlinux
	Env    []string // the image config environment
}

// resolve and blobPath are the shared image code under their old names.
var (
	resolve  = ociimage.Resolve
	blobPath = ociimage.BlobPath
)

// image returns the converted image for ref, converting it when the cache
// does not have it. Two callers that convert the same digest at once share
// one conversion through a lock file (ociimage.Prepare).
func (b *Backend) image(ctx context.Context, ref string) (guestImage, error) {
	if ref == "" {
		ref = b.opts.DefaultImage
	}
	if ref == "" {
		return guestImage{}, errors.New("chv: no image: set Spec.Image or Options.DefaultImage")
	}
	dir, env, err := ociimage.Prepare(ctx, b.opts.CacheDir, ref, convert)
	if err != nil {
		return guestImage{}, fmt.Errorf("chv: %w", err)
	}
	return guestImage{Ref: ref, Root: filepath.Join(dir, "root.ext4"), Kernel: filepath.Join(dir, "vmlinux"), Env: env}, nil
}

// convert flattens img into dir/root.ext4 and copies its kernel to
// dir/vmlinux. ociimage.Prepare writes env.json afterwards, which marks the
// folder complete.
func convert(img v1.Image, dir string) error {
	rootTmp, err := os.CreateTemp(dir, "root-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(rootTmp.Name())
	defer rootTmp.Close()
	kernTmp, err := os.CreateTemp(dir, "vmlinux-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(kernTmp.Name())
	defer kernTmp.Close()

	flat := ociimage.Extract(img)
	defer flat.Close()
	pr, pw := io.Pipe()
	kernel := make(chan error, 1)
	go func() { kernel <- copyKernel(pr, kernTmp) }()
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
func copyKernel(r io.Reader, out io.Writer) error {
	defer io.Copy(io.Discard, r)
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
		if path.Clean(strings.TrimPrefix(h.Name, "./")) != kernelPath {
			continue
		}
		if h.Typeflag != tar.TypeReg {
			return fmt.Errorf("/%s is not a regular file", kernelPath)
		}
		n, err := io.Copy(out, io.LimitReader(tr, maxKernelBytes+1))
		if err != nil {
			return err
		}
		if n > maxKernelBytes {
			return fmt.Errorf("/%s is larger than %d bytes", kernelPath, maxKernelBytes)
		}
		found = true
	}
	if !found {
		return fmt.Errorf("the image has no /%s", kernelPath)
	}
	return nil
}
