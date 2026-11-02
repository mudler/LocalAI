// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"encoding/json"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"net/http"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func TestOwnerBinding(t *testing.T) {
	ctx := context.Background()
	claim := ExecutionBindingClaim{ConversationID: "chat"}
	b := ExecutionStatusBinding{Actor: frontActor, ConversationID: "chat", AddonID: "renamed", Generation: 1, Incarnation: "inc", ExecutionTool: "run", StatusTool: "health", SelectionRevision: "owner:1:2:3:4"}
	g := &Gateway{Revalidate: func(context.Context, contract.Actor) error { return nil }}
	if _, err := g.BindingForActor(ctx, frontActor, claim); err == nil {
		t.Fatal("missing resolver granted binding")
	}
	calls := 0
	g.ResolveExecutionBinding = func(_ context.Context, actor contract.Actor, got ExecutionBindingClaim) (ExecutionStatusBinding, error) {
		calls++
		if actor != frontActor || got != claim {
			return ExecutionStatusBinding{}, ErrForbidden
		}
		return b, nil
	}
	got, err := g.BindingForActor(ctx, frontActor, claim)
	if err != nil || got != b {
		t.Fatalf("binding: %+v %v", got, err)
	}
	if err := g.ValidateExecutionBinding(ctx, frontActor, b); err != nil {
		t.Fatal(err)
	}
	stale := b
	stale.SelectionRevision = "old"
	if g.ValidateExecutionBinding(ctx, frontActor, stale) == nil {
		t.Fatal("stale revision granted")
	}
	forged := b
	forged.Actor.UserID = "other"
	before := calls
	if g.ValidateExecutionBinding(ctx, frontActor, forged) == nil || calls != before {
		t.Fatal("forged actor reached resolver")
	}
	b.SelectionRevision = "owner:1:2:3:6" // includes ABA mutations, not current-state hash
	if g.ValidateExecutionBinding(ctx, frontActor, got) == nil {
		t.Fatal("ABA binding survived")
	}
}
func TestBindingClaimStrict(t *testing.T) {
	for _, raw := range []string{`{"ConversationID":"chat"}`, `{"conversationId":"chat","actor":{}}`, `{"conversationId":"chat","conversationId":"other"}`, `null`} {
		var c ExecutionBindingClaim
		if json.Unmarshal([]byte(raw), &c) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

var _ = Describe("owner binding carrier", func() {
	It("resolves only the authenticated claim over actual mTLS", func() {
		b := ExecutionStatusBinding{Actor: frontActor, ConversationID: "chat", AddonID: "renamed", Generation: 1, Incarnation: "inc", ExecutionTool: "run", StatusTool: "health", SelectionRevision: "owner:1:2:3:4"}
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
			resp.Body.Close()
		}
		Expect(e).To(HaveOccurred())
		Expect(calls).To(Equal(before))
		g.ResolveExecutionBinding = nil
		_, e = front.BindingForActor(context.Background(), frontActor, ExecutionBindingClaim{ConversationID: "chat"})
		Expect(e).To(HaveOccurred())
	})
	It("fails closed on unsupported owners and mismatched binding replies", func() {
		b := ExecutionStatusBinding{Actor: frontActor, ConversationID: "other-chat", AddonID: "renamed", Generation: 1, Incarnation: "inc", ExecutionTool: "run", StatusTool: "health", SelectionRevision: "rev"}
		for _, supported := range []bool{false, true} {
			_, cs := carrierFixture(func(car *Carrier) http.Handler {
				g := &Gateway{Revalidate: func(context.Context, contract.Actor) error { return nil }}
				h, e := car.OwnerHandler(g)
				Expect(e).NotTo(HaveOccurred())
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if !supported && r.URL.Path == "/addons/capabilities" {
						json.NewEncoder(w).Encode(Capabilities{Protocol: 2})
						return
					}
					if r.URL.Path == "/addons/forward" {
						json.NewEncoder(w).Encode(executionBindingReply{Version: 1, Binding: b})
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

func TestBindingDTOCompatibility(t *testing.T) {
	legacy := `{"actor":{"userId":"u","credentialId":"c","mode":"named-user"},"addonId":"renamed","generation":1,"incarnation":"inc","executionTool":"run","statusTool":"health","selectionRevision":"rev"}`
	var b ExecutionStatusBinding
	if json.Unmarshal([]byte(legacy), &b) != nil || b.ConversationID != "" {
		t.Fatal("legacy observation binding rejected")
	}
	for _, raw := range []string{
		strings.Replace(legacy, `"addonId"`, `"AddonId"`, 1),
		strings.Replace(legacy, `"userId"`, `"UserId"`, 1),
		strings.Replace(legacy, `"addonId":"renamed"`, `"addonId":"renamed","addonId":"other"`, 1),
		strings.Replace(legacy, `"actor":`, `"conversationId":"chat","ConversationID":"foreign","actor":`, 1),
	} {
		if json.Unmarshal([]byte(raw), &b) == nil {
			t.Fatalf("accepted alias or duplicate: %s", raw)
		}
	}
}

var _ = Describe("generic execution provider identity", func() {
	It("resolves two renamed providers with distinct status tools without product dispatch", func() {
		for _, pair := range [][2]string{{"runner-one", "inspect"}, {"runner-two", "readiness"}} {
			want := ExecutionStatusBinding{Actor: frontActor, ConversationID: "chat", AddonID: pair[0], Generation: 2, Incarnation: "live", ExecutionTool: "execute", StatusTool: pair[1], SelectionRevision: "owner:revision"}
			g := &Gateway{Revalidate: func(context.Context, contract.Actor) error { return nil }, ResolveExecutionBinding: func(context.Context, contract.Actor, ExecutionBindingClaim) (ExecutionStatusBinding, error) {
				return want, nil
			}}
			got, err := g.BindingForActor(context.Background(), frontActor, ExecutionBindingClaim{ConversationID: "chat"})
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(want))
		}
	})
})
