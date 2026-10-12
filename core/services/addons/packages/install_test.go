// SPDX-License-Identifier: MIT
package packages

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/credentials"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPackages(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Addon packages") }
func archive(headers []*tar.Header, manifest []byte) []byte {
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	for _, f := range []struct {
		name string
		data []byte
	}{{"manifest.json", manifest}, {"bin/sample", []byte("executable")}} {
		Expect(w.WriteHeader(&tar.Header{Name: f.name, Mode: 0755, Size: int64(len(f.data))})).To(Succeed())
		_, e := w.Write(f.data)
		Expect(e).NotTo(HaveOccurred())
	}
	for _, h := range headers {
		Expect(w.WriteHeader(h)).To(Succeed())
		if h.Size > 0 {
			_, e := w.Write(make([]byte, h.Size))
			Expect(e).NotTo(HaveOccurred())
		}
	}
	Expect(w.Close()).To(Succeed())
	return b.Bytes()
}
func fixture(data []byte, edits ...func(*catalog.Release, *catalog.Source)) (*Installer, catalog.Release) {
	artifact := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			w.WriteHeader(403)
			return
		}
		w.Write(data)
	}))
	DeferCleanup(artifact.Close)
	r := catalog.Release{AddonID: "sample", Name: "Sample", Version: "1.2.3", Channel: "latest", Artifact: artifact.URL + "/package.tar", Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(data)), OS: "linux", Arch: "amd64"}
	src := catalog.Source{ID: "test", Name: "Test", Enabled: true, AllowLoopbackHTTP: true}
	for _, edit := range edits {
		edit(&r, &src)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, rq *http.Request) {
		if rq.Header.Get("Authorization") != "Bearer catalog-only" {
			w.WriteHeader(403)
			return
		}
		json.NewEncoder(w).Encode([]catalog.Release{r})
	}))
	DeferCleanup(server.Close)
	store, e := credentials.Parse([]byte("- match: "+server.URL+"\n  allow_insecure: true\n  bearer: catalog-only"), nil)
	Expect(e).NotTo(HaveOccurred())
	s, e := catalog.NewSources(filepath.Join(GinkgoT().TempDir(), "sources"), func(context.Context, contract.Actor) error { return nil }, catalog.CredentialResolver{References: map[string]catalog.CredentialBinding{"catalog": {Origin: server.URL, Store: store}}})
	Expect(e).NotTo(HaveOccurred())
	src.URL = server.URL
	src.AuthReference = "catalog"
	Expect(s.Save(context.Background(), contract.Actor{}, src)).To(Succeed())
	_, e = s.Refresh(context.Background(), "test")
	Expect(e).NotTo(HaveOccurred())
	c := catalog.Catalog{Sources: s, TargetOS: "linux", TargetArch: "amd64"}
	resolved, e := c.Resolve(context.Background(), "test", "sample", "latest")
	Expect(e).NotTo(HaveOccurred())
	root := GinkgoT().TempDir()
	DeferCleanup(func() {
		filepath.Walk(root, func(p string, info os.FileInfo, e error) error {
			if e == nil && info.IsDir() {
				os.Chmod(p, 0700)
			}
			return nil
		})
	})
	return &Installer{Root: root, Catalog: c, Host: contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: "linux", Arch: "amd64"}}, resolved
}

var _ = Describe("immutable packages", func() {
	var m []byte
	BeforeEach(func() {
		var e error
		m, e = os.ReadFile("../../../../tests/addons/fixtures/packages/manifest.json")
		Expect(e).NotTo(HaveOccurred())
	})
	It("stages verified HTTP bytes and commits immutable receipts without activation", func() {
		i, r := fixture(archive(nil, m))
		s, e := i.Stage(context.Background(), r, nil)
		Expect(e).NotTo(HaveOccurred())
		receipt, e := i.Commit(context.Background(), s)
		Expect(e).NotTo(HaveOccurred())
		Expect(receipt.Digest).To(Equal(r.Digest))
		Expect(receipt.Channel).To(Equal("latest"))
		Expect(receipt.Reference).To(Equal(r.Artifact))
		Expect(receipt.Manifest.ID).To(Equal("sample"))
		Expect(receipt.SourceID).To(Equal("test"))
		Expect(receipt.Verification).To(Equal("sha256"))
		Expect(filepath.IsAbs(receipt.Directory)).To(BeTrue())
		_, e = i.Commit(context.Background(), s)
		Expect(e).To(HaveOccurred())
		Expect(filepath.Join(receipt.Directory, "bin/sample")).To(BeAnExistingFile())
	})
	It("rejects untrusted integrity forged releases and digest mismatches", func() {
		i, r := fixture(archive(nil, m))
		r.Digest = ""
		_, e := i.Stage(context.Background(), r, nil)
		Expect(e).To(HaveOccurred())
		r.Digest = fmt.Sprintf("sha256:%064d", 0)
		_, e = i.Stage(context.Background(), r, nil)
		Expect(e).To(HaveOccurred())
	})
	It("rejects traversal links collisions and excessive extraction", func() {
		for _, h := range []*tar.Header{{Name: "../escape"}, {Name: "/escape"}, {Name: "link", Typeflag: tar.TypeSymlink, Linkname: "../escape"}, {Name: "link", Typeflag: tar.TypeLink, Linkname: "bin/sample"}, {Name: "bin/sample"}, {Name: "bin/sample/child"}, {Name: "huge", Size: 8192}} {
			i, r := fixture(archive([]*tar.Header{h}, m))
			i.MaxBytes = 1 << 20
			if h.Name == "huge" {
				i.MaxBytes = 4096
			}
			_, e := i.Stage(context.Background(), r, nil)
			Expect(e).To(HaveOccurred())
		}
	})
	It("rejects cancellation disk full and preserves existing packages", func() {
		i, r := fixture(archive(nil, m))
		Expect(os.WriteFile(filepath.Join(i.Root, "old"), []byte("old"), 0600)).To(Succeed())
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, e := i.Stage(ctx, r, nil)
		Expect(e).To(HaveOccurred())
		i.writeFile = func(string, os.FileMode) (fileWriter, error) { return nil, syscall.ENOSPC }
		_, e = i.Stage(context.Background(), r, nil)
		Expect(e).To(HaveOccurred())
		b, e := os.ReadFile(filepath.Join(i.Root, "old"))
		Expect(e).NotTo(HaveOccurred())
		Expect(string(b)).To(Equal("old"))
	})
	It("rejects platform data API SDK and missing asset mismatches", func() {
		for _, change := range []func(*contract.Manifest){func(m *contract.Manifest) { m.OS = "darwin" }, func(m *contract.Manifest) { m.API.Min = 2; m.API.Max = 2 }, func(m *contract.Manifest) { m.DataFormat.Min = 2; m.DataFormat.Max = 2 }, func(m *contract.Manifest) {
			m.Capabilities.UI = &contract.UI{Entry: "ui.js", SDK: contract.VersionRange{Min: 2, Max: 2}}
		}, func(m *contract.Manifest) { m.Executable = "missing" }} {
			var manifest contract.Manifest
			Expect(json.Unmarshal(m, &manifest)).To(Succeed())
			change(&manifest)
			b, e := json.Marshal(manifest)
			Expect(e).NotTo(HaveOccurred())
			i, r := fixture(archive(nil, b))
			_, e = i.Stage(context.Background(), r, nil)
			Expect(e).To(HaveOccurred())
		}
	})
})

var _ = Describe("distribution adapters", func() {
	It("pins and verifies real local OCI distribution bytes without a container runtime", func() {
		m, e := os.ReadFile("../../../../tests/addons/fixtures/packages/manifest.json")
		Expect(e).NotTo(HaveOccurred())
		data := archive(nil, m)
		layer, e := tarball.LayerFromReader(bytes.NewReader(data))
		Expect(e).NotTo(HaveOccurred())
		img, e := mutate.AppendLayers(empty.Image, layer)
		Expect(e).NotTo(HaveOccurred())
		cfg, e := img.ConfigFile()
		Expect(e).NotTo(HaveOccurred())
		cfg.OS = "linux"
		cfg.Architecture = "amd64"
		img, e = mutate.ConfigFile(img, cfg)
		Expect(e).NotTo(HaveOccurred())
		server := httptest.NewServer(registry.New())
		DeferCleanup(server.Close)
		ref, e := name.ParseReference(strings.TrimPrefix(server.URL, "http://") + "/sample:latest")
		Expect(e).NotTo(HaveOccurred())
		Expect(remote.Write(ref, img)).To(Succeed())
		digest, e := img.Digest()
		Expect(e).NotTo(HaveOccurred())
		edit := func(r *catalog.Release, s *catalog.Source) {
			r.Artifact = "oci://" + ref.Name()
			r.Digest = digest.String()
		}
		i, r := fixture(data, edit)
		staged, e := i.Stage(context.Background(), r, nil)
		Expect(e).NotTo(HaveOccurred())
		receipt, e := i.Commit(context.Background(), staged)
		Expect(e).NotTo(HaveOccurred())
		Expect(receipt.Digest).To(Equal(digest.String()))
		// A trusted signature requirement must not be satisfied by a mere digest.
		i, r = fixture(data, edit, func(r *catalog.Release, s *catalog.Source) {
			s.Policy = catalog.VerificationPolicy{RequireSignature: true, Issuer: "https://issuer.invalid", Identity: "trusted"}
		})
		_, e = i.Stage(context.Background(), r, nil)
		Expect(e).To(HaveOccurred())
	})
	It("rejects downloaded digest mismatch redirects cancellation and file count limits", func() {
		m, e := os.ReadFile("../../../../tests/addons/fixtures/packages/manifest.json")
		Expect(e).NotTo(HaveOccurred())
		data := archive(nil, m)
		i, r := fixture(data, func(r *catalog.Release, _ *catalog.Source) { r.Digest = "sha256:" + strings.Repeat("0", 64) })
		_, e = i.Stage(context.Background(), r, nil)
		Expect(e).To(HaveOccurred())
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { Fail("redirect was followed") }))
		DeferCleanup(target.Close)
		redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
		DeferCleanup(redirect.Close)
		i, r = fixture(data, func(r *catalog.Release, _ *catalog.Source) { r.Artifact = redirect.URL + "/p.tar" })
		_, e = i.Stage(context.Background(), r, nil)
		Expect(e).To(HaveOccurred())
		i, r = fixture(data)
		i.MaxFiles = 1
		_, e = i.Stage(context.Background(), r, nil)
		Expect(e).To(HaveOccurred())
		i, r = fixture(data)
		ctx, cancel := context.WithCancel(context.Background())
		_, e = i.Stage(ctx, r, func(p Progress) { cancel() })
		Expect(e).To(HaveOccurred())
		i, r = fixture(data)
		i.writeFile = func(p string, m os.FileMode) (fileWriter, error) {
			f, e := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, m)
			return fullFile{f}, e
		}
		_, e = i.Stage(context.Background(), r, nil)
		Expect(e).To(MatchError(syscall.ENOSPC))
		Expect(i.Commit(context.Background(), StagedPackage{})).Error().To(HaveOccurred())
	})
})

type fullFile struct{ *os.File }

func (f fullFile) Write([]byte) (int, error) { return 0, syscall.ENOSPC }

func (f fullFile) ReadFrom(io.Reader) (int64, error) { return 0, syscall.ENOSPC }

var _ = Describe("package asset boundaries", func() {
	It("accepts optional UI only with real immutable assets and rejects non executable entrypoints", func() {
		b, e := os.ReadFile("../../../../tests/addons/fixtures/packages/manifest.json")
		Expect(e).NotTo(HaveOccurred())
		var m contract.Manifest
		Expect(json.Unmarshal(b, &m)).To(Succeed())
		m.Capabilities.UI = &contract.UI{Entry: "ui/main.js", CSS: []string{"ui/style.css"}, SDK: contract.VersionRange{Min: 1, Max: 1}, Pages: []string{"home"}}
		b, e = json.Marshal(m)
		Expect(e).NotTo(HaveOccurred())
		i, r := fixture(archive([]*tar.Header{{Name: "ui/main.js", Mode: 0644}, {Name: "ui/style.css", Mode: 0644}}, b))
		st, e := i.Stage(context.Background(), r, nil)
		Expect(e).NotTo(HaveOccurred())
		receipt, e := i.Commit(context.Background(), st)
		Expect(e).NotTo(HaveOccurred())
		Expect(filepath.Join(receipt.Directory, "ui/main.js")).To(BeAnExistingFile())
		m.Executable = "ui/main.js"
		b, e = json.Marshal(m)
		Expect(e).NotTo(HaveOccurred())
		i, r = fixture(archive([]*tar.Header{{Name: "ui/main.js", Mode: 0644}, {Name: "ui/style.css", Mode: 0644}}, b))
		_, e = i.Stage(context.Background(), r, nil)
		Expect(e).To(HaveOccurred())
	})
	It("does not allow remote verification policy to replace owner trust", func() {
		m, e := os.ReadFile("../../../../tests/addons/fixtures/packages/manifest.json")
		Expect(e).NotTo(HaveOccurred())
		i, r := fixture(archive(nil, m), func(r *catalog.Release, s *catalog.Source) {
			r.Policy = catalog.VerificationPolicy{RequireSignature: true, Issuer: "remote", Identity: "remote"}
		})
		Expect(r.Policy.RequireSignature).To(BeFalse())
		_, e = i.Stage(context.Background(), r, nil)
		Expect(e).NotTo(HaveOccurred())
	})
})
