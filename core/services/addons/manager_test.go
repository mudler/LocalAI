// SPDX-License-Identifier: MIT
package addons

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLifecycle(t *testing.T) {
	// The owner pins every component of its root and refuses a symbolic link on
	// the path. A temporary directory on macOS lies behind one, so the specs use
	// its resolved path.
	if dir, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
		t.Setenv("TMPDIR", dir)
	}
	RegisterFailHandler(Fail)
	RunSpecs(t, "Addon lifecycle")
}

var ctx = context.Background()
var actor = contract.Actor{UserID: "admin", CredentialID: "key", Mode: "named-user"}

func fixture() (OwnerConfig, catalog.Catalog, *packages.Installer) {
	manifest, e := os.ReadFile("../../../tests/addons/fixtures/packages/manifest.json")
	Expect(e).NotTo(HaveOccurred())
	return fixtureFor(manifest)
}

// uiFixture is the package fixture with a UI that accepts the given SDK range.
func uiFixture(min, max uint64) (OwnerConfig, catalog.Catalog, *packages.Installer) {
	manifest, e := os.ReadFile("../../../tests/addons/fixtures/packages/manifest.json")
	Expect(e).NotTo(HaveOccurred())
	var m contract.Manifest
	Expect(json.Unmarshal(manifest, &m)).To(Succeed())
	m.Capabilities.UI = &contract.UI{Entry: "ui.js", SDK: contract.VersionRange{Min: min, Max: max}, Pages: []string{"home"}, MainPage: "home"}
	manifest, e = json.Marshal(m)
	Expect(e).NotTo(HaveOccurred())
	return fixtureFor(manifest)
}

func fixtureFor(manifest []byte) (OwnerConfig, catalog.Catalog, *packages.Installer) {
	root := GinkgoT().TempDir()
	var e error
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	for name, data := range map[string][]byte{"manifest.json": manifest, "bin/sample": []byte("binary"), "ui.js": []byte("export {}")} {
		Expect(tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(data))})).To(Succeed())
		_, e = tw.Write(data)
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
	cfg := OwnerConfig{InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://owner.example", StorageID: "disk-one", Root: filepath.Join(root, "owner")}
	i := &packages.Installer{Root: filepath.Join(cfg.Root, "packages"), Catalog: c, Host: contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: "linux", Arch: "amd64"}}
	DeferCleanup(func() {
		_ = filepath.Walk(root, func(p string, info os.FileInfo, e error) error {
			if e == nil && info.IsDir() {
				_ = os.Chmod(p, 0700)
			}
			return nil
		})
	})
	return cfg, c, i
}
func admin(_ context.Context, a contract.Actor) error {
	if a != actor {
		return errors.New("denied")
	}
	return nil
}
func open(cfg OwnerConfig, c catalog.Catalog, i *packages.Installer) *Manager {
	m, e := NewManager(ctx, cfg, c, i, admin)
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(m.Close()).To(Succeed()) })
	return m
}
func command(id, action string) LifecycleCommand {
	return LifecycleCommand{OperationID: id, AddonID: "sample", Action: action, SourceID: "test", Version: "1.2.3"}
}

var _ = Describe("designated owner lifecycle", func() {
	It("carries the SDK refusal text to the caller of a failed install and keeps the generic error", func() {
		cfg, c, i := uiFixture(2, 2) // the installer host provides SDK 1
		m := open(cfg, c, i)
		_, e := m.Submit(ctx, actor, command("op-sdk", "install"))
		Expect(e).To(MatchError(ErrOperationFailed))
		Expect(e.Error()).To(ContainSubstring("the add-on needs SDK 2 to 2 and this host provides SDK 1"))
		// The same operation ID again returns the stored failure, without the text.
		_, e = m.Submit(ctx, actor, command("op-sdk", "install"))
		Expect(e).To(MatchError(ErrOperationFailed))
		Expect(e.Error()).NotTo(ContainSubstring("SDK"))
	})

	It("installs an add-on whose SDK range contains the host SDK", func() {
		cfg, c, i := uiFixture(1, 2)
		m := open(cfg, c, i)
		st, e := m.Submit(ctx, actor, command("op-sdk-ok", "install"))
		Expect(e).NotTo(HaveOccurred())
		Expect(st.Phase).To(Equal("completed"))
	})

	It("rejects symlinked package roots without touching external staging", func() {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		external := GinkgoT().TempDir()
		stage := filepath.Join(external, "stage-sentinel")
		DeferCleanup(func() { _ = os.Chmod(stage, 0700) })
		Expect(os.Mkdir(stage, 0700)).To(Succeed())
		sentinel := filepath.Join(stage, "keep")
		Expect(os.WriteFile(sentinel, []byte("keep"), 0400)).To(Succeed())
		Expect(os.Chmod(stage, 0500)).To(Succeed())
		Expect(os.Symlink(external, i.Root)).To(Succeed())
		err := m.Reconcile(ctx)
		data, e := os.ReadFile(sentinel)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal("keep"))
		info, e := os.Stat(stage)
		Expect(e).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0500)))
		Expect(err).To(HaveOccurred())
	})

	It("recovers every durable journal boundary without replaying publication", func() {
		for _, boundary := range []string{"intent", "published", "prepared", "applied", "completed"} {
			cfg, c, i := fixture()
			m := open(cfg, c, i)
			m.boundary = func(phase string) error {
				if phase == boundary {
					return errors.New("crash")
				}
				return nil
			}
			_, e := m.Submit(ctx, actor, command("install", "install"))
			Expect(e).To(HaveOccurred())
			Expect(m.Close()).To(Succeed())
			m = open(cfg, c, i)
			Expect(m.Reconcile(ctx)).To(Succeed())
			s, e := m.Status(ctx, "sample")
			Expect(e).NotTo(HaveOccurred())
			Expect(s.Installed).To(Equal(boundary != "intent" && boundary != "published"))
			Expect(s.ObservedHealth).To(Equal("stopped"))
			op, e := m.Submit(ctx, actor, command("install", "install"))
			if boundary == "intent" || boundary == "published" {
				Expect(e).To(MatchError(ErrOperationFailed))
				Expect(op.Phase).To(Equal("failed"))
			} else {
				Expect(e).NotTo(HaveOccurred())
				Expect(op.Phase).To(Equal("completed"))
				Expect(s.Generation).To(Equal(uint64(1)))
			}
		}
	})
	It("removes abandoned staging without removing published packages on recovery", func() {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		abandoned := filepath.Join(i.Root, "stage-abandoned")
		Expect(os.MkdirAll(abandoned, 0700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(abandoned, "partial"), []byte("partial"), 0400)).To(Succeed())
		Expect(os.Chmod(abandoned, 0500)).To(Succeed())
		_, e := m.Submit(ctx, actor, command("one", "install"))
		Expect(e).NotTo(HaveOccurred())
		s, e := m.Status(ctx, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(m.Reconcile(ctx)).To(Succeed())
		Expect(abandoned).NotTo(BeADirectory())
		Expect(s.Generations[0].Receipt.Directory).To(BeADirectory())
	})
	It("keeps immutable history on successful upgrade and reinstallation", func() {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		for n, action := range []string{"install", "upgrade", "uninstall", "install"} {
			_, e := m.Submit(ctx, actor, command(fmt.Sprint(n), action))
			Expect(e).NotTo(HaveOccurred())
		}
		s, e := m.Status(ctx, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(s.Generation).To(Equal(uint64(3)))
		Expect(s.Generations).To(HaveLen(3))
		for n, g := range s.Generations {
			Expect(g.Number).To(Equal(uint64(n + 1)))
			Expect(filepath.Join(g.Receipt.Directory, "receipt.json")).To(BeAnExistingFile())
		}
	})

	It("excludes competing owners and rejects conflicting durable identities", func() {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		_, e := NewManager(ctx, cfg, c, i, admin)
		Expect(e).To(HaveOccurred())
		Expect(m.Close()).To(Succeed())
		cfg.StorageID = "other"
		_, e = NewManager(ctx, cfg, c, i, admin)
		Expect(e).To(HaveOccurred())
		cfg.StorageID = "disk-one"
		cfg.OwnerID = "other"
		cfg.InstanceID = "other"
		_, e = NewManager(ctx, cfg, c, i, admin)
		Expect(e).To(HaveOccurred())
	})
	It("never opens replica roots or elects an absent owner", func() {
		cfg, c, i := fixture()
		cfg.InstanceID = "frontend"
		cfg.Root = filepath.Join(cfg.Root, "must-not-exist")
		m := open(cfg, c, i)
		_, e := m.Submit(ctx, actor, command("x", "install"))
		Expect(e).To(MatchError(ErrNotOwner))
		_, e = m.Status(ctx, "sample")
		Expect(e).To(MatchError(ErrNotOwner))
		Expect(m.Reconcile(ctx)).To(MatchError(ErrNotOwner))
		Expect(cfg.Root).NotTo(BeADirectory())
		cfg.SharedStorage = true
		_, e = NewManager(ctx, cfg, c, i, admin)
		Expect(e).To(HaveOccurred())
		cfg.SharedStorage = false
		cfg.OwnerID = ""
		_, e = NewManager(ctx, cfg, c, i, admin)
		Expect(e).To(HaveOccurred())
	})
	It("refuses commands and asset reads for an add-on ID the host serves as a route", func() {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		for _, id := range contract.ReservedAddonIDs() {
			cmd := command("reserved-"+id, "install")
			cmd.AddonID = id
			_, e := m.Submit(ctx, actor, cmd)
			Expect(e).To(MatchError(ErrConflict), id)
			_, e = m.CatalogDetails(ctx, actor, "local", id, "1.0.0")
			Expect(e).To(HaveOccurred(), id)
		}
	})
	It("binds duplicate operation IDs to actor and command and serializes conflicts", func() {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		cmd := command("one", "install")
		a, e := m.Submit(ctx, actor, cmd)
		Expect(e).NotTo(HaveOccurred())
		b, e := m.Submit(ctx, actor, cmd)
		Expect(e).NotTo(HaveOccurred())
		Expect(b).To(Equal(a))
		cmd.Action = "uninstall"
		_, e = m.Submit(ctx, actor, cmd)
		Expect(e).To(MatchError(ErrConflict))
		var wg sync.WaitGroup
		for _, action := range []string{"enable", "disable"} {
			wg.Add(1)
			go func(action string) {
				defer GinkgoRecover()
				defer wg.Done()
				_, e := m.Submit(ctx, actor, command(action, action))
				Expect(e).NotTo(HaveOccurred())
			}(action)
		}
		wg.Wait()
		s, e := m.Status(ctx, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(s.Generation).To(Equal(uint64(1)))
	})
	It("retains generations identity data and grants across uninstall and restart", func() {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		_, e := m.Submit(ctx, actor, command("one", "install"))
		Expect(e).NotTo(HaveOccurred())
		s, e := m.Status(ctx, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(os.MkdirAll(s.DataDirectory, 0700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(s.DataDirectory, "sentinel"), []byte("data"), 0600)).To(Succeed())
		Expect(os.WriteFile(s.GrantsPath, []byte("grants"), 0600)).To(Succeed())
		_, e = m.Submit(ctx, actor, command("two", "enable"))
		Expect(e).NotTo(HaveOccurred())
		Expect(m.Close()).To(Succeed())
		m = open(cfg, c, i)
		Expect(m.Reconcile(ctx)).To(Succeed())
		enabled, e := m.Status(ctx, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(enabled.DesiredEnabled).To(BeTrue())
		Expect(enabled.StartEligible).To(BeTrue())
		Expect(enabled.ObservedHealth).To(Equal("stopped"))
		_, e = m.Submit(ctx, actor, command("three", "uninstall"))
		Expect(e).NotTo(HaveOccurred())
		after, e := m.Status(ctx, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(after.Installed).To(BeFalse())
		Expect(after.DesiredEnabled).To(BeFalse())
		Expect(after.Identity).To(Equal(s.Identity))
		Expect(after.Generations).To(Equal(s.Generations))
		Expect(filepath.Join(s.DataDirectory, "sentinel")).To(BeAnExistingFile())
		Expect(s.GrantsPath).To(BeAnExistingFile())
		Expect(s.Generations[0].Receipt.Directory).To(BeADirectory())
	})
	It("preserves old packages and active generation on manager upgrade failure", func() {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		_, e := m.Submit(ctx, actor, command("one", "install"))
		Expect(e).NotTo(HaveOccurred())
		before, e := m.Status(ctx, "sample")
		Expect(e).NotTo(HaveOccurred())
		i.MaxBytes = 1
		_, e = m.Submit(ctx, actor, command("two", "upgrade"))
		Expect(e).To(HaveOccurred())
		after, e := m.Status(ctx, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(after).To(Equal(before))
		Expect(filepath.Join(before.Generations[0].Receipt.Directory, "bin/sample")).To(BeAnExistingFile())
	})
	It("requires current authoritative administration even for idempotent retries", func() {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		_, e := m.Submit(ctx, contract.Actor{UserID: "admin", Mode: "admin"}, command("one", "install"))
		Expect(e).To(MatchError(ErrForbidden))
		_, e = m.Submit(ctx, actor, command("one", "install"))
		Expect(e).NotTo(HaveOccurred())
		m.admin = func(context.Context, contract.Actor) error { return errors.New("revoked") }
		_, e = m.Submit(ctx, actor, command("one", "install"))
		Expect(e).To(MatchError(ErrForbidden))
	})
})

var _ = Describe("read-only lifecycle operation lookup", func() {
	It("binds lifecycle lookup to current admin and original actor without writes or repair", func() {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		_, e := m.Submit(ctx, actor, command("lookup-install", "install"))
		Expect(e).NotTo(HaveOccurred())
		before, e := os.ReadFile(filepath.Join(cfg.Root, "journal.json"))
		Expect(e).NotTo(HaveOccurred())
		m.boundary = func(string) error { Fail("lookup must not persist"); return nil }
		s, e := m.OperationStatus(ctx, actor, "lookup-install")
		Expect(e).NotTo(HaveOccurred())
		Expect(s.Phase).To(Equal("completed"))
		Expect(s.RuntimeState).To(Equal("not-observed"))
		s.AddonID = "changed"
		s, e = m.OperationStatus(ctx, actor, "lookup-install")
		Expect(e).NotTo(HaveOccurred())
		Expect(s.AddonID).To(Equal("sample"))
		_, e = m.OperationStatus(ctx, actor, "missing")
		Expect(e).To(Equal(ErrOperationNotFound))
		other := actor
		other.UserID = "other"
		m.admin = func(context.Context, contract.Actor) error { return nil }
		_, e = m.OperationStatus(ctx, other, "lookup-install")
		Expect(e).To(Equal(ErrOperationNotFound))
		op := m.journal.Operations["lookup-install"]
		op.Actor = contract.Actor{}
		m.journal.Operations["legacy"] = op
		_, e = m.OperationStatus(ctx, actor, "legacy")
		Expect(e).To(Equal(ErrOperationNotFound))
		m.runtimeRecoveryRequired = true
		s, e = m.OperationStatus(ctx, actor, "lookup-install")
		Expect(e).NotTo(HaveOccurred())
		Expect(s.RuntimeState).To(Equal("recovery-required"))
		m.admin = func(context.Context, contract.Actor) error { return ErrForbidden }
		_, e = m.OperationStatus(ctx, actor, "lookup-install")
		Expect(e).To(Equal(ErrForbidden))
		after, e := os.ReadFile(filepath.Join(cfg.Root, "journal.json"))
		Expect(e).NotTo(HaveOccurred())
		Expect(after).To(Equal(before))
	})
})

var _ = Describe("cancellable operation observation", func() {
	It("cancels status lookup while a lifecycle writer holds the journal lock", func() {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		m.mu.Lock()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		done := make(chan error, 1)
		go func() { _, e := m.OperationStatus(ctx, actor, "unknown"); done <- e }()
		defer m.mu.Unlock()
		Eventually(done).Should(Receive(Equal(context.Canceled)))
	})
})
