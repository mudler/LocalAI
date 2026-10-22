// SPDX-License-Identifier: MIT
package backendlib

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The install of the entries the full backend library holds: meta backends,
// mutable development images and mirrors.
var _ = Describe("installing the entries of the full backend library", func() {
	const sum = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	var (
		in  *Installer
		got *gallery.GalleryBackend
	)

	serve := func(rs ...catalog.Release) catalog.Catalog {
		GinkgoHelper()
		lib := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = catalog.WriteIndex(w, rs)
		}))
		DeferCleanup(lib.Close)
		s, err := catalog.NewSources(filepath.Join(GinkgoT().TempDir(), "sources.json"), allow, catalog.CredentialResolver{})
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Save(ctx, contract.Actor{}, catalog.Source{ID: "lib", Name: "lib", URL: lib.URL, Enabled: true})).To(Succeed())
		_, err = s.Refresh(ctx, "lib")
		Expect(err).NotTo(HaveOccurred())
		return catalog.Catalog{Sources: s, TargetOS: "linux", TargetArch: "amd64"}
	}

	concrete := func(id string) catalog.Release {
		return catalog.Release{
			Kind: catalog.KindBackend, ID: id, Name: id, Version: "0.0.0-latest.aaaaaaaaaaaa", Channel: "latest",
			Artifact: "oci://quay.example/acme/backends:latest-" + id, Digest: sum, OS: "linux", Arch: "amd64",
		}
	}
	meta := func(id string, caps map[string]string) catalog.Release {
		return catalog.Release{Kind: catalog.KindBackend, ID: id, Name: id, Version: "0.0.0-latest", Channel: "latest", Capabilities: caps}
	}

	BeforeEach(func() {
		got = nil
		state, err := system.GetSystemState(system.WithBackendPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		in = &Installer{SystemState: state, ModelLoader: model.NewModelLoader(state)}
		in.install = func(_ context.Context, _ *system.SystemState, _ *model.ModelLoader, gb *gallery.GalleryBackend, _ func(string, string, string, float64), _ bool) error {
			got = gb
			return nil
		}
	})

	Describe("a meta backend", func() {
		It("installs the concrete backend its capability names, pinned by digest", func() {
			in.Catalog = serve(
				meta("demo", map[string]string{"default": "cpu-demo", "nvidia": "cuda12-demo"}),
				concrete("cpu-demo"), concrete("cuda12-demo"),
			)
			// This host reports no GPU, so it takes the default capability.
			Expect(in.Install(ctx, "lib", "demo", "latest", Progress{})).To(Succeed())
			Expect(got.Name).To(Equal("cpu-demo"))
			Expect(got.URI).To(Equal("oci://quay.example/acme/backends@" + sum))
			Expect(got.Library).To(Equal("lib"))
		})

		It("refuses a meta backend that names nothing for this host", func() {
			in.Catalog = serve(meta("demo", map[string]string{"nvidia": "cuda12-demo"}), concrete("cuda12-demo"))
			err := in.Install(ctx, "lib", "demo", "latest", Progress{})
			Expect(err).To(HaveOccurred())
			Expect(got).To(BeNil())
		})

		It("refuses a meta backend whose target is missing from the library", func() {
			in.Catalog = serve(meta("demo", map[string]string{"default": "cpu-demo"}))
			Expect(in.Install(ctx, "lib", "demo", "latest", Progress{})).To(MatchError(catalog.ErrNotFound))
			Expect(got).To(BeNil())
		})

		It("refuses a meta backend that points at another meta backend or at itself", func() {
			in.Catalog = serve(
				meta("demo", map[string]string{"default": "other"}),
				meta("other", map[string]string{"default": "cpu-demo"}),
				concrete("cpu-demo"),
			)
			Expect(in.Install(ctx, "lib", "demo", "latest", Progress{})).To(HaveOccurred())
			in.Catalog = serve(meta("loop", map[string]string{"default": "loop"}))
			Expect(in.Install(ctx, "lib", "loop", "latest", Progress{})).To(MatchError(ErrNoCapability))
			Expect(got).To(BeNil())
		})
	})

	Describe("a mutable development entry", func() {
		dev := func() catalog.Release {
			r := concrete("cpu-demo-development")
			r.Artifact, r.Digest, r.Mutable = "oci://quay.example/acme/backends:master-cpu-demo", "", true
			r.Version, r.Channel = "0.0.0-development", "development"
			return r
		}

		It("keeps its tag in the library and is pinned at install", func() {
			var asked []string
			in.lookup = func(_ context.Context, image, platform string) (string, error) {
				asked = append(asked, image+" "+platform)
				return sum, nil
			}
			in.Catalog = serve(dev())
			Expect(in.Install(ctx, "lib", "cpu-demo-development", "development", Progress{})).To(Succeed())
			Expect(asked).To(Equal([]string{"quay.example/acme/backends:master-cpu-demo linux/amd64"}))
			Expect(got.URI).To(Equal("oci://quay.example/acme/backends@" + sum))
			Expect(got.Version).To(Equal("0.0.0-development.cccccccccccc"))
		})

		It("is refused when the tag cannot be resolved, and nothing is installed", func() {
			in.lookup = func(context.Context, string, string) (string, error) { return "", context.DeadlineExceeded }
			in.Catalog = serve(dev())
			Expect(in.Install(ctx, "lib", "cpu-demo-development", "development", Progress{})).To(MatchError(ErrInvalid))
			Expect(got).To(BeNil())
		})

		It("is refused when the registry answers with something that is not a digest", func() {
			in.lookup = func(context.Context, string, string) (string, error) { return "latest", nil }
			in.Catalog = serve(dev())
			Expect(in.Install(ctx, "lib", "cpu-demo-development", "development", Progress{})).To(MatchError(ErrInvalid))
		})

		It("does not look anything up for a pinned entry", func() {
			in.lookup = func(context.Context, string, string) (string, error) {
				Fail("a pinned entry needs no lookup")
				return "", nil
			}
			in.Catalog = serve(concrete("cpu-demo"))
			Expect(in.Install(ctx, "lib", "cpu-demo", "latest", Progress{})).To(Succeed())
		})
	})

	It("pins the mirrors with the same digest as the artifact", func() {
		r := concrete("cpu-demo")
		r.Mirrors = []string{"oci://hub.example/acme/mirror:latest-cpu-demo", "oci://other.example/mirror:latest-cpu-demo"}
		in.Catalog = serve(r)
		Expect(in.Install(ctx, "lib", "cpu-demo", "latest", Progress{})).To(Succeed())
		Expect(got.Mirrors).To(Equal([]string{
			"oci://hub.example/acme/mirror@" + sum,
			"oci://other.example/mirror@" + sum,
		}))
		Expect(strings.Join(got.Mirrors, " ")).NotTo(ContainSubstring("latest-cpu-demo"))
	})
})
