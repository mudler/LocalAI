// SPDX-License-Identifier: MIT
package executionbridge

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/agentruntime/ownership"
	"github.com/mudler/LocalAI/core/services/agentruntime/toolbroker"
	chat "github.com/mudler/nib/chat"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func configureApproval(f *shellFixture, wait func(context.Context, Control) error) {
	base := f.dep.Resolve
	f.dep.Resolve = func(ctx context.Context, s Scope, c HostCandidate) (Registration, error) {
		r, e := base(ctx, s, c)
		r.Authority.Policy = toolbroker.RequireApproval
		return r, e
	}
	f.dep.Waiting = wait
	f.dep.Approvals = func(_ context.Context, d toolbroker.ApprovalDecision) (toolbroker.Approval, error) {
		a := d.Approval
		if a.Binding.RequestID() == "" || a.ActorID != "trusted" || (a.Actor != toolbroker.HumanActor && a.Actor != toolbroker.HostActor) {
			return toolbroker.Approval{}, ErrHold
		}
		a.ExpiresAt = time.Now().Add(10 * time.Second).UnixNano()
		return a, nil
	}
	b, e := New(f.scope, f.dep)
	Expect(e).NotTo(HaveOccurred())
	f.b = b
	DeferCleanup(b.Close)
}

type failCoordinator struct {
	Coordinator
	complete, admit bool
}

func (m failCoordinator) Complete(ctx context.Context, l ownership.Lease, n time.Time, c ownership.ToolCall, v []byte) (ownership.ToolRecord, error) {
	if m.complete {
		return ownership.ToolRecord{}, ErrHold
	}
	return m.Coordinator.Complete(ctx, l, n, c, v)
}
func (m failCoordinator) AdmitExecution(ctx context.Context, l ownership.Lease, n time.Time, v ownership.ExecutionMetadata) (ownership.ExecutionAdmission, error) {
	if m.admit {
		return ownership.ExecutionAdmission{}, ErrHold
	}
	return m.Coordinator.AdmitExecution(ctx, l, n, v)
}

var _ = Describe("Host approval controls and failure gates", func() {
	It("requires a separately authenticated exact host approval despite native auto approval", func() {
		f := newShellFixture()
		var handle Control
		configureApproval(f, func(ctx context.Context, c Control) error {
			handle = c
			return c.Decide(ctx, toolbroker.HumanActor, "trusted", true)
		})
		f.run(context.Background(), nil)
		Expect(f.count.Load()).To(Equal(int32(1)))
		Expect(f.records()[0].Status).To(Equal(ownership.Completed))
		Expect(handle.Decide(context.Background(), toolbroker.HumanActor, "trusted", true)).To(MatchError(ErrHold))
	})
	It("denies ordinary native approval as a broker grant", func() {
		f := newShellFixture()
		configureApproval(f, func(ctx context.Context, c Control) error {
			return c.Decide(ctx, toolbroker.HumanActor, "untrusted", true)
		})
		f.run(context.Background(), nil)
		Expect(f.count.Load()).To(BeZero())
	})
	It("rejects invalid actor before the trusted recorder", func() {
		f := newShellFixture()
		configureApproval(f, func(ctx context.Context, c Control) error { return c.Decide(ctx, 0, "trusted", true) })
		f.run(context.Background(), nil)
		Expect(f.count.Load()).To(BeZero())
	})
	It("does not revive a denied exact handle", func() {
		f := newShellFixture()
		var h Control
		configureApproval(f, func(ctx context.Context, c Control) error {
			h = c
			return c.Decide(ctx, toolbroker.HumanActor, "trusted", false)
		})
		f.run(context.Background(), nil)
		Expect(f.count.Load()).To(BeZero())
		Expect(h.Decide(context.Background(), toolbroker.HumanActor, "trusted", true)).To(MatchError(ErrHold))
	})
	It("cancels a waiting candidate with no body and no late approval revival", func() {
		f := newShellFixture()
		var h Control
		configureApproval(f, func(ctx context.Context, c Control) error { h = c; return c.Cancel(ctx) })
		f.run(context.Background(), nil)
		Expect(f.count.Load()).To(BeZero())
		Expect(h.Decide(context.Background(), toolbroker.HostActor, "trusted", true)).To(MatchError(ErrHold))
	})
	It("expires waiting approval using the trusted clock", func() {
		f := newShellFixture()
		var expired bool
		now := time.Now().UTC()
		f.dep.Clock = func() time.Time {
			if expired {
				return now.Add(2 * time.Minute)
			}
			return now
		}
		configureApproval(f, func(context.Context, Control) error { expired = true; return nil })
		f.run(context.Background(), nil)
		Expect(f.count.Load()).To(BeZero())
	})
	It("closes during approval notification without lock reentry deadlock", func() {
		f := newShellFixture()
		configureApproval(f, func(context.Context, Control) error { f.b.Close(); return nil })
		f.run(context.Background(), nil)
		Expect(f.count.Load()).To(BeZero())
	})
	It("supports body reentry into bridge controls without a shell lock", func() {
		f := newShellFixture()
		f.run(context.Background(), func(bridge chat.ToolExecutionBridge) chat.ToolExecutionBridge {
			return func(ctx context.Context, c chat.ToolExecutionCall, body chat.ToolExecutionBody) (chat.ToolExecutionResult, error) {
				return bridge(ctx, c, func(ctx context.Context) (chat.ToolExecutionResult, error) {
					Expect(f.b.live()).To(BeTrue())
					return body(ctx)
				})
			}
		})
		Expect(f.records()[0].Status).To(Equal(ownership.Completed))
	})
	It("holds journal completion failure with no false publication or reexecution", func() {
		f := newShellFixture()
		f.dep.Manager = failCoordinator{Coordinator: f.manager, complete: true}
		b, e := New(f.scope, f.dep)
		Expect(e).NotTo(HaveOccurred())
		f.b = b
		f.run(context.Background(), nil)
		Expect(f.records()[0].Status).To(Equal(ownership.Dispatched))
		Expect(f.output).NotTo(ContainSubstring("authorized-substitution"))
		_, e = f.duplicate()
		Expect(e).To(MatchError(ErrHold))
		Expect(f.count.Load()).To(Equal(int32(1)))
	})
	It("holds admission failure before any native body or journal write", func() {
		f := newShellFixture()
		f.dep.Manager = failCoordinator{Coordinator: f.manager, admit: true}
		b, e := New(f.scope, f.dep)
		Expect(e).NotTo(HaveOccurred())
		f.b = b
		f.run(context.Background(), nil)
		Expect(f.count.Load()).To(BeZero())
		Expect(f.records()).To(BeEmpty())
	})
	It("cancels the actual in-process MCP handler and keeps unresolved dispatch held", func() {
		f := newShellFixture()
		f.mcpCancel = true
		f.mcpEntered = make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			select {
			case <-f.mcpEntered:
				cancel()
			case <-time.After(5 * time.Second):
				cancel()
			}
		}()
		f.run(ctx, nil)
		Expect(f.count.Load()).To(Equal(int32(1)))
		Expect(f.records()[0].Status).To(Equal(ownership.Dispatched))
		Expect(f.output).NotTo(ContainSubstring("private-native"))
	})
	It("returns the same completed envelope bytes on reconstruction replay", func() {
		f := newShellFixture()
		f.run(context.Background(), nil)
		before := f.records()[0].Result
		f.rebuild()
		_, e := f.duplicate()
		Expect(e).NotTo(HaveOccurred())
		Expect(f.records()[0].Result).To(Equal(before))
	})
	It("arbitrates fresh concurrent duplicate native candidates across separate pools", func() {
		f := newShellFixture()
		f.run(context.Background(), func(bridge chat.ToolExecutionBridge) chat.ToolExecutionBridge {
			return func(ctx context.Context, c chat.ToolExecutionCall, body chat.ToolExecutionBody) (chat.ToolExecutionResult, error) {
				m, s, _ := openShellDB(f.path)
				dep := f.dep
				dep.Manager = m
				dep.Reader = s
				other, e := New(f.scope, dep)
				Expect(e).NotTo(HaveOccurred())
				defer other.Close()
				var wg sync.WaitGroup
				wg.Add(1)
				go func() {
					defer GinkgoRecover()
					defer wg.Done()
					_, _ = other.Execution()(ctx, c, func(context.Context) (chat.ToolExecutionResult, error) {
						f.count.Add(1)
						return chat.ToolExecutionResult{Text: "duplicate-private"}, nil
					})
				}()
				r, e := bridge(ctx, c, body)
				wg.Wait()
				return r, e
			}
		})
		Expect(f.count.Load()).To(Equal(int32(1)))
		Expect(f.records()).To(HaveLen(1))
		Expect(f.records()[0].Status).To(Equal(ownership.Completed))
	})
	It("recovers an explicit origin under a new attempt using original codec provenance", func() {
		f := newShellFixture()
		f.run(context.Background(), nil)
		args, e := json.Marshal(f.native.Arguments())
		Expect(e).NotTo(HaveOccurred())
		target, e := candidate(f.native.InvocationID(), f.native.ToolCallID(), f.native.AgentID(), f.native.Name(), args)
		Expect(e).NotTo(HaveOccurred())
		c, e := f.b.prepare(context.Background(), target)
		Expect(e).NotTo(HaveOccurred())
		Expect(f.b.admit(context.Background(), &c)).To(Succeed())
		origin, e := NewOrigin(c.admission, c.call, c.registration.Provenance, c.registration.Projection)
		Expect(e).NotTo(HaveOccurred())
		scope := f.scope
		scope.Identity.Attempt++
		dep := f.dep
		base := dep.Resolve
		dep.Resolve = func(ctx context.Context, s Scope, c HostCandidate) (Registration, error) {
			r, e := base(ctx, s, c)
			r.Projection = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
			return r, e
		}
		b, e := New(scope, dep)
		Expect(e).NotTo(HaveOccurred())
		defer b.Close()
		text, e := b.Recover(context.Background(), target, origin)
		Expect(e).NotTo(HaveOccurred())
		Expect(text).To(Equal("authorized-substitution"))
		Expect(f.count.Load()).To(Equal(int32(1)))
	})
	It("requires separate origin authorization without automatic replay fallback", func() {
		f := newShellFixture()
		f.dep.AuthorizeOrigin = func(context.Context, HostCandidate, ownership.ExecutionAdmission) error { return ErrHold }
		b, e := New(f.scope, f.dep)
		Expect(e).NotTo(HaveOccurred())
		_, e = b.Recover(context.Background(), HostCandidate{}, Origin{})
		Expect(e).To(MatchError(ErrHold))
		Expect(f.count.Load()).To(BeZero())
	})
})
