//go:build linux

// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the install queue of the add-on manager", func() {
	// publish builds a release whose manifest asks for the given operations,
	// and sets the hint of its index entry to hint (nil: no hint).
	publish := func(r *updateRig, req, opt []string, hint *contract.HostOperations, content string) catalog.Release {
		r.lib.transform = func(m map[string]any) map[string][]byte {
			caps := map[string]any{"service": true}
			if len(req)+len(opt) > 0 {
				caps["hostOperations"] = map[string]any{"required": req, "optional": opt}
			}
			m["capabilities"] = caps
			return nil
		}
		rel := r.lib.publish("1.2.3-dev", "development", content, catalog.Requirements{})
		r.lib.mu.Lock()
		for n := range r.lib.releases {
			if r.lib.releases[n].Digest == rel.Digest {
				r.lib.releases[n].HostOperations = hint
			}
		}
		r.lib.mu.Unlock()
		r.lib.refresh()
		rel.HostOperations = hint
		return rel
	}
	queued := func(id string, ap *HostApproval) LifecycleCommand {
		return LifecycleCommand{OperationID: id, AddonID: "sample", Action: "install", SourceID: "test", Version: "development", Approval: ap}
	}
	phaseOf := func(r *updateRig, id string) (string, string) {
		r.m.mu.Lock()
		defer r.m.mu.Unlock()
		st := r.m.journal.Operations[id].Status
		return st.Phase, st.FailureCode
	}
	stages := func(r *updateRig) []string {
		entries, _ := os.ReadDir(filepath.Join(r.cfg.Root, "packages"))
		var out []string
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "stage-") || strings.HasPrefix(e.Name(), "download-") {
				out = append(out, e.Name())
			}
		}
		return out
	}

	It("answers the host access of a release from its index hint without a download", func() {
		r := newUpdateRig(false)
		hint := &contract.HostOperations{Required: []string{"workspace.materialize"}, Optional: []string{"egress.check"}}
		rel := publish(r, hint.Required, hint.Optional, hint, "a")
		// Without the package the preview could not stage anything.
		r.lib.mu.Lock()
		r.lib.archives = map[string][]byte{}
		r.lib.mu.Unlock()
		out, e := r.m.PreviewRelease(ctx, "test", "sample", "development")
		Expect(e).NotTo(HaveOccurred())
		Expect(out.FromIndex).To(BeTrue())
		Expect(out.Digest).To(Equal(rel.Digest))
		Expect(out.Required).To(Equal([]string{"workspace.materialize"}))
		Expect(out.Optional).To(Equal([]string{"egress.check"}))
	})

	It("stages the package for the host access of a release without an index hint", func() {
		r := newUpdateRig(false)
		publish(r, []string{"workspace.materialize"}, nil, nil, "a")
		out, e := r.m.PreviewRelease(ctx, "test", "sample", "development")
		Expect(e).NotTo(HaveOccurred())
		Expect(out.FromIndex).To(BeFalse())
		Expect(out.Required).To(Equal([]string{"workspace.materialize"}))
	})

	It("records an approved install, runs it later through the lifecycle path and leaves the add-on off", func() {
		r := newUpdateRig(false)
		g := newGrantSim(r.m)
		ops := []string{"workspace.materialize"}
		rel := publish(r, ops, nil, &contract.HostOperations{Required: ops}, "a")
		ap := &HostApproval{Digest: rel.Digest, Ops: ops, FromIndex: true, Reviewed: ops}
		st, e := r.m.AcceptInstall(ctx, actor, queued("q1", ap))
		Expect(e).NotTo(HaveOccurred())
		Expect(st.Phase).To(Equal("accepted"))
		// The same request again is the same operation.
		again, e := r.m.AcceptInstall(ctx, actor, queued("q1", ap))
		Expect(e).NotTo(HaveOccurred())
		Expect(again.OperationID).To(Equal("q1"))
		// Nothing is installed until the queue runs it, and the add-on is
		// held for the queued install.
		s, _ := r.m.Status(ctx, "sample")
		Expect(s.Installed).To(BeFalse())
		_, e = r.m.Submit(ctx, actor, queued("other", ap))
		Expect(e).To(HaveOccurred())

		var mu sync.Mutex
		var phases []string
		var bytes int64
		runCtx := WithInstallProgress(ctx, func(p InstallProgress) {
			mu.Lock()
			defer mu.Unlock()
			if len(phases) == 0 || phases[len(phases)-1] != p.Phase {
				phases = append(phases, p.Phase)
			}
			if p.Phase == InstallPhaseDownloading {
				bytes = p.Bytes
				Expect(p.Cancellable).To(BeTrue())
			}
			if p.Phase == InstallPhaseCommitting {
				Expect(p.Cancellable).To(BeFalse())
			}
		})
		st, e = r.m.RunAccepted(runCtx, actor, "q1")
		Expect(e).NotTo(HaveOccurred())
		Expect(st.Phase).To(Equal("completed"))
		Expect(phases).To(Equal([]string{InstallPhaseResolving, InstallPhaseDownloading, InstallPhaseChecking, InstallPhaseCommitting}))
		Expect(bytes).To(BeNumerically(">", 0))
		s, _ = r.m.Status(ctx, "sample")
		Expect(s.Installed).To(BeTrue())
		Expect(s.DesiredEnabled).To(BeFalse(), "a queued install never turns the add-on on")
		Expect(g.stagedOps("q1")).To(Equal(ops))
		Expect(g.staged["q1"].ExpectedRevision).To(BeZero(), "the queue checks the revision when it accepts, not when it runs")
		// A second run of the same operation installs nothing again.
		st, e = r.m.RunAccepted(ctx, actor, "q1")
		Expect(e).NotTo(HaveOccurred())
		Expect(st.Phase).To(Equal("completed"))
	})

	It("holds a queued install whose verified manifest asks for more than the reviewed index hint", func() {
		r := newUpdateRig(false)
		g := newGrantSim(r.m)
		// The index lists one operation; the package asks for two.
		rel := publish(r, []string{"workspace.materialize"}, []string{"egress.check"}, &contract.HostOperations{Required: []string{"workspace.materialize"}}, "a")
		ap := &HostApproval{Digest: rel.Digest, Ops: []string{"workspace.materialize"}, FromIndex: true, Reviewed: []string{"workspace.materialize"}}
		_, e := r.m.AcceptInstall(ctx, actor, queued("q1", ap))
		Expect(e).NotTo(HaveOccurred())
		_, e = r.m.RunAccepted(ctx, actor, "q1")
		var stale *ApprovalStaleError
		Expect(errors.As(e, &stale)).To(BeTrue())
		Expect(stale.Summary.Optional).To(ContainElement("egress.check"))
		phase, code := phaseOf(r, "q1")
		Expect(phase).To(Equal("failed"))
		Expect(code).To(Equal("approval_stale"))
		Expect(g.staged).To(BeEmpty(), "nothing is granted")
		s, _ := r.m.Status(ctx, "sample")
		Expect(s.Installed).To(BeFalse())
		Expect(stages(r)).To(BeEmpty())
	})

	It("holds the plain install of an approval reviewed from the index hint the same way", func() {
		r := newUpdateRig(false)
		rel := publish(r, []string{"workspace.materialize", "workspace.commit"}, nil, &contract.HostOperations{Required: []string{"workspace.materialize"}}, "a")
		_, e := r.m.Submit(ctx, actor, queued("i1", &HostApproval{Digest: rel.Digest, Ops: []string{"workspace.materialize"}, FromIndex: true, Reviewed: []string{"workspace.materialize"}}))
		var stale *ApprovalStaleError
		Expect(errors.As(e, &stale)).To(BeTrue())
	})

	It("binds an unpinned approval to the reviewed operations only", func() {
		r := newUpdateRig(false)
		g := newGrantSim(r.m)
		ops := []string{"workspace.materialize"}
		publish(r, ops, nil, &contract.HostOperations{Required: ops}, "a")
		_, e := r.m.Submit(ctx, actor, queued("i1", &HostApproval{Digest: "unpinned", Ops: ops, Unpinned: true, FromIndex: true, Reviewed: ops}))
		Expect(e).NotTo(HaveOccurred())
		Expect(g.stagedOps("i1")).To(Equal(ops))

		// Unpinned without a review from the index is never an approval.
		r2 := newUpdateRig(false)
		publish(r2, ops, nil, &contract.HostOperations{Required: ops}, "a")
		_, e = r2.m.Submit(ctx, actor, queued("i2", &HostApproval{Digest: "unpinned", Ops: ops, Unpinned: true}))
		var stale *ApprovalStaleError
		Expect(errors.As(e, &stale)).To(BeTrue())
	})

	It("drops a queued install that is cancelled before it runs and frees the add-on", func() {
		r := newUpdateRig(false)
		rel := publish(r, nil, nil, &contract.HostOperations{}, "a")
		ap := &HostApproval{Digest: rel.Digest, FromIndex: true, Reviewed: []string{}}
		_, e := r.m.AcceptInstall(ctx, actor, queued("q1", ap))
		Expect(e).NotTo(HaveOccurred())
		st, e := r.m.DropAccepted(ctx, actor, "q1")
		Expect(e).NotTo(HaveOccurred())
		Expect(st.Phase).To(Equal("failed"))
		Expect(st.FailureCode).To(Equal(FailureCancelled))
		// A run after the drop installs nothing.
		_, e = r.m.RunAccepted(ctx, actor, "q1")
		Expect(errors.Is(e, ErrOperationFailed)).To(BeTrue())
		s, _ := r.m.Status(ctx, "sample")
		Expect(s.Installed).To(BeFalse())
		// The add-on is free for another install.
		_, e = r.m.AcceptInstall(ctx, actor, queued("q2", ap))
		Expect(e).NotTo(HaveOccurred())
	})

	It("stops a running install when its context is cancelled and leaves no stage behind", func() {
		r := newUpdateRig(false)
		rel := publish(r, nil, nil, &contract.HostOperations{}, "a")
		r.lib.mu.Lock()
		r.lib.archiveDelay = 300 * time.Millisecond
		r.lib.mu.Unlock()
		_, e := r.m.AcceptInstall(ctx, actor, queued("q1", &HostApproval{Digest: rel.Digest, FromIndex: true, Reviewed: []string{}}))
		Expect(e).NotTo(HaveOccurred())
		runCtx, cancel := context.WithCancel(ctx)
		started := make(chan struct{}, 1)
		progressCtx := WithInstallProgress(runCtx, func(p InstallProgress) {
			if p.Phase == InstallPhaseResolving {
				started <- struct{}{}
			}
		})
		done := make(chan error, 1)
		go func() { _, e := r.m.RunAccepted(progressCtx, actor, "q1"); done <- e }()
		Eventually(started).Should(Receive())
		cancel()
		Eventually(done, 5*time.Second).Should(Receive(HaveOccurred()))
		phase, code := phaseOf(r, "q1")
		Expect(phase).To(Equal("failed"))
		Expect(code).To(Equal("interrupted"))
		Expect(stages(r)).To(BeEmpty())
		s, _ := r.m.Status(ctx, "sample")
		Expect(s.Installed).To(BeFalse())
	})

	It("fails a queued install as interrupted after a restart, so it never runs unasked", func() {
		r := newUpdateRig(false)
		rel := publish(r, nil, nil, &contract.HostOperations{}, "a")
		_, e := r.m.AcceptInstall(ctx, actor, queued("q1", &HostApproval{Digest: rel.Digest, FromIndex: true, Reviewed: []string{}}))
		Expect(e).NotTo(HaveOccurred())
		c, i := r.m.catalog, r.m.installer
		Expect(r.m.Close()).To(Succeed())
		second := open(r.cfg, c, i)
		Expect(second.Reconcile(ctx)).To(Succeed())
		st, e := second.OperationStatus(ctx, actor, "q1")
		Expect(e).NotTo(HaveOccurred())
		Expect(st.Phase).To(Equal("failed"))
		Expect(st.FailureCode).To(Equal("interrupted"))
		_, e = second.RunAccepted(ctx, actor, "q1")
		Expect(errors.Is(e, ErrOperationFailed)).To(BeTrue())
		s, _ := second.Status(ctx, "sample")
		Expect(s.Installed).To(BeFalse())
	})

	It("refuses what the queue cannot take: no approval, a local file, an installed add-on, another administrator's run", func() {
		r := newUpdateRig(false)
		rel := publish(r, nil, nil, &contract.HostOperations{}, "a")
		ap := &HostApproval{Digest: rel.Digest, FromIndex: true, Reviewed: []string{}}
		_, e := r.m.AcceptInstall(ctx, actor, queued("q0", nil))
		Expect(errors.Is(e, ErrConflict)).To(BeTrue())
		file := queued("q0", ap)
		file.SourceID = "local-file"
		_, e = r.m.AcceptInstall(ctx, actor, file)
		Expect(errors.Is(e, ErrConflict)).To(BeTrue())

		_, e = r.m.AcceptInstall(ctx, actor, queued("q1", ap))
		Expect(e).NotTo(HaveOccurred())
		other := contract.Actor{UserID: "someone-else", CredentialID: "key", Mode: "named-user"}
		_, e = r.m.RunAccepted(ctx, other, "q1")
		Expect(errors.Is(e, ErrOperationNotFound)).To(BeTrue())
		_, e = r.m.DropAccepted(ctx, other, "q1")
		Expect(errors.Is(e, ErrOperationNotFound)).To(BeTrue())
		_, e = r.m.RunAccepted(ctx, actor, "q1")
		Expect(e).NotTo(HaveOccurred())
		_, e = r.m.AcceptInstall(ctx, actor, queued("q2", ap))
		Expect(errors.Is(e, ErrConflict)).To(BeTrue(), "the add-on is installed")
	})

	It("records a pinned upgrade for the queue, runs it later and keeps the last good generation until then", func() {
		r := newUpdateRig(true)
		installed := r.installChannel().Generations[0].Receipt
		next := r.lib.publish("1.2.3-dev", "development", "build-2", catalog.Requirements{})
		r.lib.refresh()
		cmd := upgrade("qu-1", installed.Digest, next.Digest)
		st, e := r.m.AcceptUpgrade(ctx, actor, cmd)
		Expect(e).NotTo(HaveOccurred())
		Expect(st.Phase).To(Equal("accepted"))
		// Accepting runs nothing, and the same request is the same operation.
		s, _ := r.m.Status(ctx, "sample")
		Expect(s.Generation).To(Equal(uint64(1)))
		again, e := r.m.AcceptUpgrade(ctx, actor, cmd)
		Expect(e).NotTo(HaveOccurred())
		Expect(again.OperationID).To(Equal("qu-1"))
		// The same transition under another operation ID is a conflict.
		_, e = r.m.AcceptUpgrade(ctx, actor, upgrade("qu-2", installed.Digest, next.Digest))
		Expect(e).To(MatchError(ErrTransitionConflict))
		// The status reads while it waits.
		view, e := r.m.OperationStatus(ctx, actor, "qu-1")
		Expect(e).NotTo(HaveOccurred())
		Expect(view.Phase).To(Equal("accepted"))

		st, e = r.m.RunAccepted(ctx, actor, "qu-1")
		Expect(e).NotTo(HaveOccurred())
		Expect(st.Phase).To(Equal("completed"))
		s, _ = r.m.Status(ctx, "sample")
		Expect(s.Generation).To(Equal(uint64(2)))
		Expect(s.Generations[1].Receipt.Digest).To(Equal(next.Digest))
	})

	It("refuses at the run an upgrade whose candidate changed while it waited, and changes nothing", func() {
		r := newUpdateRig(true)
		installed := r.installChannel().Generations[0].Receipt
		next := r.lib.publish("1.2.3-dev", "development", "build-2", catalog.Requirements{})
		r.lib.refresh()
		_, e := r.m.AcceptUpgrade(ctx, actor, upgrade("qu-1", installed.Digest, next.Digest))
		Expect(e).NotTo(HaveOccurred())
		r.lib.publish("1.2.3-dev", "development", "build-3", catalog.Requirements{})
		r.lib.refresh()
		_, e = r.m.RunAccepted(ctx, actor, "qu-1")
		Expect(e).To(HaveOccurred())
		phase, _ := phaseOf(r, "qu-1")
		Expect(phase).To(Equal("failed"))
		s, _ := r.m.Status(ctx, "sample")
		Expect(s.Generation).To(Equal(uint64(1)))
	})

	It("drops a queued upgrade cancelled before it runs, so the add-on can be upgraded again", func() {
		r := newUpdateRig(true)
		installed := r.installChannel().Generations[0].Receipt
		next := r.lib.publish("1.2.3-dev", "development", "build-2", catalog.Requirements{})
		r.lib.refresh()
		_, e := r.m.AcceptUpgrade(ctx, actor, upgrade("qu-1", installed.Digest, next.Digest))
		Expect(e).NotTo(HaveOccurred())
		st, e := r.m.DropAccepted(ctx, actor, "qu-1")
		Expect(e).NotTo(HaveOccurred())
		Expect(st.FailureCode).To(Equal(FailureCancelled))
		s, _ := r.m.Status(ctx, "sample")
		Expect(s.Generation).To(Equal(uint64(1)))
		_, e = r.m.AcceptUpgrade(ctx, actor, upgrade("qu-2", installed.Digest, next.Digest))
		Expect(e).NotTo(HaveOccurred())
	})

	It("records an approved upgrade for the queue, runs it later and drops it when it is cancelled", func() {
		r := newUpdateRig(true)
		installed := r.installChannel().Generations[0].Receipt
		next := r.lib.publish("1.2.3-dev", "development", "build-2", catalog.Requirements{})
		r.lib.refresh()
		ap := &HostApproval{Digest: next.Digest, FromIndex: false}
		approved := func(op string) LifecycleCommand {
			c := upgrade(op, installed.Digest, next.Digest)
			c.SourceID, c.Version, c.Approval = "test", "development", ap
			return c
		}
		// One pin alone is refused, and an install takes no pin.
		onePin := approved("qa-0")
		onePin.CandidateDigest = ""
		_, e := r.m.AcceptInstall(ctx, actor, onePin)
		Expect(errors.Is(e, ErrConflict)).To(BeTrue())
		pinnedInstall := approved("qa-0")
		pinnedInstall.Action = "install"
		_, e = r.m.AcceptInstall(ctx, actor, pinnedInstall)
		Expect(errors.Is(e, ErrConflict)).To(BeTrue())
		wrong := approved("qa-0")
		wrong.ExpectedDigest = "sha256:not-installed"
		_, e = r.m.AcceptInstall(ctx, actor, wrong)
		Expect(e).To(MatchError(ErrStaleInstalled))

		st, e := r.m.AcceptInstall(ctx, actor, approved("qa-1"))
		Expect(e).NotTo(HaveOccurred())
		Expect(st.Phase).To(Equal("accepted"))
		s, _ := r.m.Status(ctx, "sample")
		Expect(s.Generation).To(Equal(uint64(1)), "accepting changes nothing")
		again, e := r.m.AcceptInstall(ctx, actor, approved("qa-1"))
		Expect(e).NotTo(HaveOccurred())
		Expect(again.OperationID).To(Equal("qa-1"))
		_, e = r.m.AcceptInstall(ctx, actor, approved("qa-2"))
		Expect(e).To(MatchError(ErrTransitionConflict), "the same transition under another ID")

		// Cancelled before it ran: failed as cancelled, and the same upgrade can be approved again.
		dropped, e := r.m.DropAccepted(ctx, actor, "qa-1")
		Expect(e).NotTo(HaveOccurred())
		Expect(dropped.FailureCode).To(Equal(FailureCancelled))
		_, e = r.m.AcceptInstall(ctx, actor, approved("qa-3"))
		Expect(e).NotTo(HaveOccurred())

		st, e = r.m.RunAccepted(ctx, actor, "qa-3")
		Expect(e).NotTo(HaveOccurred())
		Expect(st.Phase).To(Equal("completed"))
		s, _ = r.m.Status(ctx, "sample")
		Expect(s.Generation).To(Equal(uint64(2)))
		Expect(s.Generations[1].Receipt.Digest).To(Equal(next.Digest))
	})

	It("takes an approved upgrade without pins, as the install route of the approval does, and refuses it for an add-on that is not installed", func() {
		r := newUpdateRig(true)
		next := r.lib.publish("1.2.3-dev", "development", "build-2", catalog.Requirements{})
		r.lib.refresh()
		cmd := LifecycleCommand{OperationID: "qb-1", AddonID: "sample", Action: "upgrade", SourceID: "test", Version: "development", Approval: &HostApproval{Digest: next.Digest}}
		_, e := r.m.AcceptInstall(ctx, actor, cmd)
		Expect(errors.Is(e, ErrConflict)).To(BeTrue(), "nothing to upgrade yet")
		r.installChannel()
		next = r.lib.publish("1.2.3-dev", "development", "build-3", catalog.Requirements{})
		r.lib.refresh()
		cmd.Approval = &HostApproval{Digest: next.Digest}
		_, e = r.m.AcceptInstall(ctx, actor, cmd)
		Expect(e).NotTo(HaveOccurred())
		st, e := r.m.RunAccepted(ctx, actor, "qb-1")
		Expect(e).NotTo(HaveOccurred())
		Expect(st.Phase).To(Equal("completed"))
		s, _ := r.m.Status(ctx, "sample")
		Expect(s.Generation).To(Equal(uint64(2)))
	})

	// manyAddons installs n add-ons from one library and publishes a newer
	// build of each. It returns the installed digest and the candidate digest
	// of every add-on, by ID.
	manyAddons := func(r *updateRig, n int) (ids []string, installed, candidate map[string]string) {
		installed, candidate = map[string]string{}, map[string]string{}
		for i := range n {
			id := fmt.Sprintf("sample-%d", i)
			ids = append(ids, id)
			rel := r.lib.publishAs(id, "1.2.3-dev", "development", "build-1", catalog.Requirements{})
			installed[id] = rel.Digest
		}
		r.lib.refresh()
		for _, id := range ids {
			_, e := r.m.Submit(ctx, actor, LifecycleCommand{OperationID: "install-" + id, AddonID: id, Action: "install", SourceID: "test", Version: "development"})
			Expect(e).NotTo(HaveOccurred())
		}
		for _, id := range ids {
			candidate[id] = r.lib.publishAs(id, "1.2.3-dev", "development", "build-2", catalog.Requirements{}).Digest
		}
		r.lib.refresh()
		return ids, installed, candidate
	}
	pinned := func(op, id, expected, candidate string) LifecycleCommand {
		return LifecycleCommand{OperationID: op, AddonID: id, Action: "upgrade", ExpectedDigest: expected, CandidateDigest: candidate}
	}

	It("accepts the upgrades of many different add-ons at once and runs every one", func() {
		r := newUpdateRig(true)
		ids, installed, candidate := manyAddons(r, 8)
		errs := make([]error, len(ids))
		var wg sync.WaitGroup
		for i, id := range ids {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				_, errs[i] = r.m.AcceptUpgrade(ctx, actor, pinned("up-"+id, id, installed[id], candidate[id]))
			}()
		}
		wg.Wait()
		for i, e := range errs {
			Expect(e).NotTo(HaveOccurred(), ids[i])
		}
		// The queue runs them one after the other or in parallel; each ends.
		for _, id := range ids {
			st, e := r.m.RunAccepted(ctx, actor, "up-"+id)
			Expect(e).NotTo(HaveOccurred(), id)
			Expect(st.Phase).To(Equal("completed"), id)
			s, _ := r.m.Status(ctx, id)
			Expect(s.Generation).To(Equal(uint64(2)), id)
		}
	})

	It("keeps refusing the same add-on twice, a reused operation ID with another body and a stale request while other add-ons queue", func() {
		r := newUpdateRig(true)
		ids, installed, candidate := manyAddons(r, 3)
		a, b := ids[0], ids[1]
		_, e := r.m.AcceptUpgrade(ctx, actor, pinned("op-a", a, installed[a], candidate[a]))
		Expect(e).NotTo(HaveOccurred())
		// Another add-on is not blocked by it.
		_, e = r.m.AcceptUpgrade(ctx, actor, pinned("op-b", b, installed[b], candidate[b]))
		Expect(e).NotTo(HaveOccurred())
		// The same add-on again: the same transition under another ID, and
		// another transition while the first one is in flight.
		_, e = r.m.AcceptUpgrade(ctx, actor, pinned("op-a2", a, installed[a], candidate[a]))
		Expect(e).To(MatchError(ErrTransitionConflict))
		_, e = r.m.AcceptUpgrade(ctx, actor, pinned("op-a3", a, installed[a], installed[b]))
		Expect(e).To(MatchError(ErrConflict))
		// A reused operation ID with another body.
		_, e = r.m.AcceptUpgrade(ctx, actor, pinned("op-b", ids[2], installed[ids[2]], candidate[ids[2]]))
		Expect(e).To(MatchError(ErrConflict))
		// A stale request: the pin is not the installed package.
		_, e = r.m.AcceptUpgrade(ctx, actor, pinned("op-c", ids[2], candidate[ids[2]], candidate[ids[2]]))
		Expect(e).To(MatchError(ErrStaleInstalled))
	})
})
