// SPDX-License-Identifier: MIT
package packages

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// redirectRegistry serves an image from a registry that asks for a bearer
// token and then redirects every blob GET to a second "CDN" server, the way
// quay.io does. The CDN counts its requests and records any Authorization
// header it receives. With tamper set, the CDN serves other bytes.
type redirectRegistry struct {
	artifact, digest string
	cdnHits          atomic.Int64
	cdnAuth          atomic.Value // string: last Authorization header the CDN saw
	blobAuthed       atomic.Int64 // blob GETs that reached the registry with its token
}

func newRedirectRegistry(data []byte, tamper, byHostName bool) *redirectRegistry {
	rr := &redirectRegistry{}
	rr.cdnAuth.Store("")
	layer, e := tarball.LayerFromReader(bytes.NewReader(data))
	Expect(e).NotTo(HaveOccurred())
	img, e := mutate.AppendLayers(empty.Image, layer)
	Expect(e).NotTo(HaveOccurred())
	cfg, e := img.ConfigFile()
	Expect(e).NotTo(HaveOccurred())
	cfg.OS, cfg.Architecture = "linux", "amd64"
	img, e = mutate.ConfigFile(img, cfg)
	Expect(e).NotTo(HaveOccurred())

	inner := registry.New()
	var secured atomic.Bool
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rr.cdnHits.Add(1)
		rr.cdnAuth.Store(r.Header.Get("Authorization"))
		if tamper {
			_, _ = w.Write([]byte("not the blob"))
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = strings.TrimPrefix(r.URL.Path, "/cdn")
		inner.ServeHTTP(w, r2)
	}))
	DeferCleanup(cdn.Close)
	target := cdn.URL
	if byHostName {
		// A host name, not an IP literal: the loopback rule must not accept it.
		target = strings.Replace(target, "127.0.0.1", "localhost", 1)
	}
	var reg *httptest.Server
	reg = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "t1"})
			return
		}
		if secured.Load() {
			if r.Header.Get("Authorization") != "Bearer t1" {
				w.Header().Set("WWW-Authenticate", `Bearer realm="`+reg.URL+`/token",service="reg"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/blobs/sha256:") {
				rr.blobAuthed.Add(1)
				http.Redirect(w, r, target+"/cdn"+r.URL.Path, http.StatusFound)
				return
			}
		}
		inner.ServeHTTP(w, r)
	}))
	DeferCleanup(reg.Close)
	ref, e := name.ParseReference(strings.TrimPrefix(reg.URL, "http://") + "/sample:latest")
	Expect(e).NotTo(HaveOccurred())
	Expect(remote.Write(ref, img)).To(Succeed())
	secured.Store(true)
	d, e := img.Digest()
	Expect(e).NotTo(HaveOccurred())
	rr.artifact, rr.digest = "oci://"+ref.Name(), d.String()
	return rr
}

var _ = Describe("OCI blob redirects", func() {
	manifest := func() []byte {
		m, e := os.ReadFile("../../../../tests/addons/fixtures/packages/manifest.json")
		Expect(e).NotTo(HaveOccurred())
		return archive(nil, m)
	}
	It("follows a digest-pinned blob redirect without the registry credential", func() {
		data := manifest()
		rr := newRedirectRegistry(data, false, false)
		i, r := fixture(data, func(r *catalog.Release, _ *catalog.Source) { r.Artifact, r.Digest = rr.artifact, rr.digest })
		_, e := i.Stage(context.Background(), r, nil)
		Expect(e).NotTo(HaveOccurred())
		Expect(rr.blobAuthed.Load()).To(BeNumerically(">", 0), "the registry must have seen its own token")
		Expect(rr.cdnHits.Load()).To(BeNumerically(">", 0))
		Expect(rr.cdnAuth.Load()).To(BeEmpty(), "the CDN must not receive the registry credential")
	})
	It("still verifies the digest of the redirected bytes", func() {
		data := manifest()
		rr := newRedirectRegistry(data, true, false)
		i, r := fixture(data, func(r *catalog.Release, _ *catalog.Source) { r.Artifact, r.Digest = rr.artifact, rr.digest })
		_, e := i.Stage(context.Background(), r, nil)
		Expect(e).To(HaveOccurred())
		Expect(rr.cdnHits.Load()).To(BeNumerically(">", 0))
	})
	It("refuses a plaintext redirect to a host name instead of a loopback address", func() {
		data := manifest()
		rr := newRedirectRegistry(data, false, true)
		i, r := fixture(data, func(r *catalog.Release, _ *catalog.Source) { r.Artifact, r.Digest = rr.artifact, rr.digest })
		_, e := i.Stage(context.Background(), r, nil)
		Expect(e).To(HaveOccurred())
		Expect(rr.cdnHits.Load()).To(BeZero())
	})
})
