package gallery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/oci"
	"github.com/mudler/xlog"
)

const (
	// galleryArtifactType is what a published gallery declares as its
	// artifactType. The puller refuses anything else, so a plain container
	// image cannot be unpacked and read as a gallery.
	galleryArtifactType = "application/vnd.localai.gallery.v1"

	// galleryIndexFile is the layer title the index is published under. The
	// rest of the tree is kept next to it because entry URLs resolve against
	// the gallery root.
	galleryIndexFile = "index.yaml"

	// A gallery is a handful of small YAML files. Caps far below the puller's
	// defaults keep a hostile or mistaken publisher from filling the disk of
	// a machine that only asked to list some models.
	maxGalleryArtifactLayers = 512
	maxGalleryArtifactBytes  = int64(64 << 20)
)

// ociGalleryCacheTTL is how long an unpacked gallery artifact is served
// without asking the registry again.
//
// A gallery URL usually points at a moving tag, so an unpacked copy that never
// expired would pin a machine to whatever the publisher shipped the first time
// it looked. An hour matches the in-memory gallery cache in gallery.go, so a
// refresh that reaches this layer is one the listing cache already decided to
// make.
//
// A var so specs can drive expiry without sleeping.
var ociGalleryCacheTTL = time.Hour

// verifyGalleryArtifact checks the publisher signature over a digest-pinned
// artifact reference.
//
// A var so tests can drive the policy path without reaching the public
// Sigstore TUF mirror, which is a network dependency a unit test must not
// have.
var verifyGalleryArtifact = func(ctx context.Context, policy *config.GalleryVerification, digestRef string) error {
	verifier, err := newGalleryVerifier(policy)
	if err != nil {
		return err
	}
	return verifier.VerifyImage(ctx, digestRef)
}

// looksLikeOCIGallery reports whether a gallery candidate is an OCI artifact
// reference rather than something the HTTP downloader handles.
//
// Only the explicit oci:// scheme counts. downloader.URI.LooksLikeOCI also
// treats bare quay.io/ghcr.io/docker.io prefixes as OCI, which is right for a
// backend URI but wrong here: a gallery index served over HTTPS from one of
// those hosts is a perfectly ordinary URL, and it is written with a scheme.
func looksLikeOCIGallery(candidate string) bool {
	return strings.HasPrefix(candidate, downloader.OCIPrefix)
}

// ociGalleryCacheDir is where an unpacked gallery artifact lives.
//
// It follows galleryCachePath's convention: a sibling of the models directory
// so the unpacked YAML is never mistaken for an installed model config, named
// by a digest of the gallery URL so two galleries cannot collide, and empty
// for a non-absolute models directory because only an absolute one names a
// location we can reason about.
func ociGalleryCacheDir(basePath, url string) string {
	root := ociGalleryCacheRoot(basePath)
	if root == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(url))
	return filepath.Join(root, hex.EncodeToString(sum[:]))
}

// ociGalleryCacheRoot is the directory every unpacked gallery artifact lives
// under, or "" when the models directory does not name one we can reason
// about.
//
// It is named on its own because it is also the trusted root a read of an
// unpacked entry is confined to: the cache is deliberately a sibling of the
// models directory, so the models directory cannot be that root.
func ociGalleryCacheRoot(basePath string) string {
	if !filepath.IsAbs(basePath) {
		return ""
	}
	return filepath.Join(basePath, "..", "cache", "gallery", "oci")
}

// readCachedOCIGallery returns the cached index, if the cache holds one that
// is fresh and usable.
//
// The directory only exists because a completed pull was renamed into place,
// so its presence already means the content was verified before it landed.
// The index is still probed: a directory truncated by a full disk or edited by
// hand must not be served as if it were the gallery.
func readCachedOCIGallery(cacheDir string) ([]byte, bool) {
	info, err := os.Stat(cacheDir)
	if err != nil || !info.IsDir() {
		return nil, false
	}
	if time.Since(info.ModTime()) > ociGalleryCacheTTL {
		return nil, false
	}
	// #nosec G304 -- cacheDir is ociGalleryCacheDir's own construction, a hex
	// digest under a fixed directory, and the file name is a constant.
	body, err := os.ReadFile(filepath.Join(cacheDir, galleryIndexFile))
	if err != nil || !isUsableGalleryIndex(body) {
		return nil, false
	}
	return body, true
}

// fetchOCIGalleryIndex pulls a gallery artifact from a registry and returns
// its index.
//
// The pull lands in a staging directory and is renamed into place only once
// the whole tree is on disk and the index reads back as an index. The puller
// writes its layers with O_EXCL, so a directory holding partial files from an
// interrupted attempt would fail every later pull; and a partial tree that a
// later fetch served would hand the user a truncated gallery with no sign that
// anything went wrong.
func fetchOCIGalleryIndex(ctx context.Context, g config.Gallery, candidate, basePath string, requireIntegrity bool) ([]byte, error) {
	cacheDir := ociGalleryCacheDir(basePath, candidate)
	if cacheDir == "" {
		return nil, fmt.Errorf("gallery %q needs an absolute models directory to cache %q", g.Name, candidate)
	}
	if body, ok := readCachedOCIGallery(cacheDir); ok {
		return body, nil
	}

	pullRef := downloader.URI(candidate).OCIReference()

	if g.Verification != nil {
		// Resolve first, verify the digest, then pull that same digest.
		// Nothing has been fetched at this point beyond the manifest, so a
		// policy failure leaves no content anywhere.
		digestRef, err := oci.ResolveArtifactDigestRef(ctx, pullRef)
		if err != nil {
			return nil, err
		}
		if err := verifyGalleryArtifact(ctx, g.Verification, digestRef); err != nil {
			return nil, fmt.Errorf("gallery %q failed signature verification: %w", g.Name, err)
		}
		pullRef = digestRef
	} else if requireIntegrity {
		return nil, fmt.Errorf("strict integrity: gallery %q has no verification policy for %q (set verification: in the gallery configuration or disable --require-backend-integrity)", g.Name, candidate)
	} else {
		xlog.Warn("fetching an OCI gallery without signature verification",
			"gallery", g.Name, "url", candidate)
	}

	if err := os.MkdirAll(filepath.Dir(cacheDir), 0o750); err != nil {
		return nil, fmt.Errorf("could not create the gallery cache directory: %w", err)
	}
	staging, err := os.MkdirTemp(filepath.Dir(cacheDir), ".staging-*")
	if err != nil {
		return nil, fmt.Errorf("could not stage the gallery artifact: %w", err)
	}
	// Removed on every path but the successful rename, which moves the
	// directory out from under this call.
	defer func() {
		if rmErr := os.RemoveAll(staging); rmErr != nil && !os.IsNotExist(rmErr) {
			xlog.Debug("could not clean up a gallery staging directory", "path", staging, "error", rmErr)
		}
	}()

	if _, err := oci.PullArtifact(ctx, pullRef, staging,
		oci.WithArtifactType(galleryArtifactType),
		oci.WithMaxArtifactLayers(maxGalleryArtifactLayers),
		oci.WithMaxArtifactBytes(maxGalleryArtifactBytes)); err != nil {
		return nil, err
	}

	// #nosec G304 -- staging is this function's own temporary directory and
	// the file name is a constant.
	body, err := os.ReadFile(filepath.Join(staging, galleryIndexFile))
	if err != nil {
		return nil, fmt.Errorf("gallery artifact %q carries no %s: %w", candidate, galleryIndexFile, err)
	}
	if !isUsableGalleryIndex(body) {
		return nil, fmt.Errorf("gallery artifact %q has an %s that is not a gallery index", candidate, galleryIndexFile)
	}

	// The bytes are already in hand, so a rename that loses a race with
	// another fetch, or fails on a read-only cache, costs nothing but the next
	// fetch pulling again. The caller still gets this gallery.
	if err := os.RemoveAll(cacheDir); err != nil {
		xlog.Debug("could not clear a stale gallery cache entry", "path", cacheDir, "error", err)
	} else if err := os.Rename(staging, cacheDir); err != nil {
		xlog.Debug("could not install the gallery cache entry", "path", cacheDir, "error", err)
	}

	return body, nil
}
