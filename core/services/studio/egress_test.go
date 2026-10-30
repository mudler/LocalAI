package studio_test

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/egress"
	"github.com/mudler/LocalAI/core/services/studio/egress/audit"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the Sandbox network state of a chat", func() {
	ctx := context.Background()

	It("starts a chat with the instance default, capped by the ceiling, and a fork copies the preset and the allow rules", func() {
		st, _ := store.NewJSON("")
		settings := egress.Settings{Ceiling: egress.PresetRegistries, Default: egress.PresetAll}
		svc := newServiceOn(st, studiotest.NewGateHarness(), func(d *studio.Deps) {
			d.EgressSettings = func() egress.Settings { return settings }
		})
		id := create(svc, alice)
		rec, err := svc.Get(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Egress.Preset).To(Equal(egress.PresetRegistries))

		Expect(st.UpdateEgress(ctx, alice, id, func(s *egress.ChatState) error {
			s.PutRule(egress.Rule{Host: "example.com", Port: 443, Allow: true})
			s.PutRule(egress.Rule{Host: "evil.example", Port: 443})
			s.PutCard(egress.Card{ID: "k", Host: "x.example", Port: 443, State: egress.CardDenied})
			return nil
		})).To(Succeed())
		fork, err := svc.Fork(ctx, alice, id, 0, "")
		Expect(err).NotTo(HaveOccurred())
		got, _ := svc.Get(ctx, alice, fork.ID)
		Expect(got.Egress.Preset).To(Equal(egress.PresetRegistries))
		Expect(got.Egress.Rules).To(ConsistOf(HaveField("Host", "example.com")))
		Expect(got.Egress.Cards).To(BeEmpty())
	})

	It("starts with off when nothing is set", func() {
		st, _ := store.NewJSON("")
		svc := newServiceOn(st, studiotest.NewGateHarness())
		rec, _ := svc.Get(ctx, alice, create(svc, alice))
		Expect(rec.Egress.Preset).To(Equal(egress.PresetOff))
	})

	It("prunes the audit lines older than the retention", func() {
		st, _ := store.NewJSON("")
		a, _ := audit.NewJSON("")
		now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
		svc := newServiceOn(st, studiotest.NewGateHarness(), func(d *studio.Deps) {
			d.EgressAudit = a
			d.EgressAuditDays = func() int { return 2 }
			d.Clock = func() time.Time { return now }
		})
		Expect(a.Add(ctx, audit.Event{Host: "old", Verdict: "allow", At: now.Add(-72 * time.Hour)}, audit.Event{Host: "new", Verdict: "allow", At: now.Add(-time.Hour)})).To(Succeed())
		n, err := svc.PruneEgressAudit(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(int64(1)))
	})
})
