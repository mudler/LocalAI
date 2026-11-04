//go:build linux

// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// grantSim stands in for the owner's grants store and for the Authority built
// on it. Like the real one it counts a staged entry only after its operation
// completed, and it asks the manager for that without the manager lock.
type grantSim struct {
	mu        sync.Mutex
	m         *Manager
	staged    map[string]GrantStage
	aborted   []string
	removed   []string
	hint      []bool
	deny      map[string]bool
	blanket   []string
	stageErr  error
	onStage   func()
	reconcil  int
	removeErr error
}

func newGrantSim(m *Manager) *grantSim {
	g := &grantSim{m: m, staged: map[string]GrantStage{}, deny: map[string]bool{}}
	m.SetAuthority(g)
	m.SetGrantStore(g)
	return g
}

func (g *grantSim) Stage(_ context.Context, s GrantStage) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stageErr != nil {
		return g.stageErr
	}
	g.staged[s.OperationID] = s
	if g.onStage != nil {
		g.onStage()
	}
	return nil
}
func (g *grantSim) Abort(_ context.Context, id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.aborted = append(g.aborted, id)
	delete(g.staged, id)
	return nil
}
func (g *grantSim) Remove(_ context.Context, id string, keep bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.removeErr != nil {
		return g.removeErr
	}
	g.removed = append(g.removed, id)
	g.hint = append(g.hint, keep)
	for op, s := range g.staged {
		if s.AddonID == id {
			delete(g.staged, op)
		}
	}
	return nil
}
func (g *grantSim) Reconcile(context.Context, map[string]string, func(string) bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.reconcil++
	return nil
}
func (g *grantSim) Blanket(_ context.Context, q BlanketQuery) ([]string, string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.blanket) == 0 {
		return nil, ""
	}
	return slices.Clone(g.blanket), "blanket:test@rev1"
}
func (g *grantSim) Allows(addon, op string) (bool, hostops.Reason) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.deny[op] {
		return false, hostops.ReasonDeniedByAdmin
	}
	for id, s := range g.staged {
		if s.AddonID == addon && slices.Contains(s.Ops, op) && g.m.OperationCompleted(id) {
			return true, hostops.ReasonNone
		}
	}
	return false, hostops.ReasonNotGranted
}
func (g *grantSim) Snapshot(addon string) hostops.EffectiveSet {
	g.mu.Lock()
	ids := make([]string, 0, len(g.staged))
	for id := range g.staged {
		ids = append(ids, id)
	}
	g.mu.Unlock()
	var set hostops.EffectiveSet
	for _, op := range hostops.SupportedOperations() {
		if ok, _ := g.Allows(addon, op); ok {
			set.Operations = append(set.Operations, op)
		}
	}
	return set
}
func (g *grantSim) Entitled(addon string) bool { return len(g.Snapshot(addon).Operations) > 0 }

func (g *grantSim) stagedOps(opID string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.staged[opID].Ops
}

var _ = Describe("install-time host operation grants in the manager", func() {
	// publish builds a release whose manifest asks for the given operations.
	publish := func(r *updateRig, req, opt []string, content string) catalog.Release {
		r.lib.transform = func(m map[string]any) map[string][]byte {
			m["capabilities"] = map[string]any{"service": true, "hostOperations": map[string]any{"required": req, "optional": opt}}
			return nil
		}
		rel := r.lib.publish("1.2.3-dev", "development", content, catalog.Requirements{})
		r.lib.refresh()
		return rel
	}
	install := func(r *updateRig, id string, ap *HostApproval) (OperationStatus, error) {
		return r.m.Submit(ctx, actor, LifecycleCommand{OperationID: id, AddonID: "sample", Action: "install", SourceID: "test", Version: "development", Approval: ap})
	}
	failureOf := func(r *updateRig, id string) string {
		r.m.mu.Lock()
		defer r.m.mu.Unlock()
		return r.m.journal.Operations[id].Status.FailureCode
	}

	It("refuses an install that needs an operation nobody approved, and says to approve it", func() {
		r := newUpdateRig(false)
		g := newGrantSim(r.m)
		publish(r, []string{"workspace.materialize"}, nil, "a")
		_, e := install(r, "i1", nil)
		var refusal *HostRequirementsRefusal
		Expect(errors.As(e, &refusal)).To(BeTrue())
		Expect(refusal.Blocked()).To(BeFalse())
		Expect(refusal.Error()).To(ContainSubstring("approve host access"))
		Expect(failureOf(r, "i1")).To(Equal("approval_required"))
		st, _ := r.m.Status(ctx, "sample")
		Expect(st.Installed).To(BeFalse())
		Expect(g.staged).To(BeEmpty())
	})

	It("grants only what was approved, declared and registered, and counts it after completion", func() {
		r := newUpdateRig(false)
		g := newGrantSim(r.m)
		rel := publish(r, []string{"workspace.materialize"}, []string{"egress.check", "egress.audit"}, "a")
		_, e := install(r, "i1", &HostApproval{Digest: rel.Digest, Ops: []string{"workspace.materialize", "egress.check", "finetune.start", "no.such.op"}, Revision: 7})
		Expect(e).NotTo(HaveOccurred())
		// The undeclared finetune.start and the unregistered name are dropped;
		// the optional egress.audit was not ticked.
		Expect(g.stagedOps("i1")).To(Equal([]string{"egress.check", "workspace.materialize"}))
		Expect(g.staged["i1"].ExpectedRevision).To(Equal(uint64(7)))
		Expect(g.staged["i1"].Generation).To(Equal(uint64(1)))
		Expect(g.staged["i1"].Identity).To(Equal("disk-one:sample"))
		Expect(r.m.OperationCompleted("i1")).To(BeTrue())
		ok, _ := g.Allows("sample", "workspace.materialize")
		Expect(ok).To(BeTrue())
		ok, _ = g.Allows("sample", "egress.audit")
		Expect(ok).To(BeFalse())
	})

	It("never admits another package with the approval of one digest", func() {
		r := newUpdateRig(false)
		g := newGrantSim(r.m)
		old := publish(r, []string{"workspace.materialize"}, nil, "a")
		publish(r, []string{"workspace.materialize"}, nil, "b")
		_, e := install(r, "i1", &HostApproval{Digest: old.Digest, Ops: []string{"workspace.materialize"}})
		var stale *ApprovalStaleError
		Expect(errors.As(e, &stale)).To(BeTrue())
		Expect(stale.Summary.Required).To(Equal([]string{"workspace.materialize"}))
		Expect(failureOf(r, "i1")).To(Equal("approval_stale"))
		Expect(g.staged).To(BeEmpty())
		st, _ := r.m.Status(ctx, "sample")
		Expect(st.Installed).To(BeFalse())
	})

	It("refuses a required operation that is denied, and installs with a denied optional one degraded", func() {
		r := newUpdateRig(false)
		g := newGrantSim(r.m)
		g.deny["workspace.materialize"] = true
		rel := publish(r, []string{"workspace.materialize"}, nil, "a")
		_, e := install(r, "i1", &HostApproval{Digest: rel.Digest, Ops: []string{"workspace.materialize"}})
		var refusal *HostRequirementsRefusal
		Expect(errors.As(e, &refusal)).To(BeTrue())
		Expect(refusal.Blocked()).To(BeTrue())
		Expect(refusal.Error()).To(ContainSubstring("blocks"))
		Expect(failureOf(r, "i1")).To(Equal("blocked_by_policy"))
		Expect(g.staged).To(BeEmpty(), "a denied operation writes nothing")

		delete(g.deny, "workspace.materialize")
		g.deny["egress.check"] = true
		rel = publish(r, []string{"workspace.materialize"}, []string{"egress.check"}, "b")
		_, e = install(r, "i2", &HostApproval{Digest: rel.Digest, Ops: []string{"workspace.materialize", "egress.check"}})
		Expect(e).NotTo(HaveOccurred())
		Expect(g.stagedOps("i2")).To(Equal([]string{"workspace.materialize"}))
		st, _ := r.m.Status(ctx, "sample")
		Expect(r.m.requests(st.Generations[0].Receipt).DeniedOptional).To(Equal([]string{"egress.check"}))
	})

	It("holds an upgrade that needs more required operations while the old version stays", func() {
		r := newUpdateRig(true)
		g := newGrantSim(r.m)
		v1 := publish(r, []string{"workspace.materialize"}, nil, "a")
		_, e := install(r, "i1", &HostApproval{Digest: v1.Digest, Ops: []string{"workspace.materialize"}})
		Expect(e).NotTo(HaveOccurred())
		v2 := publish(r, []string{"workspace.materialize", "workspace.commit"}, nil, "b")
		up := func(id string, ap *HostApproval) error {
			_, e := r.m.Submit(ctx, actor, LifecycleCommand{OperationID: id, AddonID: "sample", Action: "upgrade", ExpectedDigest: v1.Digest, CandidateDigest: v2.Digest, Approval: ap})
			return e
		}
		e = up("u1", nil)
		var refusal *HostRequirementsRefusal
		Expect(errors.As(e, &refusal)).To(BeTrue())
		Expect(refusal.Summary.MissingRequired).To(Equal([]string{"workspace.commit"}))
		Expect(failureOf(r, "u1")).To(Equal("approval_required"))
		st, _ := r.m.Status(ctx, "sample")
		Expect(st.Generation).To(Equal(uint64(1)))
		Expect(st.Generations[0].Receipt.Digest).To(Equal(v1.Digest))
		ok, _ := g.Allows("sample", "workspace.materialize")
		Expect(ok).To(BeTrue(), "the old grant keeps working")

		Expect(up("u2", &HostApproval{Digest: v2.Digest, Ops: []string{"workspace.commit"}})).To(Succeed())
		Expect(g.stagedOps("u2")).To(Equal([]string{"workspace.commit"}))
		st, _ = r.m.Status(ctx, "sample")
		Expect(st.Generation).To(Equal(uint64(2)))
		ok, _ = g.Allows("sample", "workspace.commit")
		Expect(ok).To(BeTrue())
	})

	It("runs an upgrade with a new optional operation degraded, and shrinks nothing when fewer are asked", func() {
		r := newUpdateRig(true)
		g := newGrantSim(r.m)
		v1 := publish(r, []string{"workspace.materialize", "workspace.commit"}, nil, "a")
		_, e := install(r, "i1", &HostApproval{Digest: v1.Digest, Ops: []string{"workspace.materialize", "workspace.commit"}})
		Expect(e).NotTo(HaveOccurred())
		v2 := publish(r, []string{"workspace.materialize", "workspace.commit"}, []string{"egress.check"}, "b")
		_, e = r.m.Submit(ctx, actor, LifecycleCommand{OperationID: "u1", AddonID: "sample", Action: "upgrade", ExpectedDigest: v1.Digest, CandidateDigest: v2.Digest})
		Expect(e).NotTo(HaveOccurred())
		Expect(g.staged).NotTo(HaveKey("u1"), "nothing is granted without approval")
		ok, _ := g.Allows("sample", "egress.check")
		Expect(ok).To(BeFalse())
		v3 := publish(r, []string{"workspace.materialize"}, nil, "c")
		_, e = r.m.Submit(ctx, actor, LifecycleCommand{OperationID: "u2", AddonID: "sample", Action: "upgrade", ExpectedDigest: v2.Digest, CandidateDigest: v3.Digest})
		Expect(e).NotTo(HaveOccurred())
		ok, _ = g.Allows("sample", "workspace.commit")
		Expect(ok).To(BeTrue(), "an operation the manifest no longer asks for stays")
		Expect(g.removed).To(BeEmpty())
		Expect(g.aborted).To(BeEmpty())
	})

	It("removes the pending entry of an operation that fails after the grants were written", func() {
		r := newUpdateRig(false)
		g := newGrantSim(r.m)
		rel := publish(r, []string{"workspace.materialize"}, nil, "a")
		// The caller goes away right after the grants are written, so the
		// package cannot be committed.
		cctx, cancel := context.WithCancel(ctx)
		g.onStage = cancel
		_, e := r.m.Submit(cctx, actor, LifecycleCommand{OperationID: "i1", AddonID: "sample", Action: "install", SourceID: "test", Version: "development",
			Approval: &HostApproval{Digest: rel.Digest, Ops: []string{"workspace.materialize"}}})
		Expect(e).To(HaveOccurred())
		st, _ := r.m.Status(ctx, "sample")
		Expect(st.Installed).To(BeFalse())
		Expect(g.aborted).To(ConsistOf("i1"))
		Expect(g.staged).NotTo(HaveKey("i1"))
	})

	It("fails the install when the grants cannot be written, and keeps nothing", func() {
		r := newUpdateRig(false)
		g := newGrantSim(r.m)
		g.stageErr = errors.New("disk full")
		rel := publish(r, []string{"workspace.materialize"}, nil, "a")
		_, e := install(r, "i1", &HostApproval{Digest: rel.Digest, Ops: []string{"workspace.materialize"}})
		Expect(e).To(MatchError(ErrOperationFailed))
		st, _ := r.m.Status(ctx, "sample")
		Expect(st.Installed).To(BeFalse())
		g.stageErr = ErrGrantsConflict
		_, e = install(r, "i2", &HostApproval{Digest: rel.Digest, Ops: []string{"workspace.materialize"}})
		var stale *ApprovalStaleError
		Expect(errors.As(e, &stale)).To(BeTrue())
	})

	It("deletes the grants of an add-on when it is uninstalled", func() {
		r := newUpdateRig(false)
		g := newGrantSim(r.m)
		rel := publish(r, []string{"workspace.materialize"}, nil, "a")
		_, e := install(r, "i1", &HostApproval{Digest: rel.Digest, Ops: []string{"workspace.materialize"}})
		Expect(e).NotTo(HaveOccurred())
		_, e = r.m.Submit(ctx, actor, LifecycleCommand{OperationID: "x1", AddonID: "sample", Action: "uninstall"})
		Expect(e).NotTo(HaveOccurred())
		Expect(g.removed).To(Equal([]string{"sample"}))
		Expect(g.hint).To(Equal([]bool{false}))
		ok, _ := g.Allows("sample", "workspace.materialize")
		Expect(ok).To(BeFalse())
		// A reinstall under the same ID starts with nothing.
		_, e = install(r, "i2", nil)
		Expect(e).To(HaveOccurred())
	})

	It("keeps the add-on installed and its grants when the grants cannot be deleted at uninstall", func() {
		r := newUpdateRig(false)
		g := newGrantSim(r.m)
		rel := publish(r, []string{"workspace.materialize"}, nil, "a")
		_, e := install(r, "i1", &HostApproval{Digest: rel.Digest, Ops: []string{"workspace.materialize"}})
		Expect(e).NotTo(HaveOccurred())
		g.mu.Lock()
		g.removeErr = errors.New("store is down")
		g.mu.Unlock()
		_, e = r.m.Submit(ctx, actor, LifecycleCommand{OperationID: "x1", AddonID: "sample", Action: "uninstall"})
		Expect(e).To(HaveOccurred())
		st, _ := r.m.Status(ctx, "sample")
		Expect(st.Installed).To(BeTrue(), "the uninstall failed closed")
		// The same operation can be repeated once the store works.
		g.mu.Lock()
		g.removeErr = nil
		g.mu.Unlock()
		_, e = r.m.Submit(ctx, actor, LifecycleCommand{OperationID: "x2", AddonID: "sample", Action: "uninstall"})
		Expect(e).NotTo(HaveOccurred())
		st, _ = r.m.Status(ctx, "sample")
		Expect(st.Installed).To(BeFalse())
		Expect(g.removed).To(Equal([]string{"sample"}))
	})

	It("holds an upgrade that comes from another library or signer until it is approved", func() {
		for _, change := range []string{"library", "signer"} {
			r := newUpdateRig(false)
			g := newGrantSim(r.m)
			v1 := publish(r, []string{"workspace.materialize"}, nil, "a")
			_, e := install(r, "i1", &HostApproval{Digest: v1.Digest, Ops: []string{"workspace.materialize"}})
			Expect(e).NotTo(HaveOccurred())
			// The installed package now reads as installed from elsewhere, so the
			// next one resolves to a different library or signer.
			r.m.mu.Lock()
			st := r.m.journal.Addons["sample"]
			for i := range st.Generations {
				if change == "library" {
					st.Generations[i].Receipt.SourceID = "elsewhere"
				} else {
					st.Generations[i].Receipt.Verification = "signed-by-someone-else"
				}
			}
			r.m.journal.Addons["sample"] = st
			r.m.mu.Unlock()
			v2 := publish(r, []string{"workspace.materialize"}, nil, "b")
			up := func(id string, ap *HostApproval) error {
				_, e := r.m.Submit(ctx, actor, LifecycleCommand{OperationID: id, AddonID: "sample", Action: "upgrade", SourceID: "test", Version: "development", Approval: ap})
				return e
			}
			e = up("u1", nil)
			var refusal *HostRequirementsRefusal
			Expect(errors.As(e, &refusal)).To(BeTrue(), change)
			Expect(refusal.SourceChanged).To(BeTrue())
			Expect(failureOf(r, "u1")).To(Equal("approval_required"))
			now, _ := r.m.Status(ctx, "sample")
			Expect(now.Generation).To(Equal(uint64(1)), change+": the old version keeps running")
			ok, _ := g.Allows("sample", "workspace.materialize")
			Expect(ok).To(BeTrue())

			Expect(up("u2", &HostApproval{Digest: v2.Digest})).To(Succeed(), change+": a person approved this package")
			now, _ = r.m.Status(ctx, "sample")
			Expect(now.Generation).To(Equal(uint64(2)))
		}
	})

	It("never approves in an automatic upgrade, and does not retry the same candidate", func() {
		r := newUpdateRig(true)
		g := newGrantSim(r.m)
		v1 := publish(r, []string{"workspace.materialize"}, nil, "a")
		_, e := install(r, "i1", &HostApproval{Digest: v1.Digest, Ops: []string{"workspace.materialize"}})
		Expect(e).NotTo(HaveOccurred())
		publish(r, []string{"workspace.materialize", "workspace.commit"}, nil, "b")
		res := r.m.AutoUpgrade(ctx)
		Expect(res).To(HaveLen(1))
		Expect(res[0].Upgraded).To(BeFalse())
		Expect(res[0].HeldForApproval).To(BeTrue())
		st, _ := r.m.Status(ctx, "sample")
		Expect(st.Generation).To(Equal(uint64(1)))
		Expect(g.staged).To(HaveLen(1))
		res = r.m.AutoUpgrade(ctx)
		Expect(res).To(HaveLen(1))
		Expect(res[0].SkippedAsFailure).To(BeTrue())
	})

	It("lets the blanket switch grant the required operations of an automatic upgrade", func() {
		r := newUpdateRig(true)
		g := newGrantSim(r.m)
		v1 := publish(r, []string{"workspace.materialize"}, nil, "a")
		_, e := install(r, "i1", &HostApproval{Digest: v1.Digest, Ops: []string{"workspace.materialize"}})
		Expect(e).NotTo(HaveOccurred())
		g.blanket = []string{"workspace.commit", "finetune.start"}
		publish(r, []string{"workspace.materialize", "workspace.commit"}, nil, "b")
		res := r.m.AutoUpgrade(ctx)
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).NotTo(HaveOccurred())
		Expect(res[0].Upgraded).To(BeTrue())
		var staged GrantStage
		for _, s := range g.staged {
			if s.Action == "upgrade" {
				staged = s
			}
		}
		Expect(staged.Ops).To(Equal([]string{"workspace.commit"}), "only what the manifest declares")
		Expect(staged.Blanket).To(Equal("blanket:test@rev1"))
	})

	It("never lets the blanket switch grant a denied operation", func() {
		r := newUpdateRig(false)
		g := newGrantSim(r.m)
		g.deny["workspace.materialize"] = true
		g.blanket = []string{"workspace.materialize"}
		publish(r, []string{"workspace.materialize"}, nil, "a")
		_, e := install(r, "i1", nil)
		Expect(e).To(HaveOccurred())
		Expect(failureOf(r, "i1")).To(Equal("blocked_by_policy"))
		Expect(g.staged).To(BeEmpty())
	})

	It("tells the Apps listing which operations are held and which are denied", func() {
		r := newUpdateRig(false)
		g := newGrantSim(r.m)
		rel := publish(r, []string{"workspace.materialize"}, []string{"egress.check", "egress.audit"}, "a")
		_, e := install(r, "i1", &HostApproval{Digest: rel.Digest, Ops: []string{"workspace.materialize"}})
		Expect(e).NotTo(HaveOccurred())
		_, e = r.m.Submit(ctx, actor, LifecycleCommand{OperationID: "en", AddonID: "sample", Action: "enable"})
		Expect(e).NotTo(HaveOccurred())
		g.mu.Lock()
		g.deny["egress.check"] = true
		g.mu.Unlock()
		apps, e := r.m.Apps(ctx, actor)
		Expect(e).NotTo(HaveOccurred())
		Expect(apps).To(HaveLen(1))
		h := apps[0].Admin.HostOperations
		Expect(h.Granted).To(Equal([]string{"workspace.materialize"}))
		Expect(h.MissingOptional).To(Equal([]string{"egress.audit", "egress.check"}))
		Expect(h.DeniedOptional).To(Equal([]string{"egress.check"}))
	})

	It("keeps a deployment that grants the prefix in the policy file working as before", func() {
		r := newUpdateRig(false)
		r.m.SetHostOps(map[string][]string{"sample": {"workspace."}})
		publish(r, []string{"workspace.materialize"}, nil, "a")
		_, e := install(r, "i1", nil)
		Expect(e).NotTo(HaveOccurred())
	})

	It("is safe to read the grants while installs and changes run", func() {
		r := newUpdateRig(false)
		g := newGrantSim(r.m)
		rel := publish(r, []string{"workspace.materialize"}, nil, "a")
		var wg sync.WaitGroup
		stop := make(chan struct{})
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
						_, _ = r.m.Apps(ctx, actor)
						_ = r.m.OperationCompleted("i1")
						_, _ = g.Allows("sample", "workspace.materialize")
					}
				}
			}()
		}
		_, e := install(r, "i1", &HostApproval{Digest: rel.Digest, Ops: []string{"workspace.materialize"}})
		Expect(e).NotTo(HaveOccurred())
		Expect(r.m.HostGrantsChanged(ctx)).To(Succeed())
		close(stop)
		wg.Wait()
	})

	It("stops a running add-on that loses a required operation and starts it again on a grant", func() {
		r := newUpdateRig(false)
		g := newGrantSim(r.m)
		r.m.installer.Host.SDK = 2
		binary := filepath.Join(GinkgoT().TempDir(), "fixture")
		out, e := exec.Command("go", "build", "-o", binary, "../../../tests/addons/fixture/cmd").CombinedOutput()
		Expect(e).NotTo(HaveOccurred(), string(out))
		executable, e := os.ReadFile(binary)
		Expect(e).NotTo(HaveOccurred())
		r.lib.transform = func(m map[string]any) map[string][]byte {
			m["capabilities"] = map[string]any{"service": true, "hostOperations": map[string]any{"required": []string{"workspace.materialize"}}}
			return map[string][]byte{"bin/sample": executable, "fixture.json": []byte(`{"mode":"normal"}`)}
		}
		rel := r.lib.publish("1.2.3-dev", "development", "live", catalog.Requirements{})
		r.lib.refresh()
		s, e := addonruntime.NewSupervisor(addonruntime.Config{StateRoot: filepath.Join(r.cfg.Root, "runtime"), Limits: contract.DefaultLimits(), ReadyTimeout: time.Second, StableHealth: time.Second})
		Expect(e).NotTo(HaveOccurred())
		Expect(r.m.AttachRuntime(ctx, s)).To(Succeed())
		_, e = install(r, "i1", &HostApproval{Digest: rel.Digest, Ops: []string{"workspace.materialize"}})
		Expect(e).NotTo(HaveOccurred())
		_, e = r.m.Submit(ctx, actor, LifecycleCommand{OperationID: "en", AddonID: "sample", Action: "enable"})
		Expect(e).NotTo(HaveOccurred())
		_, e = r.m.Runtime(ctx, "sample")
		Expect(e).NotTo(HaveOccurred())

		g.mu.Lock()
		g.deny["workspace.materialize"] = true
		g.mu.Unlock()
		started, stopped, e := r.m.HostGrantsChangedReport(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(started).To(BeEmpty())
		Expect(stopped).To(Equal([]string{"sample"}), "the report names the add-on that stopped")
		_, e = r.m.Runtime(ctx, "sample")
		Expect(e).To(HaveOccurred(), "the add-on stopped when its required operation was denied")
		r.m.mu.Lock()
		Expect(r.m.instances).NotTo(HaveKey("sample"))
		r.m.mu.Unlock()
		_, e = r.m.Submit(ctx, actor, LifecycleCommand{OperationID: "en2", AddonID: "sample", Action: "enable"})
		Expect(e).To(HaveOccurred(), "enabling is refused while the operation is denied")

		g.mu.Lock()
		delete(g.deny, "workspace.materialize")
		g.mu.Unlock()
		started, stopped, e = r.m.HostGrantsChangedReport(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(started).To(Equal([]string{"sample"}))
		Expect(stopped).To(BeEmpty())
		_, e = r.m.Runtime(ctx, "sample")
		Expect(e).NotTo(HaveOccurred(), "the add-on starts again once the operation is allowed")
	})
})
