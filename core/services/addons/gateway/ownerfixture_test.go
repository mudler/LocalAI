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

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const uiManifest = `{"schemaVersion":1,"id":"sample","version":"1.2.3","executable":"bin/sample","os":"linux","arch":"amd64","host":{"min":1,"max":2},"api":{"min":1,"max":1},"protocol":{"min":1,"max":1},"dataFormat":{"min":1,"max":2},"capabilities":{"service":true,"ui":{"entry":"ui.js","css":["ui.css"],"sdk":{"min":1,"max":1}}}}`

// ownerManager builds a real owner journal. With a UI package it also installs
// the addon "sample" through the real catalog and installer, operation "install".
func ownerManager(actor contract.Actor, install bool) *addons.Manager {
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
	for _, f := range []struct{ name, data string }{{"manifest.json", uiManifest}, {"bin/sample", "binary"}, {"ui.js", "export default {};"}, {"ui.css", "body{}"}, {"secret.txt", "private"}} {
		Expect(tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0755, Size: int64(len(f.data))})).To(Succeed())
		_, e := tw.Write([]byte(f.data))
		Expect(e).NotTo(HaveOccurred())
	}
	Expect(tw.Close()).To(Succeed())
	artifact := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(b.Bytes()) }))
	DeferCleanup(artifact.Close)
	release := catalog.Release{AddonID: "sample", Name: "Sample", Version: "1.2.3", Artifact: artifact.URL + "/package.tar", Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(b.Bytes())), OS: "linux", Arch: "amd64"}
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
	i := &packages.Installer{Root: filepath.Join(cfg.Root, "packages"), Catalog: c, Host: contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: "linux", Arch: "amd64"}}
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
	if install {
		_, e = m.Submit(ctx, actor, addons.LifecycleCommand{AddonID: "sample", OperationID: "install", Action: "install", SourceID: "test", Version: "1.2.3"})
		Expect(e).NotTo(HaveOccurred())
	}
	return m
}
