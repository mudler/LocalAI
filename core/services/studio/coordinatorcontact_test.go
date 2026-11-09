package studio_test

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/coordinator"
	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// contactRig is a coordinator rig whose host reads the clock of the shared
// stores, so a test moves time.
type contactRig struct{ *coordRig }

func newContactRigOn(sh *shared, st coordinator.Store) *contactRig {
	r := sh.rig(coordScript, studio.TriggerOptions{})
	host, err := studio.NewCoordinators(r.svc, studio.CoordinatorOptions{Store: st, Clock: sh.clock.Now})
	Expect(err).NotTo(HaveOccurred())
	r.svc.SetCoordinators(host)
	return &contactRig{&coordRig{trigRig: r, host: host, store: st}}
}

func newContactRig() *contactRig {
	st, err := coordinator.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	return newContactRigOn(newShared(), st)
}

// desk makes a coordinator whose contacts are the owner ("me") and the
// config's caps, and returns its agent and the tool chat.
func (r *contactRig) desk(perMinute, perDay int) (agentID string, chat builtins.Chat) {
	in := studio.CoordinatorInput{
		Agent: agents.Agent{Name: "Desk", Model: "m"},
		Config: coordinator.Config{
			SendPerMinute: perMinute, SendPerDay: perDay,
			Contacts: []coordinator.Contact{{ID: "me", Name: "Me", Description: "the owner", Kind: coordinator.ContactUser, User: alice}},
		},
	}
	v, err := r.host.Create(context.Background(), alice, in)
	Expect(err).NotTo(HaveOccurred())
	return v.Agent.ID, builtins.Chat{
		Actor:  workspace.Actor{Principal: alice, ChatID: v.Config.ChatID},
		Origin: "coordinator", OriginID: v.Agent.ID, Agent: true, AgentID: v.Agent.ID, Model: "m",
	}
}

func (r *contactRig) send(chat builtins.Chat, to, text string) string {
	out, err := r.host.Contact(context.Background(), chat, studio.ContactInput{Action: "send", To: to, Text: text})
	Expect(err).NotTo(HaveOccurred())
	return out
}

func (r *contactRig) audit(agentID string) []coordinator.AuditEntry {
	rows, err := r.host.Audit(context.Background(), studio.CoordinatorActor{User: alice}, coordinator.AuditQuery{CoordinatorID: agentID, Kind: coordinator.KindContact})
	Expect(err).NotTo(HaveOccurred())
	return rows
}

func (r *contactRig) inbox() []notify.Notification {
	ns, err := r.trigRig.inbox.List(context.Background(), alice, false, 50)
	Expect(err).NotTo(HaveOccurred())
	return ns
}

var _ = Describe("coordinator contact", func() {
	ctx := context.Background()

	It("list gives ids, names and descriptions and no binding or chat handle", func() {
		r := newContactRig()
		_, chat := r.desk(0, 0)
		out, err := r.host.Contact(ctx, chat, studio.ContactInput{Action: "list"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("me"))
		Expect(out).To(ContainSubstring("Me"))
		Expect(out).To(ContainSubstring("the owner"))
		Expect(out).NotTo(ContainSubstring(alice), "the account of a user contact is not shown")
	})

	It("send to a user contact writes an inbox item for that user", func() {
		r := newContactRig()
		id, chat := r.desk(0, 0)
		text := strings.Repeat("é", 400)
		Expect(r.send(chat, "me", text)).To(ContainSubstring("Sent"))
		ns := r.inbox()
		Expect(ns).To(HaveLen(1))
		Expect(ns[0].Source).To(Equal(notify.SourceCoordinator))
		Expect(ns[0].Kind).To(Equal(notify.KindNeedsAttention))
		Expect([]rune(ns[0].Body)).To(HaveLen(notify.MaxBodyRunes))
		Expect(ns[0].Link).To(Equal(notify.ChatLink(chat.Actor.ChatID)))
		Expect(ns[0].Code).To(Equal("coordinator_message"))
		Expect(ns[0].Params).To(HaveKeyWithValue("coordinator", "Desk"))
		rows := r.audit(id)
		Expect(rows).To(HaveLen(1))
		Expect(rows[0]).To(MatchFields(IgnoreExtras, Fields{
			"Outcome": Equal(coordinator.OutcomeSent), "Reason": Equal(coordinator.ReasonOK),
			"Target": Equal("me"), "Via": Equal("inbox"), "Actor": Equal("coordinator"),
		}))
	})

	It("send to an unknown contact is denied and audited, nothing reaches the inbox", func() {
		r := newContactRig()
		id, chat := r.desk(0, 0)
		Expect(r.send(chat, "stranger", "hello")).To(ContainSubstring("not a contact"))
		Expect(r.inbox()).To(BeEmpty())
		rows := r.audit(id)
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].Outcome).To(Equal(coordinator.OutcomeDenied))
		Expect(rows[0].Reason).To(Equal(coordinator.ReasonNotAContact))
	})

	It("the third send in a minute is denied with a cap of 2 and the next minute passes", func() {
		r := newContactRig()
		id, chat := r.desk(2, 0)
		Expect(r.send(chat, "me", "one")).To(ContainSubstring("Sent"))
		Expect(r.send(chat, "me", "two")).To(ContainSubstring("Sent"))
		Expect(r.send(chat, "me", "three")).To(ContainSubstring("minute"))
		Expect(r.inbox()).To(HaveLen(2))
		r.sh.clock.Advance(time.Minute)
		Expect(r.send(chat, "me", "four")).To(ContainSubstring("Sent"))
		Expect(r.inbox()).To(HaveLen(3))
		var denied []string
		for _, e := range r.audit(id) {
			if e.Outcome == coordinator.OutcomeDenied {
				denied = append(denied, e.Reason)
			}
		}
		Expect(denied).To(Equal([]string{coordinator.ReasonMinuteCap}))
	})

	It("the daily cap resets at UTC midnight", func() {
		r := newContactRig()
		_, chat := r.desk(60, 2)
		Expect(r.send(chat, "me", "one")).To(ContainSubstring("Sent"))
		Expect(r.send(chat, "me", "two")).To(ContainSubstring("Sent"))
		Expect(r.send(chat, "me", "three")).To(ContainSubstring("day"))
		// The rig starts at 07:00 UTC.
		r.sh.clock.Advance(17*time.Hour + time.Minute)
		Expect(r.send(chat, "me", "next day")).To(ContainSubstring("Sent"))
	})

	It("two frontends share the cap", func() {
		sh := newShared()
		st, err := coordinator.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		a, b := newContactRigOn(sh, st), newContactRigOn(sh, st)
		id, chatA := a.desk(60, 3)
		chatB := chatA // the same coordinator and chat, seen from the other frontend
		sent := 0
		for _, c := range []struct {
			r    *contactRig
			chat builtins.Chat
		}{{a, chatA}, {b, chatB}, {a, chatA}, {b, chatB}} {
			if strings.Contains(c.r.send(c.chat, "me", "x"), "Sent") {
				sent++
			}
		}
		Expect(sent).To(Equal(3))
		rows := a.audit(id)
		denied := 0
		for _, e := range rows {
			if e.Outcome == coordinator.OutcomeDenied && e.Reason == coordinator.ReasonDayCap {
				denied++
			}
		}
		Expect(denied).To(Equal(1))
	})

	It("a send after pause is denied even when the turn was not stopped", func() {
		r := newContactRig()
		id, chat := r.desk(0, 0)
		_, _, err := r.store.SetPaused(ctx, id, true, alice, time.Now())
		Expect(err).NotTo(HaveOccurred())
		Expect(r.send(chat, "me", "still going")).To(ContainSubstring("paused"))
		Expect(r.inbox()).To(BeEmpty())
		rows := r.audit(id)
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].Reason).To(Equal(coordinator.ReasonPaused))
	})

	It("a helper and an ordinary chat are not offered the tool", func() {
		r := newContactRig()
		_, chat := r.desk(0, 0)
		_, err := r.svc.Send(ctx, alice, chat.Actor.ChatID, "hello")
		Expect(err).NotTo(HaveOccurred())
		until("the turn to end", func() bool { return !turn(r.svc, alice, chat.Actor.ChatID).Running })
		Expect(r.h.ToolNames(chat.Actor.ChatID)).To(ContainElement(studio.ContactName))

		plain := create(r.svc, alice)
		_, err = r.svc.Send(ctx, alice, plain, "hello")
		Expect(err).NotTo(HaveOccurred())
		until("the turn to end", func() bool { return !turn(r.svc, alice, plain).Running })
		Expect(r.h.ToolNames(plain)).NotTo(ContainElement(studio.ContactName))

		helper := chat
		helper.SubAgent = true
		out, err := r.host.Contact(ctx, helper, studio.ContactInput{Action: "list"})
		Expect(err).To(HaveOccurred(), out)
	})

	It("empty and over-long text are refused and audited bad_text", func() {
		r := newContactRig()
		id, chat := r.desk(0, 0)
		Expect(r.send(chat, "me", "  ")).To(ContainSubstring("text"))
		Expect(r.send(chat, "me", strings.Repeat("a", 4001))).To(ContainSubstring("text"))
		Expect(r.inbox()).To(BeEmpty())
		rows := r.audit(id)
		Expect(rows).To(HaveLen(2))
		for _, e := range rows {
			Expect(e.Reason).To(Equal(coordinator.ReasonBadText))
		}
	})

	It("the audit row holds length and hash, not the text", func() {
		r := newContactRig()
		id, chat := r.desk(0, 0)
		r.send(chat, "me", "a secret plan")
		rows := r.audit(id)
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].Length).To(Equal(len("a secret plan")))
		Expect(rows[0].Hash).To(HaveLen(64))
		Expect(rows[0].Hash).NotTo(ContainSubstring("secret"))
		Expect(rows[0].Target + rows[0].Via + rows[0].Reason).NotTo(ContainSubstring("secret"))
	})

	It("denies a send to a user contact when there is no inbox", func() {
		sh := newShared()
		sh.deps = func(d *studio.Deps) { d.Notify = nil }
		st, err := coordinator.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		r := newContactRigOn(sh, st)
		id, chat := r.desk(0, 0)
		Expect(r.send(chat, "me", "hello")).To(ContainSubstring("inbox"))
		Expect(r.audit(id)[0].Reason).To(Equal(coordinator.ReasonInboxOff))
	})

	It("says an unknown action is not one", func() {
		r := newContactRig()
		_, chat := r.desk(0, 0)
		_, err := r.host.Contact(ctx, chat, studio.ContactInput{Action: "shout"})
		Expect(err).To(HaveOccurred())
	})
})
