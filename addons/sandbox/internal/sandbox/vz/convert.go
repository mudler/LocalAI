package vz

import (
	"context"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ext4image"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ociimage"
)

// maxKernelBytes bounds the kernel that one conversion writes to the cache.
var maxKernelBytes = ext4image.DefaultMaxKernelBytes

// image returns the converted image for ref, converting it when the cache
// does not have it. The guest image of a Mac is the arm64 variant of the
// reference: ociimage picks the platform of this process.
func (b *Backend) image(ctx context.Context, ref string) (guestImage, error) {
	if ref == "" {
		ref = b.opts.DefaultImage
	}
	if b.loadImage != nil {
		return b.loadImage(ctx, ref)
	}
	if ref == "" {
		return guestImage{}, errors.New("vz: no image: set Spec.Image or Options.DefaultImage")
	}
	return b.imageFromCache(ctx, ref)
}

// imageFromCache converts ref into the cache, or finds it there.
func (b *Backend) imageFromCache(ctx context.Context, ref string) (guestImage, error) {
	if ref == "" {
		ref = b.opts.DefaultImage
	}
	img, err := ext4image.Load(ctx, b.opts.CacheDir, ref, maxKernelBytes)
	if err != nil {
		return guestImage{}, fmt.Errorf("vz: %w", err)
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
