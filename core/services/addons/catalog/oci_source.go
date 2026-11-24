// SPDX-License-Identifier: MIT
package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/mudler/LocalAI/pkg/credentials"
	"github.com/mudler/LocalAI/pkg/oci/cosignverify"
	"github.com/mudler/LocalAI/pkg/oci/ociindex"
	"github.com/mudler/xlog"
	"github.com/sigstore/sigstore-go/pkg/root"
)

const (
	// LibraryArtifactType is the artifactType a published add-on library
	// declares. The puller refuses anything else, so a plain container image
	// cannot be unpacked and read as a library.
	LibraryArtifactType = "application/vnd.localai.library.v1"

	// ociScheme marks a source whose index is an artifact in a registry.
	ociScheme = "oci://"

	// libraryIndexFile is the layer title the index is published under.
	libraryIndexFile = "index.yaml"

	// A library index is one small YAML file. Caps far below the puller's
	// defaults keep a hostile or mistaken publisher from filling the disk of a
	// host that only asked to list add-ons.
	maxLibraryArtifactLayers = 64
	maxLibraryArtifactBytes  = int64(16 << 20)
)

// libraryCacheTTL is how long an unpacked library artifact is served without
// asking the registry again. A var so specs can drive expiry without sleeping.
var libraryCacheTTL = time.Hour

// verifyLibraryArtifact checks the publisher signature over a digest-pinned
// artifact reference. A nil trust uses the public Sigstore trust root; a
// non-nil trust is host-provisioned material (see Sources.signatureTrust),
// never material that a library or a registry supplies.
//
// A var so tests can drive the policy path without reaching the public
// Sigstore TUF mirror, which a unit test must not depend on.
var verifyLibraryArtifact = func(ctx context.Context, p VerificationPolicy, digestRef string, trust root.TrustedMaterial) error {
	verifier, err := cosignverify.NewVerifier(p.CosignPolicy(), nil, nil)
	if trust != nil {
		verifier, err = cosignverify.NewVerifierWithTrustedMaterial(p.CosignPolicy(), nil, nil, trust)
	}
	if err != nil {
		return err
	}
	return verifier.VerifyImage(ctx, digestRef)
}

// isOCISource reports whether the source index is an OCI artifact.
func isOCISource(src Source) bool {
	return IsOCIAddress(src.URL)
}

// IsOCIAddress reports whether a library address uses the oci:// scheme. The
// scheme is matched without regard to case, as the reader does, so every
// check of an address agrees on which sources are OCI artifacts.
func IsOCIAddress(u string) bool {
	return ociReference(u) != ""
}

// validOCISource checks the address of an oci:// library: a parseable
// registry reference with no credentials and no whitespace. Credentials never
// travel in the address; the shared keychain supplies them.
func validOCISource(s Source) bool {
	if s.Kind != "" && s.Kind != KindAddon {
		return false
	}
	// The policy verifies the library artifact itself. A pattern alone could
	// let a signature that a shared signing workflow made for another
	// repository admit the library, so a pattern needs the source repository.
	// An https add-on library may keep a pattern alone, because it can also
	// list backends; the install of its add-ons refuses that policy.
	if !s.Policy.ValidForAddon() {
		return false
	}
	ref := ociReference(s.URL)
	if !validGalleryURL(s.URL) || ref == "" {
		return false
	}
	host, _, _ := strings.Cut(ref, "/")
	if strings.ContainsAny(host, "@ ") {
		return false
	}
	_, err := name.ParseReference(ref)
	return err == nil
}

// ociReference is the registry reference of an oci:// address.
func ociReference(u string) string {
	if len(u) < len(ociScheme) || !strings.EqualFold(u[:len(ociScheme)], ociScheme) {
		return ""
	}
	return u[len(ociScheme):]
}

// libraryCacheName names the unpacked copy of a library by its address and the
// policy it was fetched under, so a copy admitted under an older, looser policy
// is never served after the policy is tightened.
func libraryCacheName(url string, policy VerificationPolicy) string {
	// A struct of strings and a bool marshals deterministically and cannot fail.
	encoded, _ := json.Marshal(policy)
	sum := sha256.Sum256([]byte(url + "\x00" + string(encoded)))
	return hex.EncodeToString(sum[:])
}

// libraryCacheDir is where the unpacked artifact of src lives: next to the
// source configuration, in the protected owner directory.
func (s *Sources) libraryCacheDir(src Source) string {
	return filepath.Join(filepath.Dir(s.path), "cache", "library", libraryCacheName(src.URL, src.Policy))
}

// usableLibraryIndex reports whether the bytes are an index with at least one
// release, which is what a refresh needs before it may replace a good copy.
func usableLibraryIndex(b []byte) bool {
	indexed, err := ReadIndex(b, ReadableKinds...)
	return err == nil && len(indexed.Releases) > 0
}

// fetchOCI returns the index bytes of an oci:// library.
//
// Resolve, verify, pull and cache live in ociindex, shared with the model and
// backend galleries, so credentials, signature verification and caching are
// the same code. This keeps what is specific to a library: the policy, and
// the choice between refusing and serving the last verified copy.
func (s *Sources) fetchOCI(ctx context.Context, src Source) ([]byte, error) {
	cacheDir := s.libraryCacheDir(src)
	opts := ociindex.Options{
		ArtifactType: LibraryArtifactType,
		IndexFile:    libraryIndexFile,
		CacheDir:     cacheDir,
		CacheTTL:     libraryCacheTTL,
		MaxLayers:    maxLibraryArtifactLayers,
		MaxBytes:     maxLibraryArtifactBytes,
		Usable:       usableLibraryIndex,
	}
	if src.Policy.RequireSignature {
		policy, trust := src.Policy, s.signatureTrust
		opts.Verify = func(ctx context.Context, digestRef string) error {
			return verifyLibraryArtifact(ctx, policy, digestRef, trust)
		}
	} else {
		xlog.Warn("fetching an OCI library without signature verification", "source", src.ID)
	}

	body, err := ociindex.Fetch(ctx, ociReference(src.URL), opts)
	if err == nil {
		return body, nil
	}

	var verr *ociindex.VerifyError
	if errors.As(err, &verr) && errors.Is(verr.Err, cosignverify.ErrPolicyRejected) {
		// A decision about the artifact, not an outage: an older copy must
		// not turn a refusal into a silent downgrade.
		return nil, fmt.Errorf("%w: the library was refused by its signature policy", ErrRefresh)
	}
	var authErr *credentials.AuthError
	if errors.As(err, &authErr) {
		// The detail says whether a rule matched and was refused or no rule
		// matched. It holds no secret, but the caller only gets
		// ErrCredential, so the admin finds the detail in the log.
		xlog.Warn("the registry answered an OCI library read with an authentication error", "source", src.ID, "error", authErr.Error())
		return nil, fmt.Errorf("%w: %w", ErrRefresh, ErrCredential)
	}

	// An outage, or a pull that did not yield a usable index (an empty or
	// unparseable one included). Fetch never replaces a good copy with a
	// failed pull, and the copy sits under a name keyed by this policy, so
	// serving it keeps the listing the host already trusted.
	if ctx.Err() == nil {
		if stale, ok := readStaleLibrary(cacheDir); ok {
			xlog.Warn("library could not be refreshed; serving the last good copy", "source", src.ID, "error", err)
			return stale, nil
		}
	}
	if verr != nil {
		return nil, fmt.Errorf("%w: the signature of the library could not be checked", ErrRefresh)
	}
	return nil, fmt.Errorf("%w: the library artifact could not be read", ErrRefresh)
}

// readStaleLibrary reads the cached index whatever its age.
func readStaleLibrary(cacheDir string) ([]byte, bool) {
	// #nosec G304 -- cacheDir is built from a digest and the file name is a constant.
	body, err := os.ReadFile(filepath.Join(cacheDir, libraryIndexFile))
	if err != nil || !usableLibraryIndex(body) {
		return nil, false
	}
	return body, true
}
