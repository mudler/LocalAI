package gallery

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

// pushBackendImage publishes a minimal backend image (just a run.sh) to the
// in-process registry and returns the registry host and the manifest digest.
func pushBackendImage(serverURL, repoPath, tag, runScript string) (string, string) {
	GinkgoHelper()
	host := strings.TrimPrefix(serverURL, "http://")

	layer, err := crane.Layer(map[string][]byte{"run.sh": []byte(runScript)})
	Expect(err).ToNot(HaveOccurred())
	img, err := mutate.AppendLayers(empty.Image, layer)
	Expect(err).ToNot(HaveOccurred())

	ref, err := name.ParseReference(host + "/" + repoPath + ":" + tag)
	Expect(err).ToNot(HaveOccurred())
	Expect(remote.Write(ref, img)).To(Succeed())

	digest, err := img.Digest()
	Expect(err).ToNot(HaveOccurred())
	return host, digest.String()
}

var _ = Describe("backends installed from an oci:// URI", func() {
	var (
		registryURL string
		systemState *system.SystemState
		ml          *model.ModelLoader
		backendsDir string
	)

	BeforeEach(func() {
		srv, _, _ := ociRegistry()
		registryURL = srv.URL

		backendsDir = GinkgoT().TempDir()
		var err error
		systemState, err = system.GetSystemState(system.WithBackendPath(backendsDir))
		Expect(err).ToNot(HaveOccurred())
		ml = model.NewModelLoader(systemState)
	})

	installedDigest := func(name string) string {
		GinkgoHelper()
		meta, err := readBackendMetadata(filepath.Join(backendsDir, name))
		Expect(err).ToNot(HaveOccurred())
		Expect(meta).ToNot(BeNil())
		return meta.Digest
	}

	// The digest lookup that follows an install used to hand the raw oci://
	// URI to the registry client, which read "oci" as the registry host and
	// queried https://oci/v2/. The install still succeeded, so the only trace
	// was an empty digest in metadata.json and a warning in the log.
	DescribeTable("records the image digest from the registry the URI names",
		func(form string) {
			host, digest := pushBackendImage(registryURL, "acme/backend", "v1", "#!/bin/sh\necho v1\n")
			uri := "oci://" + host + "/acme/backend:v1"
			if form == "digest" {
				uri = "oci://" + host + "/acme/backend@" + digest
			}

			Expect(InstallBackend(context.Background(), systemState, ml, &GalleryBackend{
				Metadata: Metadata{Name: "acme-backend"},
				URI:      uri,
			}, nil, false)).To(Succeed())

			Expect(filepath.Join(backendsDir, "acme-backend", "run.sh")).To(BeARegularFile())
			Expect(installedDigest("acme-backend")).To(Equal(digest))
		},
		Entry("tag form", "tag"),
		Entry("digest form", "digest"),
	)

	It("records the new image digest after an upgrade", func() {
		host, oldDigest := pushBackendImage(registryURL, "acme/backend", "v1", "#!/bin/sh\necho v1\n")
		_, newDigest := pushBackendImage(registryURL, "acme/backend", "v2", "#!/bin/sh\necho v2\n")
		Expect(newDigest).ToNot(Equal(oldDigest))

		Expect(InstallBackend(context.Background(), systemState, ml, &GalleryBackend{
			Metadata: Metadata{Name: "acme-backend"},
			URI:      "oci://" + host + "/acme/backend:v1",
		}, nil, false)).To(Succeed())
		Expect(installedDigest("acme-backend")).To(Equal(oldDigest))

		galleryFile := filepath.Join(backendsDir, "gallery.yaml")
		data, err := yaml.Marshal([]GalleryBackend{{
			Metadata: Metadata{Name: "acme-backend"},
			URI:      "oci://" + host + "/acme/backend@" + newDigest,
		}})
		Expect(err).ToNot(HaveOccurred())
		Expect(os.WriteFile(galleryFile, data, 0o644)).To(Succeed())
		galleries := []config.Gallery{{Name: "acme", URL: "file://" + galleryFile}}

		upgrades, err := CheckBackendUpgrades(context.Background(), galleries, systemState)
		Expect(err).ToNot(HaveOccurred())
		Expect(upgrades).To(HaveKey("acme-backend"))
		Expect(upgrades["acme-backend"].AvailableDigest).To(Equal(newDigest))

		Expect(UpgradeBackend(context.Background(), systemState, ml, galleries, "acme-backend", nil, false)).To(Succeed())
		Expect(installedDigest("acme-backend")).To(Equal(newDigest))
	})
})

// ocifile:// and ollama:// look like OCI to the downloader but name no
// registry image, so the digest lookup that follows an install or backs an
// upgrade check can only fail against them, with a warning every time.
var _ = Describe("registry digest lookups", func() {
	var (
		systemState *system.SystemState
		ml          *model.ModelLoader
		backendsDir string
		looked      []string
	)

	BeforeEach(func() {
		backendsDir = GinkgoT().TempDir()
		var err error
		systemState, err = system.GetSystemState(system.WithBackendPath(backendsDir))
		Expect(err).ToNot(HaveOccurred())
		ml = model.NewModelLoader(systemState)

		looked = nil
		original := lookupImageDigest
		lookupImageDigest = func(ref string) (string, error) {
			looked = append(looked, ref)
			return original(ref)
		}
		DeferCleanup(func() { lookupImageDigest = original })
	})

	// ociTarball writes a minimal backend image as a local OCI tarball, the
	// form an ocifile:// URI names.
	ociTarball := func() string {
		GinkgoHelper()
		layer, err := crane.Layer(map[string][]byte{"run.sh": []byte("#!/bin/sh\necho local\n")})
		Expect(err).ToNot(HaveOccurred())
		img, err := mutate.AppendLayers(empty.Image, layer)
		Expect(err).ToNot(HaveOccurred())
		ref, err := name.ParseReference("local/backend:v1")
		Expect(err).ToNot(HaveOccurred())
		path := filepath.Join(GinkgoT().TempDir(), "backend.tar")
		Expect(tarball.WriteToFile(path, ref, img)).To(Succeed())
		return path
	}

	It("does not ask a registry for the digest of a backend installed from ocifile://", func() {
		uri := "ocifile://" + ociTarball()

		Expect(InstallBackend(context.Background(), systemState, ml, &GalleryBackend{
			Metadata: Metadata{Name: "local-backend"},
			URI:      uri,
		}, nil, false)).To(Succeed())
		Expect(filepath.Join(backendsDir, "local-backend", "run.sh")).To(BeARegularFile())
		Expect(looked).To(BeEmpty(), "a local tarball was looked up in a registry")
	})

	It("does not ask a registry for the digest of an ocifile:// gallery entry in an upgrade check", func() {
		uri := "ocifile://" + ociTarball()
		Expect(InstallBackend(context.Background(), systemState, ml, &GalleryBackend{
			Metadata: Metadata{Name: "local-backend"},
			URI:      uri,
		}, nil, false)).To(Succeed())

		galleryFile := filepath.Join(backendsDir, "gallery.yaml")
		data, err := yaml.Marshal([]GalleryBackend{{Metadata: Metadata{Name: "local-backend"}, URI: uri}})
		Expect(err).ToNot(HaveOccurred())
		Expect(os.WriteFile(galleryFile, data, 0o644)).To(Succeed())

		_, err = CheckBackendUpgrades(context.Background(),
			[]config.Gallery{{Name: "local", URL: "file://" + galleryFile}}, systemState)
		Expect(err).ToNot(HaveOccurred())
		Expect(looked).To(BeEmpty(), "a local tarball was looked up in a registry")
	})
})
