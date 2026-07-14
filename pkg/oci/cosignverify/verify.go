// Package cosignverify verifies cosign-signed OCI images using sigstore-go.
//
// LocalAI uses this to gate backend installs on a keyless-cosign signature
// from a trusted GitHub Actions OIDC identity, so a registry/tag compromise
// alone is not sufficient to ship a tampered backend image.
//
// Producer side: CI signs each pushed backend image with cosign 2.2+ and
// the `--new-bundle-format --registry-referrers-mode=oci-1-1` flags. The
// signature is then a standalone Sigstore bundle stored as an OCI 1.1
// referrer of the image manifest.
//
// Consumer side (this package): bundle.go discovers the bundle via the
// referrers API and hands it directly to sigstore-go's verifier. There is
// no legacy-cosign-annotation fallback — we own both ends.
package cosignverify

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	registrytypes "github.com/docker/docker/api/types/registry"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// Policy is the verification policy a backend image must satisfy.
//
// At least one of Issuer / IssuerRegex must be set, and at least one of
// Identity / IdentityRegex. The (Issuer, Identity) pair pins which OIDC
// principal Fulcio issued the signing cert to — for GitHub Actions keyless
// signing this is typically:
//
//	Issuer:        "https://token.actions.githubusercontent.com"
//	IdentityRegex: "^https://github.com/<org>/<repo>/\\.github/workflows/<file>@refs/.*"
//
// A registry compromise alone cannot satisfy this; the attacker would also
// need to compromise the GitHub Actions OIDC identity to obtain a Fulcio
// cert with a matching SAN.
type Policy struct {
	Issuer        string
	IssuerRegex   string
	Identity      string
	IdentityRegex string

	// TUFRootURL overrides the default sigstore public-good TUF mirror
	// (tuf-repo-cdn.sigstore.dev). Leave empty for the public good.
	TUFRootURL string

	// TUFCachePath overrides the on-disk cache directory for the TUF
	// metadata. Leave empty for the sigstore-go default.
	TUFCachePath string

	// RequireTLog requires an inclusion proof from the Rekor transparency
	// log. Defaults to true; only disable for testing.
	RequireTLog *bool

	// RequireSCT requires the signing certificate to embed a Signed
	// Certificate Timestamp from the certificate-transparency log.
	// Defaults to true.
	RequireSCT *bool

	// NotBefore rejects signatures whose Rekor integrated time is older
	// than this. This is the revocation lever: keyless cosign certs are
	// ephemeral so there is no CA-side revocation, but advancing NotBefore
	// in the gallery YAML invalidates any signature produced before a
	// known compromise window. Zero value means no time-based cutoff.
	NotBefore time.Time
}

func boolOrTrue(b *bool) bool {
	if b == nil {
		return true
	}
	return *b
}

// Validate returns an error if the policy is missing required fields.
func (p Policy) Validate() error {
	if p.Issuer == "" && p.IssuerRegex == "" {
		return errors.New("cosignverify: policy must set Issuer or IssuerRegex")
	}
	if p.Identity == "" && p.IdentityRegex == "" {
		return errors.New("cosignverify: policy must set Identity or IdentityRegex")
	}
	return nil
}

// Verifier verifies cosign-signed OCI images against a fixed Policy.
//
// Cheap to construct, safe for concurrent use. The TUF trusted root is
// fetched once per (root URL, cache path) tuple across all Verifiers in
// the process — installing N backends from the same gallery does one TUF
// fetch, not N.
type Verifier struct {
	policy Policy

	// Registry plumbing — reused from the existing pkg/oci surface so we
	// honor the same auth / transport conventions.
	auth      *registrytypes.AuthConfig
	transport http.RoundTripper
}

// NewVerifier constructs a Verifier. The trusted root is not fetched yet;
// it is loaded on the first call to VerifyImage. auth and t may be nil.
func NewVerifier(p Policy, auth *registrytypes.AuthConfig, t http.RoundTripper) (*Verifier, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &Verifier{policy: p, auth: auth, transport: t}, nil
}

// trustedMaterialCacheKey identifies which TUF mirror + on-disk cache a
// Verifier wants. Two Verifiers with identical keys share trusted material.
type trustedMaterialCacheKey struct {
	URL  string
	Path string
}

type trustedMaterialEntry struct {
	once     sync.Once
	material root.TrustedMaterialCollection
	err      error
}

var trustedMaterialCache sync.Map // map[trustedMaterialCacheKey]*trustedMaterialEntry

func (v *Verifier) loadTrustedMaterial() (root.TrustedMaterialCollection, error) {
	key := trustedMaterialCacheKey{URL: v.policy.TUFRootURL, Path: v.policy.TUFCachePath}
	val, _ := trustedMaterialCache.LoadOrStore(key, &trustedMaterialEntry{})
	entry := val.(*trustedMaterialEntry)
	entry.once.Do(func() {
		opts := tuf.DefaultOptions()
		if v.policy.TUFRootURL != "" {
			opts.RepositoryBaseURL = v.policy.TUFRootURL
		}
		if v.policy.TUFCachePath != "" {
			opts.CachePath = v.policy.TUFCachePath
		}
		client, err := tuf.New(opts)
		if err != nil {
			entry.err = fmt.Errorf("cosignverify: initialising TUF client: %w", err)
			return
		}
		trustedRootJSON, err := client.GetTarget("trusted_root.json")
		if err != nil {
			entry.err = fmt.Errorf("cosignverify: fetching trusted_root.json: %w", err)
			return
		}
		tr, err := root.NewTrustedRootFromJSON(trustedRootJSON)
		if err != nil {
			entry.err = fmt.Errorf("cosignverify: parsing trusted root: %w", err)
			return
		}
		entry.material = root.TrustedMaterialCollection{tr}
	})
	return entry.material, entry.err
}

// VerifyImage resolves imageRef to its manifest digest, fetches the cosign
// signature attachment (the conventional `:sha256-<hex>.sig` tag), assembles
// a Sigstore bundle from the cosign annotations, and verifies that bundle
// against the configured Policy.
//
// Returns nil on the first signature in the attachment that satisfies the
// policy. Returns an error if none do, or if any part of the fetch fails.
func (v *Verifier) VerifyImage(ctx context.Context, imageRef string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	trusted, err := v.loadTrustedMaterial()
	if err != nil {
		return err
	}

	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return fmt.Errorf("cosignverify: parse image ref %q: %w", imageRef, err)
	}

	opts := v.remoteOptions(ctx)

	// Resolve the image to its manifest digest. With the new-bundle-format
	// flow the cosign signature is taken over the manifest digest directly,
	// so this is also the artifact we ask the verifier to bind against.
	// Skip the HEAD when the ref is already digest-pinned (the typical
	// path from pkg/downloader, which resolves the digest before calling
	// us): name.ParseReference returns a name.Digest in that case.
	var digest v1.Hash
	if d, ok := ref.(name.Digest); ok {
		h, herr := v1.NewHash(d.DigestStr())
		if herr != nil {
			return fmt.Errorf("cosignverify: parsing pinned digest %q: %w", d.DigestStr(), herr)
		}
		digest = h
	} else {
		desc, herr := remote.Head(ref, opts...)
		if herr != nil {
			return fmt.Errorf("cosignverify: resolving image descriptor: %w", herr)
		}
		digest = desc.Digest
	}

	bun, err := bundleFromOCISignature(ref, digest, opts)
	if err != nil {
		return err
	}

	verifierOpts := []verify.VerifierOption{}
	if boolOrTrue(v.policy.RequireSCT) {
		verifierOpts = append(verifierOpts, verify.WithSignedCertificateTimestamps(1))
	}
	if boolOrTrue(v.policy.RequireTLog) {
		verifierOpts = append(verifierOpts, verify.WithTransparencyLog(1))
		verifierOpts = append(verifierOpts, verify.WithObserverTimestamps(1))
	}

	certID, err := verify.NewShortCertificateIdentity(
		v.policy.Issuer,
		v.policy.IssuerRegex,
		v.policy.Identity,
		v.policy.IdentityRegex,
	)
	if err != nil {
		return fmt.Errorf("cosignverify: building identity policy: %w", err)
	}

	sev, err := verify.NewVerifier(trusted, verifierOpts...)
	if err != nil {
		return fmt.Errorf("cosignverify: constructing verifier: %w", err)
	}

	artifactDigest, err := hex.DecodeString(digest.Hex)
	if err != nil {
		return fmt.Errorf("cosignverify: decoding image digest: %w", err)
	}
	artifactPolicy := verify.WithArtifactDigest(digest.Algorithm, artifactDigest)

	result, err := sev.Verify(bun, verify.NewPolicy(artifactPolicy, verify.WithCertificateIdentity(certID)))
	if err != nil {
		return fmt.Errorf("cosignverify: verification failed for %s: %w", imageRef, err)
	}

	if !v.policy.NotBefore.IsZero() {
		if err := enforceNotBefore(result, v.policy.NotBefore); err != nil {
			return fmt.Errorf("cosignverify: %s: %w", imageRef, err)
		}
	}
	return nil
}

// enforceNotBefore rejects a verification result whose earliest verified
// timestamp predates cutoff. Used as a revocation lever — see Policy.NotBefore.
func enforceNotBefore(result *verify.VerificationResult, cutoff time.Time) error {
	if result == nil || len(result.VerifiedTimestamps) == 0 {
		// Defensive: with RequireTLog=true (the default) sigstore-go will
		// have already failed verification if there was no verifiable
		// timestamp, so this branch is only reachable if a caller set
		// RequireTLog=false. Treat as a hard error: if you opted into
		// NotBefore, you implicitly opted into needing a timestamp.
		return errors.New("signature has no verified timestamp; cannot enforce NotBefore")
	}
	earliest := result.VerifiedTimestamps[0].Timestamp
	for _, ts := range result.VerifiedTimestamps[1:] {
		if ts.Timestamp.Before(earliest) {
			earliest = ts.Timestamp
		}
	}
	if earliest.Before(cutoff) {
		return fmt.Errorf("signature integrated time %s is before NotBefore cutoff %s",
			earliest.Format(time.RFC3339), cutoff.Format(time.RFC3339))
	}
	return nil
}

func (v *Verifier) remoteOptions(ctx context.Context) []remote.Option {
	t := v.transport
	if t == nil {
		t = http.DefaultTransport
	}
	// Match the retry policy used elsewhere in pkg/oci so transient
	// registry hiccups don't fail verification.
	t = transport.NewRetry(t)

	opts := []remote.Option{
		remote.WithContext(ctx),
		remote.WithTransport(t),
	}
	if v.auth != nil {
		opts = append(opts, remote.WithAuth(staticAuth{auth: v.auth}))
	} else {
		opts = append(opts, remote.WithAuthFromKeychain(authn.DefaultKeychain))
	}
	return opts
}

// staticAuth mirrors pkg/oci's adapter so callers can pass the same
// docker auth config they use everywhere else.
type staticAuth struct {
	auth *registrytypes.AuthConfig
}

func (s staticAuth) Authorization() (*authn.AuthConfig, error) {
	if s.auth == nil {
		return nil, nil
	}
	return &authn.AuthConfig{
		Username:      s.auth.Username,
		Password:      s.auth.Password,
		Auth:          s.auth.Auth,
		IdentityToken: s.auth.IdentityToken,
		RegistryToken: s.auth.RegistryToken,
	}, nil
}
