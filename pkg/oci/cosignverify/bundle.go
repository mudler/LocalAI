// Sigstore-bundle discovery for cosign-signed OCI images.
//
// Cosign 2.2+ with `--new-bundle-format --registry-referrers-mode=oci-1-1`
// stores the signature as a standalone OCI artifact discoverable via the
// OCI 1.1 referrers API. The artifact payload is a Sigstore protobuf
// bundle that sigstore-go consumes natively (no manual annotation parsing).
//
// go-containerregistry's remote.Referrers transparently falls back to the
// referrers-tag scheme (`<algo>-<hex>` tag) for registries that don't yet
// implement the referrers endpoint, so the same code path covers both.
//
// We deliberately do not support the legacy `:sha256-<hex>.sig` cosign
// signature attachment with per-annotation cert/sig/Rekor fields. CI is
// expected to sign with `--new-bundle-format`; this is a fresh integration
// and LocalAI controls both the producer (CI) and the consumer (this
// binary), so there is no reason to carry the legacy path.

package cosignverify

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/sigstore/sigstore-go/pkg/bundle"
)

// sigstoreBundleMediaTypePrefix matches every published Sigstore bundle
// version (0.1, 0.2, 0.3, ...). The artifactType lives on the referrer
// descriptor in the OCI image index returned by the referrers API.
const sigstoreBundleMediaTypePrefix = "application/vnd.dev.sigstore.bundle."

// isSigstoreBundleArtifactType reports whether the given OCI artifactType
// identifies a Sigstore bundle blob.
func isSigstoreBundleArtifactType(mt string) bool {
	return strings.HasPrefix(mt, sigstoreBundleMediaTypePrefix) && strings.HasSuffix(mt, "+json")
}

// bundleFromOCISignature locates a cosign-produced Sigstore bundle for the
// image identified by ref+imageDigest by querying the OCI 1.1 referrers
// API and returns the parsed bundle.
//
// Returns the first bundle whose JSON parses successfully — verification
// of identity, transparency log inclusion, and artifact digest is the
// caller's responsibility (driven by the Verifier).
func bundleFromOCISignature(ref name.Reference, imageDigest v1.Hash, opts []remote.Option) (*bundle.Bundle, error) {
	digestRef := ref.Context().Digest(imageDigest.String())

	idx, err := remote.Referrers(digestRef, opts...)
	if err != nil {
		return nil, fmt.Errorf("cosignverify: querying referrers for %s: %w", digestRef.Name(), err)
	}
	manifest, err := idx.IndexManifest()
	if err != nil {
		return nil, fmt.Errorf("cosignverify: reading referrers index: %w", err)
	}

	if len(manifest.Manifests) == 0 {
		return nil, fmt.Errorf("cosignverify: no referrers found for %s", digestRef.Name())
	}

	var lastErr error
	for _, desc := range manifest.Manifests {
		if !isSigstoreBundleArtifactType(string(desc.ArtifactType)) {
			continue
		}
		b, err := fetchBundleFromReferrer(ref, desc, opts)
		if err != nil {
			lastErr = err
			continue
		}
		return b, nil
	}
	if lastErr != nil {
		return nil, fmt.Errorf("cosignverify: no usable Sigstore bundle referrer for %s: %w", digestRef.Name(), lastErr)
	}
	return nil, fmt.Errorf("cosignverify: no Sigstore bundle referrer for %s (signed with --new-bundle-format?)", digestRef.Name())
}

func fetchBundleFromReferrer(ref name.Reference, desc v1.Descriptor, opts []remote.Option) (*bundle.Bundle, error) {
	artRef := ref.Context().Digest(desc.Digest.String())
	img, err := remote.Image(artRef, opts...)
	if err != nil {
		return nil, fmt.Errorf("fetching referrer image %s: %w", artRef.Name(), err)
	}
	layers, err := img.Layers()
	if err != nil {
		return nil, fmt.Errorf("reading referrer layers: %w", err)
	}
	if len(layers) == 0 {
		return nil, errors.New("referrer artifact has no layers")
	}

	rc, err := layers[0].Uncompressed()
	if err != nil {
		return nil, fmt.Errorf("opening referrer blob: %w", err)
	}
	defer func() { _ = rc.Close() }()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("reading referrer blob: %w", err)
	}

	b := &bundle.Bundle{}
	if err := b.UnmarshalJSON(data); err != nil {
		return nil, fmt.Errorf("parsing bundle JSON: %w", err)
	}
	return b, nil
}
