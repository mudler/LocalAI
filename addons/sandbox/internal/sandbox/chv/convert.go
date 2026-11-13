package chv

import (
	"context"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ext4image"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ociimage"
)

// layoutPrefix marks a reference to an image in a local OCI layout folder.
const layoutPrefix = ociimage.LayoutPrefix

// maxKernelBytes bounds the kernel that one conversion writes to the cache.
// A kernel of this build is 50 to 80 MB.
var maxKernelBytes = ext4image.DefaultMaxKernelBytes

// guestImage is a converted image in the cache.
type guestImage = ext4image.Image

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
	img, err := ext4image.Load(ctx, b.opts.CacheDir, ref, maxKernelBytes)
	if err != nil {
		return guestImage{}, fmt.Errorf("chv: %w", err)
	}
	return img, nil
}

// ImageCached says whether ref is converted in the cache.
func (b *Backend) ImageCached(ctx context.Context, ref string) bool {
	return ociimage.Cached(ctx, b.opts.CacheDir, ref)
}

// PrepareImage pulls and converts ref, so that a sandbox made later boots
// without waiting for it.
func (b *Backend) PrepareImage(ctx context.Context, ref string) error {
	_, err := b.image(ctx, ref)
	return err
}

// ForgetImage deletes the converted image of ref.
func (b *Backend) ForgetImage(ctx context.Context, ref string) error {
	return ociimage.Forget(ctx, b.opts.CacheDir, ref)
}
