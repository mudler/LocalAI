// SPDX-License-Identifier: MIT
package executionbridge

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mudler/LocalAI/core/services/agentruntime/ownership"
	chat "github.com/mudler/nib/chat"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func shellOrigin(f *shellFixture) (HostCandidate, Origin) {
	args, e := json.Marshal(f.native.Arguments())
	Expect(e).NotTo(HaveOccurred())
	target, e := candidate(f.native.InvocationID(), f.native.ToolCallID(), f.native.AgentID(), f.native.Name(), args)
	Expect(e).NotTo(HaveOccurred())
	c, e := f.b.prepare(context.Background(), target)
	Expect(e).NotTo(HaveOccurred())
	Expect(f.b.admit(context.Background(), &c)).To(Succeed())
	origin, e := NewOrigin(c.admission, c.call, c.registration.Provenance, c.registration.Projection)
	Expect(e).NotTo(HaveOccurred())
	return target, origin
}

var _ = Describe("Host late publication boundaries", func() {
	for _, route := range []string{"fresh", "recorded", "recovery"} {
		for _, change := range []string{"cancel", "close", "registration", "lease", "revision", "turn"} {
			route, change := route, change
			It(route+" holds after Release changes "+change, func() {
				f := newShellFixture()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var target HostCandidate
				var origin Origin
				if route != "fresh" {
					f.run(context.Background(), nil)
					target, origin = shellOrigin(f)
				}
				calls := 0
				f.b.dep.Release = func(context.Context, HostCandidate, ownership.ExecutionAdmission) error {
					calls++
					if route != "recovery" && calls != 2 {
						return nil
					}
					switch change {
					case "cancel":
						cancel()
					case "close":
						f.b.Close()
					case "registration":
						f.resolveFail.Store(true)
					case "lease":
						Expect(f.manager.Release(context.Background(), f.scope.Lease, time.Now().UTC())).To(Succeed())
					case "revision":
						Expect(f.db.Table("runtime_conversations").Where("principal = ? AND conversation = ?", "alice", "chat").Update("revision", 2).Error).To(Succeed())
					case "turn":
						Expect(f.db.Table("runtime_conversations").Where("principal = ? AND conversation = ?", "alice", "chat").Update("running_turn", "other").Error).To(Succeed())
					}
					return nil
				}
				if route == "fresh" {
					f.run(ctx, nil)
					Expect(f.output).NotTo(ContainSubstring("authorized-substitution"))
				} else if route == "recorded" {
					result, e := f.b.Execution()(ctx, f.native, func(context.Context) (chat.ToolExecutionResult, error) {
						Fail("recorded body invoked")
						return chat.ToolExecutionResult{}, nil
					})
					Expect(e).To(MatchError(ErrHold))
					Expect(result.Text).To(BeEmpty())
				} else {
					text, e := f.b.Recover(ctx, target, origin)
					Expect(e).To(MatchError(ErrHold))
					Expect(text).To(BeEmpty())
				}
				Expect(f.count.Load()).To(Equal(int32(1)))
				Expect(calls).To(Equal(map[bool]int{true: 1, false: 2}[route == "recovery"]))
			})
		}
	}
	It("rechecks exact origin-target relation after Release", func() {
		f := newShellFixture()
		f.run(context.Background(), nil)
		target, origin := shellOrigin(f)
		revoked := false
		checks := 0
		f.b.dep.Release = func(_ context.Context, c HostCandidate, a ownership.ExecutionAdmission) error {
			Expect(c.InvocationID()).To(Equal(target.InvocationID()))
			ref, _, _, _ := origin.open()
			Expect(a.Digest()).To(Equal(ref.Digest()))
			revoked = true
			return nil
		}
		f.b.dep.AuthorizeOrigin = func(context.Context, HostCandidate, ownership.ExecutionAdmission) error {
			checks++
			if revoked {
				return ErrHold
			}
			return nil
		}
		text, e := f.b.Recover(context.Background(), target, origin)
		Expect(e).To(MatchError(ErrHold))
		Expect(text).To(BeEmpty())
		Expect(checks).To(Equal(2))
		Expect(f.count.Load()).To(Equal(int32(1)))
	})
	It("builds only witnessed stage facts and refreshes every current guard", func() {
		f := newShellFixture()
		target, e := candidate("i", "p", "", "counter", []byte("{}"))
		Expect(e).NotTo(HaveOccurred())
		c, e := f.b.prepare(context.Background(), target)
		Expect(e).NotTo(HaveOccurred())
		facts := f.b.facts(context.Background(), c, shellEvidence{})
		Expect(facts.Durable).To(BeFalse())
		Expect(facts.Normal).To(BeFalse())
		Expect(facts.Projected).To(BeFalse())
		Expect(facts.ReleaseAuthorized).To(BeFalse())
		Expect(facts.OriginAuthorized).To(BeFalse())
		Expect(facts.OriginCompleted).To(BeFalse())
		Expect(facts.ExactOrigin).To(BeFalse())
		Expect(f.b.admit(context.Background(), &c)).To(Succeed())
		Expect(f.b.facts(context.Background(), c, shellEvidence{}).Durable).To(BeTrue())
		f.resolveFail.Store(true)
		Expect(f.b.facts(context.Background(), c, shellEvidence{}).Authorized).To(BeFalse())
	})
})

var _ = Describe("Host final dependency guards", func() {
	It("samples cancellation after a reentrant resolver returns", func() {
		f := newShellFixture()
		f.run(context.Background(), nil)
		target, origin := shellOrigin(f)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		base := f.b.dep.Resolve
		armed := false
		f.b.dep.Release = func(context.Context, HostCandidate, ownership.ExecutionAdmission) error { armed = true; return nil }
		f.b.dep.Resolve = func(ctx context.Context, s Scope, c HostCandidate) (Registration, error) {
			r, e := base(ctx, s, c)
			if armed {
				cancel()
			}
			return r, e
		}
		text, e := f.b.Recover(ctx, target, origin)
		Expect(e).To(MatchError(ErrHold))
		Expect(text).To(BeEmpty())
	})
	It("samples bridge closure after a reentrant resolver returns", func() {
		f := newShellFixture()
		f.run(context.Background(), nil)
		target, origin := shellOrigin(f)
		base := f.b.dep.Resolve
		armed := false
		f.b.dep.Release = func(context.Context, HostCandidate, ownership.ExecutionAdmission) error { armed = true; return nil }
		f.b.dep.Resolve = func(ctx context.Context, s Scope, c HostCandidate) (Registration, error) {
			r, e := base(ctx, s, c)
			if armed {
				f.b.Close()
			}
			return r, e
		}
		text, e := f.b.Recover(context.Background(), target, origin)
		Expect(e).To(MatchError(ErrHold))
		Expect(text).To(BeEmpty())
	})
})
