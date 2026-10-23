// SPDX-License-Identifier: MIT
package addons

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var user = contract.Actor{UserID: "reader", CredentialID: "key", Mode: "named-user"}

func appByID(list []apps.App, id string) (apps.App, bool) {
	for _, a := range list {
		if a.ID == id {
			return a, true
		}
	}
	return apps.App{}, false
}

var _ = Describe("apps and stale lifecycle generations", func() {
	It("denies enable and disable for a stale generation without recording anything", func() {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		_, e := m.Submit(ctx, actor, command("install", "install"))
		Expect(e).NotTo(HaveOccurred())
		for _, action := range []string{"enable", "disable"} {
			stale := command("stale-"+action, action)
			stale.Generation = 7
			_, e = m.Submit(ctx, actor, stale)
			Expect(e).To(MatchError(ErrStaleGeneration), action)
			_, recorded := m.journal.Operations[stale.OperationID]
			Expect(recorded).To(BeFalse(), "a denied request leaves no operation behind")
		}
		s, e := m.Status(ctx, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(s.DesiredEnabled).To(BeFalse())
		// The right generation is applied, and the same operation replays.
		enable := command("enable", "enable")
		enable.Generation = 1
		op, e := m.Submit(ctx, actor, enable)
		Expect(e).NotTo(HaveOccurred())
		Expect(op.Phase).To(Equal("completed"))
		s, _ = m.Status(ctx, "sample")
		Expect(s.DesiredEnabled).To(BeTrue())
		_, e = m.Submit(ctx, actor, enable)
		Expect(e).NotTo(HaveOccurred())
		// The same identifier with another generation is a different command.
		enable.Generation = 2
		_, e = m.Submit(ctx, actor, enable)
		Expect(e).To(MatchError(ErrConflict))
		// A caller that makes no claim keeps the earlier behavior.
		disable := command("disable", "disable")
		_, e = m.Submit(ctx, actor, disable)
		Expect(e).NotTo(HaveOccurred())
		// The generation is checked after authorization.
		denied := command("denied", "enable")
		denied.Generation = 9
		_, e = m.Submit(ctx, user, denied)
		Expect(e).To(MatchError(ErrForbidden))
	})

	It("lists the identifiers of installed addons, disabled ones included", func() {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		for id, st := range map[string]Status{
			"on":      {Installed: true, DesiredEnabled: true, Generation: 1},
			"off":     {Installed: true, Generation: 1},
			"removed": {Installed: false, Generation: 1},
		} {
			st.AddonID = id
			m.journal.Addons[id] = st
		}
		got, e := m.InstalledIDs(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(got).To(Equal([]string{"off", "on"}))
	})

	It("refuses to list installed identifiers while recovery is required", func() {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		m.journal.Addons["on"] = Status{AddonID: "on", Installed: true, DesiredEnabled: true, Generation: 1}
		m.uncertain = true
		_, e := m.InstalledIDs(ctx)
		Expect(e).To(MatchError(ErrRecoveryRequired))
		m.uncertain = false
		m.runtimeRecoveryRequired = true
		_, e = m.InstalledIDs(ctx)
		Expect(e).To(MatchError(ErrRecoveryRequired))
	})

	It("lists apps by visibility and serves an icon only for a visible app at its generation", func() {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		dir := GinkgoT().TempDir()
		icon := append([]byte("\x89PNG\r\n\x1a\n"), 1, 2, 3)
		Expect(os.WriteFile(filepath.Join(dir, "icon.png"), icon, 0400)).To(Succeed())
		sum := sha256.Sum256(icon)
		v := contract.VersionRange{Min: 1, Max: 1}
		ui := &contract.UI{Entry: "ui.js", SDK: v, Pages: []string{"home"}, MainPage: "home", Icon: "icon.png"}
		receipt := func(id string, withIcon bool) packages.PackageReceipt {
			r := packages.PackageReceipt{Directory: dir, Manifest: contract.Manifest{SchemaVersion: 1, ID: id, Version: "1.0.0", Executable: "bin/x", OS: "linux", Arch: "amd64", Host: v, API: v, Protocol: v, DataFormat: v, Capabilities: contract.Capabilities{Service: true, UI: ui}}}
			if withIcon {
				r.Icon = &packages.PackageIcon{Path: "icon.png", Digest: fmt.Sprintf("sha256:%x", sum), Size: int64(len(icon)), MediaType: "image/png"}
			}
			return r
		}
		for id, st := range map[string]Status{
			"on":      {Installed: true, DesiredEnabled: true, Generation: 2},
			"off":     {Installed: true, Generation: 1},
			"removed": {Installed: false, Generation: 1},
			"plain":   {Installed: true, DesiredEnabled: true, Generation: 1},
		} {
			st.AddonID = id
			st.Generations = []Generation{{Number: st.Generation, Receipt: receipt(id, id != "plain")}}
			m.journal.Addons[id] = st
		}
		ids := func(list []apps.App) []string {
			out := []string{}
			for _, a := range list {
				out = append(out, a.ID)
			}
			return out
		}
		got, e := m.Apps(ctx, user)
		Expect(e).NotTo(HaveOccurred())
		Expect(ids(got)).To(Equal([]string{"on", "plain"}), "a reader sees enabled installed addons only")
		on, _ := appByID(got, "on")
		Expect(on.Icon).To(Equal("/api/addons/apps/on/icon?generation=2"))
		Expect(on.MainPage).To(Equal("/app/addons/on/home"))
		Expect(on.Generation).To(Equal(uint64(2)))
		Expect(on.State).To(Equal("stopped"), "no runtime is attached to observe")
		Expect(on.Admin).To(BeNil())
		plain, _ := appByID(got, "plain")
		Expect(plain.Icon).To(BeEmpty(), "no verified icon, no link")
		got, e = m.Apps(ctx, actor)
		Expect(e).NotTo(HaveOccurred())
		Expect(ids(got)).To(Equal([]string{"off", "on", "plain"}), "an administrator also sees the installed disabled addon, never the removed one")
		off, _ := appByID(got, "off")
		Expect(off.Admin).To(Equal(&apps.AdminView{CanStart: true, CanStop: false, ManagementURL: "/app/addons?manage=off"}))

		icon1, e := m.AppIcon(ctx, user, "on", 2)
		Expect(e).NotTo(HaveOccurred())
		Expect(icon1.Icon.Path).To(Equal("icon.png"))
		for name, call := range map[string]func() error{
			"stale generation":      func() error { _, e := m.AppIcon(ctx, user, "on", 1); return e },
			"zero generation":       func() error { _, e := m.AppIcon(ctx, user, "on", 0); return e },
			"disabled for a reader": func() error { _, e := m.AppIcon(ctx, user, "off", 1); return e },
			"no icon":               func() error { _, e := m.AppIcon(ctx, actor, "plain", 1); return e },
			"removed":               func() error { _, e := m.AppIcon(ctx, actor, "removed", 1); return e },
			"unknown":               func() error { _, e := m.AppIcon(ctx, actor, "missing", 1); return e },
			"traversing identifier": func() error { _, e := m.AppIcon(ctx, actor, "../on", 2); return e },
		} {
			Expect(call()).To(MatchError(ErrForbidden), name)
		}
		_, e = m.AppIcon(ctx, actor, "off", 1)
		Expect(e).NotTo(HaveOccurred(), "an administrator may preview a disabled addon's icon")

		m.uncertain = true
		_, e = m.Apps(ctx, user)
		Expect(e).To(MatchError(ErrRecoveryRequired))
		_, e = m.AppIcon(ctx, user, "on", 2)
		Expect(e).To(MatchError(ErrRecoveryRequired))
	})
})

var _ = Describe("immutable app assets", Label("assets"), func() {
	It("resolves a package for the current enabled generation and digest only", func() {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		dir := GinkgoT().TempDir()
		v := contract.VersionRange{Min: 1, Max: 1}
		hex := func(c string) string { return strings.Repeat(c, 64) }
		receipt := func(id, digest string, ui *contract.UI) packages.PackageReceipt {
			return packages.PackageReceipt{Directory: dir, Digest: "sha256:" + digest, Manifest: contract.Manifest{SchemaVersion: 1, ID: id, Version: "1.0.0", Executable: "bin/x", OS: "linux", Arch: "amd64", Host: v, API: v, Protocol: v, DataFormat: v, Capabilities: contract.Capabilities{Service: true, UI: ui}}}
		}
		ui := &contract.UI{Entry: "ui.js", SDK: v, Pages: []string{"home"}}
		old := receipt("on", hex("1"), ui)
		current := receipt("on", hex("2"), ui)
		for id, st := range map[string]Status{
			"on":      {Installed: true, DesiredEnabled: true, Generation: 2, Generations: []Generation{{Number: 1, Receipt: old}, {Number: 2, Receipt: current}}},
			"off":     {Installed: true, Generation: 1, Generations: []Generation{{Number: 1, Receipt: receipt("off", hex("3"), ui)}}},
			"removed": {Installed: false, DesiredEnabled: true, Generation: 1, Generations: []Generation{{Number: 1, Receipt: receipt("removed", hex("4"), ui)}}},
			"plain":   {Installed: true, DesiredEnabled: true, Generation: 1, Generations: []Generation{{Number: 1, Receipt: receipt("plain", hex("5"), nil)}}},
		} {
			st.AddonID = id
			m.journal.Addons[id] = st
		}
		got, generation, e := m.AppAsset(ctx, user, "on", hex("2"))
		Expect(e).NotTo(HaveOccurred())
		Expect(generation).To(Equal(uint64(2)))
		Expect(got.Directory).To(Equal(dir))
		for name, call := range map[string]func() error{
			"a digest of an older generation": func() error { _, _, e := m.AppAsset(ctx, user, "on", hex("1")); return e },
			"another digest":                  func() error { _, _, e := m.AppAsset(ctx, user, "on", hex("9")); return e },
			"disabled for a reader":           func() error { _, _, e := m.AppAsset(ctx, user, "off", hex("3")); return e },
			"disabled for an administrator":   func() error { _, _, e := m.AppAsset(ctx, actor, "off", hex("3")); return e },
			"uninstalled":                     func() error { _, _, e := m.AppAsset(ctx, actor, "removed", hex("4")); return e },
			"no UI":                           func() error { _, _, e := m.AppAsset(ctx, actor, "plain", hex("5")); return e },
			"unknown":                         func() error { _, _, e := m.AppAsset(ctx, actor, "missing", hex("2")); return e },
			"traversing identifier":           func() error { _, _, e := m.AppAsset(ctx, actor, "../on", hex("2")); return e },
			"an empty digest":                 func() error { _, _, e := m.AppAsset(ctx, actor, "on", ""); return e },
			"a prefixed digest":               func() error { _, _, e := m.AppAsset(ctx, actor, "on", "sha256:"+hex("2")); return e },
		} {
			Expect(call()).To(MatchError(ErrForbidden), name)
		}
		m.uncertain = true
		_, _, e = m.AppAsset(ctx, user, "on", hex("2"))
		Expect(e).To(MatchError(ErrRecoveryRequired))
	})
})

var _ = Describe("apps observation", func() {
	It("reports the supervisor state of an app through a crash and a restart", func() {
		if goruntime.GOOS != "linux" {
			Skip("the add-on runtime exists on Linux only")
		}
		cfg, c, installer := fixture()
		dir := GinkgoT().TempDir()
		out, e := exec.Command("go", "build", "-o", filepath.Join(dir, "fixture"), "../../../tests/addons/fixture/cmd").CombinedOutput()
		Expect(e).NotTo(HaveOccurred(), string(out))
		b, e := os.ReadFile("../../../tests/addons/fixtures/contracts/service.json")
		Expect(e).NotTo(HaveOccurred())
		manifest, e := contract.ParseManifest(b)
		Expect(e).NotTo(HaveOccurred())
		manifest.Executable = "fixture"
		receipt := packages.PackageReceipt{Directory: dir, Manifest: manifest}
		b, e = json.Marshal(receipt)
		Expect(e).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(dir, "receipt.json"), b, 0400)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "fixture.json"), []byte(`{"mode":"normal"}`), 0600)).To(Succeed())
		m, e := NewManager(ctx, cfg, c, installer, admin)
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(m.Close)
		m.journal.Addons["sample"] = Status{AddonID: "sample", Installed: true, DesiredEnabled: true, Generation: 1, Generations: []Generation{{Number: 1, Receipt: receipt}}}
		limits := contract.DefaultLimits()
		limits.BackoffInitial, limits.BackoffMax = time.Second, 2*time.Second
		s, e := addonruntime.NewSupervisor(addonruntime.Config{StateRoot: filepath.Join(cfg.Root, "runtime"), Limits: limits, ReadyTimeout: 1500 * time.Millisecond, StableHealth: time.Second})
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(s.Close(ctx)).To(Succeed()) })
		Expect(m.AttachRuntime(ctx, s)).To(Succeed())
		state := func() string {
			list, e := m.Apps(ctx, user)
			if e != nil || len(list) != 1 {
				return "error"
			}
			return list[0].State
		}
		Expect(state()).To(Equal("running"))
		old, e := m.Runtime(ctx, "sample")
		Expect(e).NotTo(HaveOccurred())
		// The replacement never becomes ready, so it stays starting until the
		// readiness bound; the test then lets the next launch succeed.
		Expect(os.WriteFile(filepath.Join(dir, "fixture.json"), []byte(`{"mode":"never-ready"}`), 0600)).To(Succeed())
		Expect(syscall.Kill(old.PID, syscall.SIGKILL)).To(Succeed())
		Eventually(state, 3*time.Second, 5*time.Millisecond).Should(Equal("failed"), "the child died and the restart waits for its backoff")
		Eventually(state, 3*time.Second, 5*time.Millisecond).Should(Equal("starting"))
		Expect(os.WriteFile(filepath.Join(dir, "fixture.json"), []byte(`{"mode":"normal"}`), 0600)).To(Succeed())
		Eventually(state, 10*time.Second, 20*time.Millisecond).Should(Equal("running"))
		next, e := m.Runtime(ctx, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(next.Incarnation).NotTo(Equal(old.Incarnation))
		// A disabled addon is stopped for every observer.
		_, e = m.Submit(ctx, actor, LifecycleCommand{OperationID: "off", AddonID: "sample", Action: "disable", Generation: 1})
		Expect(e).NotTo(HaveOccurred())
		list, e := m.Apps(ctx, actor)
		Expect(e).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].State).To(Equal("stopped"))
		Expect(list[0].Admin.CanStart).To(BeTrue())
		list, e = m.Apps(ctx, user)
		Expect(e).NotTo(HaveOccurred())
		Expect(list).To(BeEmpty())
	})
})
