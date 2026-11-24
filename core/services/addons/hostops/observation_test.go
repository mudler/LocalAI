// SPDX-License-Identifier: MIT
package hostops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Observation callback boundary", func() {
	It("denies every correlated class before handlers and retains late denial", func() {
		f := &fixture{inflight: NewInFlight()}
		f.b = New(f.inflight, always, contract.DefaultLimits())
		calls := 0
		file := filepath.Join(GinkgoT().TempDir(), "effect")
		for op, scope := range map[string]string{"workspace.materialize": "read", "workspace.export": "read", "workspace.commit": "act", "finetune.start": "apply", "connector.inbound": ScopeInbound} {
			feature := "chats"
			if scope == ScopeInbound {
				feature = ""
			}
			Expect(f.b.Register(Spec{Op: op, Scope: scope, Feature: feature, Handler: func(context.Context, Call) (json.RawMessage, error) {
				calls++
				_ = os.WriteFile(file, []byte("effect"), 0600)
				return json.RawMessage(`{}`), nil
			}})).To(Succeed())
		}
		f.h = f.b.HandlerForRuntime("fine-tuning", "secret", NewStaticAuthority(map[string][]string{"fine-tuning": []string{"workspace.", "finetune.", "connector."}}), 1, "inc")
		e := env("fine-tuning", "status", "read", time.Now().Add(time.Minute))
		end, denied, err := f.inflight.BeginObservation(e, Binding{}, "inc", "request")
		Expect(err).NotTo(HaveOccurred())
		for _, op := range []string{"workspace.materialize", "workspace.export", "workspace.commit", "finetune.start", "connector.inbound"} {
			code, _ := f.call("secret", "status", op, `{}`)
			Expect(code).To(Equal(http.StatusForbidden), op)
		}
		Expect(denied()).To(BeTrue())
		Expect(calls).To(BeZero())
		_, statErr := os.Stat(file)
		Expect(os.IsNotExist(statErr)).To(BeTrue())
		ordinary := env("fine-tuning", "ordinary", "act", time.Now().Add(time.Minute))
		DeferCleanup(f.inflight.BeginRuntime(ordinary, Binding{}, false, "inc"))
		code, _ := f.call("secret", "ordinary", "workspace.export", `{}`)
		Expect(code).To(Equal(http.StatusOK))
		code, _ = f.call("secret", "ordinary", "workspace.commit", `{}`)
		Expect(code).To(Equal(http.StatusOK))
		Expect(calls).To(Equal(2))
		contents, err := os.ReadFile(file)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(contents)).To(Equal("effect"))
		end()
		end()
		_, live := f.inflight.Lookup("fine-tuning", "status")
		Expect(live).To(BeFalse())
		code, _ = f.call("secret", "status", "connector.inbound", `{}`)
		Expect(code).To(Equal(http.StatusForbidden))

	})
	It("cannot overwrite an observation with a forged identity or duplicate operation", func() {
		f := NewInFlight()
		e := env("fine-tuning", "status", "read", time.Now().Add(time.Minute))
		end, _, err := f.BeginObservation(e, Binding{}, "inc", "request")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(end)
		forged := e
		forged.Request.Generation = 2
		DeferCleanup(f.BeginRuntime(forged, Binding{}, false, "other"))
		_, ok := f.Lookup("fine-tuning", "status")
		Expect(ok).To(BeFalse())
		_, _, err = f.BeginObservation(e, Binding{}, "inc", "request2")
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("Observation identity checks", func() {
	It("rejects mismatched authenticated incarnation and forged IDs without touching handlers", func() {
		f := newFixture(always)
		e := env("fine-tuning", "status", "read", time.Now().Add(time.Minute))
		end, denied, err := f.inflight.BeginObservation(e, Binding{}, "inc", "q")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(end)
		f.h = f.b.HandlerForRuntime("fine-tuning", "secret", NewStaticAuthority(map[string][]string{"fine-tuning": []string{"finetune."}}), 2, "replacement")
		code, _ := f.call("secret", "status", "finetune.get", `{}`)
		Expect(code).To(Equal(403))
		Expect(denied()).To(BeTrue())
		code, _ = f.call("secret", "forged", "finetune.get", `{}`)
		Expect(code).To(Equal(403))
		code, _ = f.call("forged", "status", "finetune.get", `{}`)
		Expect(code).To(Equal(401))
		Expect(f.seen.AddonID).To(BeEmpty())
	})
	It("removes retired state on endpoint close", func() {
		f := NewInFlight()
		e := env("fine-tuning", "status", "read", time.Now().Add(time.Minute))
		end, _, err := f.BeginObservation(e, Binding{}, "inc", "q")
		Expect(err).NotTo(HaveOccurred())
		end()
		Expect(f.observations).To(HaveLen(1))
		f.ForgetRuntime("fine-tuning", "inc")
		f.ForgetRuntime("fine-tuning", "inc")
		Expect(f.observations).To(BeEmpty())
		Expect(f.entries).To(BeEmpty())
	})
	It("fails closed at the retained correlation bound without restricting ordinary operations", func() {
		f := NewInFlight()
		e := env("fine-tuning", "status", "read", time.Now().Add(time.Minute))
		for n := 0; n < 4096; n++ {
			e.Request.OperationID = fmt.Sprintf("status-%d", n)
			end, _, err := f.BeginObservation(e, Binding{}, "inc", "q")
			Expect(err).NotTo(HaveOccurred())
			end()
		}
		e.Request.OperationID = "overflow"
		_, _, err := f.BeginObservation(e, Binding{}, "inc", "q")
		Expect(err).To(HaveOccurred())
		e.Request.OperationID = "ordinary"
		end := f.BeginRuntime(e, Binding{}, false, "inc")
		defer end()
		_, live := f.Lookup("fine-tuning", "ordinary")
		Expect(live).To(BeTrue())
		f.ForgetRuntime("fine-tuning", "inc")
		Expect(f.observations).To(BeEmpty())
	})

})

// A body barrier runs only after authentication and after all request bytes have
// been read. Returning EOF resumes decoding and the correlation denial check.
type callbackBodyBarrier struct {
	*strings.Reader
	read   chan struct{}
	resume chan struct{}
}

func (b *callbackBodyBarrier) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err != nil {
		close(b.read)
		<-b.resume
	}
	return n, err
}
func (b *callbackBodyBarrier) Close() error { return nil }

var _ = Describe("Callback endpoint retirement", func() {
	It("keeps authenticated pending inbound callbacks denied while another endpoint works", func() {
		f := NewInFlight()
		b := New(f, always, contract.DefaultLimits())
		var inbound, ordinary atomic.Int32
		Expect(b.Register(Spec{Op: "connector.inbound", Scope: ScopeInbound, Handler: func(context.Context, Call) (json.RawMessage, error) {
			inbound.Add(1)
			return json.RawMessage(`{}`), nil
		}})).To(Succeed())
		Expect(b.Register(Spec{Op: "workspace.export", Scope: "read", Feature: "chats", Handler: func(context.Context, Call) (json.RawMessage, error) {
			ordinary.Add(1)
			return json.RawMessage(`{}`), nil
		}})).To(Succeed())
		end, _, err := f.BeginObservation(env("fine-tuning", "status", "read", time.Now().Add(time.Minute)), Binding{}, "inc", "q")
		Expect(err).NotTo(HaveOccurred())
		end()
		h := b.EndpointForRuntime("fine-tuning", "secret", NewStaticAuthority(map[string][]string{"fine-tuning": []string{"connector."}}), 1, "inc")
		body := &callbackBodyBarrier{Reader: strings.NewReader(`{"operationId":"status","op":"connector.inbound","arguments":{}}`), read: make(chan struct{}), resume: make(chan struct{})}
		req := httptest.NewRequest(http.MethodPost, "/host", body)
		req.Header.Set(contract.HostCredentialHeader, "secret")
		rec := httptest.NewRecorder()
		done := make(chan struct{})
		go func() { h.ServeHTTP(rec, req); close(done) }()
		<-body.read
		h.Stop()
		retired := make(chan struct{})
		go func() { h.Drain(); f.ForgetRuntime("fine-tuning", "inc"); close(retired) }()
		select {
		case <-retired:
			Fail("retired before callback drained")
		default:
		}
		Expect(f.callbackDenied("fine-tuning", "status", 1, "inc")).To(BeTrue())
		other := &fixture{b: b, inflight: f, h: b.HandlerForRuntime("other", "other-secret", NewStaticAuthority(map[string][]string{"other": []string{"workspace."}}), 1, "other-inc")}
		defer f.BeginRuntime(env("other", "ordinary", "read", time.Now().Add(time.Minute)), Binding{}, false, "other-inc")()
		code, _ := other.call("other-secret", "ordinary", "workspace.export", `{}`)
		Expect(code).To(Equal(http.StatusOK))
		close(body.resume)
		<-done
		Expect(inbound.Load()).To(BeZero())
		Expect(rec.Code).To(Equal(http.StatusForbidden))
		Expect(ordinary.Load()).To(Equal(int32(1)))
		<-retired
		Expect(f.observations).To(BeEmpty())
		Expect(f.entries).To(HaveLen(1))
		closed := &fixture{h: h}
		code, _ = closed.call("secret", "fresh", "connector.inbound", `{}`)
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(inbound.Load()).To(BeZero())
	})
})

var _ = Describe("Decoded callback dispatch retirement", func() {
	It("refuses an authorized decoded callback paused across endpoint Stop", func() {
		f := NewInFlight()
		authorized, resume := make(chan struct{}), make(chan struct{})
		b := New(f, func(string, string) bool { close(authorized); <-resume; return true }, contract.DefaultLimits())
		var calls atomic.Int32
		Expect(b.Register(Spec{Op: "workspace.export", Scope: "read", Feature: "chats", Handler: func(context.Context, Call) (json.RawMessage, error) {
			calls.Add(1)
			return json.RawMessage(`{}`), nil
		}})).To(Succeed())
		h := b.EndpointForRuntime("fine-tuning", "secret", NewStaticAuthority(map[string][]string{"fine-tuning": []string{"workspace."}}), 1, "inc")
		defer f.BeginRuntime(env("fine-tuning", "ordinary", "read", time.Now().Add(time.Minute)), Binding{}, false, "inc")()
		fixture := &fixture{h: h}
		result := make(chan int, 1)
		go func() { code, _ := fixture.call("secret", "ordinary", "workspace.export", `{}`); result <- code }()
		<-authorized // Decoded and correlation checked; paused in the real feature check.
		h.Stop()
		drained := make(chan struct{})
		go func() { h.Drain(); close(drained) }()
		select {
		case <-drained:
			Fail("drained before authorized request returned")
		default:
		}
		close(resume)
		Expect(<-result).To(Equal(http.StatusForbidden))
		<-drained
		Expect(calls.Load()).To(BeZero())
	})
	It("stops without waiting for a handler that ignores cancellation", func() {
		f := NewInFlight()
		b := New(f, always, contract.DefaultLimits())
		entered, release := make(chan struct{}), make(chan struct{})
		var h *RuntimeEndpoint
		Expect(b.Register(Spec{Op: "connector.inbound", Scope: ScopeInbound, Handler: func(context.Context, Call) (json.RawMessage, error) {
			close(entered)
			<-release // Deliberately ignores request cancellation.
			h.Stop()  // No lifecycle lock may be held across the handler or Drain.
			return json.RawMessage(`{}`), nil
		}})).To(Succeed())
		h = b.EndpointForRuntime("fine-tuning", "secret", NewStaticAuthority(map[string][]string{"fine-tuning": []string{"connector."}}), 1, "inc")
		fixture := &fixture{h: h}
		done := make(chan struct{})
		go func() { fixture.call("secret", "ordinary", "connector.inbound", `{}`); close(done) }()
		<-entered
		stopped := make(chan struct{})
		go func() { h.Stop(); close(stopped) }()
		Eventually(stopped).Should(BeClosed())
		drained := make(chan struct{})
		go func() { h.Drain(); close(drained) }()
		select {
		case <-drained:
			Fail("drained while handler still running")
		default:
		}
		code, _ := fixture.call("secret", "fresh", "connector.inbound", `{}`)
		Expect(code).To(Equal(http.StatusForbidden))
		close(release)
		Eventually(done).Should(BeClosed())
		Eventually(drained).Should(BeClosed())
	})
})
