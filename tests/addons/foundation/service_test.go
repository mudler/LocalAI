// SPDX-License-Identifier: MIT
package foundation_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"time"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/addons/foundation/cluster"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// owner is the lifecycle service of an owner, built the way the application
// builds it (manager, installer, supervisor) over the real fixture process.
type owner struct {
	m     *addons.Manager
	s     *addonruntime.Supervisor
	actor contract.Actor
	root  string
}

// openOwner opens or reopens the owner on root.
func openOwner(root string, lib *cluster.Library, ready time.Duration) (*owner, error) {
	ctx := context.Background()
	actor := contract.Actor{UserID: "admin", CredentialID: "key:admin", Mode: "named-user"}
	admin := func(_ context.Context, a contract.Actor) error {
		if a != actor {
			return errors.New("denied")
		}
		return nil
	}
	sources, err := catalog.NewSources(filepath.Join(root, "sources.json"), admin, catalog.CredentialResolver{})
	if err != nil {
		return nil, err
	}
	if err := sources.Save(ctx, actor, catalog.Source{ID: "fixture", Name: "Fixture", URL: lib.URL(), Enabled: true, AllowLoopbackHTTP: true}); err != nil {
		return nil, err
	}
	if _, err := sources.Refresh(ctx, "fixture"); err != nil {
		return nil, err
	}
	cat := catalog.Catalog{Sources: sources, TargetOS: goruntime.GOOS, TargetArch: goruntime.GOARCH}
	host := contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: goruntime.GOOS, Arch: goruntime.GOARCH}
	installer := &packages.Installer{Root: filepath.Join(root, "packages"), Catalog: cat, Host: host}
	m, err := addons.NewManager(ctx, addons.OwnerConfig{InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://owner.example", StorageID: "disk", Root: root}, cat, installer, admin)
	if err != nil {
		return nil, err
	}
	if err := m.Reconcile(ctx); err != nil {
		_ = m.Close()
		return nil, err
	}
	limits := contract.DefaultLimits()
	s, err := addonruntime.NewSupervisor(addonruntime.Config{StateRoot: filepath.Join(root, "runtime"), Limits: limits, ReadyTimeout: ready, StableHealth: limits.BackoffMax})
	if err != nil {
		_ = m.Close()
		return nil, err
	}
	if err := m.AttachRuntime(ctx, s); err != nil {
		_ = s.Close(context.Background())
		_ = m.Close()
		return nil, err
	}
	return &owner{m: m, s: s, actor: actor, root: root}, nil
}

func (o *owner) submit(id, action string, generation uint64, version string) (addons.OperationStatus, error) {
	return o.m.Submit(context.Background(), o.actor, addons.LifecycleCommand{OperationID: nextID("svc"), AddonID: id, Action: action, SourceID: "fixture", Version: version, Generation: generation})
}

func (o *owner) ready(id string, generation uint64) bool {
	regs, err := o.s.Registry.Snapshot(context.Background())
	Expect(err).NotTo(HaveOccurred())
	for _, r := range regs {
		if r.AddonID == id && r.Generation == generation {
			return true
		}
	}
	return false
}

func makeOwner(lib *cluster.Library, ready time.Duration) (*owner, string) {
	GinkgoHelper()
	root, err := os.MkdirTemp("", "foundation-owner-*")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		_ = filepath.Walk(root, func(p string, i os.FileInfo, e error) error {
			if e == nil && i.IsDir() {
				_ = os.Chmod(p, 0o700)
			}
			return nil
		})
		_ = os.RemoveAll(root)
	})
	o, err := openOwner(root, lib, ready)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = o.s.Close(context.Background()); _ = o.m.Close() })
	return o, root
}

var _ = Describe("lifecycle service over the real fixture process", func() {
	var lib *cluster.Library
	var effects string

	BeforeEach(func() {
		var err error
		lib, err = cluster.NewLibrary(paths.Fixture)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(lib.Close)
		effects = GinkgoT().TempDir()
	})

	It("keeps the last good generation running and the retained data when the new generation cannot start", func() {
		Expect(lib.Add(cluster.ToolPackage("1.0.0", nil), effects)).To(Succeed())
		Expect(lib.Add(cluster.ToolPackage("2.0.0", map[string]any{"mode": "exit-start"}), effects)).To(Succeed())
		Expect(lib.Add(cluster.ToolPackage("3.0.0", nil), effects)).To(Succeed())
		o, _ := makeOwner(lib, 5*time.Second)
		ctx := context.Background()
		_, err := o.submit("found-tool", "install", 0, "1.0.0")
		Expect(err).NotTo(HaveOccurred())
		_, err = o.submit("found-tool", "enable", 1, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(o.ready("found-tool", 1)).To(BeTrue())
		before, err := o.m.Status(ctx, "found-tool")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.MkdirAll(before.DataDirectory, 0o700)).To(Succeed())
		marker := filepath.Join(before.DataDirectory, "rows.db")
		Expect(os.WriteFile(marker, []byte("retained rows"), 0o600)).To(Succeed())
		firstPackage := hashTree(before.Generations[0].Receipt.Directory)

		status, err := o.submit("found-tool", "upgrade", 0, "2.0.0")
		Expect(err).To(HaveOccurred(), "an upgrade that cannot start is a failed operation: %+v", status)

		after, err := o.m.Status(ctx, "found-tool")
		Expect(err).NotTo(HaveOccurred())
		Expect(after.Generation).To(Equal(uint64(1)), "the last good generation stays the installed one")
		Expect(after.ObservedHealth).To(Equal("ready"), "and it stays running")
		Expect(o.ready("found-tool", 1)).To(BeTrue())
		Expect(hashTree(before.Generations[0].Receipt.Directory)).To(Equal(firstPackage), "its code is untouched")
		b, err := os.ReadFile(marker)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).To(Equal("retained rows"))
		// The failed attempt leaves nothing that blocks a good upgrade.
		_, err = o.submit("found-tool", "upgrade", 0, "3.0.0")
		Expect(err).NotTo(HaveOccurred())
		Expect(o.ready("found-tool", 2)).To(BeTrue(), "the good release is the next generation")
	})

	It("starts the owner when an enabled add-on cannot become ready and fails only that add-on", func() {
		Expect(lib.Add(cluster.ToolPackage("1.0.0", nil), effects)).To(Succeed())
		Expect(lib.Add(cluster.ServicePackage("1.0.0", map[string]any{"mode": "exit-start"}), effects)).To(Succeed())
		o, root := makeOwner(lib, 5*time.Second)
		for _, id := range []string{"found-tool", "found-svc"} {
			_, err := o.submit(id, "install", 0, "1.0.0")
			Expect(err).NotTo(HaveOccurred())
		}
		_, err := o.submit("found-tool", "enable", 1, "")
		Expect(err).NotTo(HaveOccurred())
		// The broken add-on is enabled, and its own command says it did not start.
		_, err = o.submit("found-svc", "enable", 1, "")
		Expect(err).To(HaveOccurred())
		status, err := o.m.Status(context.Background(), "found-svc")
		Expect(err).NotTo(HaveOccurred())
		marker := filepath.Join(status.DataDirectory, "rows.db")
		Expect(os.MkdirAll(status.DataDirectory, 0o700)).To(Succeed())
		Expect(os.WriteFile(marker, []byte("retained rows"), 0o600)).To(Succeed())
		Expect(o.s.Close(context.Background())).To(Succeed())
		Expect(o.m.Close()).To(Succeed())

		// A restart must still bring the owner up, with the healthy add-on
		// running and the broken one stopped.
		again, err := openOwner(root, lib, 5*time.Second)
		Expect(err).NotTo(HaveOccurred(), "one add-on that cannot start must not stop the owner")
		DeferCleanup(func() { _ = again.s.Close(context.Background()); _ = again.m.Close() })
		Expect(again.ready("found-tool", 1)).To(BeTrue())
		Expect(again.ready("found-svc", 1)).To(BeFalse())
		// A command on the healthy add-on is not failed by the broken one.
		_, err = again.submit("found-tool", "disable", 1, "")
		Expect(err).NotTo(HaveOccurred())
		_, err = again.submit("found-svc", "disable", 1, "")
		Expect(err).NotTo(HaveOccurred())
		b, err := os.ReadFile(marker)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).To(Equal("retained rows"))
	})
})
