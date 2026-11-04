// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"net/http"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

var _ = Describe("owner binding validation", func() {
	It("validates current selection and authenticated actor", func() {
		ctx := context.Background()
		claim := ExecutionBindingClaim{ConversationID: "chat"}
		b := ExecutionStatusBinding{Actor: frontActor, ConversationID: "chat", AddonID: "renamed", Generation: 1, Incarnation: "inc", ExecutionTool: "run", StatusTool: "health", CurrentSelection: "owner:1:2:3:4"}
		g := &Gateway{Revalidate: func(context.Context, contract.Actor) error { return nil }}
		_, err := g.BindingForActor(ctx, frontActor, claim)
		Expect(err).To(HaveOccurred())
		calls := 0
		g.ResolveExecutionBinding = func(_ context.Context, actor contract.Actor, got ExecutionBindingClaim) (ExecutionStatusBinding, error) {
			calls++
			if actor != frontActor || got != claim {
				return ExecutionStatusBinding{}, ErrForbidden
			}
			return b, nil
		}
		got, err := g.BindingForActor(ctx, frontActor, claim)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(b))
		Expect(g.ValidateExecutionBinding(ctx, frontActor, b)).To(Succeed())
		stale := b
		stale.CurrentSelection = "old"
		Expect(g.ValidateExecutionBinding(ctx, frontActor, stale)).NotTo(Succeed())
		forged := b
		forged.Actor.UserID = "other"
		before := calls
		Expect(g.ValidateExecutionBinding(ctx, frontActor, forged)).NotTo(Succeed())
		Expect(calls).To(Equal(before))
		b.CurrentSelection = "current-selection" // The resolver reports a different current selection.
		Expect(g.ValidateExecutionBinding(ctx, frontActor, got)).NotTo(Succeed())
	})
})
var _ = Describe("binding claim strictness", func() {
	It("rejects aliases, duplicate fields and null claims", func() {
		for _, raw := range []string{`{"ConversationID":"chat"}`, `{"conversationId":"chat","actor":{}}`, `{"conversationId":"chat","conversationId":"other"}`, `null`} {
			var c ExecutionBindingClaim
			Expect(json.Unmarshal([]byte(raw), &c)).NotTo(Succeed(), raw)
		}
	})
})

var _ = Describe("owner binding carrier", func() {
	It("resolves only the authenticated claim over actual mTLS", func() {
		b := ExecutionStatusBinding{Actor: frontActor, ConversationID: "chat", AddonID: "renamed", Generation: 1, Incarnation: "inc", ExecutionTool: "run", StatusTool: "health", CurrentSelection: "owner:1:2:3:4"}
		calls := 0
		g := &Gateway{Revalidate: func(context.Context, contract.Actor) error { return nil }, ResolveExecutionBinding: func(_ context.Context, a contract.Actor, c ExecutionBindingClaim) (ExecutionStatusBinding, error) {
			calls++
			if a != frontActor || c.ConversationID != "chat" {
				return ExecutionStatusBinding{}, ErrForbidden
			}
			return b, nil
		}}
		_, cs := carrierFixture(func(car *Carrier) http.Handler {
			h, e := car.OwnerHandler(g)
			Expect(e).NotTo(HaveOccurred())
			return h
		})
		front := &Gateway{Forward: cs["front-a"], Revalidate: g.Revalidate}
		got, e := front.BindingForActor(context.Background(), frontActor, ExecutionBindingClaim{ConversationID: "chat"})
		Expect(e).NotTo(HaveOccurred())
		Expect(got).To(Equal(b))
		Expect(calls).To(Equal(1))
		Expect(front.ValidateExecutionBinding(context.Background(), frontActor, b)).To(Succeed())
		stale := b
		stale.Incarnation = "old"
		Expect(front.ValidateExecutionBinding(context.Background(), frontActor, stale)).NotTo(Succeed())
		_, e = front.BindingForActor(context.Background(), frontActor, ExecutionBindingClaim{ConversationID: "foreign"})
		Expect(e).To(HaveOccurred())
		before := calls
		for _, raw := range []string{
			`{"version":1,"claim":{"conversationId":"chat","actor":{}}}`,
			`{"version":1,"claim":{"ConversationId":"chat"}}`,
			`{"version":1,"claim":{"conversationId":"chat","conversationId":"foreign"}}`,
			`{"version":2,"claim":{"conversationId":"chat"}}`,
		} {
			body := `{"request":{"action":"execution-binding-v1","operationId":"bad"},"executionBinding":` + raw + `}`
			var wire wireRequest
			Expect(decode(strings.NewReader(body), &wire)).NotTo(Succeed())
		}
		// A mixed door is signed and authenticated, but still has no authority.
		resp, e := cs["front-a"].send(context.Background(), frontActor, wireRequest{Request: contract.Request{Action: executionBindingAction, OperationID: "mixed", AddonID: "forged"}, ExecutionBinding: &executionBindingCall{Version: 1, Claim: ExecutionBindingClaim{ConversationID: "chat"}}})
		if resp != nil {
			Expect(resp.Body.Close()).To(Succeed())
		}
		Expect(e).To(HaveOccurred())
		Expect(calls).To(Equal(before))
		g.ResolveExecutionBinding = nil
		_, e = front.BindingForActor(context.Background(), frontActor, ExecutionBindingClaim{ConversationID: "chat"})
		Expect(e).To(HaveOccurred())
	})
	It("fails closed on unsupported owners and mismatched binding replies", func() {
		b := ExecutionStatusBinding{Actor: frontActor, ConversationID: "other-chat", AddonID: "renamed", Generation: 1, Incarnation: "inc", ExecutionTool: "run", StatusTool: "health", CurrentSelection: "rev"}
		for _, supported := range []bool{false, true} {
			_, cs := carrierFixture(func(car *Carrier) http.Handler {
				g := &Gateway{Revalidate: func(context.Context, contract.Actor) error { return nil }}
				h, e := car.OwnerHandler(g)
				Expect(e).NotTo(HaveOccurred())
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if !supported && r.URL.Path == "/addons/capabilities" {
						Expect(json.NewEncoder(w).Encode(Capabilities{Protocol: 2})).To(Succeed())
						return
					}
					if r.URL.Path == "/addons/forward" {
						Expect(json.NewEncoder(w).Encode(executionBindingReply{Version: 1, Binding: b})).To(Succeed())
						return
					}
					h.ServeHTTP(w, r)
				})
			})
			front := &Gateway{Forward: cs["front-a"], Revalidate: func(context.Context, contract.Actor) error { return nil }}
			got, e := front.BindingForActor(context.Background(), frontActor, ExecutionBindingClaim{ConversationID: "chat"})
			Expect(e).To(HaveOccurred())
			Expect(got).To(Equal(ExecutionStatusBinding{}))
		}
	})
})

var _ = Describe("binding DTO strictness", func() {
	It("accepts canonical bindings and rejects aliases and duplicates", func() {
		rawBinding := `{"actor":{"userId":"u","credentialId":"c","mode":"named-user"},"addonId":"renamed","generation":1,"incarnation":"inc","executionTool":"run","statusTool":"health","conversationId":"chat","currentSelection":"current"}`
		var b ExecutionStatusBinding
		Expect(json.Unmarshal([]byte(rawBinding), &b)).To(Succeed())
		Expect(b.ConversationID).To(Equal("chat"))
		for _, raw := range []string{
			strings.Replace(rawBinding, `"addonId"`, `"AddonId"`, 1),
			strings.Replace(rawBinding, `"userId"`, `"UserId"`, 1),
			strings.Replace(rawBinding, `"addonId":"renamed"`, `"addonId":"renamed","addonId":"other"`, 1),
			strings.Replace(rawBinding, `"actor":`, `"conversationId":"chat","ConversationID":"foreign","actor":`, 1),
		} {
			Expect(json.Unmarshal([]byte(raw), &b)).NotTo(Succeed(), raw)
		}
	})
})

var _ = Describe("generic execution provider identity", func() {
	It("passes through owner-resolved bindings with distinct provider and status names", func() {
		for _, pair := range [][2]string{{"runner-one", "inspect"}, {"runner-two", "readiness"}} {
			want := ExecutionStatusBinding{Actor: frontActor, ConversationID: "chat", AddonID: pair[0], Generation: 2, Incarnation: "live", ExecutionTool: "execute", StatusTool: pair[1], CurrentSelection: "current-inputs"}
			g := &Gateway{Revalidate: func(context.Context, contract.Actor) error { return nil }, ResolveExecutionBinding: func(context.Context, contract.Actor, ExecutionBindingClaim) (ExecutionStatusBinding, error) {
				return want, nil
			}}
			got, err := g.BindingForActor(context.Background(), frontActor, ExecutionBindingClaim{ConversationID: "chat"})
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(want))
		}
	})
})
