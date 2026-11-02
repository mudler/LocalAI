// SPDX-License-Identifier: MIT
package studio_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/workerctl"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("automatic memory boundaries", func() {
	for _, mode := range []string{"off", "incoming_only", "incoming_and_replies", "summary"} {
		It("captures a direct exchange in "+mode, func() {
			var st store.Store
			r, _ := memoryRig(sayOK, func(d *studio.Deps) { st = d.Store })
			a := memoryPolicy()
			a.Memory.AddMemory = false
			a.Memory.AutomaticIngestion = mode
			r.save(a)
			id := r.chatWith(a.ID)
			sent(r.svc, alice, id, "only these words")
			idle(r.svc, alice, id)
			records, err := st.ListCaptures(context.Background(), alice, id)
			Expect(err).NotTo(HaveOccurred())
			texts := []string{}
			kinds := []string{}
			for _, record := range records {
				texts = append(texts, record.RawText)
				kinds = append(kinds, record.Identity.Kind)
			}
			switch mode {
			case "off":
				Expect(records).To(BeEmpty())
			case "incoming_only":
				Expect(texts).To(ConsistOf("only these words"))
				Expect(kinds).To(ConsistOf("incoming"))
			case "incoming_and_replies":
				Expect(texts).To(ConsistOf("only these words", "ok"))
				Expect(kinds).To(ConsistOf("incoming", "reply"))
			case "summary":
				Expect(texts).To(ConsistOf("only these words\n\nok"))
				Expect(kinds).To(ConsistOf("summary"))
			}
			Expect(r.h.ToolNames(id)).NotTo(ContainElement("add_memory"))
		})
	}
})

// These tests inspect durable intents without running the independent capture
// worker: answering must not wait for a collection backend.
var _ = Describe("automatic memory lifecycle", func() {
	for _, ending := range []string{"stopped", "paused", "failed"} {
		It("keeps accepted pending input but not the "+ending+" reply", func() {
			var st store.Store
			h := studiotest.NewGateHarness()
			if ending == "paused" {
				h.PauseSteps = 4
			}
			if ending == "failed" {
				h.FailRequest = 1
			}
			r, _ := memoryRig(sayOK, func(d *studio.Deps) { st = d.Store; d.Harness = h })
			a := memoryPolicy()
			a.Memory.AutomaticIngestion = "incoming_and_replies"
			r.save(a)
			id := r.chatWith(a.ID)
			sent(r.svc, alice, id, "first")
			until("harness start", func() bool { return len(h.Sent()) == 1 })
			p := sent(r.svc, alice, id, "accepted original")
			Expect(r.svc.EditPending(context.Background(), alice, id, p.PendingID, "edited for answering")).To(Succeed())
			if ending == "stopped" {
				Expect(r.svc.CancelPending(context.Background(), alice, id, p.PendingID)).To(Succeed())
				Expect(r.svc.Stop(context.Background(), alice, id)).To(Succeed())
			} else {
				h.Gate <- struct{}{}
			}
			idle(r.svc, alice, id)
			records, err := st.ListCaptures(context.Background(), alice, id)
			Expect(err).NotTo(HaveOccurred())
			texts := []string{}
			for _, rec := range records {
				Expect(rec.Identity.Kind).To(Equal("incoming"))
				texts = append(texts, rec.RawText)
			}
			Expect(texts).To(ConsistOf("first", "accepted original"))
		})
	}
	It("summarizes consumed pending input at acceptance, not its edit or older history", func() {
		var st store.Store
		h := studiotest.NewGateHarness()
		r, _ := memoryRig(sayOK, func(d *studio.Deps) { st = d.Store; d.Harness = h })
		a := memoryPolicy()
		a.Memory.AutomaticIngestion = "summary"
		r.save(a)
		id := r.chatWith(a.ID)
		sent(r.svc, alice, id, "first")
		until("harness start", func() bool { return len(h.Sent()) == 1 })
		p := sent(r.svc, alice, id, "accepted original")
		Expect(r.svc.EditPending(context.Background(), alice, id, p.PendingID, "edited")).To(Succeed())
		h.Gate <- struct{}{}
		idle(r.svc, alice, id)
		records, err := st.ListCaptures(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(HaveLen(1))
		Expect(records[0].RawText).To(Equal("first\n\naccepted original\n\nworking on: firstdone"))
		sent(r.svc, alice, id, "second")
		until("second start", func() bool { return len(h.Sent()) == 2 })
		h.Gate <- struct{}{}
		idle(r.svc, alice, id)
		records, err = st.ListCaptures(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(HaveLen(2))
		texts := []string{}
		for _, rec := range records {
			texts = append(texts, rec.RawText)
		}
		Expect(texts).To(ContainElement("second\n\nworking on: seconddone"))
	})
	for _, origin := range []string{session.OriginConnector, session.OriginTrigger} {
		It("captures explicit incoming text on a worker for "+origin, func() {
			var st store.Store
			r, _ := memoryRig(sayOK, func(d *studio.Deps) { st = d.Store })
			a := memoryPolicy()
			a.Memory.AutomaticIngestion = "incoming_and_replies"
			r.save(a)
			rec, err := r.svc.CreateAs(context.Background(), alice, studio.CreateInput{Model: "m", AgentID: a.ID}, studio.Origin{Kind: origin, ID: "source-binding"})
			Expect(err).NotTo(HaveOccurred())
			reply := r.svc.ServeStart(context.Background(), workerctl.ChatStartRequest{Principal: alice, ChatID: rec.ID, Text: "explicit incoming"})
			Expect(reply.Error).To(BeEmpty())
			idle(r.svc, alice, rec.ID)
			records, err := st.ListCaptures(context.Background(), alice, rec.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(records).To(HaveLen(2))
			for _, record := range records {
				Expect(record.Origin.Kind).To(Equal(origin))
				Expect(record.Origin.ConnectorID).To(Equal("source-binding"))
			}
			chat, err := r.svc.Get(context.Background(), alice, rec.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(chat.Model).To(Equal("m"))
		})
	}
	It("regenerates a distinct reply without accepting the input again", func() {
		var st store.Store
		r, _ := memoryRig(sayOK, func(d *studio.Deps) { st = d.Store })
		a := memoryPolicy()
		a.Memory.AutomaticIngestion = "incoming_and_replies"
		r.save(a)
		id := r.chatWith(a.ID)
		sent(r.svc, alice, id, "one input")
		idle(r.svc, alice, id)
		_, err := r.svc.Regenerate(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		idle(r.svc, alice, id)
		records, err := st.ListCaptures(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(HaveLen(3))
		kinds := []string{}
		sources := map[string]bool{}
		for _, record := range records {
			kinds = append(kinds, record.Identity.Kind)
			sources[record.Identity.Source] = true
		}
		Expect(kinds).To(ConsistOf("incoming", "reply", "reply"))
		Expect(sources).To(HaveLen(3))
	})

	It("does not capture plain chats", func() {
		var st store.Store
		r, _ := memoryRig(sayOK, func(d *studio.Deps) { st = d.Store })
		rec, err := r.svc.Create(context.Background(), alice, studio.CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		sent(r.svc, alice, rec.ID, "plain")
		idle(r.svc, alice, rec.ID)
		records, err := st.ListCaptures(context.Background(), alice, rec.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(BeEmpty())
	})
})

var _ = Describe("durable worker delivery identity", func() {
	for _, kind := range []string{"off", "plain"} {
		It("deduplicates "+kind+" worker deliveries without capture intents", func() {
			var st store.Store
			r, _ := memoryRig(sayOK, func(d *studio.Deps) { st = d.Store })
			a := memoryPolicy()
			a.Memory.AutomaticIngestion = "off"
			r.save(a)
			agent := a.ID
			if kind == "plain" {
				agent = ""
			}
			id := r.chatWith(agent)
			req := workerctl.ChatStartRequest{Principal: alice, ChatID: id, Text: "same words", RequestID: "generic-1"}
			first := r.svc.ServeStart(context.Background(), req)
			Expect(first.Error).To(BeEmpty())
			idle(r.svc, alice, id)
			Expect(r.svc.ServeStart(context.Background(), req)).To(Equal(first))
			idle(r.svc, alice, id)
			Expect(r.h.Sent(id)).To(HaveLen(1))
			req.RequestID = "generic-2"
			Expect(r.svc.ServeStart(context.Background(), req).Error).To(BeEmpty())
			idle(r.svc, alice, id)
			Expect(r.h.Sent(id)).To(HaveLen(2))
			records, err := st.ListCaptures(context.Background(), alice, id)
			Expect(err).NotTo(HaveOccurred())
			Expect(records).To(BeEmpty())
		})
	}

	It("replays accepted worker delivery after automatic memory is switched off", func() {
		r, _ := memoryRig(sayOK, func(d *studio.Deps) {})
		a := memoryPolicy()
		a.Memory.AutomaticIngestion = "incoming_only"
		r.save(a)
		id := r.chatWith(a.ID)
		req := workerctl.ChatStartRequest{Principal: alice, ChatID: id, Text: "once", RequestID: "policy-replay"}
		first := r.svc.ServeStart(context.Background(), req)
		Expect(first.Error).To(BeEmpty())
		idle(r.svc, alice, id)
		a.Memory.AutomaticIngestion = "off"
		r.save(a)
		replay := r.svc.ServeStart(context.Background(), req)
		Expect(replay).To(Equal(first))
		idle(r.svc, alice, id)
		entries, err := r.svc.Messages(context.Background(), alice, id, 0, 100)
		Expect(err).NotTo(HaveOccurred())
		users := 0
		for _, e := range entries {
			if e.Role == "user" {
				users++
			}
		}
		Expect(users).To(Equal(1))
	})

	It("deduplicates a redelivered remote request without merging intentional identical inputs", func() {
		var st store.Store
		r, _ := memoryRig(sayOK, func(d *studio.Deps) { st = d.Store })
		a := memoryPolicy()
		a.Memory.AutomaticIngestion = "incoming_and_replies"
		r.save(a)
		id := r.chatWith(a.ID)
		request := func(identity, text string) workerctl.ChatStartRequest {
			raw, err := json.Marshal(map[string]string{"principal": alice, "chat_id": id, "text": text, "request_id": identity})
			Expect(err).NotTo(HaveOccurred())
			var req workerctl.ChatStartRequest
			Expect(json.Unmarshal(raw, &req)).To(Succeed())
			return req
		}
		first := r.svc.ServeStart(context.Background(), request("delivery-1", "frozen words"))
		Expect(first.Error).To(BeEmpty())
		idle(r.svc, alice, id)
		replay := r.svc.ServeStart(context.Background(), request("delivery-1", "changed on retry"))
		Expect(replay).To(Equal(first))
		idle(r.svc, alice, id)
		records, err := st.ListCaptures(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(HaveLen(2))
		texts := []string{}
		for _, v := range records {
			texts = append(texts, v.RawText)
		}
		Expect(texts).To(ConsistOf("frozen words", "ok"))
		next := r.svc.ServeStart(context.Background(), request("delivery-2", "frozen words"))
		Expect(next.Error).To(BeEmpty())
		idle(r.svc, alice, id)
		records, err = st.ListCaptures(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(records).To(HaveLen(4))
	})
})

// Acceptance and TakePending still use the real store: only the redundant
// standalone binding write is unavailable.
type failingMemoryBindStore struct {
	store.Store
}

func (s failingMemoryBindStore) BindMemoryTurn(context.Context, string, string, string, []string) error {
	return errors.New("memory binding unavailable")
}

var _ = Describe("worker memory binding failure isolation", func() {
	for _, requestID := range []string{"", "accepted-worker-delivery"} {
		It("answers and releases the claim with failing standalone binding for request "+requestID, func() {
			ctx := context.Background()
			var st store.Store
			r, _ := memoryRig(sayOK, func(d *studio.Deps) {
				st = d.Store
				d.Store = failingMemoryBindStore{Store: st}
			})
			a := memoryPolicy()
			a.Memory.AutomaticIngestion = "incoming_and_replies"
			r.save(a)
			id := r.chatWith(a.ID)
			reply := r.svc.ServeStart(ctx, workerctl.ChatStartRequest{
				Principal: alice, ChatID: id, Text: "remember despite binding failure", RequestID: requestID,
			})
			Expect(reply.Error).To(BeEmpty())
			Expect(reply.Started).To(BeTrue())
			idle(r.svc, alice, id)
			info, err := st.Turn(ctx, alice, id)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Running).To(BeFalse())
			Expect(info.Last).To(Equal(store.TurnDone))
			entries, err := r.svc.Messages(ctx, alice, id, 0, 100)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(ContainElement(And(HaveField("Role", "assistant"), HaveField("Text", "ok"))))
			records, err := st.ListCaptures(ctx, alice, id)
			Expect(err).NotTo(HaveOccurred())
			texts := []string{}
			for _, record := range records {
				texts = append(texts, record.RawText)
			}
			Expect(texts).To(ConsistOf("remember despite binding failure", "ok"))
			Expect(st.ClaimTurn(ctx, alice, id, "next-run")).To(Succeed())
			Expect(st.ReleaseTurn(ctx, alice, id, "next-run", store.TurnDone)).To(Succeed())
		})
	}
})
