// SPDX-License-Identifier: MIT
package application

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("add-on automatic upgrade setting", func() {
	// start boots a real owner against a library with one add-on at 1.2.3,
	// installs it, then lists 1.2.4. It returns the application and a function
	// that reports the installed version.
	start := func(autoUpgrade bool) (*Application, func() string) {
		requireAddonRuntime()
		c := config.AddonConfig{Enabled: true, InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://owner.example", StorageID: "local",
			Root: filepath.Join(realTempDir(), "addons"), AdminCheck: func(context.Context, contract.Actor) error { return nil },
			Host: contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: "linux", Arch: "amd64"}}
		DeferCleanup(func() {
			_ = filepath.Walk(c.Root, func(p string, i os.FileInfo, e error) error {
				if e == nil && i.IsDir() {
					_ = os.Chmod(p, 0700)
				}
				return nil
			})
		})
		manifest, e := os.ReadFile("../../tests/addons/fixtures/packages/manifest.json")
		Expect(e).NotTo(HaveOccurred())
		var mu sync.Mutex
		archives := map[string][]byte{}
		var releases []catalog.Release
		var url string
		publish := func(version string) {
			var m map[string]any
			Expect(json.Unmarshal(manifest, &m)).To(Succeed())
			m["version"] = version
			data, _ := json.Marshal(m)
			var b bytes.Buffer
			tw := tar.NewWriter(&b)
			for name, body := range map[string][]byte{"manifest.json": data, "bin/sample": []byte("binary " + version)} {
				Expect(tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(body))})).To(Succeed())
				_, e := tw.Write(body)
				Expect(e).NotTo(HaveOccurred())
			}
			Expect(tw.Close()).To(Succeed())
			mu.Lock()
			defer mu.Unlock()
			archives["/"+version+".tar"] = b.Bytes()
			releases = append(releases, catalog.Release{AddonID: "sample", Name: "Sample", Version: version, OS: "linux", Arch: "amd64",
				Artifact: url + "/" + version + ".tar", Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(b.Bytes()))})
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			if data, ok := archives[r.URL.Path]; ok {
				_, _ = w.Write(data)
				return
			}
			_ = catalog.WriteIndex(w, releases)
		}))
		DeferCleanup(server.Close)
		url = server.URL
		publish("1.2.3")
		Expect(os.MkdirAll(c.Root, 0700)).To(Succeed())
		sources, e := catalog.NewSources(filepath.Join(c.Root, "sources.json"), c.AdminCheck, catalog.CredentialResolver{})
		Expect(e).NotTo(HaveOccurred())
		Expect(sources.Save(context.Background(), contract.Actor{}, catalog.Source{ID: "test", Name: "Test", URL: url, Enabled: true, AllowLoopbackHTTP: true})).To(Succeed())
		a := &Application{applicationConfig: &config.ApplicationConfig{Context: context.Background(), AutoUpgradeAddons: autoUpgrade}}
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).To(Succeed())
		DeferCleanup(a.Shutdown)
		_, e = a.AddonManager().Submit(context.Background(), contract.Actor{}, addons.LifecycleCommand{OperationID: "install", AddonID: "sample", Action: "install", SourceID: "test", Version: "1.2.3"})
		Expect(e).NotTo(HaveOccurred())
		publish("1.2.4")
		version := func() string {
			s, e := a.AddonManager().Status(context.Background(), "sample")
			Expect(e).NotTo(HaveOccurred())
			return s.Generations[len(s.Generations)-1].Receipt.Manifest.Version
		}
		return a, version
	}

	It("is off by default", func() {
		Expect((&config.ApplicationConfig{}).AutoUpgradeAddons).To(BeFalse())
	})

	It("leaves an installed add-on alone when the setting is off", func() {
		a, version := start(false)
		a.autoUpgradeAddons(context.Background())
		Expect(version()).To(Equal("1.2.3"))
	})

	It("upgrades an installed add-on through the manager when the setting is on", func() {
		a, version := start(true)
		a.autoUpgradeAddons(context.Background())
		Expect(version()).To(Equal("1.2.4"))
	})

	It("follows the setting as it changes at run time", func() {
		a, version := start(false)
		a.autoUpgradeAddons(context.Background())
		Expect(version()).To(Equal("1.2.3"))
		a.applicationConfig.AutoUpgradeAddons = true
		a.autoUpgradeAddons(context.Background())
		Expect(version()).To(Equal("1.2.4"))
	})
})
