// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// leaseRecorder is an owner-side ShareAuthority that records what it was
// asked. lost ends the next watch as lost.
type leaseRecorder struct {
	mu    sync.Mutex
	calls []string
	lost  chan struct{}
	err   error
}

func (l *leaseRecorder) record(s string) {
	l.mu.Lock()
	l.calls = append(l.calls, s)
	l.mu.Unlock()
}

func (l *leaseRecorder) seen() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.calls...)
}

func (l *leaseRecorder) ShareViews(_ context.Context, _ contract.Actor, kind string) (ShareViews, error) {
	l.record("views " + kind)
	return ShareViews{Kind: kind, Views: []ShareView{{ID: "r1", Role: "use", OwnerID: "alice"}}}, l.err
}

func (l *leaseRecorder) CreateShare(_ context.Context, _ contract.Actor, kind string) (ShareRef, error) {
	l.record("create " + kind)
	return ShareRef{ResourceID: "r1"}, l.err
}

func (l *leaseRecorder) AdmitShare(_ context.Context, _ contract.Actor, kind, id, action string) (ShareLease, error) {
	l.record("admit " + kind + " " + id + " " + action)
	return ShareLease{Lease: "l1", WatchMillis: 50}, l.err
}

func (l *leaseRecorder) WatchShare(ctx context.Context, _ contract.Actor, lease string, stopping bool) (ShareLeaseState, error) {
	if stopping {
		l.record("renew " + lease)
	} else {
		l.record("watch " + lease)
	}
	if stopping {
		select {
		case <-ctx.Done():
		case <-time.After(50 * time.Millisecond):
		}
		return ShareLeaseState{Lost: true}, nil
	}
	select {
	case <-l.lost:
		return ShareLeaseState{Lost: true}, nil
	case <-time.After(50 * time.Millisecond):
		return ShareLeaseState{}, nil
	}
}

func (l *leaseRecorder) ReleaseShare(_ context.Context, _ contract.Actor, lease string) error {
	l.record("release " + lease)
	return nil
}

func (l *leaseRecorder) EnsureShare(_ context.Context, _ contract.Actor, kind, local string) (ShareRef, error) {
	l.record("ensure " + kind + " " + local)
	return ShareRef{ResourceID: "r2"}, l.err
}

func (l *leaseRecorder) ForgetShare(_ context.Context, _ contract.Actor, kind, id string) error {
	l.record("forget " + kind + " " + id)
	return l.err
}

var _ = Describe("the lease calls of a kind that is not an add-on", func() {
	actor := contract.Actor{UserID: "bob", CredentialID: "chats", Mode: "named-user"}
	allow := func(context.Context, contract.Actor) error { return nil }
	ctx := context.Background()

	It("run the work under the owner's lease, stop it when the owner says the lease is lost, and release it", func() {
		rec := &leaseRecorder{lost: make(chan struct{})}
		g := &Gateway{ShareAuthority: rec, Revalidate: allow, RevalidateAccount: allow}
		views, err := g.ShareViews(ctx, actor, "example:note")
		Expect(err).NotTo(HaveOccurred())
		Expect(views.Views).To(HaveLen(1))
		ref, err := g.CreateShare(ctx, actor, "example:note")
		Expect(err).NotTo(HaveOccurred())
		Expect(ref.ResourceID).To(Equal("r1"))
		var stoppedAt time.Time
		err = g.RunShared(ctx, actor, "example:note", "r1", "output", func(run context.Context) error {
			time.AfterFunc(120*time.Millisecond, func() { close(rec.lost) })
			<-run.Done()
			// The run takes a while to stop: the lease is renewed meanwhile.
			time.Sleep(120 * time.Millisecond)
			stoppedAt = time.Now()
			return errors.New("stopped")
		})
		Expect(err).To(MatchError("stopped"))
		Expect(stoppedAt).NotTo(BeZero())
		calls := rec.seen()
		Expect(calls[:3]).To(Equal([]string{"views example:note", "create example:note", "admit example:note r1 output"}))
		Expect(calls).To(ContainElement("watch l1"))
		Expect(calls).To(ContainElement("renew l1"), "the lease stays alive while the run stops")
		Expect(calls[len(calls)-1]).To(Equal("release l1"))
	})

	It("run nothing when the owner refuses the admission, and are unavailable on an owner without a ShareAuthority", func() {
		rec := &leaseRecorder{lost: make(chan struct{}), err: ErrResourceUnreachable}
		g := &Gateway{ShareAuthority: rec, Revalidate: allow, RevalidateAccount: allow}
		err := g.RunShared(ctx, actor, "example:note", "r1", "output", func(context.Context) error {
			Fail("ran without an admission")
			return nil
		})
		Expect(errors.Is(err, ErrForbidden)).To(BeTrue())
		Expect(rec.seen()).NotTo(ContainElement("release l1"))

		bare := &Gateway{Revalidate: allow, RevalidateAccount: allow}
		_, err = bare.ShareViews(ctx, actor, "example:note")
		Expect(StatusFor(err)).To(Equal(http.StatusServiceUnavailable))
		err = bare.RunShared(ctx, actor, "example:note", "r1", "output", func(context.Context) error {
			Fail("ran without an owner")
			return nil
		})
		Expect(StatusFor(err)).To(Equal(http.StatusServiceUnavailable))
		// Only the admission and the creation are mutations: a lost watch or
		// release is plain unavailability, never an unknown outcome.
		for _, op := range []string{shareViews, shareWatch, shareRelease} {
			Expect(mutation(ResourceCall{Op: op}.wireAction())).To(BeFalse(), op)
			Expect(ResourceCall{Op: op}.sharing()).To(BeTrue(), op)
		}
		for _, op := range []string{shareCreate, shareAdmit} {
			Expect(mutation(ResourceCall{Op: op}.wireAction())).To(BeTrue(), op)
			Expect(ResourceCall{Op: op}.sharing()).To(BeTrue(), op)
		}
	})

	It("keep the record of a resource the service names through the owner, and are mutations", func() {
		rec := &leaseRecorder{lost: make(chan struct{})}
		g := &Gateway{ShareAuthority: rec, Revalidate: allow, RevalidateAccount: allow}
		ref, err := g.EnsureShare(ctx, actor, "connection", "binding-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(ref.ResourceID).To(Equal("r2"))
		Expect(g.ForgetShare(ctx, actor, "connection", "r2")).To(Succeed())
		Expect(rec.seen()).To(Equal([]string{"ensure connection binding-1", "forget connection r2"}))

		_, err = g.EnsureShare(ctx, actor, "connection", strings.Repeat("x", 257))
		Expect(errors.Is(err, ErrInvalidRequest)).To(BeTrue(), "a name longer than a record keeps is refused before the policy is asked")
		Expect(rec.seen()).To(HaveLen(2))
		for _, op := range []string{shareEnsure, shareForget} {
			Expect(mutation(ResourceCall{Op: op}.wireAction())).To(BeTrue(), op)
			Expect(ResourceCall{Op: op}.sharing()).To(BeTrue(), op)
		}
	})
})
