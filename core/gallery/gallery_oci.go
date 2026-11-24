package gallery

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/oci/cosignverify"
	"github.com/mudler/LocalAI/pkg/oci/ociindex"
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

// galleryVerificationError marks a fetch that reached the source and was
// refused by the verification policy (or by strict integrity), as opposed to
// one that could not reach it. The caller must not answer a refusal with an
// older cached copy.
//
// strict tells the two refusals apart, so the message names the setting the
// operator has to change rather than a policy that may not even exist.
type galleryVerificationError struct {
	err    error
	strict bool
}

func (e *galleryVerificationError) Error() string { return e.err.Error() }
func (e *galleryVerificationError) Unwrap() error { return e.err }

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
// by a digest of the gallery URL and its verification policy so two galleries
// cannot collide and a changed policy never reuses what another one admitted,
// and empty
// for a non-absolute models directory because only an absolute one names a
// location we can reason about.
func ociGalleryCacheDir(basePath, url string, policy *config.GalleryVerification) string {
	root := ociGalleryCacheRoot(basePath)
	if root == "" {
		return ""
	}
	return filepath.Join(root, galleryCacheName(url, policy))
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

// fetchOCIGalleryIndex pulls a gallery artifact from a registry and returns
// its index.
//
// The resolve, verify, stage, validate and cache steps live in ociindex, shared
// with every other catalog that can be an oci:// source. This keeps what is
// specific to a gallery: the verification policy, the strict-integrity refusal
// and the mapping of a verifier error to refusal versus outage.
func fetchOCIGalleryIndex(ctx context.Context, g config.Gallery, candidate, basePath string, requireIntegrity bool) ([]byte, error) {
	policy := galleryArtifactPolicy(g)
	// Checked before the cache: a copy unpacked while strict integrity was
	// off was never verified, and turning strict integrity on must not keep
	// serving it for the rest of its TTL.
	if policy == nil && requireIntegrity {
		return nil, &galleryVerificationError{
			strict: true,
			err:    fmt.Errorf("no verification policy is set for %q (set artifact_verification: in the gallery configuration or disable --require-backend-integrity)", candidate),
		}
	}

	cacheDir := ociGalleryCacheDir(basePath, candidate, policy)
	if cacheDir == "" {
		return nil, fmt.Errorf("gallery %q needs an absolute models directory to cache %q", g.Name, candidate)
	}

	opts := ociindex.Options{
		ArtifactType: galleryArtifactType,
		IndexFile:    galleryIndexFile,
		CacheDir:     cacheDir,
		CacheTTL:     ociGalleryCacheTTL,
		MaxLayers:    maxGalleryArtifactLayers,
		MaxBytes:     maxGalleryArtifactBytes,
		Usable:       isUsableGalleryIndex,
	}
	if policy != nil {
		opts.Verify = func(ctx context.Context, digestRef string) error {
			return verifyGalleryArtifact(ctx, policy, digestRef)
		}
	} else {
		xlog.Warn("fetching an OCI gallery without signature verification",
			"gallery", g.Name, "url", candidate)
	}

	body, err := ociindex.Fetch(ctx, downloader.URI(candidate).OCIReference(), opts)
	var verr *ociindex.VerifyError
	if errors.As(err, &verr) {
		// Only a decision about the artifact is a refusal. The verifier
		// also reaches the Sigstore TUF mirror and the registry, and a
		// timeout or a 5xx there says nothing about the gallery: it is an
		// outage, and the caller may serve the copy this same policy
		// verified before.
		if errors.Is(verr.Err, cosignverify.ErrPolicyRejected) {
			return nil, &galleryVerificationError{err: fmt.Errorf("signature verification of %q failed: %w", candidate, verr.Err)}
		}
		return nil, fmt.Errorf("could not verify the signature of %q: %w", candidate, verr.Err)
	}
	return body, err
}

func galleryArtifactPolicy(g config.Gallery) *config.GalleryVerification {
	if g.ArtifactVerification != nil {
		return g.ArtifactVerification
	}
	return g.Verification
}
