package oci_test

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/registry"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	oras "oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/registry/remote"

	localoci "github.com/mudler/LocalAI/pkg/oci"
)

const testGalleryArtifactType = "application/vnd.localai.gallery.v1"

// artifactFile is one layer of a test artifact: the title annotation the
// puller lays the content out by, and the content itself.
type artifactFile struct {
	title string
	body  string
}

// pushTestArtifact publishes an ORAS artifact to the in-process registry and
// returns its reference and manifest digest, so specs can assert on the digest
// the puller reports back.
func pushTestArtifact(serverURL, repoPath, artifactType string, files []artifactFile) (string, string) {
	host := strings.TrimPrefix(serverURL, "http://")
	ctx := context.Background()

	store := memory.New()
	layers := []ocispec.Descriptor{}
	for _, f := range files {
		body := []byte(f.body)
		desc := content.NewDescriptorFromBytes("application/yaml", body)
		if f.title != "" {
			desc.Annotations = map[string]string{ocispec.AnnotationTitle: f.title}
		}
		Expect(store.Push(ctx, desc, bytes.NewReader(body))).To(Succeed())
		layers = append(layers, desc)
	}

	manifestDesc, err := oras.PackManifest(ctx, store, oras.PackManifestVersion1_1, artifactType, oras.PackManifestOptions{
		Layers: layers,
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(store.Tag(ctx, manifestDesc, "latest")).To(Succeed())

	repo, err := remote.NewRepository(host + "/" + repoPath)
	Expect(err).NotTo(HaveOccurred())
	repo.PlainHTTP = true
	_, err = oras.Copy(ctx, store, "latest", repo, "latest", oras.DefaultCopyOptions)
	Expect(err).NotTo(HaveOccurred())

	return host + "/" + repoPath + ":latest", manifestDesc.Digest.String()
}

var _ = Describe("PullArtifact", func() {
	var (
		server *httptest.Server
		dest   string
	)

	BeforeEach(func() {
		server = httptest.NewServer(registry.New())
		DeferCleanup(server.Close)
		// A nested destination keeps an escaping title landing in a fresh
		// directory of this spec's own, not in the shared temp root.
		dest = filepath.Join(GinkgoT().TempDir(), "tree")
		Expect(os.MkdirAll(dest, 0o750)).To(Succeed())
	})

	It("writes every layer under the destination, preserving subdirectories", func() {
		ref, manifestDigest := pushTestArtifact(server.URL, "galleries/premium", testGalleryArtifactType, []artifactFile{
			{title: "index.yaml", body: "- name: one\n"},
			{title: "base/virtual.yaml", body: "- name: two\n"},
		})

		digest, err := localoci.PullArtifact(context.Background(), ref, dest,
			localoci.WithArtifactType(testGalleryArtifactType))
		Expect(err).NotTo(HaveOccurred())
		Expect(digest).To(Equal(manifestDigest))

		Expect(os.ReadFile(filepath.Join(dest, "index.yaml"))).To(BeEquivalentTo("- name: one\n"))
		Expect(os.ReadFile(filepath.Join(dest, "base", "virtual.yaml"))).To(BeEquivalentTo("- name: two\n"))
		for _, relative := range []string{"index.yaml", "base/virtual.yaml"} {
			info, err := os.Stat(filepath.Join(dest, relative))
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Mode().Perm() & 0o077).To(BeZero())
		}
	})

	It("refuses a layer whose title escapes the destination with ..", func() {
		ref, _ := pushTestArtifact(server.URL, "galleries/escape", testGalleryArtifactType, []artifactFile{
			{title: "../escape.yaml", body: "owned\n"},
		})

		_, err := localoci.PullArtifact(context.Background(), ref, dest,
			localoci.WithArtifactType(testGalleryArtifactType))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("../escape.yaml"))
		Expect(filepath.Join(filepath.Dir(dest), "escape.yaml")).NotTo(BeAnExistingFile())
	})

	It("refuses a layer whose title is an absolute path", func() {
		outside := filepath.Join(GinkgoT().TempDir(), "absolute.yaml")
		ref, _ := pushTestArtifact(server.URL, "galleries/absolute", testGalleryArtifactType, []artifactFile{
			{title: outside, body: "owned\n"},
		})

		_, err := localoci.PullArtifact(context.Background(), ref, dest,
			localoci.WithArtifactType(testGalleryArtifactType))
		Expect(err).To(HaveOccurred())
		Expect(outside).NotTo(BeAnExistingFile())
	})

	It("refuses a layer that carries no title", func() {
		ref, _ := pushTestArtifact(server.URL, "galleries/untitled", testGalleryArtifactType, []artifactFile{
			{title: "", body: "untitled\n"},
		})

		_, err := localoci.PullArtifact(context.Background(), ref, dest,
			localoci.WithArtifactType(testGalleryArtifactType))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("title"))
		entries, readErr := os.ReadDir(dest)
		Expect(readErr).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())
	})

	It("refuses an artifact published under a different artifact type", func() {
		ref, _ := pushTestArtifact(server.URL, "galleries/wrongtype", "application/vnd.localai.backend.v1", []artifactFile{
			{title: "index.yaml", body: "- name: one\n"},
		})

		_, err := localoci.PullArtifact(context.Background(), ref, dest,
			localoci.WithArtifactType(testGalleryArtifactType))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("application/vnd.localai.backend.v1"))
		Expect(filepath.Join(dest, "index.yaml")).NotTo(BeAnExistingFile())
	})

	It("refuses an artifact larger than the total size cap", func() {
		ref, _ := pushTestArtifact(server.URL, "galleries/toobig", testGalleryArtifactType, []artifactFile{
			{title: "index.yaml", body: strings.Repeat("a", 4096)},
		})

		_, err := localoci.PullArtifact(context.Background(), ref, dest,
			localoci.WithArtifactType(testGalleryArtifactType),
			localoci.WithMaxArtifactBytes(1024))
		Expect(err).To(HaveOccurred())
		Expect(filepath.Join(dest, "index.yaml")).NotTo(BeAnExistingFile())
	})

	It("refuses an artifact with more layers than the layer cap", func() {
		ref, _ := pushTestArtifact(server.URL, "galleries/toomany", testGalleryArtifactType, []artifactFile{
			{title: "index.yaml", body: "- name: one\n"},
			{title: "base/virtual.yaml", body: "- name: two\n"},
			{title: "base/extra.yaml", body: "- name: three\n"},
		})

		_, err := localoci.PullArtifact(context.Background(), ref, dest,
			localoci.WithArtifactType(testGalleryArtifactType),
			localoci.WithMaxArtifactLayers(2))
		Expect(err).To(HaveOccurred())
		Expect(filepath.Join(dest, "index.yaml")).NotTo(BeAnExistingFile())
	})
})

var _ = Describe("ResolveArtifactDigestRef", func() {
	var server *httptest.Server

	BeforeEach(func() {
		server = httptest.NewServer(registry.New())
		DeferCleanup(server.Close)
	})

	It("turns a tag into the digest the signature is taken over", func() {
		ref, manifestDigest := pushTestArtifact(server.URL, "galleries/resolve", testGalleryArtifactType, []artifactFile{
			{title: "index.yaml", body: "- name: one\n"},
		})

		digestRef, err := localoci.ResolveArtifactDigestRef(context.Background(), ref)
		Expect(err).NotTo(HaveOccurred())
		Expect(digestRef).To(Equal(strings.TrimSuffix(ref, ":latest") + "@" + manifestDigest))

		// The pinned reference must be pullable as it stands, since that is
		// what a verified caller pulls once the signature checks out.
		dest := filepath.Join(GinkgoT().TempDir(), "pinned")
		Expect(os.MkdirAll(dest, 0o750)).To(Succeed())
		_, err = localoci.PullArtifact(context.Background(), digestRef, dest,
			localoci.WithArtifactType(testGalleryArtifactType))
		Expect(err).NotTo(HaveOccurred())
		Expect(os.ReadFile(filepath.Join(dest, "index.yaml"))).To(BeEquivalentTo("- name: one\n"))
	})

	It("reports a reference that does not exist", func() {
		host := strings.TrimPrefix(server.URL, "http://")
		_, err := localoci.ResolveArtifactDigestRef(context.Background(), host+"/galleries/absent:latest")
		Expect(err).To(HaveOccurred())
	})
})
