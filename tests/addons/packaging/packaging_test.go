// SPDX-License-Identifier: MIT

// Package packaging_test installs the package that scripts/addons/package.sh
// builds, through the real installer and a real local OCI distribution, and
// starts the packaged Tables executable under the real supervisor. The
// registry is an in-process distribution on a loopback port; nothing is
// pushed anywhere else and nothing is signed with a real identity.
package packaging_test

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/packaging"
	"github.com/mudler/LocalAI/pkg/credentials"
	"github.com/mudler/LocalAI/tests/addons/fixtures/signing"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

func TestPackagingContract(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Add-on package installer contract")
}

const (
	repoRoot = "../../.."
	revision = "0123456789abcdef0123456789abcdef01234567"
	bundle   = "application/vnd.dev.sigstore.bundle.v0.3+json"
	issuer   = "https://token.actions.githubusercontent.com"
)

var (
	binary   string
	hostPlat = goruntime.GOOS + "/" + goruntime.GOARCH
)

var _ = BeforeSuite(func() {
	binary = filepath.Join(GinkgoT().TempDir(), "tables")
	cmd := exec.Command("go", "build", "-trimpath", "-o", binary, filepath.Join(repoRoot, "addons/tables/cmd/tables"))
	cmd.Env = append(os.Environ(), "CGO_ENABLED=1")
	out, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(out))
	_, err = os.Stat(filepath.Join(repoRoot, "addons/tables/ui/dist/ui/tables.js"))
	Expect(err).NotTo(HaveOccurred(), "build the UI first: node addons/tables/ui/build.mjs")
})

// matrix writes a platform matrix that enables the platform of this host.
func matrix() string {
	GinkgoHelper()
	p := filepath.Join(GinkgoT().TempDir(), "platforms.yaml")
	body := "schema: 1\naddon: tables\nexecutable: tables\nmanifest: addons/tables/manifest.json\nui: addons/tables/ui/dist\nplatforms:\n" +
		"  - platform: " + hostPlat + "\n    enabled: true\n    runner: native\n    native_check: executable-start\n"
	Expect(os.WriteFile(p, []byte(body), 0o644)).To(Succeed())
	return p
}

// packageSh runs the real entrypoint script.
func packageSh(args ...string) (string, error) {
	cmd := exec.Command("sh", append([]string{filepath.Join(repoRoot, "scripts/addons/package.sh")}, args...)...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

type published struct {
	repository string
	digest     string // platform image digest the catalog pins
	index      string
	entry      map[string]any
	registry   string
	authority  *signing.Authority
}

// publishPackage builds, merges, publishes (to the local distribution) and
// renders the catalog entry of the package for the host platform.
func publishPackage(version string, signAs string) published {
	GinkgoHelper()
	cfg := matrix()
	work := GinkgoT().TempDir()
	layout := filepath.Join(work, "leg")
	out, err := packageSh("--config", cfg, "--layout", layout, "--version", version, "--platform", hostPlat, "--executable", binary, "--revision", revision)
	Expect(err).NotTo(HaveOccurred(), out)
	merged := filepath.Join(work, "merged")
	Expect(packaging.Run([]string{"merge", "--config", cfg, "--out", merged, "--version", version, "--revision", revision, layout}, io.Discard)).To(Succeed())

	handler := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	server := httptest.NewServer(handler)
	DeferCleanup(server.Close)
	host := strings.TrimPrefix(server.URL, "http://")
	repository := host + "/addons/tables"
	var pub strings.Builder
	Expect(packaging.Run([]string{"publish", "--config", cfg, "--layout", merged, "--repository", repository, "--tag", version}, &pub)).To(Succeed())

	catalogFile := filepath.Join(work, "catalog.yaml")
	Expect(packaging.Run([]string{"catalog", "--config", cfg, "--template", filepath.Join(repoRoot, "gallery/addons/tables.yaml.tmpl"),
		"--merged", merged, "--repository", repository, "--tag", version, "--channel", "latest",
		"--owner-repo", "example/localai", "--signer-ref", "refs/tags/v" + version, "--out", catalogFile}, io.Discard)).To(Succeed())
	b, err := os.ReadFile(catalogFile)
	Expect(err).NotTo(HaveOccurred())
	// The host's own reader is the judge of the generated index.
	hostEntries, err := catalog.DecodeIndex(b)
	Expect(err).NotTo(HaveOccurred())
	Expect(hostEntries).To(HaveLen(1))
	var entries []map[string]any
	Expect(yaml.Unmarshal(b, &entries)).To(Succeed())
	Expect(entries).To(HaveLen(1))

	p := published{repository: repository, entry: entries[0], registry: host, authority: nil}
	p.digest = entries[0]["digest"].(string)
	for _, l := range strings.Split(pub.String(), "\n") {
		if v, ok := strings.CutPrefix(l, "digest="); ok {
			p.index = v
		}
	}
	// Sign the platform image the catalog pins, as the workflow's signing
	// step does (the identity is what the workflow certificate carries).
	authority, err := signing.New()
	Expect(err).NotTo(HaveOccurred())
	p.authority = authority
	identity := entries[0]["policy"].(map[string]any)["identity"].(string)
	if signAs != "" {
		identity = signAs
	}
	ref, err := name.ParseReference(repository + "@" + p.digest)
	Expect(err).NotTo(HaveOccurred())
	blob, err := authority.Bundle(strings.TrimPrefix(p.digest, "sha256:"), identity, issuer)
	Expect(err).NotTo(HaveOccurred())
	img, err := mutate.AppendLayers(empty.Image, static.NewLayer(blob, types.MediaType(bundle)))
	Expect(err).NotTo(HaveOccurred())
	h, err := img.Digest()
	Expect(err).NotTo(HaveOccurred())
	size, err := img.Size()
	Expect(err).NotTo(HaveOccurred())
	index := mutate.AppendManifests(empty.Index, mutate.IndexAddendum{Add: img, Descriptor: v1.Descriptor{Digest: h, Size: size, MediaType: types.DockerManifestSchema2, ArtifactType: bundle}})
	Expect(remote.WriteIndex(ref.Context().Tag(strings.Replace(p.digest, ":", "-", 1)), index)).To(Succeed())
	Expect(remote.Write(ref.Context().Tag("signature"), img)).To(Succeed())
	return p
}

// installer is an owner: a catalog source that serves the rendered entry
// with the signer policy the owner configures, and a private package root.
func installer(p published, policy catalog.VerificationPolicy) (*packages.Installer, catalog.Release) {
	GinkgoHelper()
	entry := p.entry
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer catalog-only" {
			w.WriteHeader(403)
			return
		}
		doc, err := yaml.Marshal([]map[string]any{entry})
		Expect(err).NotTo(HaveOccurred())
		_, _ = w.Write(doc)
	}))
	DeferCleanup(server.Close)
	store, err := credentials.Parse([]byte("- match: "+server.URL+"\n  allow_insecure: true\n  bearer: catalog-only"), nil)
	Expect(err).NotTo(HaveOccurred())
	sources, err := catalog.NewSources(filepath.Join(GinkgoT().TempDir(), "sources"), func(context.Context, contract.Actor) error { return nil },
		catalog.CredentialResolver{References: map[string]catalog.CredentialBinding{"catalog": {Origin: server.URL, Store: store}}})
	Expect(err).NotTo(HaveOccurred())
	src := catalog.Source{ID: "first-party", Name: "First party", Enabled: true, AllowLoopbackHTTP: true, URL: server.URL, AuthReference: "catalog", Policy: policy}
	Expect(sources.Save(context.Background(), contract.Actor{}, src)).To(Succeed())
	_, err = sources.Refresh(context.Background(), "first-party")
	Expect(err).NotTo(HaveOccurred())
	c := catalog.Catalog{Sources: sources, TargetOS: goruntime.GOOS, TargetArch: goruntime.GOARCH}
	release, err := c.Resolve(context.Background(), "first-party", "tables", "latest")
	Expect(err).NotTo(HaveOccurred())
	root := GinkgoT().TempDir()
	DeferCleanup(func() {
		_ = filepath.Walk(root, func(p string, info os.FileInfo, e error) error {
			if e == nil && info.IsDir() {
				_ = os.Chmod(p, 0o700)
			}
			return nil
		})
	})
	return &packages.Installer{
		Root: root, Catalog: c,
		Host: contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: goruntime.GOOS, Arch: goruntime.GOARCH},
	}, release
}

var _ = Describe("first-party package", func() {
	It("installs the package built by package.sh and starts its executable", func() {
		p := publishPackage("1.2.3", "")
		identity := p.entry["policy"].(map[string]any)["identity"].(string)
		Expect(identity).To(Equal("https://github.com/example/localai/.github/workflows/addons-package.yml@refs/tags/v1.2.3"))
		i, release := installer(p, catalog.VerificationPolicy{RequireSignature: true, Issuer: issuer, Identity: identity})
		i.SignatureTrust = p.authority
		Expect(release.Digest).To(Equal(p.digest))
		staged, err := i.Stage(context.Background(), release, nil)
		Expect(err).NotTo(HaveOccurred())
		receipt, err := i.Commit(context.Background(), staged)
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.Verification).To(Equal("sha256+sigstore"))
		Expect(receipt.Digest).To(Equal(p.digest))
		Expect(receipt.Manifest.ID).To(Equal("tables"))
		Expect(receipt.Manifest.Version).To(Equal("1.2.3"))
		Expect(receipt.Manifest.OS).To(Equal(goruntime.GOOS))
		Expect(receipt.Manifest.Arch).To(Equal(goruntime.GOARCH))
		Expect(receipt.Icon).NotTo(BeNil())
		info, err := os.Stat(filepath.Join(receipt.Directory, "tables"))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm() & 0o100).NotTo(BeZero())
		Expect(filepath.Join(receipt.Directory, "ui", "tables.js")).To(BeAnExistingFile())
		Expect(filepath.Join(receipt.Directory, "ui", "tables.css")).To(BeAnExistingFile())

		limits := contract.DefaultLimits()
		sup, err := addonruntime.NewSupervisor(addonruntime.Config{StateRoot: filepath.Join(GinkgoT().TempDir(), "runtime"), DataRoot: filepath.Join(GinkgoT().TempDir(), "data"), Limits: limits, ReadyTimeout: 20 * time.Second, StableHealth: limits.BackoffMax})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(sup.Close(context.Background())).To(Succeed()) })
		inst, err := sup.Start(context.Background(), receipt, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(inst.Generation).To(Equal(uint64(1)))
	})

	It("is refused when the signature carries another workflow identity", func() {
		p := publishPackage("1.2.4", "https://github.com/example/localai/.github/workflows/backend_merge.yml@refs/heads/master")
		identity := p.entry["policy"].(map[string]any)["identity"].(string)
		i, release := installer(p, catalog.VerificationPolicy{RequireSignature: true, Issuer: issuer, Identity: identity})
		i.SignatureTrust = p.authority
		_, err := i.Stage(context.Background(), release, nil)
		Expect(err).To(MatchError(packages.ErrPackage))
		entries, err := os.ReadDir(i.Root)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())
	})

	It("is refused when the owner policy names the backend workflow", func() {
		p := publishPackage("1.2.5", "")
		i, release := installer(p, catalog.VerificationPolicy{RequireSignature: true, Issuer: issuer, Identity: "https://github.com/example/localai/.github/workflows/backend_merge.yml@refs/heads/master"})
		i.SignatureTrust = p.authority
		_, err := i.Stage(context.Background(), release, nil)
		Expect(err).To(MatchError(packages.ErrPackage))
	})

	It("is refused when the pinned digest is not the published image", func() {
		p := publishPackage("1.2.6", "")
		identity := p.entry["policy"].(map[string]any)["identity"].(string)
		i, release := installer(p, catalog.VerificationPolicy{RequireSignature: true, Issuer: issuer, Identity: identity})
		i.SignatureTrust = p.authority
		release.Digest = "sha256:" + strings.Repeat("0", 64)
		_, err := i.Stage(context.Background(), release, nil)
		Expect(err).To(HaveOccurred())
	})

	It("gives identical digests for two builds of the same inputs", func() {
		cfg := matrix()
		var digests []string
		for n := 0; n < 2; n++ {
			layout := filepath.Join(GinkgoT().TempDir(), "leg")
			out, err := packageSh("--config", cfg, "--layout", layout, "--version", "2.0.0", "--platform", hostPlat, "--executable", binary, "--revision", revision)
			Expect(err).NotTo(HaveOccurred(), out)
			for _, l := range strings.Split(out, "\n") {
				if v, ok := strings.CutPrefix(l, "digest="); ok {
					digests = append(digests, v)
				}
			}
		}
		Expect(digests).To(HaveLen(2))
		Expect(digests[0]).To(Equal(digests[1]))
	})

	It("refuses a platform the matrix does not enable and a missing executable of another platform", func() {
		layout := filepath.Join(GinkgoT().TempDir(), "leg")
		out, err := packageSh("--layout", layout, "--version", "1.0.0", "--platform", hostPlat, "--executable", binary)
		Expect(err).To(HaveOccurred(), out)
		Expect(out).To(ContainSubstring("not enabled"))
		Expect(layout).NotTo(BeADirectory())
		other := "linux/arm64"
		if hostPlat == other {
			other = "linux/amd64"
		}
		out, err = packageSh("--config", matrix(), "--layout", filepath.Join(GinkgoT().TempDir(), "x"), "--version", "1.0.0", "--platform", other)
		Expect(err).To(HaveOccurred(), out)
		Expect(out).To(ContainSubstring("not this host"))
	})
})
