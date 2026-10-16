// SPDX-License-Identifier: MIT
package application

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sync/atomic"
	"time"
)

// requireAddonRuntime skips a spec that starts an owner. The add-on runtime
// exists on Linux only; other platforms fail closed (see addons_other_test.go).
func requireAddonRuntime() {
	if goruntime.GOOS != "linux" {
		Skip("the add-on runtime exists on Linux only")
	}
}

// realTempDir returns a new temporary directory whose path has no symbolic
// link. The owner pins every component of its root and refuses a link, and a
// temporary directory on macOS lies behind one.
func realTempDir() string {
	dir, err := filepath.EvalSymlinks(GinkgoT().TempDir())
	Expect(err).NotTo(HaveOccurred())
	return dir
}

var _ = Describe("addon bootstrap", func() {
	setup := func() *Application {
		return &Application{applicationConfig: &config.ApplicationConfig{Context: context.Background()}}
	}
	cfg := func() config.AddonConfig {
		return config.AddonConfig{Enabled: true, InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://owner.example", StorageID: "local", Root: filepath.Join(realTempDir(), "addons"), AdminCheck: func(context.Context, contract.Actor) error { return nil }}
	}
	It("installs from a persisted source through application bootstrap", func() {
		requireAddonRuntime()
		c := cfg()
		DeferCleanup(func() {
			_ = filepath.Walk(c.Root, func(p string, i os.FileInfo, e error) error {
				if e == nil && i.IsDir() {
					_ = os.Chmod(p, 0700)
				}
				return nil
			})
		})
		c.Host = contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: "linux", Arch: "amd64"}
		manifest, e := os.ReadFile("../../tests/addons/fixtures/packages/manifest.json")
		Expect(e).NotTo(HaveOccurred())
		var b bytes.Buffer
		tw := tar.NewWriter(&b)
		for name, data := range map[string][]byte{"manifest.json": manifest, "bin/sample": []byte("binary")} {
			Expect(tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(data))})).To(Succeed())
			_, e = tw.Write(data)
			Expect(e).NotTo(HaveOccurred())
		}
		Expect(tw.Close()).To(Succeed())
		var url string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/package.tar" {
				_, _ = w.Write(b.Bytes())
				return
			}
			_ = catalog.WriteIndex(w, []catalog.Release{{AddonID: "sample", Name: "Sample", Version: "1.2.3", OS: "linux", Arch: "amd64", Artifact: url + "/package.tar", Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(b.Bytes()))}})
		}))
		defer server.Close()
		url = server.URL
		Expect(os.MkdirAll(c.Root, 0700)).To(Succeed())
		sources, e := catalog.NewSources(filepath.Join(c.Root, "sources.json"), c.AdminCheck, catalog.CredentialResolver{})
		Expect(e).NotTo(HaveOccurred())
		Expect(sources.Save(context.Background(), contract.Actor{}, catalog.Source{ID: "test", Name: "Test", URL: url, Enabled: true, AllowLoopbackHTTP: true})).To(Succeed())
		a := setup()
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).To(Succeed())
		DeferCleanup(a.Shutdown)
		_, e = a.AddonManager().Submit(context.Background(), contract.Actor{}, addons.LifecycleCommand{OperationID: "install", AddonID: "sample", Action: "install", SourceID: "test", Version: "1.2.3"})
		Expect(e).NotTo(HaveOccurred())
	})

	It("bounds offline refresh and ignores disabled sources and forwarders", func() {
		requireAddonRuntime()
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); <-r.Context().Done() }))
		defer server.Close()
		c := cfg()
		Expect(os.MkdirAll(c.Root, 0700)).To(Succeed())
		sources, e := catalog.NewSources(filepath.Join(c.Root, "sources.json"), c.AdminCheck, catalog.CredentialResolver{})
		Expect(e).NotTo(HaveOccurred())
		src := catalog.Source{ID: "offline", Name: "Offline", URL: server.URL, Enabled: false}
		Expect(sources.Save(context.Background(), contract.Actor{}, src)).To(Succeed())
		a := setup()
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).To(Succeed())
		Expect(calls.Load()).To(BeZero())
		Expect(a.AddonSources()).NotTo(BeNil())
		_, e = a.AddonSources().Refresh(context.Background(), src.ID)
		Expect(e).To(MatchError(catalog.ErrUnavailable))
		Expect(a.Shutdown()).To(Succeed())
		src.Enabled = true
		Expect(sources.Save(context.Background(), contract.Actor{}, src)).To(Succeed())
		a = setup()
		a.applicationConfig.Addons = c
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		a.applicationConfig.Context = ctx
		Expect(a.startAddons()).NotTo(Succeed())
		Expect(a.AddonManager()).To(BeNil())
		Expect(a.AddonSources()).To(BeNil())
		Expect(calls.Load()).To(Equal(int32(1)))
		c.InstanceID = "frontend"
		a = setup()
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).To(Succeed())
		Expect(a.AddonSources()).To(BeNil())
		Expect(calls.Load()).To(Equal(int32(1)))
		Expect(a.Shutdown()).To(Succeed())
		src.Enabled = false
		Expect(sources.Save(context.Background(), contract.Actor{}, src)).To(Succeed())
		c.InstanceID = c.OwnerID
		a = setup()
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).To(Succeed())
		Expect(a.Shutdown()).To(Succeed())
	})
	It("rejects package-root links during bootstrap without external mutation", func() {
		c := cfg()
		Expect(os.MkdirAll(c.Root, 0700)).To(Succeed())
		external := GinkgoT().TempDir()
		stage := filepath.Join(external, "stage-keep")
		Expect(os.Mkdir(stage, 0700)).To(Succeed())
		DeferCleanup(func() { _ = os.Chmod(stage, 0700) })
		sentinel := filepath.Join(stage, "keep")
		Expect(os.WriteFile(sentinel, []byte("keep"), 0400)).To(Succeed())
		Expect(os.Chmod(stage, 0500)).To(Succeed())
		Expect(os.Symlink(external, filepath.Join(c.Root, "packages"))).To(Succeed())
		a := setup()
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).NotTo(Succeed())
		Expect(a.AddonManager()).To(BeNil())
		b, e := os.ReadFile(sentinel)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(b)).To(Equal("keep"))
		info, e := os.Stat(stage)
		Expect(e).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0500)))
	})

	It("leaves addons disabled by default without choosing an owner", func() { a := setup(); Expect(a.startAddons()).To(Succeed()); Expect(a.AddonManager()).To(BeNil()) })
	It("keeps forwarding bootstrap away from storage and rejects absent owners", func() {
		a := setup()
		c := cfg()
		c.InstanceID = "frontend"
		c.AdminCheck = nil
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).To(Succeed())
		DeferCleanup(a.Shutdown)
		Expect(c.Root).NotTo(BeADirectory())
		Expect(a.AddonManager().Reconcile(context.Background())).To(MatchError(addons.ErrNotOwner))
		c.OwnerID = ""
		a = setup()
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).NotTo(Succeed())
	})
	It("locks explicit owner storage and releases it on application shutdown", func() {
		requireAddonRuntime()
		c := cfg()
		a := setup()
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).To(Succeed())
		b := setup()
		b.applicationConfig.Addons = c
		Expect(b.startAddons()).NotTo(Succeed())
		Expect(a.Shutdown()).To(Succeed())
		Expect(b.startAddons()).To(Succeed())
		Expect(b.Shutdown()).To(Succeed())
		c.StorageID = "conflict"
		a = setup()
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).NotTo(Succeed())
	})
	It("reconciles interrupted intent before exposing the manager and cleans failed bootstrap", func() {
		requireAddonRuntime()
		c := cfg()
		a := setup()
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).To(Succeed())
		Expect(a.Shutdown()).To(Succeed())
		p := filepath.Join(c.Root, "journal.json")
		Expect(os.WriteFile(p, []byte(`{"Version":1,"Addons":{},"Operations":{"op":{"Command":{"OperationID":"op","AddonID":"sample","Action":"install"},"Status":{"OperationID":"op","AddonID":"sample","Phase":"intent"}}}}`), 0600)).To(Succeed())
		a = setup()
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).To(Succeed())
		Expect(a.Shutdown()).To(Succeed())
		b, e := os.ReadFile(p)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(b)).To(ContainSubstring(`"Phase":"failed"`))
		Expect(os.WriteFile(p, []byte(`{`), 0600)).To(Succeed())
		a = setup()
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).NotTo(Succeed())
		Expect(a.AddonManager()).To(BeNil())
		Expect(os.Remove(p)).To(Succeed())
		Expect(a.startAddons()).To(Succeed())
		Expect(a.Shutdown()).To(Succeed())
	})
	It("rejects shared storage and missing authoritative administration", func() {
		for _, shared := range []bool{true, false} {
			a := setup()
			c := cfg()
			c.SharedStorage = shared
			if !shared {
				c.AdminCheck = nil
			}
			a.applicationConfig.Addons = c
			Expect(a.startAddons()).NotTo(Succeed())
		}
	})
})

var _ = Describe("addon gateway bootstrap", func() {
	It("keeps public gateway unavailable without explicit transport bootstrap", func() {
		a := &Application{applicationConfig: &config.ApplicationConfig{Context: context.Background()}}
		Expect(a.AddonGateway()).To(BeNil())
	})
})
