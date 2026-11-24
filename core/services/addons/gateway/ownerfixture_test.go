// SPDX-License-Identifier: MIT
package gateway

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const uiManifest = `{"schemaVersion":1,"id":"sample","version":"1.2.3","executable":"bin/sample","os":"linux","arch":"amd64","host":{"min":1,"max":2},"api":{"min": 2, "max": 2},"protocol":{"min":1,"max":1},"dataFormat":{"min":1,"max":2},"capabilities":{"service":true,"ui":{"entry":"ui.js","css":["ui.css"],"sdk":{"min":1,"max":1}}}}`

// ownerManager builds a real owner journal. With a UI package it also installs
// the addon "sample" through the real catalog and installer, operation "install".
func ownerManager(actor contract.Actor, install bool) *addons.Manager {
	return ownerManagerPackage(actor, install, uiManifest, nil, 1)
}

func ownerManagerPackage(actor contract.Actor, install bool, manifest string, files map[string]string, sdk uint64, grants ...map[string][]string) *addons.Manager {
	root := GinkgoT().TempDir()
	ctx := context.Background()
	admin := func(_ context.Context, a contract.Actor) error {
		if a != actor {
			return errors.New("denied")
		}
		return nil
	}
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	contents := map[string]string{"manifest.json": manifest, "bin/sample": "binary", "ui.js": "export default {};", "ui.css": "body{}", "secret.txt": "private"}
	for name, data := range files {
		contents[name] = data
	}
	// Stable entry order keeps identical package contents at the same digest.
	names := make([]string, 0, len(contents))
	for name := range contents {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		data := contents[name]
		Expect(tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(data))})).To(Succeed())
		_, e := tw.Write([]byte(data))
		Expect(e).NotTo(HaveOccurred())
	}
	Expect(tw.Close()).To(Succeed())
	artifact := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(b.Bytes()) }))
	DeferCleanup(artifact.Close)
	release := catalog.Release{ID: "sample", Name: "Sample", Version: "1.2.3", Artifact: artifact.URL + "/package.tar", Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(b.Bytes())), OS: "linux", Arch: "amd64"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = catalog.WriteIndex(w, []catalog.Release{release})
	}))
	DeferCleanup(server.Close)
	sources, e := catalog.NewSources(filepath.Join(root, "sources.json"), admin, catalog.CredentialResolver{})
	Expect(e).NotTo(HaveOccurred())
	Expect(sources.Save(ctx, actor, catalog.Source{ID: "test", Name: "Test", URL: server.URL, Enabled: true, AllowLoopbackHTTP: true})).To(Succeed())
	_, e = sources.Refresh(ctx, "test")
	Expect(e).NotTo(HaveOccurred())
	c := catalog.Catalog{Sources: sources, TargetOS: "linux", TargetArch: "amd64"}
	cfg := addons.OwnerConfig{InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://owner.example", StorageID: "disk", Root: filepath.Join(root, "owner")}
	i := &packages.Installer{Root: filepath.Join(cfg.Root, "packages"), Catalog: c, Host: contract.HostCompatibility{Host: 1, API: contract.HostAPIVersion, Protocol: 1, SDK: sdk, DataFormat: 1, OS: "linux", Arch: "amd64"}}
	DeferCleanup(func() {
		_ = filepath.Walk(root, func(p string, info os.FileInfo, e error) error {
			if e == nil && info.IsDir() {
				_ = os.Chmod(p, 0700)
			}
			return nil
		})
	})
	m, e := addons.NewManager(ctx, cfg, c, i, admin)
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(m.Close()).To(Succeed()) })
	if len(grants) != 0 {
		m.SetHostOps(grants[0])
	}
	if install {
		_, e = m.Submit(ctx, actor, addons.LifecycleCommand{AddonID: "sample", OperationID: "install", Action: "install", SourceID: "test", Version: "1.2.3"})
		Expect(e).NotTo(HaveOccurred())
	}
	return m
}
