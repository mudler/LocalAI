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
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/mudler/LocalAI/tests/addons/fixtures/signing"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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
		_, _ = w.Write(data)
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
		_ = catalog.WriteIndex(w, []catalog.Release{r})
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
		_ = filepath.Walk(root, func(p string, info os.FileInfo, e error) error {
			if e == nil && info.IsDir() {
				_ = os.Chmod(p, 0700)
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

func localOCI(data ...[]byte) (string, string, *atomic.Int64) {
	img := empty.Image
	for _, b := range data {
		layer, e := tarball.LayerFromReader(bytes.NewReader(b))
		Expect(e).NotTo(HaveOccurred())
		img, e = mutate.AppendLayers(img, layer)
		Expect(e).NotTo(HaveOccurred())
	}
	cfg, e := img.ConfigFile()
	Expect(e).NotTo(HaveOccurred())
	cfg.OS = "linux"
	cfg.Architecture = "amd64"
	img, e = mutate.ConfigFile(img, cfg)
	Expect(e).NotTo(HaveOccurred())
	calls := &atomic.Int64{}
	handler := registry.New()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); handler.ServeHTTP(w, r) }))
	DeferCleanup(server.Close)
	ref, e := name.ParseReference(strings.TrimPrefix(server.URL, "http://") + "/sample:latest")
	Expect(e).NotTo(HaveOccurred())
	Expect(remote.Write(ref, img)).To(Succeed())
	digest, e := img.Digest()
	Expect(e).NotTo(HaveOccurred())
	calls.Store(0)
	return "oci://" + ref.Name(), digest.String(), calls
}

var _ = Describe("security regressions", func() {
	It("requires source opt in before any plaintext OCI request", func() {
		m, e := os.ReadFile("../../../../tests/addons/fixtures/packages/manifest.json")
		Expect(e).NotTo(HaveOccurred())
		data := archive(nil, m)
		artifact, digest, calls := localOCI(data)
		i, r := fixture(data, func(r *catalog.Release, s *catalog.Source) {
			r.Artifact = artifact
			r.Digest = digest
			s.AllowLoopbackHTTP = false
		})
		_, e = i.Stage(context.Background(), r, nil)
		Expect(e).To(HaveOccurred())
		Expect(calls.Load()).To(BeZero())
	})
	for label, h := range map[string]*tar.Header{
		"PAX": {Name: "metadata", Format: tar.FormatPAX, PAXRecords: map[string]string{"comment": strings.Repeat("x", 128<<10)}},
		"GNU": {Name: strings.Repeat("a", 128<<10), Format: tar.FormatGNU},
	} {
		It("bounds compressed OCI "+label+" expansion before tar parsing", func() {
			m, e := os.ReadFile("../../../../tests/addons/fixtures/packages/manifest.json")
			Expect(e).NotTo(HaveOccurred())
			data := archive([]*tar.Header{h}, m)
			layer, e := tarball.LayerFromReader(bytes.NewReader(data))
			Expect(e).NotTo(HaveOccurred())
			size, e := layer.Size()
			Expect(e).NotTo(HaveOccurred())
			Expect(size).To(BeNumerically("<", 8192))
			artifact, digest, _ := localOCI(data)
			i, r := fixture(data, func(r *catalog.Release, s *catalog.Source) { r.Artifact = artifact; r.Digest = digest })
			i.MaxBytes = 8192
			Expect(os.WriteFile(filepath.Join(i.Root, "old"), []byte("old"), 0600)).To(Succeed())
			_, e = i.Stage(context.Background(), r, nil)
			Expect(e).To(MatchError(ErrPackage))
			b, e := os.ReadFile(filepath.Join(i.Root, "old"))
			Expect(e).NotTo(HaveOccurred())
			Expect(string(b)).To(Equal("old"))
		})
	}
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
		// Block unexpected external trust downloads, not signature decisions.
		externalRequests := &atomic.Int64{}
		oldTransport := http.DefaultTransport
		http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Hostname() != "127.0.0.1" {
				externalRequests.Add(1)
				return nil, fmt.Errorf("unexpected external request: %s", req.URL.Host)
			}
			return oldTransport.RoundTrip(req)
		})
		DeferCleanup(func() { http.DefaultTransport = oldTransport })
		// A trusted signature requirement must not be satisfied by a mere digest.
		i, r = fixture(data, edit, func(r *catalog.Release, s *catalog.Source) {
			s.Policy = catalog.VerificationPolicy{RequireSignature: true, Issuer: "https://issuer.invalid", Identity: "trusted"}
		})
		_, e = i.Stage(context.Background(), r, nil)
		Expect(e).To(HaveOccurred())
		Expect(externalRequests.Load()).To(BeZero())
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

var _ = Describe("expanded stream boundaries", func() {
	It("counts padding trailing bytes and layers at the exact expanded boundary", func() {
		var b bytes.Buffer
		w := tar.NewWriter(&b)
		Expect(w.WriteHeader(&tar.Header{Name: "one", Size: 1})).To(Succeed())
		_, e := w.Write([]byte("x"))
		Expect(e).NotTo(HaveOccurred())
		Expect(w.Close()).To(Succeed())
		data := b.Bytes()
		for _, extra := range []int{0, 1} {
			i := &Installer{MaxBytes: int64(len(data))}
			x := &extraction{seen: map[string]bool{}}
			e = i.extract(context.Background(), bytes.NewReader(append(append([]byte{}, data...), make([]byte, extra)...)), GinkgoT().TempDir(), x)
			if extra == 0 {
				Expect(e).To(Succeed())
				Expect(x.bytes).To(Equal(int64(len(data))))
			} else {
				Expect(e).To(MatchError(ErrPackage))
			}
		}
		i := &Installer{MaxBytes: int64(len(data))*2 - 1}
		x := &extraction{seen: map[string]bool{}}
		Expect(i.extract(context.Background(), bytes.NewReader(data), GinkgoT().TempDir(), x)).To(Succeed())
		// An empty second tar has no duplicate paths, but its padding still counts.
		trailing := make([]byte, len(data))
		Expect(i.extract(context.Background(), bytes.NewReader(trailing), GinkgoT().TempDir(), x)).To(MatchError(ErrPackage))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(i.extract(ctx, bytes.NewReader(data), GinkgoT().TempDir(), &extraction{seen: map[string]bool{}})).To(MatchError(context.Canceled))
	})
	It("bounds expanded bytes cumulatively across real OCI layers", func() {
		m, e := os.ReadFile("../../../../tests/addons/fixtures/packages/manifest.json")
		Expect(e).NotTo(HaveOccurred())
		data := archive(nil, m)
		second := make([]byte, 2048)
		artifact, digest, _ := localOCI(data, second)
		i, r := fixture(data, func(r *catalog.Release, s *catalog.Source) { r.Artifact = artifact; r.Digest = digest })
		i.MaxBytes = int64(len(data) + len(second))
		_, e = i.Stage(context.Background(), r, nil)
		Expect(e).To(Succeed())
		i.MaxBytes--
		_, e = i.Stage(context.Background(), r, nil)
		Expect(e).To(MatchError(ErrPackage))
	})
	It("denies nonloopback plaintext and redirect responses at the transport boundary", func() {
		calls := 0
		base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 302, Body: io.NopCloser(strings.NewReader(""))}, nil
		})
		for _, allow := range []bool{false, true} {
			t := boundTransport{ctx: context.Background(), base: base, allowLoopbackHTTP: allow}
			for _, u := range []string{"http://example.invalid/v2/", "http://192.0.2.1/token"} {
				req, e := http.NewRequest("GET", u, nil)
				Expect(e).NotTo(HaveOccurred())
				_, e = t.RoundTrip(req)
				Expect(e).To(MatchError(ErrPackage))
			}
		}
		Expect(calls).To(BeZero())
		t := boundTransport{ctx: context.Background(), base: base, allowLoopbackHTTP: true}
		req, e := http.NewRequest("GET", "http://127.0.0.1/v2/", nil)
		Expect(e).NotTo(HaveOccurred())
		_, e = t.RoundTrip(req)
		Expect(e).To(MatchError(ErrPackage))
		Expect(calls).To(Equal(1))
	})
})

// Go's writer deliberately filters GNU sparse keys. Emit same-length private
// PAX keys, then replace only the PAX payload to construct valid sparse 0.1 TARs.
func sparseArchive(names ...string) []byte {
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	for _, name := range names {
		Expect(w.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: 1, Format: tar.FormatPAX, PAXRecords: map[string]string{
			"XXX.sparse.map": "65535,1", "XXX.sparse.size": "65536", "XXX.sparse.numblocks": "1", "XXX.sparse.name": name,
		}})).To(Succeed())
		_, err := w.Write([]byte("x"))
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(w.Close()).To(Succeed())
	return bytes.ReplaceAll(b.Bytes(), []byte("XXX.sparse."), []byte("GNU.sparse."))
}

var _ = Describe("sparse output boundaries", func() {
	It("bounds sparse logical output across files and layers preserving old packages", func() {
		m, err := os.ReadFile("../../../../tests/addons/fixtures/packages/manifest.json")
		Expect(err).NotTo(HaveOccurred())
		base := archive(nil, m)
		for _, layers := range [][][]byte{{sparseArchive("one", "two", "three")}, {sparseArchive("one"), sparseArchive("two"), sparseArchive("three")}} {
			// Each valid regular entry has a logical size of 64 KiB, while its
			// stored data is one byte. Independently prove the fixture is valid.
			for _, data := range layers {
				tr := tar.NewReader(bytes.NewReader(data))
				for {
					h, e := tr.Next()
					if e == io.EOF {
						break
					}
					Expect(e).NotTo(HaveOccurred())
					Expect(h.Typeflag).To(Equal(byte(tar.TypeReg)))
					n, e := io.Copy(io.Discard, tr)
					Expect(e).NotTo(HaveOccurred())
					Expect(n).To(Equal(int64(65536)))
				}
				Expect(len(data)).To(BeNumerically("<", 16384))
			}
			artifact, digest, _ := localOCI(append([][]byte{base}, layers...)...)
			i, r := fixture(base, func(r *catalog.Release, s *catalog.Source) { r.Artifact = artifact; r.Digest = digest })
			i.MaxBytes = 128 << 10
			Expect(os.WriteFile(filepath.Join(i.Root, "old"), []byte("old"), 0600)).To(Succeed())
			_, err = i.Stage(context.Background(), r, nil)
			Expect(err).To(MatchError(ErrPackage))
			old, e := os.ReadFile(filepath.Join(i.Root, "old"))
			Expect(e).NotTo(HaveOccurred())
			Expect(string(old)).To(Equal("old"))
			entries, e := os.ReadDir(i.Root)
			Expect(e).NotTo(HaveOccurred())
			Expect(entries).To(HaveLen(1))
		}
	})
	It("accepts exact sparse output budget and cancels synthesized writes", func() {
		data := sparseArchive("one", "two")
		for _, limit := range []int64{131072, 131071} {
			i := &Installer{MaxBytes: limit}
			x := &extraction{seen: map[string]bool{}}
			err := i.extract(context.Background(), bytes.NewReader(data), GinkgoT().TempDir(), x)
			if limit == 131072 {
				Expect(err).To(Succeed())
			} else {
				Expect(err).To(MatchError(ErrPackage))
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		i := &Installer{MaxBytes: 131072}
		i.writeFile = func(p string, m os.FileMode) (fileWriter, error) {
			f, e := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, m)
			return cancelFile{f, cancel}, e
		}
		Expect(i.extract(ctx, bytes.NewReader(data), GinkgoT().TempDir(), &extraction{seen: map[string]bool{}})).To(MatchError(context.Canceled))
	})
})

// Do not embed os.File: its promoted ReadFrom bypasses the cancelling Write.
type cancelFile struct {
	file   *os.File
	cancel context.CancelFunc
}

func (f cancelFile) Write(b []byte) (int, error) { n, e := f.file.Write(b); f.cancel(); return n, e }

func (f cancelFile) Sync() error  { return f.file.Sync() }
func (f cancelFile) Close() error { return f.file.Close() }

var _ = Describe("trusted signed staging", func() {
	It("stages real trusted signatures and rejects signed identity digest and trust mismatches offline", func() {
		original := http.DefaultTransport
		var external atomic.Int64
		http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Hostname() != "127.0.0.1" {
				external.Add(1)
				return nil, fmt.Errorf("external network forbidden")
			}
			return original.RoundTrip(r)
		})
		DeferCleanup(func() { http.DefaultTransport = original })
		authority, e := signing.New()
		Expect(e).NotTo(HaveOccurred())
		m, e := os.ReadFile("../../../../tests/addons/fixtures/packages/manifest.json")
		Expect(e).NotTo(HaveOccurred())
		data := archive(nil, m)
		for _, scenario := range []string{"valid", "identity", "digest", "trust"} {
			artifact, digest, _ := localOCI(data)
			ref, e := name.ParseReference(strings.TrimPrefix(artifact, "oci://"))
			Expect(e).NotTo(HaveOccurred())
			identity := "builder@example.invalid"
			signedDigest := digest[7:]
			if scenario == "identity" {
				identity = "attacker@example.invalid"
			}
			if scenario == "digest" {
				signedDigest = strings.Repeat("a", 64)
			}
			blob, e := authority.Bundle(signedDigest, identity, "https://issuer.invalid")
			Expect(e).NotTo(HaveOccurred())
			layer := static.NewLayer(blob, types.MediaType("application/vnd.dev.sigstore.bundle.v0.3+json"))
			img, e := mutate.AppendLayers(empty.Image, layer)
			Expect(e).NotTo(HaveOccurred())
			sigref := ref.Context().Tag("signature")
			Expect(remote.Write(sigref, img)).To(Succeed())
			hash, e := img.Digest()
			Expect(e).NotTo(HaveOccurred())
			size, e := img.Size()
			Expect(e).NotTo(HaveOccurred())
			index := mutate.AppendManifests(empty.Index, mutate.IndexAddendum{Add: img, Descriptor: v1.Descriptor{Digest: hash, Size: size, MediaType: types.DockerManifestSchema2, ArtifactType: "application/vnd.dev.sigstore.bundle.v0.3+json"}})
			Expect(remote.WriteIndex(ref.Context().Tag(strings.Replace(digest, ":", "-", 1)), index)).To(Succeed())
			i, r := fixture(data, func(r *catalog.Release, s *catalog.Source) {
				r.Artifact = artifact
				r.Digest = digest
				s.Policy = catalog.VerificationPolicy{RequireSignature: true, Issuer: "https://issuer.invalid", Identity: "builder@example.invalid"}
			})
			i.SignatureTrust = authority
			if scenario == "trust" {
				i.SignatureTrust, e = signing.New()
				Expect(e).NotTo(HaveOccurred())
			}
			Expect(os.WriteFile(filepath.Join(i.Root, "old"), []byte("old"), 0600)).To(Succeed())
			staged, e := i.Stage(context.Background(), r, nil)
			if scenario == "valid" {
				Expect(e).To(Succeed())
				receipt, e := i.Commit(context.Background(), staged)
				Expect(e).NotTo(HaveOccurred())
				Expect(receipt.Verification).To(Equal("sha256+sigstore"))
				Expect(receipt.Digest).To(Equal(digest))
			} else {
				Expect(e).To(MatchError(ErrPackage))
				entries, e := os.ReadDir(i.Root)
				Expect(e).NotTo(HaveOccurred())
				Expect(entries).To(HaveLen(1))
			}
			old, e := os.ReadFile(filepath.Join(i.Root, "old"))
			Expect(e).NotTo(HaveOccurred())
			Expect(string(old)).To(Equal("old"))
		}
		Expect(external.Load()).To(BeZero())
	})
})
