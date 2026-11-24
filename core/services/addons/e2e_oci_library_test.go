// SPDX-License-Identifier: MIT
package addons

import (
	"archive/tar"
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/credentials"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	oras "oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
	oraslib "oras.land/oras-go/v2/registry/remote"
)

// e2eRegistry is a registry that wants basic auth and sends every blob read to
// a second host, as a registry in front of an object store does. The second
// host has the registry's host name and another port: net/http ignores the
// port when it decides to copy Authorization to a redirect target, so this is
// the case a client leaks to unless it guards the redirect itself. The second
// host records every Authorization header it receives.
type e2eRegistry struct {
	host         string // host:port of the registry
	cdnHost      string // host:port of the redirect target
	cdnMu        sync.Mutex
	cdnAuth      []string     // Authorization header of every request the second host saw
	unauthorized atomic.Int64 // requests answered 401
}

// cdnHeaders returns the Authorization header of every request the redirect
// target received, in order.
func (rr *e2eRegistry) cdnHeaders() []string {
	rr.cdnMu.Lock()
	defer rr.cdnMu.Unlock()
	return append([]string(nil), rr.cdnAuth...)
}

const (
	e2eUser     = "reader"
	e2ePassword = "s3cret-for-the-e2e"
)

func newE2ERegistry() (*e2eRegistry, *atomic.Bool) {
	GinkgoHelper()
	rr := &e2eRegistry{}
	inner := registry.New()
	var secured atomic.Bool
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rr.cdnMu.Lock()
		rr.cdnAuth = append(rr.cdnAuth, r.Header.Get("Authorization"))
		rr.cdnMu.Unlock()
		r2 := r.Clone(r.Context())
		r2.URL.Path = strings.TrimPrefix(r.URL.Path, "/cdn")
		inner.ServeHTTP(w, r2)
	}))
	DeferCleanup(cdn.Close)
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if secured.Load() {
			user, pass, ok := r.BasicAuth()
			if !ok || user != e2eUser || pass != e2ePassword {
				rr.unauthorized.Add(1)
				w.Header().Set("WWW-Authenticate", `Basic realm="registry.example"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/blobs/sha256:") {
				http.Redirect(w, r, cdn.URL+"/cdn"+r.URL.Path, http.StatusTemporaryRedirect)
				return
			}
		}
		inner.ServeHTTP(w, r)
	}))
	DeferCleanup(reg.Close)
	rr.host = strings.TrimPrefix(reg.URL, "http://")
	rr.cdnHost = strings.TrimPrefix(cdn.URL, "http://")
	return rr, &secured
}

// pushPackage publishes the smallest installable add-on image and returns its
// address and digest.
func (rr *e2eRegistry) pushPackage() (string, string) {
	GinkgoHelper()
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	manifest := []byte(`{"schemaVersion":1,"id":"fixture-addon","version":"1.0.0","executable":"bin/fixture-addon","os":"linux","arch":"amd64","host":{"min":1,"max":2},"api":{"min":2,"max":2},"protocol":{"min":1,"max":1},"dataFormat":{"min":1,"max":2},"capabilities":{"service":true}}`)
	for _, f := range []struct {
		name string
		data []byte
	}{{"manifest.json", manifest}, {"bin/fixture-addon", []byte("executable")}} {
		Expect(w.WriteHeader(&tar.Header{Name: f.name, Mode: 0755, Size: int64(len(f.data))})).To(Succeed())
		_, err := w.Write(f.data)
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(w.Close()).To(Succeed())
	layer, err := tarball.LayerFromReader(bytes.NewReader(b.Bytes()))
	Expect(err).NotTo(HaveOccurred())
	img, err := mutate.AppendLayers(empty.Image, layer)
	Expect(err).NotTo(HaveOccurred())
	cfg, err := img.ConfigFile()
	Expect(err).NotTo(HaveOccurred())
	cfg.OS, cfg.Architecture = "linux", "amd64"
	img, err = mutate.ConfigFile(img, cfg)
	Expect(err).NotTo(HaveOccurred())
	ref, err := name.ParseReference(rr.host + "/packages/fixture-addon:1")
	Expect(err).NotTo(HaveOccurred())
	Expect(remote.Write(ref, img)).To(Succeed())
	digest, err := img.Digest()
	Expect(err).NotTo(HaveOccurred())
	return "oci://" + ref.Name(), digest.String()
}

// pushLibrary publishes a one-file library artifact and returns its address.
func (rr *e2eRegistry) pushLibrary(index []byte) string {
	GinkgoHelper()
	store := memory.New()
	desc := content.NewDescriptorFromBytes("application/yaml", index)
	desc.Annotations = map[string]string{ocispec.AnnotationTitle: "index.yaml"}
	Expect(store.Push(context.Background(), desc, bytes.NewReader(index))).To(Succeed())
	manifest, err := oras.PackManifest(context.Background(), store, oras.PackManifestVersion1_1, catalog.LibraryArtifactType,
		oras.PackManifestOptions{Layers: []ocispec.Descriptor{desc}})
	Expect(err).NotTo(HaveOccurred())
	Expect(store.Tag(context.Background(), manifest, "latest")).To(Succeed())
	repo, err := oraslib.NewRepository(rr.host + "/libraries/fixture-addon")
	Expect(err).NotTo(HaveOccurred())
	repo.PlainHTTP = true
	_, err = oras.Copy(context.Background(), store, "latest", repo, "latest", oras.DefaultCopyOptions)
	Expect(err).NotTo(HaveOccurred())
	return "oci://" + rr.host + "/libraries/fixture-addon:latest"
}

var _ = Describe("an oci:// add-on library behind registry credentials", func() {
	var (
		rr       *e2eRegistry
		libraryA string
		sources  *catalog.Sources
		release  catalog.Release
		admin    = contract.Actor{UserID: "owner", CredentialID: "session", Mode: "named-user"}
	)
	allowAll := func(context.Context, contract.Actor) error { return nil }

	BeforeEach(func() {
		var secured *atomic.Bool
		rr, secured = newE2ERegistry()
		artifact, digest := rr.pushPackage()
		release = catalog.Release{ID: "fixture-addon", Name: "Fixture add-on", Version: "1.0.0", Channel: "latest", Artifact: artifact, Digest: digest, OS: "linux", Arch: "amd64"}
		var index bytes.Buffer
		Expect(catalog.WriteIndex(&index, []catalog.Release{release})).To(Succeed())
		libraryA = rr.pushLibrary(index.Bytes())
		secured.Store(true)

		previous := credentials.SetDefault(nil)
		DeferCleanup(func() { credentials.SetDefault(previous) })

		var err error
		sources, err = catalog.NewSources(filepath.Join(GinkgoT().TempDir(), "sources.json"), allowAll, catalog.CredentialResolver{})
		Expect(err).NotTo(HaveOccurred())
		Expect(sources.Save(context.Background(), admin, catalog.Source{ID: "lib", Name: "lib", URL: libraryA, Enabled: true, AllowLoopbackHTTP: true})).To(Succeed())
	})

	catalogOf := func() catalog.Catalog {
		return catalog.Catalog{Sources: sources, TargetOS: "linux", TargetArch: "amd64"}
	}

	It("fails with a credential error and installs nothing without a rule", func() {
		_, err := sources.Refresh(context.Background(), "lib")
		Expect(err).To(MatchError(catalog.ErrCredential))
		Expect(rr.unauthorized.Load()).To(BeNumerically(">", 0))
		c := catalogOf()
		_, err = c.Resolve(context.Background(), "lib", "fixture-addon", "latest")
		Expect(err).To(MatchError(catalog.ErrRefresh))
		Expect(rr.cdnHeaders()).To(BeEmpty())
	})

	It("lists and installs with a rule, and sends no credential to the redirect target", func() {
		rules := "- match: " + rr.host + "\n  allow_insecure: true\n  username: " + e2eUser + "\n  password_env: E2E_REGISTRY_PASSWORD\n"
		store, err := credentials.Parse([]byte(rules), func(key string) (string, bool) {
			return e2ePassword, key == "E2E_REGISTRY_PASSWORD"
		})
		Expect(err).NotTo(HaveOccurred())
		credentials.SetDefault(store)

		result, err := sources.Refresh(context.Background(), "lib")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Count).To(Equal(1))
		c := catalogOf()
		resolved, err := c.Resolve(context.Background(), "lib", "fixture-addon", "latest")
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved.Artifact).To(Equal(release.Artifact))

		installer := &packages.Installer{
			Root: GinkgoT().TempDir(), Catalog: c,
			Host: contract.HostCompatibility{Host: 1, API: contract.HostAPIVersion, Protocol: 1, SDK: 1, DataFormat: 1, OS: "linux", Arch: "amd64"},
		}
		_, err = installer.Stage(context.Background(), resolved, nil)
		Expect(err).NotTo(HaveOccurred())

		regHost, _, err := net.SplitHostPort(rr.host)
		Expect(err).NotTo(HaveOccurred())
		cdnHost, _, err := net.SplitHostPort(rr.cdnHost)
		Expect(err).NotTo(HaveOccurred())
		Expect(cdnHost).To(Equal(regHost), "the redirect target must share the registry's host name")
		Expect(rr.cdnHost).NotTo(Equal(rr.host))

		seen := rr.cdnHeaders()
		Expect(seen).NotTo(BeEmpty(), "the blob reads must have been redirected")
		for i, h := range seen {
			Expect(h).To(BeEmpty(), "request %d to the second host carried the registry credential", i)
		}
	})
})
