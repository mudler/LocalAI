// SPDX-License-Identifier: MIT
package addons

import (
	"encoding/json"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

var _ = Describe("Runtime lifecycle", func() {
	It("executes enabled owner intent on startup and stops on disable and close without forwarder execution", func() {
		cfg, c, installer := fixture()
		dir := GinkgoT().TempDir()
		binary := filepath.Join(dir, "fixture")
		out, e := exec.Command("go", "build", "-o", binary, "../../../tests/addons/fixture/cmd").CombinedOutput()
		Expect(e).NotTo(HaveOccurred(), string(out))
		data, e := os.ReadFile("../../../tests/addons/fixtures/contracts/service.json")
		Expect(e).NotTo(HaveOccurred())
		manifest, e := contract.ParseManifest(data)
		Expect(e).NotTo(HaveOccurred())
		manifest.Executable = "fixture"
		receipt := packages.PackageReceipt{Directory: dir, Manifest: manifest}
		data, e = json.Marshal(receipt)
		Expect(e).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(dir, "receipt.json"), data, 0400)).To(Succeed())
		m, e := NewManager(ctx, cfg, c, installer, admin)
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(m.Close)
		m.journal.Addons["sample"] = Status{AddonID: "sample", Installed: true, DesiredEnabled: true, Generation: 1, Generations: []Generation{{Number: 1, Receipt: receipt}}}
		Expect(m.persist("test")).To(Succeed())
		makeRuntime := func() *addonruntime.Supervisor {
			s, e := addonruntime.NewSupervisor(addonruntime.Config{Limits: contract.DefaultLimits(), ReadyTimeout: time.Second, StableHealth: time.Second})
			Expect(e).NotTo(HaveOccurred())
			DeferCleanup(func() { Expect(s.Close(ctx)).To(Succeed()) })
			return s
		}
		s := makeRuntime()
		Expect(m.AttachRuntime(ctx, s)).To(Succeed())
		r, e := s.Registry.Snapshot(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(r).To(HaveLen(1))
		_, e = m.Submit(ctx, actor, LifecycleCommand{OperationID: "disable-runtime", AddonID: "sample", Action: "disable"})
		Expect(e).NotTo(HaveOccurred())
		r, e = s.Registry.Snapshot(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(r).To(BeEmpty())
		_, e = m.Submit(ctx, actor, LifecycleCommand{OperationID: "enable-runtime", AddonID: "sample", Action: "enable"})
		Expect(e).NotTo(HaveOccurred())
		r, e = s.Registry.Snapshot(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(r).To(HaveLen(1))
		Expect(m.Close()).To(Succeed())
		r, e = s.Registry.Snapshot(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(r).To(BeEmpty())
		m, e = NewManager(ctx, cfg, c, installer, admin)
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(m.Close)
		s = makeRuntime()
		Expect(m.AttachRuntime(ctx, s)).To(Succeed())
		r, e = s.Registry.Snapshot(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(r).To(HaveLen(1))
		cfg.InstanceID = "forwarder"
		forwarder, e := NewManager(ctx, cfg, c, nil, nil)
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(forwarder.Close)
		local := makeRuntime()
		Expect(forwarder.AttachRuntime(ctx, local)).To(Equal(ErrNotOwner))
		r, e = local.Registry.Snapshot(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(r).To(BeEmpty())
	})
})
