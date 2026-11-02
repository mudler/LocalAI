// SPDX-License-Identifier: MIT
package hostops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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
		f.h = f.b.HandlerForRuntime("fine-tuning", "secret", []string{"workspace.", "finetune.", "connector."}, 1, "inc")
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
		f.h = f.b.HandlerForRuntime("fine-tuning", "secret", []string{"finetune."}, 2, "replacement")
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
