package cosignverify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeRegistry serves manifests and blobs from memory and answers the
// referrers API with a 404, the way CNCF distribution 3.0.0 does: that build
// does not register the route at all, so every client falls back to the
// referrers-tag scheme.
type fakeRegistry struct {
	manifests   map[string]manifestEntry // by tag and by digest
	blobs       map[string][]byte        // by digest
	unavailable map[string]bool          // manifest digests answered with a 503
}

type manifestEntry struct {
	mediaType string
	body      []byte
}

func (f *fakeRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case p == "/v2/":
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	case strings.Contains(p, "/referrers/"):
		// Exactly what distribution answers: the router has no such route, so
		// this is Go's plain text 404 rather than a registry error document.
		http.NotFound(w, r)
	case strings.Contains(p, "/manifests/"):
		ref := p[strings.LastIndex(p, "/manifests/")+len("/manifests/"):]
		if f.unavailable[ref] {
			http.Error(w, "upstream down", http.StatusServiceUnavailable)
			return
		}
		m, ok := f.manifests[ref]
		if !ok {
			http.Error(w, "unknown manifest", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", m.mediaType)
		w.Header().Set("Docker-Content-Digest", digestOf(m.body))
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", fmt.Sprint(len(m.body)))
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write(m.body)
	case strings.Contains(p, "/blobs/"):
		d := p[strings.LastIndex(p, "/blobs/")+len("/blobs/"):]
		b, ok := f.blobs[d]
		if !ok {
			http.Error(w, "unknown blob", http.StatusNotFound)
			return
		}
		_, _ = w.Write(b)
	default:
		http.NotFound(w, r)
	}
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// signedByCosignWithoutReferrersAPI builds the shape a premium release
// actually publishes: a subject manifest, a signature manifest that carries
// the Sigstore artifact type, and a referrers-tag index whose entry reports
// the CONFIG media type instead of the manifest's own artifact type. That
// last part is what cosign writes when the registry has no referrers API,
// and it is what made every premium signature unverifiable.
func signedByCosignWithoutReferrersAPI(bundleBlob []byte) (*fakeRegistry, string) {
	reg := &fakeRegistry{manifests: map[string]manifestEntry{}, blobs: map[string][]byte{}}

	emptyCfg := []byte("{}")
	reg.blobs[digestOf(emptyCfg)] = emptyCfg
	reg.blobs[digestOf(bundleBlob)] = bundleBlob

	subject, _ := json.Marshal(v1.Manifest{
		SchemaVersion: 2,
		MediaType:     "application/vnd.oci.image.manifest.v1+json",
		Config:        v1.Descriptor{MediaType: "application/vnd.oci.empty.v1+json", Size: int64(len(emptyCfg)), Digest: mustHash(digestOf(emptyCfg))},
	})
	subjectDigest := digestOf(subject)
	reg.manifests[subjectDigest] = manifestEntry{mediaType: "application/vnd.oci.image.manifest.v1+json", body: subject}

	sig, _ := json.Marshal(v1.Manifest{
		SchemaVersion: 2,
		MediaType:     "application/vnd.oci.image.manifest.v1+json",
		ArtifactType:  "application/vnd.dev.sigstore.bundle.v0.3+json",
		Config:        v1.Descriptor{MediaType: "application/vnd.oci.empty.v1+json", Size: int64(len(emptyCfg)), Digest: mustHash(digestOf(emptyCfg))},
		Layers: []v1.Descriptor{{
			MediaType: "application/vnd.dev.sigstore.bundle.v0.3+json",
			Size:      int64(len(bundleBlob)),
			Digest:    mustHash(digestOf(bundleBlob)),
		}},
	})
	sigDigest := digestOf(sig)
	reg.manifests[sigDigest] = manifestEntry{mediaType: "application/vnd.oci.image.manifest.v1+json", body: sig}

	// The referrers tag, with the wrong artifactType on the entry.
	idx, _ := json.Marshal(v1.IndexManifest{
		SchemaVersion: 2,
		MediaType:     "application/vnd.oci.image.index.v1+json",
		Manifests: []v1.Descriptor{{
			MediaType:    "application/vnd.oci.image.manifest.v1+json",
			Size:         int64(len(sig)),
			Digest:       mustHash(sigDigest),
			ArtifactType: "application/vnd.oci.empty.v1+json",
		}},
	})
	reg.manifests[strings.Replace(subjectDigest, ":", "-", 1)] = manifestEntry{mediaType: "application/vnd.oci.image.index.v1+json", body: idx}

	return reg, subjectDigest
}

func mustHash(s string) v1.Hash {
	h, err := v1.NewHash(s)
	if err != nil {
		panic(err)
	}
	return h
}

var _ = Describe("referrer discovery", func() {
	It("finds a bundle whose index entry reports the wrong artifact type", func() {
		// Not a valid bundle: this asserts discovery, so reaching the parse
		// step is the pass condition and parsing is expected to fail.
		reg, subjectDigest := signedByCosignWithoutReferrersAPI([]byte(`{"not":"a bundle"}`))
		srv := httptest.NewServer(reg)
		defer srv.Close()
		u, err := url.Parse(srv.URL)
		Expect(err).NotTo(HaveOccurred())

		ref, err := name.ParseReference(u.Host+"/gallery:backends-0.1.2", name.Insecure)
		Expect(err).NotTo(HaveOccurred())

		_, err = bundleFromOCISignature(ref, mustHash(subjectDigest), []remote.Option{})
		Expect(err).To(HaveOccurred())
		// Before the fix this stopped at the index entry and never fetched the
		// referrer, so it reported that no bundle referrer exists at all.
		Expect(err.Error()).NotTo(ContainSubstring("no Sigstore bundle referrer"))
		Expect(err.Error()).To(ContainSubstring("parsing bundle JSON"))
	})
})
