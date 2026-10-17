//go:build linux

// SPDX-License-Identifier: MIT
package addons

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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
			s, e := addonruntime.NewSupervisor(addonruntime.Config{StateRoot: filepath.Join(cfg.Root, "runtime"), Limits: contract.DefaultLimits(), ReadyTimeout: time.Second, StableHealth: time.Second})
			Expect(e).NotTo(HaveOccurred())
			DeferCleanup(func() { Expect(s.Close(ctx)).To(Succeed()) })
			return s
		}
		s := makeRuntime()
		Expect(m.AttachRuntime(ctx, s)).To(Succeed())
		r, e := s.Registry.Snapshot(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(r).To(HaveLen(1))
		// A real rename failure during applied persistence closes the runtime.
		journalPath := filepath.Join(cfg.Root, "journal.json")
		m.boundary = func(phase string) error {
			if phase == "prepared" {
				Expect(os.Rename(journalPath, journalPath+".saved")).To(Succeed())
				Expect(os.Mkdir(journalPath, 0700)).To(Succeed())
			}
			return nil
		}
		_, fault := m.Submit(ctx, actor, LifecycleCommand{OperationID: "persistence-fault", AddonID: "sample", Action: "enable"})
		Expect(fault).To(HaveOccurred())
		m.boundary = nil
		Expect(os.Remove(journalPath)).To(Succeed())
		Expect(os.Rename(journalPath+".saved", journalPath)).To(Succeed())
		Expect(m.Reconcile(ctx)).To(Equal(ErrRecoveryRequired))
		_, fault = m.Runtime(ctx, "sample")
		Expect(fault).To(HaveOccurred())
		Expect(m.Close()).To(Succeed())
		m, e = NewManager(ctx, cfg, c, installer, admin)
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(m.Close)
		Expect(m.Reconcile(ctx)).To(Succeed())
		s = makeRuntime()
		Expect(m.AttachRuntime(ctx, s)).To(Succeed())
		old, e := m.Runtime(ctx, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(syscall.Kill(old.PID, syscall.SIGKILL)).To(Succeed())
		var current addonruntime.Instance
		Eventually(func() string {
			current, e = m.Runtime(ctx, "sample")
			if e != nil {
				return ""
			}
			return current.Incarnation
		}, 3*time.Second).Should(And(Not(BeEmpty()), Not(Equal(old.Incarnation))))
		env := contract.Envelope{Request: contract.Request{AddonID: "sample", Generation: 1, OperationID: "after-restart", Action: "read", Arguments: json.RawMessage(`{}`)}, Authority: contract.Authority{Actor: actor, Scope: "read", ExpiresAt: time.Now().Add(time.Minute)}}
		_, e = current.Client.Read(ctx, env)
		Expect(e).NotTo(HaveOccurred())
		_, e = old.Client.Read(ctx, env)
		Expect(e).To(HaveOccurred())
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
		local, e := addonruntime.NewSupervisor(addonruntime.Config{StateRoot: GinkgoT().TempDir(), Limits: contract.DefaultLimits(), ReadyTimeout: time.Second, StableHealth: time.Second})
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(local.Close(ctx)).To(Succeed()) })
		Expect(forwarder.AttachRuntime(ctx, local)).To(Equal(ErrNotOwner))
		r, e = local.Registry.Snapshot(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(r).To(BeEmpty())
	})
})

var _ = Describe("Restart lifecycle cancellation", func() {
	It("retries runtime synchronization after canceled disable and generation upgrade", func() {
		for _, upgrade := range []bool{false, true} {
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
			m, e := NewManager(ctx, cfg, c, installer, admin)
			Expect(e).NotTo(HaveOccurred())
			DeferCleanup(m.Close)
			m.journal.Addons["sample"] = Status{AddonID: "sample", Installed: true, DesiredEnabled: true, Generation: 1, Generations: []Generation{{Number: 1, Receipt: receipt}}}
			s, e := addonruntime.NewSupervisor(addonruntime.Config{StateRoot: filepath.Join(cfg.Root, "runtime"), Limits: contract.DefaultLimits(), ReadyTimeout: time.Second, StableHealth: time.Second})
			Expect(e).NotTo(HaveOccurred())
			Expect(m.AttachRuntime(ctx, s)).To(Succeed())
			old, e := m.Runtime(ctx, "sample")
			Expect(e).NotTo(HaveOccurred())
			Expect(os.WriteFile(filepath.Join(dir, "fixture.json"), []byte(`{"mode":"never-ready"}`), 0600)).To(Succeed())
			Expect(syscall.Kill(old.PID, syscall.SIGKILL)).To(Succeed())
			var pid int
			Eventually(func() int {
				b, _ := os.ReadFile(filepath.Join(dir, "fixture.pid"))
				pid, _ = strconv.Atoi(string(b))
				return pid
			}, 3*time.Second).Should(And(BeNumerically(">", 0), Not(Equal(old.PID))))
			// Pausing the real replacement ensures cooperative EOF cannot hide overlap.
			Expect(syscall.Kill(pid, syscall.SIGSTOP)).To(Succeed())
			operationCtx, cancelOperation := context.WithCancel(ctx)
			// Cancel only after the durable transition, before runtime cleanup.
			m.boundary = func(phase string) error {
				if phase == "completed" {
					cancelOperation()
				}
				return nil
			}
			var command LifecycleCommand
			if upgrade {
				// Publish a genuine verified package through the catalog and
				// installer, then exercise Submit rather than editing the journal.
				executable, e := os.ReadFile(filepath.Join(dir, "fixture"))
				Expect(e).NotTo(HaveOccurred())
				manifest.Version = "2.0.0"
				manifestBytes, e := json.Marshal(manifest)
				Expect(e).NotTo(HaveOccurred())
				var archive bytes.Buffer
				tw := tar.NewWriter(&archive)
				for name, data := range map[string][]byte{"manifest.json": manifestBytes, "fixture": executable} {
					Expect(tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(data))})).To(Succeed())
					_, e = tw.Write(data)
					Expect(e).NotTo(HaveOccurred())
				}
				Expect(tw.Close()).To(Succeed())
				artifact := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(archive.Bytes()) }))
				DeferCleanup(artifact.Close)
				release := catalog.Release{AddonID: "sample", Name: "Sample", Version: "2.0.0", Artifact: artifact.URL + "/package.tar", Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(archive.Bytes())), OS: manifest.OS, Arch: manifest.Arch}
				library := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_ = catalog.WriteIndex(w, []catalog.Release{release})
				}))
				DeferCleanup(library.Close)
				Expect(c.Sources.Save(ctx, actor, catalog.Source{ID: "upgrade", Name: "Upgrade", URL: library.URL, Enabled: true, AllowLoopbackHTTP: true})).To(Succeed())
				_, e = c.Sources.Refresh(ctx, "upgrade")
				Expect(e).NotTo(HaveOccurred())
				command = LifecycleCommand{OperationID: "upgrade-starting", AddonID: "sample", Action: "upgrade", SourceID: "upgrade", Version: "2.0.0"}
				// The new generation is proven before the journal moves, so
				// no runtime work is left after the durable transition and the
				// canceled context may find nothing to stop. Either outcome
				// must be repeatable under the same operation ID.
				_, _ = m.Submit(operationCtx, actor, command)
				m.boundary = nil
				_, e = m.Submit(ctx, actor, command)
				Expect(e).NotTo(HaveOccurred())
				next, e := m.Runtime(ctx, "sample")
				Expect(e).NotTo(HaveOccurred())
				Expect(next.Generation).To(Equal(uint64(2)))
			} else {
				command = LifecycleCommand{OperationID: "disable-starting", AddonID: "sample", Action: "disable"}
				_, e = m.Submit(operationCtx, actor, command)
				Expect(e).To(HaveOccurred())
				m.boundary = nil
				_, e = m.Submit(ctx, actor, command)
				Expect(e).NotTo(HaveOccurred())
			}
			cancelOperation()
			if !upgrade {
				Expect(m.instances).To(BeEmpty())
			}
			b, e = os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
			if e == nil {
				fields := strings.Fields(string(b)[strings.LastIndex(string(b), ")")+1:])
				Expect(fields[0]).To(Or(Equal("Z"), Equal("X")))
			} else {
				Expect(os.IsNotExist(e)).To(BeTrue())
			}
			Expect(m.Close()).To(Succeed())
		}
	})
})
