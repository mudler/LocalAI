// SPDX-License-Identifier: MIT
package backendlib

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestBackendLibrary(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Backend library install")
}

var ctx = context.Background()

func allow(context.Context, contract.Actor) error { return nil }

// push publishes a minimal backend image (a run.sh) and returns the registry
// host and the manifest digest.
func push(host, repo, tag, script string) string {
	GinkgoHelper()
	layer, err := crane.Layer(map[string][]byte{"run.sh": []byte(script)})
	Expect(err).NotTo(HaveOccurred())
	img, err := mutate.AppendLayers(empty.Image, layer)
	Expect(err).NotTo(HaveOccurred())
	ref, err := name.ParseReference(host + "/" + repo + ":" + tag)
	Expect(err).NotTo(HaveOccurred())
	Expect(remote.Write(ref, img)).To(Succeed())
	d, err := img.Digest()
	Expect(err).NotTo(HaveOccurred())
	return d.String()
}

var _ = Describe("installing a backend from a library", func() {
	var (
		host        string
		backendsDir string
		in          *Installer
		release     catalog.Release
		digest      string
	)

	// serve starts a library that lists the release and refreshes it.
	serve := func(r catalog.Release, policy catalog.VerificationPolicy) catalog.Catalog {
		GinkgoHelper()
		lib := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = catalog.WriteIndex(w, []catalog.Release{r})
		}))
		DeferCleanup(lib.Close)
		s, err := catalog.NewSources(filepath.Join(GinkgoT().TempDir(), "sources.json"), allow, catalog.CredentialResolver{})
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Save(ctx, contract.Actor{}, catalog.Source{ID: "pilot", Name: "Pilot", URL: lib.URL, Enabled: true, Policy: policy})).To(Succeed())
		_, err = s.Refresh(ctx, "pilot")
		Expect(err).NotTo(HaveOccurred())
		return catalog.Catalog{Sources: s, TargetOS: "linux", TargetArch: "amd64"}
	}

	BeforeEach(func() {
		reg := httptest.NewServer(registry.New())
		DeferCleanup(reg.Close)
		host = strings.TrimPrefix(reg.URL, "http://")
		digest = push(host, "acme/backends", "latest-cpu-demo", "#!/bin/sh\necho demo\n")
		release = catalog.Release{
			Kind: catalog.KindBackend, ID: "cpu-demo", Alias: "demo", Name: "Demo (CPU)", Version: "0.0.0-latest.aaaaaaaaaaaa",
			Artifact: "oci://" + host + "/acme/backends:latest-cpu-demo", Digest: digest, OS: "linux", Arch: "amd64",
		}
		backendsDir = GinkgoT().TempDir()
		state, err := system.GetSystemState(system.WithBackendPath(backendsDir))
		Expect(err).NotTo(HaveOccurred())
		in = &Installer{Catalog: serve(release, catalog.VerificationPolicy{}), SystemState: state, ModelLoader: model.NewModelLoader(state)}
	})

	left := func() []string {
		entries, err := os.ReadDir(backendsDir)
		Expect(err).NotTo(HaveOccurred())
		out := []string{}
		for _, e := range entries {
			out = append(out, e.Name())
		}
		return out
	}

	It("installs by digest and records the library, the digest and the alias", func() {
		Expect(in.Install(ctx, "pilot", "cpu-demo", release.Version, Progress{})).To(Succeed())
		Expect(filepath.Join(backendsDir, "cpu-demo", "run.sh")).To(BeARegularFile())
		raw, err := os.ReadFile(filepath.Join(backendsDir, "cpu-demo", "metadata.json"))
		Expect(err).NotTo(HaveOccurred())
		var meta gallery.BackendMetadata
		Expect(json.Unmarshal(raw, &meta)).To(Succeed())
		Expect(meta.Library).To(Equal("pilot"))
		Expect(meta.Digest).To(Equal(digest))
		Expect(meta.Alias).To(Equal("demo"))
		Expect(meta.Version).To(Equal(release.Version))
		Expect(meta.URI).To(HaveSuffix("@" + digest))
		// It is a backend like any other: the system lists it.
		list, err := gallery.ListSystemBackends(in.SystemState)
		Expect(err).NotTo(HaveOccurred())
		Expect(list.Exists("cpu-demo")).To(BeTrue())
	})

	It("installs on a host that has no backends directory yet", func() {
		fresh := filepath.Join(GinkgoT().TempDir(), "backends")
		state, err := system.GetSystemState(system.WithBackendPath(fresh))
		Expect(err).NotTo(HaveOccurred())
		in.SystemState, in.ModelLoader = state, model.NewModelLoader(state)
		Expect(in.Install(ctx, "pilot", "cpu-demo", release.Version, Progress{})).To(Succeed())
		Expect(filepath.Join(fresh, "cpu-demo", "run.sh")).To(BeARegularFile())
	})

	It("installs again from the same library", func() {
		Expect(in.Install(ctx, "pilot", "cpu-demo", release.Version, Progress{})).To(Succeed())
		Expect(in.Install(ctx, "pilot", "cpu-demo", release.Version, Progress{})).To(Succeed())
	})

	It("refuses an entry whose digest is not in the registry and leaves nothing behind", func() {
		bad := release
		bad.Digest = "sha256:" + strings.Repeat("0", 64)
		in.Catalog = serve(bad, catalog.VerificationPolicy{})
		Expect(in.Install(ctx, "pilot", "cpu-demo", release.Version, Progress{})).NotTo(Succeed())
		Expect(left()).To(BeEmpty())
	})

	It("refuses an image over the size limit and leaves nothing behind", func() {
		in.MaxBytes = 1
		err := in.Install(ctx, "pilot", "cpu-demo", release.Version, Progress{})
		Expect(err).To(MatchError(ContainSubstring(downloader.ErrImageTooLarge.Error())))
		Expect(left()).To(BeEmpty())
	})

	It("uses 8 GiB as the default limit for a backend", func() {
		Expect((&Installer{}).limit()).To(Equal(int64(8 << 30)))
		Expect((&Installer{MaxBytes: 5}).limit()).To(Equal(int64(5)))
	})

	It("refuses to replace a backend that came from a gallery", func() {
		dir := filepath.Join(backendsDir, "cpu-demo")
		Expect(os.MkdirAll(dir, 0o750)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\necho gallery\n"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(`{"name":"cpu-demo","gallery_url":"https://gallery.example/index.yaml"}`), 0o644)).To(Succeed())
		Expect(in.Install(ctx, "pilot", "cpu-demo", release.Version, Progress{})).To(MatchError(ErrCollision))
		run, err := os.ReadFile(filepath.Join(dir, "run.sh"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(run)).To(ContainSubstring("gallery"))
		Expect(left()).To(Equal([]string{"cpu-demo"}))
	})

	It("refuses a library without a signature policy in strict integrity mode", func() {
		in.RequireIntegrity = true
		Expect(in.Install(ctx, "pilot", "cpu-demo", release.Version, Progress{})).To(MatchError(ErrIntegrity))
		Expect(left()).To(BeEmpty())
	})

	It("does not install an add-on entry or an unknown name", func() {
		Expect(in.Install(ctx, "pilot", "missing", "1.0.0", Progress{})).To(MatchError(catalog.ErrNotFound))
		Expect(in.Install(ctx, "nowhere", "cpu-demo", release.Version, Progress{})).NotTo(Succeed())
	})

	It("takes the signature policy of the library, with the identity pattern, and pins the image by digest", func() {
		pattern := `^https://github\.com/o/r/\.github/workflows/w\.yml@refs/(heads/master|tags/.+)$`
		policy := catalog.VerificationPolicy{RequireSignature: true, Issuer: "https://issuer.example", IdentityRegex: pattern}
		in.Catalog = serve(release, policy)
		var got *gallery.GalleryBackend
		in.install = func(_ context.Context, _ *system.SystemState, _ *model.ModelLoader, gb *gallery.GalleryBackend, _ func(string, string, string, float64), strict bool) error {
			got = gb
			return nil
		}
		in.RequireIntegrity = true
		Expect(in.Install(ctx, "pilot", "cpu-demo", release.Version, Progress{})).To(Succeed())
		Expect(got.URI).To(Equal("oci://" + host + "/acme/backends@" + digest))
		Expect(got.Gallery.Verification).NotTo(BeNil())
		Expect(got.Gallery.Verification.Issuer).To(Equal("https://issuer.example"))
		Expect(got.Gallery.Verification.IdentityRegex).To(Equal(pattern))
		Expect(got.Gallery.Verification.Identity).To(BeEmpty())
		Expect(got.MaxBytes).To(Equal(int64(8 << 30)))
		Expect(got.Library).To(Equal("pilot"))
	})

	It("reports the steps in order, with the bytes of the fetch", func() {
		steps := []string{}
		bytes := 0
		Expect(in.Install(ctx, "pilot", "cpu-demo", release.Version, Progress{
			Step:  func(s string) { steps = append(steps, s) },
			Bytes: func(string, string, string, float64) { bytes++ },
		})).To(Succeed())
		// No signature is required here, so there is no verifying step.
		Expect(steps).To(Equal([]string{StepResolving, StepSize, StepFetching, StepWriting}))
		Expect(bytes).To(BeNumerically(">", 0))
	})

	It("stops at the step that fails", func() {
		in.MaxBytes = 1
		steps := []string{}
		Expect(in.Install(ctx, "pilot", "cpu-demo", release.Version, Progress{Step: func(s string) { steps = append(steps, s) }})).NotTo(Succeed())
		Expect(steps).To(Equal([]string{StepResolving, StepSize}))
	})

	It("orders the steps the page shows", func() {
		Expect(Steps).To(Equal([]string{"resolving", "verifying", "size", "fetching", "writing"}))
	})

	Describe("updates", func() {
		// install puts the release on disk, then the library lists others.
		install := func() {
			GinkgoHelper()
			Expect(in.Install(ctx, "pilot", "cpu-demo", release.Version, Progress{})).To(Succeed())
		}
		listing := func(rs ...catalog.Release) catalog.Catalog {
			GinkgoHelper()
			lib := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = catalog.WriteIndex(w, rs)
			}))
			DeferCleanup(lib.Close)
			s, err := catalog.NewSources(filepath.Join(GinkgoT().TempDir(), "sources.json"), allow, catalog.CredentialResolver{})
			Expect(err).NotTo(HaveOccurred())
			Expect(s.Save(ctx, contract.Actor{}, catalog.Source{ID: "pilot", Name: "Pilot", URL: lib.URL, Enabled: true})).To(Succeed())
			_, err = s.Refresh(ctx, "pilot")
			Expect(err).NotTo(HaveOccurred())
			return catalog.Catalog{Sources: s, TargetOS: "linux", TargetArch: "amd64"}
		}
		next := func(version, sum string) catalog.Release {
			r := release
			r.Version, r.Digest = version, "sha256:"+strings.Repeat(sum, 64)
			return r
		}

		It("reports nothing while the library lists the installed build", func() {
			install()
			got, err := in.Updates(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(BeEmpty())
		})

		It("reports a newer build of the same channel and does not install it", func() {
			install()
			newer := next("0.0.0-latest.bbbbbbbbbbbb", "b")
			newer.Channel, release.Channel = "latest", "latest"
			in.Catalog = listing(newer)
			got, err := in.Updates(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal([]Update{{
				Name: "cpu-demo", SourceID: "pilot",
				InstalledVersion: release.Version, InstalledDigest: digest,
				Version: newer.Version, Digest: newer.Digest,
			}}))
			raw, err := os.ReadFile(filepath.Join(backendsDir, "cpu-demo", "metadata.json"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(raw)).To(ContainSubstring(digest))
		})

		It("follows the channel of the installed build, found by its version", func() {
			install()
			old, stable := release, next("1.0.0", "c")
			old.Channel, stable.Channel = "latest", "stable"
			in.Catalog = listing(old, stable)
			got, err := in.Updates(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(BeEmpty())
			// The library publishes the same version again with another image.
			moved := next(release.Version, "e")
			moved.Channel = "latest"
			in.Catalog = listing(moved, stable)
			got, err = in.Updates(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(HaveLen(1))
			Expect(got[0].Digest).To(Equal(moved.Digest))
		})

		It("says nothing when it cannot tell which channel to follow", func() {
			install()
			a, b := next("1.0.0", "c"), next("2.0.0", "d")
			a.Channel, b.Channel = "stable", "beta"
			in.Catalog = listing(a, b)
			got, err := in.Updates(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(BeEmpty())
		})

		It("ignores a backend that did not come from a library", func() {
			dir := filepath.Join(backendsDir, "cpu-demo")
			Expect(os.MkdirAll(dir, 0o750)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\n"), 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(`{"name":"cpu-demo","digest":"sha256:`+strings.Repeat("a", 64)+`"}`), 0o644)).To(Succeed())
			in.Catalog = listing(next("0.0.0-latest.bbbbbbbbbbbb", "b"))
			got, err := in.Updates(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(BeEmpty())
		})
	})
})
