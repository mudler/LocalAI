// SPDX-License-Identifier: MIT
package hostops

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// liveAuthority is a test Authority whose grants change while it is in use.
type liveAuthority struct {
	mu     sync.Mutex
	grants map[string]Reason
}

func (l *liveAuthority) set(op string, why Reason) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.grants[op] = why
}

func (l *liveAuthority) Allows(_, op string) (bool, Reason) {
	l.mu.Lock()
	defer l.mu.Unlock()
	why, ok := l.grants[op]
	if !ok {
		return false, ReasonNotGranted
	}
	return why == ReasonNone, why
}
func (l *liveAuthority) Snapshot(string) EffectiveSet { return EffectiveSet{} }
func (l *liveAuthority) Entitled(string) bool         { return true }

var _ = Describe("Broker authority", func() {
	newLive := func(auth Authority) *fixture {
		f := newFixture(always)
		f.h = f.b.HandlerForRuntime("fine-tuning", "secret", auth, 0, "")
		end := f.inflight.Begin(env("fine-tuning", "op1", "read", time.Now().Add(time.Minute)))
		DeferCleanup(end)
		return f
	}

	It("applies a revoke to the next call", func() {
		auth := &liveAuthority{grants: map[string]Reason{"finetune.get": ReasonNone}}
		f := newLive(auth)
		code, _ := f.call("secret", "op1", "finetune.get", `{}`)
		Expect(code).To(Equal(http.StatusOK))
		auth.set("finetune.get", ReasonDeniedByAdmin)
		code, out := f.call("secret", "op1", "finetune.get", `{}`)
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(out.Error.Code).To(Equal("unknown_op"))
		auth.set("finetune.get", ReasonNone)
		code, _ = f.call("secret", "op1", "finetune.get", `{}`)
		Expect(code).To(Equal(http.StatusOK), "a later grant applies with no restart")
	})

	It("answers the same to a denied and a not granted operation", func() {
		auth := &liveAuthority{grants: map[string]Reason{"finetune.get": ReasonDeniedByPolicyFile, "finetune.list": ReasonIdentityMismatch}}
		f := newLive(auth)
		codeA, a := f.call("secret", "op1", "finetune.get", `{}`)
		codeB, b := f.call("secret", "op1", "finetune.list", `{}`)
		codeC, c := f.call("secret", "op1", "finetune.start", `{}`)
		Expect([]int{codeA, codeB, codeC}).To(Equal([]int{403, 403, 403}))
		Expect(a).To(Equal(b))
		Expect(b).To(Equal(c))
	})

	It("lets a running call finish after the revoke", func() {
		auth := &liveAuthority{grants: map[string]Reason{"finetune.get": ReasonNone}}
		f := newLive(auth)
		started, release := make(chan struct{}), make(chan struct{})
		b := New(f.inflight, always, f.b.limits)
		Expect(b.Register(Spec{Op: "finetune.get", Scope: "read", Feature: "fine_tuning", Handler: func(context.Context, Call) (json.RawMessage, error) {
			close(started)
			<-release
			return json.RawMessage(`{"done":true}`), nil
		}})).To(Succeed())
		f.b, f.h = b, b.HandlerForRuntime("fine-tuning", "secret", auth, 0, "")
		done := make(chan int, 1)
		go func() {
			code, _ := f.call("secret", "op1", "finetune.get", `{}`)
			done <- code
		}()
		Eventually(started).Should(BeClosed())
		auth.set("finetune.get", ReasonDeniedByAdmin)
		close(release)
		Eventually(done).Should(Receive(Equal(http.StatusOK)))
		code, _ := f.call("secret", "op1", "finetune.get", `{}`)
		Expect(code).To(Equal(http.StatusForbidden))
	})

	It("refuses everything without an authority", func() {
		f := newFixture(always)
		f.h = f.b.HandlerForRuntime("fine-tuning", "secret", nil, 0, "")
		code, _ := f.call("secret", "op1", "finetune.get", `{}`)
		Expect(code).To(Equal(http.StatusForbidden))
	})

	It("is safe under concurrent calls and changes", func() {
		auth := &liveAuthority{grants: map[string]Reason{"finetune.get": ReasonNone}}
		f := newLive(auth)
		b := New(f.inflight, always, f.b.limits)
		Expect(b.Register(Spec{Op: "finetune.get", Scope: "read", Feature: "fine_tuning", Handler: func(context.Context, Call) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		}})).To(Succeed())
		f.b, f.h = b, b.HandlerForRuntime("fine-tuning", "secret", auth, 0, "")
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(2)
			go func() {
				defer wg.Done()
				for j := 0; j < 20; j++ {
					f.call("secret", "op1", "finetune.get", `{}`)
				}
			}()
			go func(i int) {
				defer wg.Done()
				for j := 0; j < 20; j++ {
					if (i+j)%2 == 0 {
						auth.set("finetune.get", ReasonNone)
					} else {
						auth.set("finetune.get", ReasonDeniedByAdmin)
					}
				}
			}(i)
		}
		wg.Wait()
	})
})
