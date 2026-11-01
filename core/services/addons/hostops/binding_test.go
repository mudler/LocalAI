// SPDX-License-Identifier: MIT
package hostops

import (
	"context"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the chat binding of a live request", func() {
	It("keeps the binding with the envelope and gives it to the operation", func() {
		f := newFixture(always)
		DeferCleanup(f.inflight.BeginBound(env("fine-tuning", "op1", "read", time.Now().Add(time.Minute)), Binding{Principal: "alice", ChatID: "c1"}))
		code, _ := f.call("secret", "op1", "finetune.get", `{}`)
		Expect(code).To(Equal(http.StatusOK))
		Expect(f.seen.ChatID).To(Equal("c1"))
		Expect(f.seen.ChatPrincipal).To(Equal("alice"))
		Expect(f.seen.Scope).To(Equal("read"))
		DeferCleanup(f.inflight.Begin(env("fine-tuning", "op2", "read", time.Now().Add(time.Minute))))
		code, _ = f.call("secret", "op2", "finetune.get", `{}`)
		Expect(code).To(Equal(http.StatusOK))
		Expect(f.seen.ChatID).To(BeEmpty(), "a request without a binding names no chat")
	})

	It("refuses an operation ID that two requests with different bindings share", func() {
		in := NewInFlight()
		e := env("sandbox", "op1", "read", time.Now().Add(time.Minute))
		end1 := in.BeginBound(e, Binding{Principal: "alice", ChatID: "c1"})
		_, b, ok := in.LookupBound("sandbox", "op1")
		Expect(ok).To(BeTrue())
		Expect(b).To(Equal(Binding{Principal: "alice", ChatID: "c1"}))
		end2 := in.BeginBound(e, Binding{Principal: "alice", ChatID: "c2"})
		_, _, ok = in.LookupBound("sandbox", "op1")
		Expect(ok).To(BeFalse(), "two chats on one operation: neither is trusted")
		end3 := in.Begin(e)
		_, _, ok = in.LookupBound("sandbox", "op1")
		Expect(ok).To(BeFalse())
		end1()
		end2()
		end3()
		_, _, ok = in.LookupBound("sandbox", "op1")
		Expect(ok).To(BeFalse(), "ended")
		end := in.BeginBound(e, Binding{Principal: "alice", ChatID: "c1"})
		again := in.BeginBound(e, Binding{Principal: "alice", ChatID: "c1"})
		_, _, ok = in.LookupBound("sandbox", "op1")
		Expect(ok).To(BeTrue(), "the same binding twice is one binding")
		end()
		again()
	})

	It("carries a binding in a context and ignores an incomplete one", func() {
		ctx := WithBinding(context.Background(), Binding{Principal: "alice", ChatID: "c1"})
		b, ok := BindingOf(ctx)
		Expect(ok).To(BeTrue())
		Expect(b).To(Equal(Binding{Principal: "alice", ChatID: "c1"}))
		_, ok = BindingOf(context.Background())
		Expect(ok).To(BeFalse())
		_, ok = BindingOf(WithBinding(context.Background(), Binding{Principal: "alice"}))
		Expect(ok).To(BeFalse())
		Expect(Binding{Principal: "alice", ChatID: "c1"}.Valid()).To(BeTrue())
		Expect(Binding{Principal: "alice", ChatID: strings.Repeat("x", 300)}.Valid()).To(BeFalse())
		Expect(Binding{Principal: strings.Repeat("p", 300), ChatID: "c1"}.Valid()).To(BeFalse())
	})
})
