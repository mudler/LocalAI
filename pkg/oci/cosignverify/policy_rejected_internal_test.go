package cosignverify

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
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
