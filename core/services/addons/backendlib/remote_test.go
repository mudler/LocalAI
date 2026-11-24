// SPDX-License-Identifier: MIT
package backendlib

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// ownerRemote answers a frontend's installer from the owner's installer, as the
// carrier does.
type ownerRemote struct{ owner *Installer }

func (r ownerRemote) Resolve(ctx context.Context, sourceID, backend, version, capability string) (Plan, error) {
	return r.owner.Resolve(ctx, sourceID, backend, version, capability)
}

func (r ownerRemote) Releases(ctx context.Context, refs []Ref) (map[string][]catalog.Release, error) {
	out := map[string][]catalog.Release{}
	for _, ref := range refs {
		rel, err := r.owner.Catalog.KindReleases(ctx, catalog.KindBackend, ref.SourceID, ref.Name)
		if err == nil {
			out[RefKey(ref.SourceID, ref.Name)] = rel
		}
	}
	return out, nil
}

func (r ownerRemote) Target(context.Context) (string, string, error) {
	os, arch := r.owner.Catalog.Target()
	return os, arch, nil
}

var _ = Describe("a frontend installing through the owner's plan", func() {
	It("resolves on the owner, installs on the frontend's own backends path and reports updates", func() {
		reg := httptest.NewServer(registry.New())
		DeferCleanup(reg.Close)
		host := strings.TrimPrefix(reg.URL, "http://")
		digest := push(host, "acme/backends", "latest-cpu-demo", "#!/bin/sh\necho demo\n")
		release := catalog.Release{
			Kind: catalog.KindBackend, ID: "cpu-demo", Name: "Demo (CPU)", Version: "0.0.0-latest.aaaaaaaaaaaa",
			Artifact: "oci://" + host + "/acme/backends:latest-cpu-demo", Digest: digest, OS: "linux", Arch: "amd64",
		}
		lib := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = catalog.WriteIndex(w, []catalog.Release{release})
		}))
		DeferCleanup(lib.Close)
		s, err := catalog.NewSources(filepath.Join(GinkgoT().TempDir(), "sources.json"), allow, catalog.CredentialResolver{})
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Save(ctx, contract.Actor{}, catalog.Source{ID: "pilot", Name: "Pilot", URL: lib.URL, Enabled: true})).To(Succeed())
		_, err = s.Refresh(ctx, "pilot")
		Expect(err).NotTo(HaveOccurred())

		ownerDir, frontendDir := GinkgoT().TempDir(), GinkgoT().TempDir()
		ownerState, err := system.GetSystemState(system.WithBackendPath(ownerDir))
		Expect(err).NotTo(HaveOccurred())
		frontState, err := system.GetSystemState(system.WithBackendPath(frontendDir))
		Expect(err).NotTo(HaveOccurred())
		owner := &Installer{Catalog: catalog.Catalog{Sources: s, TargetOS: "linux", TargetArch: "amd64"}, SystemState: ownerState, ModelLoader: model.NewModelLoader(ownerState)}
		frontend := &Installer{Remote: ownerRemote{owner}, SystemState: frontState, ModelLoader: model.NewModelLoader(frontState)}

		plan, err := owner.Resolve(ctx, "pilot", "cpu-demo", release.Version, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(plan.URI).To(ContainSubstring("@" + digest))
		Expect(frontend.Install(ctx, "pilot", "cpu-demo", release.Version, Progress{})).To(Succeed())
		Expect(filepath.Join(frontendDir, "cpu-demo", "run.sh")).To(BeARegularFile())
		Expect(ownerDir).To(BeADirectory())
		Expect(filepath.Join(ownerDir, "cpu-demo")).NotTo(BeADirectory())

		// The frontend's strict mode applies to a plan with no signature.
		frontend.RequireIntegrity = true
		Expect(frontend.Install(ctx, "pilot", "cpu-demo", release.Version, Progress{})).To(MatchError(ErrIntegrity))

		// The owner's cache lists the same digest, so there is no update.
		updates, err := frontend.Updates(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(updates).To(BeEmpty())
	})
})
