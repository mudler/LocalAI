// SPDX-License-Identifier: MIT
package studio_test

import (
	"context"

	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/egress"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A shared agent runs with the grantee's authority, so what the owner asked
// for in its definition never widens what the grantee's own chats start with:
// the network of its commands and the opt-in groups of built-in tools are the
// narrower of the agent's and the grantee's own defaults. The needs list says
// what the agent asks for, so the person knows before the first message.
var _ = Describe("the authority of a chat with a shared agent", func() {
	ctx := context.Background()
	// The instance lets a chat reach every host, and a new chat starts with
	// the registries only.
	settings := egress.Settings{Ceiling: egress.PresetAll, Default: egress.PresetRegistries}
	newRig := func() *shareRig {
		return newShareRig(sayOK, func(d *studio.Deps) {
			d.EgressSettings = func(context.Context) (egress.Settings, error) { return settings, nil }
		})
	}
	wide := agents.Agent{ID: "ag1", Name: "Wide", Network: string(egress.PresetAll),
		Builtins: []string{builtins.GrantHistoryRead}, BuiltinsOn: []string{builtins.GroupHistory}}

	It("starts the grantee's chat with the narrower network and none of the opt-in groups the agent turns on", func() {
		r := newRig()
		r.share(wide, bob, policy.RoleUse)
		rec, err := r.svc.Create(ctx, bob, studio.CreateInput{Model: "m", AgentID: "ag1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Egress.Preset).To(Equal(egress.PresetRegistries), "the grantee's own default, not the owner's all")
		Expect(rec.BuiltinsOn).NotTo(ContainElement(builtins.GroupHistory), "the agent does not read the grantee's earlier chats unless the grantee turns it on")

		// The owner's own chat keeps what the owner asked for.
		own, err := r.svc.Create(ctx, alice, studio.CreateInput{Model: "m", AgentID: "ag1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(own.Egress.Preset).To(Equal(egress.PresetAll))
		Expect(own.BuiltinsOn).To(ContainElement(builtins.GroupHistory))
	})

	It("lets the agent narrow the grantee's network", func() {
		narrow := wide
		narrow.Network = string(egress.PresetRegistries)
		settings := settings
		settings.Default = egress.PresetAll
		r2 := newShareRig(sayOK, func(d *studio.Deps) {
			d.EgressSettings = func(context.Context) (egress.Settings, error) { return settings, nil }
		})
		r2.share(narrow, bob, policy.RoleUse)
		rec, err := r2.svc.Create(ctx, bob, studio.CreateInput{Model: "m", AgentID: "ag1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Egress.Preset).To(Equal(egress.PresetRegistries))
	})

	It("lists the network and the opt-in groups the agent asks for, for the grantee, without the instructions", func() {
		r := newRig()
		secret := wide
		secret.Instructions = "SECRET-INSTRUCTIONS"
		r.share(secret, bob, policy.RoleUse)
		needs, err := r.svc.AgentNeeds(ctx, bob, "ag1")
		Expect(err).NotTo(HaveOccurred())
		states := map[string]string{}
		for _, n := range needs {
			states[n.Kind+":"+n.Label] = n.State
			Expect(n.Label).NotTo(ContainSubstring("SECRET"))
		}
		Expect(states).To(HaveKeyWithValue("network:all", studio.ReferenceAsk), "the chat starts narrower; the grantee may widen it")
		Expect(states).To(HaveKeyWithValue("tool-group:history", studio.ReferenceAsk))
		own, err := r.svc.AgentNeeds(ctx, alice, "ag1")
		Expect(err).NotTo(HaveOccurred())
		for _, n := range own {
			if n.Kind == "network" || n.Kind == "tool-group" {
				Expect(n.State).To(Equal(studio.ReferenceOK), n.Kind)
			}
		}
	})

	It("says a network above the instance ceiling is unavailable", func() {
		low := egress.Settings{Ceiling: egress.PresetRegistries, Default: egress.PresetOff}
		r := newShareRig(sayOK, func(d *studio.Deps) {
			d.EgressSettings = func(context.Context) (egress.Settings, error) { return low, nil }
		})
		r.share(wide, bob, policy.RoleUse)
		needs, err := r.svc.AgentNeeds(ctx, bob, "ag1")
		Expect(err).NotTo(HaveOccurred())
		states := map[string]string{}
		for _, n := range needs {
			states[n.Kind+":"+n.Label] = n.State
		}
		Expect(states).To(HaveKeyWithValue("network:all", studio.ReferenceUnavailable))
		rec, err := r.svc.Create(ctx, bob, studio.CreateInput{Model: "m", AgentID: "ag1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Egress.Preset).To(Equal(egress.PresetOff))
	})
})
