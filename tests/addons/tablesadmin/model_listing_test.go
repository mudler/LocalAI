// SPDX-License-Identifier: MIT
package tablesadmin_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	addontools "github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const modelListName = "addon__tables__databases-list"

// Neither backend fakes a listing: both enter Gateway, and the carrier reaches
// the bootstrapped owner's real resource service, policy and account database.
func modelListingBackend(h *host, forwarded bool) *gateway.Gateway {
	GinkgoHelper()
	if !forwarded {
		return h.app.AddonGateway()
	}
	carrier, err := gateway.NewCarrier(*h.front)
	Expect(err).NotTo(HaveOccurred())
	return &gateway.Gateway{Forward: carrier, Revalidate: auth.AddonRevalidate(h.app.AuthDB(), h.mode())}
}

func discoverModelList(a *addontools.ToolAdapter, actor contract.Actor) {
	GinkgoHelper()
	defs, err := a.Discover(context.Background(), actor)
	Expect(err).NotTo(HaveOccurred())
	names := []string{}
	for _, d := range defs {
		names = append(names, d.Name)
	}
	Expect(names).To(ContainElement(modelListName))
}

func executeModelList(a *addontools.ToolAdapter, actor contract.Actor) map[string]any {
	GinkgoHelper()
	out, err := a.Execute(context.Background(), actor, modelListName, json.RawMessage(`{}`))
	Expect(err).NotTo(HaveOccurred())
	Expect(out.Kind).To(Equal(addontools.ResultData))
	var payload map[string]any
	Expect(json.Unmarshal(out.Result, &payload)).To(Succeed())
	Expect(payload).To(HaveLen(2))
	Expect(payload).To(HaveKey("truncated"))
	Expect(payload["databases"]).To(BeAssignableToTypeOf([]any{}))
	return payload
}

func modelListDenied(a *addontools.ToolAdapter, actor contract.Actor, want error) {
	GinkgoHelper()
	out, err := a.Execute(context.Background(), actor, modelListName, json.RawMessage(`{}`))
	Expect(err).To(HaveOccurred())
	Expect(errors.Is(err, want)).To(BeTrue(), "%v", err)
	Expect(out.Result).To(BeEmpty())
}

var _ = Describe("model database listing through the real owner", func() {
	for _, forwarded := range []bool{false, true} {
		label := "local"
		if forwarded {
			label = "carrier"
		}
		Context(label, func() {
			It("projects only active owned/shared resources and the caller's own role, never UI management metadata", func() {
				h := ready(false)
				id := h.database("bob", "model-matrix", "Matrix")
				private := h.database("bob", "model-private", "Private")
				Expect(h.grant("bob", id, "carol", "viewer").code).To(Equal(200))
				Expect(h.grant("bob", id, "dave", "editor").code).To(Equal(200))
				a := addontools.NewToolAdapter(modelListingBackend(h, forwarded))
				for user, role := range map[string]string{"bob": "owner", "carol": "viewer", "dave": "editor", "alice": "", "erin": ""} {
					if user == "erin" {
						modelListDenied(a, h.actor(user), gateway.ErrForbidden)
						continue
					}
					discoverModelList(a, h.actor(user))
					payload := executeModelList(a, h.actor(user))
					Expect(payload["truncated"]).To(BeFalse())
					rows := payload["databases"].([]any)
					if role == "" {
						Expect(rows).To(BeEmpty())
						continue
					}
					expected := []any{map[string]any{"resourceId": id, "name": "Matrix", "role": role}}
					if user == "bob" {
						expected = append(expected, map[string]any{"resourceId": private, "name": "Private", "role": "owner"})
					}
					Expect(rows).To(ConsistOf(expected))
				}
				// An unrelated feature-enabled user has no grant, just like the
				// installation administrator (alice), who never inherits data ownership.
				Expect(h.revoke("bob", id, "dave").code).To(Equal(200))
				Expect(executeModelList(a, h.actor("dave"))["databases"]).To(BeEmpty())
			})

			It("excludes policy reservations and resources whose deletion has begun", func() {
				h := ready(false)
				p := h.app.AddonPolicy()
				active := h.database("bob", "model-active", "Active")
				deleting := h.database("bob", "model-deleting", "Deleting")
				Expect(p.BeginDelete(context.Background(), h.actor("bob"), h.key(deleting), "delete-model")).To(Succeed())
				// An indeterminate creation leaves a genuine pending policy record.
				pending := "rfffffffffffffffffffffffffffffff"
				_, err := p.CreateWithID(context.Background(), h.actor("bob"), "tables", pending, "pending-model", func(context.Context, policy.ResourceKey) error { return errors.New("fixture creation interrupted") })
				Expect(err).To(HaveOccurred())
				a := addontools.NewToolAdapter(modelListingBackend(h, forwarded))
				discoverModelList(a, h.actor("bob"))
				Expect(executeModelList(a, h.actor("bob"))["databases"]).To(Equal([]any{map[string]any{"resourceId": active, "name": "Active", "role": "owner"}}))
			})

			It("rechecks grants after discovery and independently refuses inspect on a revoked database", func() {
				h := ready(false)
				id := h.database("bob", "model-revoke", "Revoked")
				Expect(h.grant("bob", id, "carol", "viewer").code).To(Equal(200))
				a := addontools.NewToolAdapter(modelListingBackend(h, forwarded))
				actor := h.actor("carol")
				discoverModelList(a, actor)
				Expect(executeModelList(a, actor)["databases"]).To(HaveLen(1))
				Expect(h.revoke("bob", id, "carol").code).To(Equal(200))
				Expect(executeModelList(a, actor)["databases"]).To(BeEmpty())
				out, err := a.Execute(context.Background(), actor, "addon__tables__databases-inspect", json.RawMessage(fmt.Sprintf(`{"resourceId":%q}`, id)))
				Expect(err).NotTo(HaveOccurred())
				Expect(out.Kind).To(Equal(addontools.ResultError))
				// Resource ACL denials deliberately conceal resource existence.
				Expect(out.Code).To(Equal(addontools.CodeResourceUnreachable))
				Expect(out.Result).To(BeEmpty())
			})

			It("revalidates real credential and feature changes after discovery, not as successful empty lists", func() {
				h := ready(false)
				a := addontools.NewToolAdapter(modelListingBackend(h, forwarded))
				actor := h.actor("bob")
				discoverModelList(a, actor)
				db := h.app.AuthDB()
				Expect(db.Model(&auth.UserAPIKey{}).Where("id = ?", h.keys["bob"]).Update("disabled", true).Error).To(Succeed())
				modelListDenied(a, actor, gateway.ErrForbidden)
				Expect(db.Model(&auth.UserAPIKey{}).Where("id = ?", h.keys["bob"]).Update("disabled", false).Error).To(Succeed())
				discoverModelList(a, actor)
				Expect(db.Model(&auth.UserPermission{}).Where("user_id = ?", "bob").Update("permissions", auth.PermissionMap{auth.FeatureAddons: false}).Error).To(Succeed())
				modelListDenied(a, actor, gateway.ErrForbidden)
				Expect(db.Model(&auth.UserPermission{}).Where("user_id = ?", "bob").Update("permissions", auth.PermissionMap{auth.FeatureAddons: true}).Error).To(Succeed())
				discoverModelList(a, actor)
				expired := time.Now().Add(-time.Hour)
				Expect(db.Model(&auth.UserAPIKey{}).Where("id = ?", h.keys["bob"]).Update("expires_at", expired).Error).To(Succeed())
				modelListDenied(a, actor, gateway.ErrForbidden)
			})

			It("preserves typed missing resource registry and app registry failures", func() {
				h := ready(false)
				a := addontools.NewToolAdapter(modelListingBackend(h, forwarded))
				actor := h.actor("bob")
				discoverModelList(a, actor)
				owner := h.app.AddonGateway()
				registry := owner.Resources
				owner.Resources = nil
				defer func() { owner.Resources = registry }()
				modelListDenied(a, actor, gateway.ErrUnavailable)
				owner.Resources = registry
				manager := owner.Manager
				owner.Manager = nil
				defer func() { owner.Manager = manager }()
				modelListDenied(a, actor, gateway.ErrUnavailable)
			})

			It("uses the service's actual 100/101 bound in ID order with truthful truncation and absent names", func() {
				h := ready(false)
				p := h.app.AddonPolicy()
				// Policy-only registrations deliberately have no backing database names.
				// The real service still lists each authorized ID, without inventing one.
				for i := 99; i >= 0; i-- {
					id := fmt.Sprintf("r%031x", i)
					_, err := p.CreateWithID(context.Background(), h.actor("bob"), "tables", id, fmt.Sprintf("bound-%d", i), func(context.Context, policy.ResourceKey) error { return nil })
					Expect(err).NotTo(HaveOccurred())
				}
				backend := modelListingBackend(h, forwarded)
				a := addontools.NewToolAdapter(backend)
				check := func(truncated bool) {
					GinkgoHelper()
					// Check the upstream real service bound too: adapter-side sorting and
					// clipping must not hide an upstream ordering/truncation regression.
					upstream, err := backend.ListResources(context.Background(), h.actor("bob"), "tables")
					Expect(err).NotTo(HaveOccurred())
					Expect(upstream.Resources).To(HaveLen(100))
					Expect(upstream.Truncated).To(Equal(truncated))
					payload := executeModelList(a, h.actor("bob"))
					Expect(payload["truncated"]).To(Equal(truncated))
					rows := payload["databases"].([]any)
					Expect(rows).To(HaveLen(100))
					for i, row := range rows {
						id := fmt.Sprintf("r%031x", i)
						Expect(upstream.Resources[i].ID).To(Equal(id))
						Expect(row).To(Equal(map[string]any{"resourceId": id, "role": "owner"}))
					}
				}
				check(false)
				_, err := p.CreateWithID(context.Background(), h.actor("bob"), "tables", fmt.Sprintf("r%031x", 100), "bound-100", func(context.Context, policy.ResourceKey) error { return nil })
				Expect(err).NotTo(HaveOccurred())
				check(true)
			})
		})
	}

	It("rechecks credentials at the carrier owner even if the frontend still admits the old principal", func() {
		h := ready(false)
		backend := modelListingBackend(h, true)
		// Model a frontend with stale identity state; only the owner DB changes.
		backend.Revalidate = func(context.Context, contract.Actor) error { return nil }
		a := addontools.NewToolAdapter(backend)
		actor := h.actor("bob")
		discoverModelList(a, actor)
		Expect(h.app.AuthDB().Delete(&auth.UserAPIKey{}, "id = ?", h.keys["bob"]).Error).To(Succeed())
		modelListDenied(a, actor, gateway.ErrForbidden)
	})

	It("reports an unavailable carrier owner after discovery instead of fabricating empty success", func() {
		h := ready(false)
		a := addontools.NewToolAdapter(modelListingBackend(h, true))
		actor := h.actor("bob")
		discoverModelList(a, actor)
		h.stop()
		modelListDenied(a, actor, gateway.ErrUnavailable)
	})
})
