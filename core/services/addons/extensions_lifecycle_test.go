//go:build linux

// SPDX-License-Identifier: MIT
package addons

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/core/services/addons/updates"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func lifecycleCapabilities(label string) contract.Capabilities {
	return contract.Capabilities{Service: true, Actions: true, Tools: []string{"run", "health"}, ToolDefinitions: []contract.ToolDefinition{
		{Name: "run", Description: "Run", Kind: "action", Shadows: "bash", InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`)},
		{Name: "health", Description: "Health", Kind: "read", UIOnly: true, InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`)},
	}, UI: &contract.UI{Entry: "ui.js", SDK: contract.VersionRange{Min: 2, Max: 2}, Pages: []string{"home"}, MainPage: "home", HomeCommands: []contract.HomeCommand{{ID: "open", Alias: label}}, ModelActions: []contract.ModelAction{{Capability: "FLAG_IMAGE", Page: "home", Path: label}}}, ExecutionProvider: &contract.ExecutionProvider{Tools: []string{"run"}, StatusTool: "health"}, HostOperations: &contract.HostOperations{Required: []string{"workspace.materialize"}, Optional: []string{"egress.check"}}}
}

var _ = Describe("typed staged Manager lifecycle", func() {
	It("persists and recovers model actions and declarations through install manual and automatic upgrades and real activation rollback", func() {
		r := newUpdateRig(true)
		c := r.m.catalog
		installer := r.m.installer
		installer.Host.SDK = 2
		binary := filepath.Join(GinkgoT().TempDir(), "fixture")
		out, e := exec.Command("go", "build", "-o", binary, "../../../tests/addons/fixture/cmd").CombinedOutput()
		Expect(e).NotTo(HaveOccurred(), string(out))
		executable, e := os.ReadFile(binary)
		Expect(e).NotTo(HaveOccurred())
		caps := lifecycleCapabilities("first")
		mode := "normal"
		unsupported := false
		r.lib.transform = func(m map[string]any) map[string][]byte {
			m["capabilities"] = caps
			if unsupported {
				m["futureExtension"] = true
			}
			config, _ := json.Marshal(map[string]string{"mode": mode})
			return map[string][]byte{"bin/sample": executable, "ui.js": []byte("export {}"), "fixture.json": config}
		}
		attach := func() {
			s, e := addonruntime.NewSupervisor(addonruntime.Config{StateRoot: filepath.Join(r.cfg.Root, "runtime"), Limits: contract.DefaultLimits(), ReadyTimeout: time.Second, StableHealth: time.Second})
			Expect(e).NotTo(HaveOccurred())
			Expect(r.m.AttachRuntime(ctx, s)).To(Succeed())
		}
		check := func(number uint64, digest string, want contract.Capabilities) addonruntime.Instance {
			GinkgoHelper()
			st, e := r.m.Status(ctx, "sample")
			Expect(e).NotTo(HaveOccurred())
			Expect(st.Installed).To(BeTrue())
			Expect(st.DesiredEnabled).To(BeTrue())
			Expect(st.Generation).To(Equal(number))
			var chosen packages.PackageReceipt
			for _, g := range st.Generations {
				if g.Number == number {
					chosen = g.Receipt
				}
			}
			Expect(chosen.Digest).To(Equal(digest))
			Expect(chosen.Manifest.Capabilities).To(Equal(want))
			declared, e := r.m.DeclaredCapabilities(ctx, "sample", number)
			Expect(e).NotTo(HaveOccurred())
			Expect(declared.ExecutionProvider).To(Equal(want.ExecutionProvider))
			Expect(declared.HostOperations).To(Equal(want.HostOperations))
			declared.ExecutionProvider.Tools[0] = "mutated"
			declared.HostOperations.Required[0] = "mutated"
			declared.HostOperations.Optional[0] = "mutated"
			again, e := r.m.DeclaredCapabilities(ctx, "sample", number)
			Expect(e).NotTo(HaveOccurred())
			Expect(again.ExecutionProvider).To(Equal(want.ExecutionProvider))
			Expect(again.HostOperations).To(Equal(want.HostOperations))
			raw, e := os.ReadFile(filepath.Join(chosen.Directory, "receipt.json"))
			Expect(e).NotTo(HaveOccurred())
			var disk packages.PackageReceipt
			Expect(json.Unmarshal(raw, &disk)).To(Succeed())
			Expect(disk).To(Equal(chosen))
			instance, e := r.m.Runtime(ctx, "sample")
			Expect(e).NotTo(HaveOccurred())
			Expect(instance.Generation).To(Equal(number))
			regs, e := r.m.runtime.Registry.Snapshot(ctx)
			Expect(e).NotTo(HaveOccurred())
			Expect(regs).To(HaveLen(1))
			Expect(regs[0].Generation).To(Equal(number))
			Expect(regs[0].Incarnation).To(Equal(instance.Incarnation))
			Expect(regs[0].Capabilities).To(Equal(want))
			installedApps, e := r.m.Apps(ctx, actor)
			Expect(e).NotTo(HaveOccurred())
			Expect(installedApps).To(HaveLen(1))
			Expect(installedApps[0].UI).NotTo(BeNil())
			Expect(installedApps[0].UI.ModelActions).To(Equal(want.UI.ModelActions))
			installedApps[0].UI.ModelActions[0].Path = "mutated"
			freshApps, e := r.m.Apps(ctx, actor)
			Expect(e).NotTo(HaveOccurred())
			Expect(freshApps[0].UI.ModelActions).To(Equal(want.UI.ModelActions))
			return instance
		}
		reopen := func() {
			Expect(r.m.Close()).To(Succeed())
			r.m = open(r.cfg, c, installer)
			r.m.SetHostOps(map[string][]string{"sample": {"workspace."}})
			r.m.EnableUpdates(updates.Config{Now: func() time.Time { return *r.clk }, MinRefresh: time.Second, TTL: time.Hour})
			Expect(r.m.Reconcile(ctx)).To(Succeed())
			attach()
		}
		first := r.lib.publish("1.2.3-dev", "development", "typed-first", catalog.Requirements{})
		r.lib.refresh()
		// Admission must reject a verified package before publication.
		_, e = r.m.Submit(ctx, actor, LifecycleCommand{OperationID: "missing-required", AddonID: "sample", Action: "install", SourceID: "test", Version: "development"})
		Expect(e).To(HaveOccurred())
		missing, e := r.m.Status(ctx, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(missing.Installed).To(BeFalse())
		r.m.SetHostOps(map[string][]string{"sample": {"workspace."}})
		r.installChannel()
		_, e = r.m.Submit(ctx, actor, LifecycleCommand{OperationID: "enable-typed", AddonID: "sample", Action: "enable"})
		Expect(e).NotTo(HaveOccurred())
		attach()
		old := check(1, first.Digest, caps)
		check(1, first.Digest, caps)
		adminApps, e := r.m.Apps(ctx, actor)
		Expect(e).NotTo(HaveOccurred())
		Expect(adminApps).To(HaveLen(1))
		Expect(adminApps[0].Admin.HostOperations.MissingOptional).To(Equal([]string{"egress.check"}))
		Expect(adminApps[0].State).To(Equal("running"))
		// A later SetHostOps is not a hot reload or a self grant.
		r.m.SetHostOps(map[string][]string{"sample": {"workspace.", "egress."}})
		Expect(r.m.hostOps["sample"]).To(Equal([]string{"workspace."}))

		reopen()
		recovered := check(1, first.Digest, caps)
		Expect(recovered.Incarnation).NotTo(Equal(old.Incarnation))
		// A stricter candidate must not stop the active incarnation.
		beforeRefusal, e := r.m.Status(ctx, "sample")
		Expect(e).NotTo(HaveOccurred())
		caps = lifecycleCapabilities("unmet")
		caps.HostOperations.Required = append(caps.HostOperations.Required, "egress.audit")
		unmet := r.lib.publish("1.2.3-dev", "development", "typed-unmet", catalog.Requirements{})
		r.lib.refresh()
		_, e = r.m.Submit(ctx, actor, upgrade("unmet-manual", first.Digest, unmet.Digest))
		Expect(e).To(HaveOccurred())
		r.tick(2 * time.Second)
		denied := r.m.AutoUpgrade(ctx)
		Expect(denied).To(HaveLen(1))
		Expect(denied[0].Err).To(HaveOccurred())
		r.tick(2 * time.Second)
		skipped := r.m.AutoUpgrade(ctx)
		Expect(skipped).To(HaveLen(1))
		Expect(skipped[0].SkippedAsFailure).To(BeTrue())
		afterRefusal, e := r.m.Status(ctx, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(afterRefusal).To(Equal(beforeRefusal))
		Expect(check(1, first.Digest, lifecycleCapabilities("first")).Incarnation).To(Equal(recovered.Incarnation))
		caps = lifecycleCapabilities("manual")
		manual := r.lib.publish("1.2.3-dev", "development", "typed-manual", catalog.Requirements{})
		r.lib.refresh()
		_, e = r.m.Submit(ctx, actor, upgrade("typed-manual", first.Digest, manual.Digest))
		Expect(e).NotTo(HaveOccurred())
		check(2, manual.Digest, caps)
		caps = lifecycleCapabilities("automatic")
		automatic := r.lib.publish("1.2.3-dev", "development", "typed-auto", catalog.Requirements{})
		r.tick(2 * time.Second)
		results := r.m.AutoUpgrade(ctx)
		Expect(results).To(HaveLen(1))
		Expect(results[0].Err).NotTo(HaveOccurred())
		Expect(results[0].Upgraded).To(BeTrue())
		prior := check(3, automatic.Digest, caps)
		priorCaps := caps
		// The verified candidate stages successfully, but its actual subprocess
		// fails registration. Manager must start the prior receipt again.
		caps = lifecycleCapabilities("broken")
		mode = "exit-start"
		broken := r.lib.publish("1.2.3-dev", "development", "typed-broken", catalog.Requirements{})
		r.lib.refresh()
		_, e = r.m.Submit(ctx, actor, upgrade("typed-rollback", automatic.Digest, broken.Digest))
		Expect(e).To(HaveOccurred())
		restored := check(3, automatic.Digest, priorCaps)
		Expect(restored.Incarnation).NotTo(Equal(prior.Incarnation))
		recoveryHost := &managerHost{m: r.m}
		q, qe := recoveryHost.Quiesce(ctx, "sample")
		Expect(qe).NotTo(HaveOccurred())
		var receipt packages.PackageReceipt
		for _, g := range r.m.journal.Addons["sample"].Generations {
			if g.Number == 3 {
				receipt = g.Receipt
			}
		}
		Expect(recoveryHost.StartCandidate(ctx, "sample", receipt, 3)).To(Succeed())
		Expect(recoveryHost.StopCandidate(ctx, "sample")).To(Succeed())
		Expect(recoveryHost.StopCandidate(ctx, "sample")).To(Succeed())
		Expect(q.Resume(ctx)).To(Succeed())
		check(3, automatic.Digest, priorCaps)
		st, e := r.m.Status(ctx, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(st.Generations).To(HaveLen(3))
		reopen()
		restarted := check(3, automatic.Digest, priorCaps)
		Expect(restarted.Incarnation).NotTo(Equal(restored.Incarnation))
		// Strict unsupported-manifest refusal must leave the active installed
		// state and process intact, not just preserve old immutable files.
		mode = "normal"
		unsupported = true
		refused := r.lib.publish("1.2.3-dev", "development", "typed-unsupported", catalog.Requirements{})
		r.lib.refresh()
		before, e := r.m.Status(ctx, "sample")
		Expect(e).NotTo(HaveOccurred())
		_, e = r.m.Submit(ctx, actor, upgrade("typed-refusal", automatic.Digest, refused.Digest))
		Expect(e).To(HaveOccurred())
		after, e := r.m.Status(ctx, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(after).To(Equal(before))
		unchanged := check(3, automatic.Digest, priorCaps)
		Expect(unchanged.Incarnation).To(Equal(restarted.Incarnation))
		reopen()
		check(3, automatic.Digest, priorCaps)
		// Restart with removed grants retains identity and enabled intent,
		// but may not spawn a subprocess or admit tools/resources.
		Expect(r.m.Close()).To(Succeed())
		r.m = open(r.cfg, c, installer)
		r.m.SetHostOps(map[string][]string{})
		Expect(r.m.Reconcile(ctx)).To(Succeed())
		attach()
		unavailable, e := r.m.Status(ctx, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(unavailable.Installed).To(BeTrue())
		Expect(unavailable.DesiredEnabled).To(BeTrue())
		Expect(unavailable.Generation).To(Equal(uint64(3)))
		Expect(unavailable.StartEligible).To(BeFalse())
		_, e = r.m.Runtime(ctx, "sample")
		Expect(e).To(HaveOccurred())
		regs, e := r.m.runtime.Registry.Snapshot(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(regs).To(BeEmpty())
		adminApps, e = r.m.Apps(ctx, actor)
		Expect(e).NotTo(HaveOccurred())
		Expect(adminApps).To(HaveLen(1))
		Expect(adminApps[0].State).To(Equal("failed"))
		Expect(adminApps[0].Enabled).To(BeTrue())
		Expect(adminApps[0].UI).To(BeNil())
		Expect(adminApps[0].MainPage).To(BeEmpty())
		Expect(adminApps[0].Admin.HostOperations.MissingRequired).To(Equal([]string{"workspace.materialize"}))
		installed, e := r.m.Installed(ctx, actor)
		Expect(e).NotTo(HaveOccurred())
		Expect(installed).To(HaveLen(1))
		Expect(installed[0].State).To(Equal("failed"))
		Expect(installed[0].HostOperations.MissingRequired).To(Equal([]string{"workspace.materialize"}))
		Expect(installed[0].Digest).To(Equal(automatic.Digest))
		readerApps, e := r.m.Apps(ctx, contract.Actor{UserID: "reader"})
		Expect(e).NotTo(HaveOccurred())
		Expect(readerApps).To(HaveLen(1))
		Expect(readerApps[0].Admin).To(BeNil())

		_, e = r.m.DeclaredTools(ctx, "sample", 3)
		Expect(e).To(HaveOccurred())
		_, e = r.m.DeclaredCapabilities(ctx, "sample", 3)
		Expect(e).To(HaveOccurred())
		Expect(r.m.Close()).To(Succeed())
		r.m = open(r.cfg, c, installer)
		r.m.SetHostOps(map[string][]string{"sample": {"workspace.", "egress."}})
		Expect(r.m.Reconcile(ctx)).To(Succeed())
		attach()
		check(3, automatic.Digest, priorCaps)
		adminApps, e = r.m.Apps(ctx, actor)
		Expect(e).NotTo(HaveOccurred())
		Expect(adminApps[0].Admin.HostOperations.MissingRequired).To(BeEmpty())
		Expect(adminApps[0].Admin.HostOperations.MissingOptional).To(BeEmpty())
	})
})
