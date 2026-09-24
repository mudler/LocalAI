package cosignverify

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// A caller that keeps a copy verified earlier serves it through an outage and
// refuses it over a policy decision, so the two must be told apart by the
// error alone.
var _ = Describe("ErrPolicyRejected", func() {
	registryRef := func(h http.Handler) name.Reference {
		GinkgoHelper()
		srv := httptest.NewServer(h)
		DeferCleanup(srv.Close)
		u, err := url.Parse(srv.URL)
		Expect(err).NotTo(HaveOccurred())
		ref, err := name.ParseReference(u.Host+"/gallery:v1", name.Insecure)
		Expect(err).NotTo(HaveOccurred())
		return ref
	}
	// One attempt: the retry backoff would only slow the 5xx spec down.
	noRetry := []remote.Option{remote.WithRetryBackoff(remote.Backoff{Steps: 1})}

	It("marks a signature older than not_before as a policy decision", func() {
		cutoff := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
		res := &verify.VerificationResult{VerifiedTimestamps: []verify.TimestampVerificationResult{{Timestamp: cutoff.Add(-time.Hour)}}}
		Expect(errors.Is(enforceNotBefore(res, cutoff), ErrPolicyRejected)).To(BeTrue())
	})

	It("marks an image with no signature at all as a policy decision", func() {
		reg, subjectDigest := signedByCosignWithoutReferrersAPI([]byte(`{}`))
		// Drop the referrers-tag index: the image is now simply unsigned.
		for k := range reg.manifests {
			if k != subjectDigest && len(k) > 7 && k[:7] == "sha256-" {
				delete(reg.manifests, k)
			}
		}
		_, err := bundleFromOCISignature(registryRef(reg), mustHash(subjectDigest), noRetry)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrPolicyRejected)).To(BeTrue(), "got %v", err)
	})

	It("marks a signature referrer that is not a valid bundle as a policy decision", func() {
		reg, subjectDigest := signedByCosignWithoutReferrersAPI([]byte(`{"not":"a bundle"}`))
		_, err := bundleFromOCISignature(registryRef(reg), mustHash(subjectDigest), noRetry)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrPolicyRejected)).To(BeTrue(), "got %v", err)
	})

	It("does not mark a registry that answers 5xx as a policy decision", func() {
		_, subjectDigest := signedByCosignWithoutReferrersAPI([]byte(`{}`))
		down := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v2/" {
				w.WriteHeader(http.StatusOK)
				return
			}
			http.Error(w, "upstream down", http.StatusServiceUnavailable)
		})
		_, err := bundleFromOCISignature(registryRef(down), mustHash(subjectDigest), noRetry)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrPolicyRejected)).To(BeFalse(), "an outage was reported as a refusal: %v", err)
	})
})

// referrer is one entry of a test referrers index.
type referrer struct {
	// advertised puts the Sigstore artifact type on the index entry, so the
	// first pass picks it up; otherwise only the second pass, which asks the
	// manifest itself, can find it.
	advertised bool
	// unavailable makes the registry answer 503 for the referrer manifest.
	unavailable bool
	// bundle is the referrer's payload.
	bundle []byte
}

// registryWithReferrers serves a subject whose referrers-tag index lists the
// given referrers, in order.
func registryWithReferrers(refs ...referrer) (*fakeRegistry, string) {
	reg := &fakeRegistry{manifests: map[string]manifestEntry{}, blobs: map[string][]byte{}, unavailable: map[string]bool{}}
	emptyCfg := []byte("{}")
	reg.blobs[digestOf(emptyCfg)] = emptyCfg
	cfg := v1.Descriptor{MediaType: "application/vnd.oci.empty.v1+json", Size: int64(len(emptyCfg)), Digest: mustHash(digestOf(emptyCfg))}

	subject, _ := json.Marshal(v1.Manifest{SchemaVersion: 2, MediaType: "application/vnd.oci.image.manifest.v1+json", Config: cfg})
	subjectDigest := digestOf(subject)
	reg.manifests[subjectDigest] = manifestEntry{mediaType: "application/vnd.oci.image.manifest.v1+json", body: subject}

	entries := []v1.Descriptor{}
	for _, r := range refs {
		reg.blobs[digestOf(r.bundle)] = r.bundle
		sig, _ := json.Marshal(v1.Manifest{
			SchemaVersion: 2,
			MediaType:     "application/vnd.oci.image.manifest.v1+json",
			ArtifactType:  "application/vnd.dev.sigstore.bundle.v0.3+json",
			Config:        cfg,
			Layers: []v1.Descriptor{{
				MediaType: "application/vnd.dev.sigstore.bundle.v0.3+json",
				Size:      int64(len(r.bundle)),
				Digest:    mustHash(digestOf(r.bundle)),
			}},
		})
		sigDigest := digestOf(sig)
		reg.manifests[sigDigest] = manifestEntry{mediaType: "application/vnd.oci.image.manifest.v1+json", body: sig}
		if r.unavailable {
			reg.unavailable[sigDigest] = true
		}
		artifactType := "application/vnd.oci.empty.v1+json"
		if r.advertised {
			artifactType = "application/vnd.dev.sigstore.bundle.v0.3+json"
		}
		entries = append(entries, v1.Descriptor{
			MediaType:    "application/vnd.oci.image.manifest.v1+json",
			Size:         int64(len(sig)),
			Digest:       mustHash(sigDigest),
			ArtifactType: artifactType,
		})
	}
	idx, _ := json.Marshal(v1.IndexManifest{SchemaVersion: 2, MediaType: "application/vnd.oci.image.index.v1+json", Manifests: entries})
	reg.manifests[strings.Replace(subjectDigest, ":", "-", 1)] = manifestEntry{mediaType: "application/vnd.oci.image.index.v1+json", body: idx}
	return reg, subjectDigest
}

var _ = Describe("referrers the registry fails to serve", func() {
	lookup := func(reg *fakeRegistry, subjectDigest string) error {
		GinkgoHelper()
		srv := httptest.NewServer(reg)
		DeferCleanup(srv.Close)
		u, err := url.Parse(srv.URL)
		Expect(err).NotTo(HaveOccurred())
		ref, err := name.ParseReference(u.Host+"/gallery:v1", name.Insecure)
		Expect(err).NotTo(HaveOccurred())
		_, err = bundleFromOCISignature(ref, mustHash(subjectDigest),
			[]remote.Option{remote.WithRetryBackoff(remote.Backoff{Steps: 1})})
		Expect(err).To(HaveOccurred())
		return err
	}
	notABundle := []byte(`{"not":"a bundle"}`)

	// The index is served but the only referrer is not: that referrer may be
	// the signature, so this is an outage and not an unsigned image.
	It("reports an outage when an unadvertised referrer returns 503", func() {
		err := lookup(registryWithReferrers(referrer{unavailable: true, bundle: notABundle}))
		Expect(errors.Is(err, ErrPolicyRejected)).To(BeFalse(), "an unread referrer was reported as no signature: %v", err)
	})

	// Which failure comes last in the index is an accident of the registry.
	// An unread referrer may be the valid signature, so any outage makes the
	// whole lookup an outage, whatever the order.
	DescribeTable("reports an outage when one referrer is unreadable and another is not a bundle",
		func(refs ...referrer) {
			err := lookup(registryWithReferrers(refs...))
			Expect(errors.Is(err, ErrPolicyRejected)).To(BeFalse(), "reported as a refusal: %v", err)
		},
		Entry("unreadable first",
			referrer{advertised: true, unavailable: true, bundle: []byte(`{"a":1}`)},
			referrer{advertised: true, bundle: notABundle}),
		Entry("unreadable last",
			referrer{advertised: true, bundle: notABundle},
			referrer{advertised: true, unavailable: true, bundle: []byte(`{"a":1}`)}),
	)

	It("still reports a refusal when every referrer was read and none is a bundle", func() {
		err := lookup(registryWithReferrers(referrer{advertised: true, bundle: notABundle}))
		Expect(errors.Is(err, ErrPolicyRejected)).To(BeTrue(), "got %v", err)
	})
})

// A policy that cannot be built admits nothing, whatever the network does,
// so it must not read as an outage that a cached copy could cover.
var _ = Describe("an invalid policy", func() {
	It("is a policy decision", func() {
		_, err := NewVerifier(Policy{Issuer: "https://token.actions.githubusercontent.com"}, nil, nil)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrPolicyRejected)).To(BeTrue(), "got %v", err)
	})
})
