package studio_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"

	"github.com/mudler/LocalAI/core/http/auth"

	"github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const createVoiceArgs = `{"action":"create","name":"Narrator","audio":"/workspace/home/clip.wav","transcript":"Hello there.","language":"en"}`

var _ = Describe("the voice_profile tool and the consent of a person", func() {
	ctx := context.Background()

	newRig := func(args string) *localaiRig {
		return newLocalAIRig(func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: localaitools.ToolVoiceProfile, Args: args}, {Say: "done"}}
		}, func(d *studio.Deps) { d.Files = newFiles() })
	}
	pending := func(r *localaiRig, user string) int {
		GinkgoHelper()
		list, err := r.svc.Changes().List(ctx, user, contract.ProposalPending)
		Expect(err).NotTo(HaveOccurred())
		return len(list)
	}

	It("is offered to a user without any installed model, and is a client tool", func() {
		r := newLocalAIRig(sayOK)
		r.client.uninstall(0)
		Expect(r.names(bob)).To(ContainElement(localaitools.ToolVoiceProfile))
	})

	It("creates nothing when the model asks to create a profile: it proposes, and a person must confirm", func() {
		r := newRig(createVoiceArgs)
		id := create(r.svc, bob)
		sent(r.svc, bob, id, "clone me")
		idle(r.svc, bob, id)

		res := r.h.ResultsFor(localaitools.ToolVoiceProfile)
		Expect(res).To(HaveLen(1))
		Expect(res[0].Out).To(ContainSubstring("Nothing is created yet"))
		Expect(r.client.voiceCalls()).To(Equal([]string{"check"}), "the profile must not be created before a person confirms")
		Expect(pending(r, bob)).To(Equal(1))
	})

	It("creates the profile as the owner, with the consent in the context, after the person confirms", func() {
		r := newRig(createVoiceArgs)
		id := create(r.svc, bob)
		sent(r.svc, bob, id, "clone me")
		idle(r.svc, bob, id)
		list, err := r.svc.Changes().List(ctx, bob, contract.ProposalPending)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].Kind).To(Equal(contract.ChangeVoiceProfile))

		done, err := r.svc.Changes().Confirm(ctx, bob, list[0].ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(done.Status).To(Equal(contract.ProposalApplied))
		Expect(r.client.voiceCalls()).To(Equal([]string{"check", "consent"}))
		Expect(r.client.users[len(r.client.users)-1]).To(Equal(bob))
	})

	It("pins the clip at proposal time and confirms with that pin", func() {
		r := newRig(createVoiceArgs)
		id := create(r.svc, bob)
		sent(r.svc, bob, id, "clone me")
		idle(r.svc, bob, id)
		list, err := r.svc.Changes().List(ctx, bob, contract.ProposalPending)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].Content).To(ContainSubstring(`"audio_sha256":"` + strings.Repeat("ab", 32) + `"`))
		Expect(list[0].Content).To(ContainSubstring(`"audio_bytes":64044`))
		Expect(list[0].Content).To(ContainSubstring(`"audio_ms":2000`))
		done, err := r.svc.Changes().Confirm(ctx, bob, list[0].ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(done.Status).To(Equal(contract.ProposalApplied))
	})

	It("does not create the profile when the person dismisses the proposal", func() {
		r := newRig(createVoiceArgs)
		id := create(r.svc, bob)
		sent(r.svc, bob, id, "clone me")
		idle(r.svc, bob, id)
		list, _ := r.svc.Changes().List(ctx, bob, contract.ProposalPending)
		Expect(list).To(HaveLen(1))
		_, err := r.svc.Changes().Dismiss(ctx, bob, list[0].ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.client.voiceCalls()).To(Equal([]string{"check"}))
	})

	It("rejects a clip that is not a WAV file before it asks the person", func() {
		r := newRig(`{"action":"create","name":"N","audio":"/workspace/home/clip.mp3","transcript":"Hi."}`)
		id := create(r.svc, bob)
		sent(r.svc, bob, id, "clone me")
		idle(r.svc, bob, id)
		res := r.h.ResultsFor(localaitools.ToolVoiceProfile)
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).To(MatchError(ContainSubstring("must be a WAV file")))
		Expect(pending(r, bob)).To(BeZero())
	})

	It("refuses the create action of a run that nobody watches, whatever act.spend allows", func() {
		for _, origin := range []string{session.OriginTrigger, session.OriginJob} {
			r := newRig(createVoiceArgs)
			saved, err := r.svc.SaveAgent(ctx, "root", agents.Agent{Name: "Auto", Model: "m", Builtins: []string{builtins.GrantActSpend}})
			Expect(err).NotTo(HaveOccurred())
			rec, err := r.svc.CreateAs(ctx, "root", studio.CreateInput{Model: "m", AgentID: saved.ID}, studio.Origin{Kind: origin, ID: "x"})
			Expect(err).NotTo(HaveOccurred())
			r.turnNames("root", rec.ID)
			res := r.h.ResultsFor(localaitools.ToolVoiceProfile)
			Expect(res).To(HaveLen(1), origin)
			// A trigger without a policy is stopped by the act gate before the
			// tool; a job with act.spend reaches the tool, which refuses.
			Expect(res[0].Out).To(Or(ContainSubstring("nobody can confirm"), ContainSubstring("I did not run")), origin)
			Expect(r.client.voiceCalls()).To(BeEmpty(), origin)
			Expect(pending(r, "root")).To(BeZero(), origin)
		}
	})

	It("refuses delete in a run that nobody watches, even with act.spend, and lets list run without it", func() {
		unattendedRun := func(args string, grants ...string) (*localaiRig, string) {
			r := newRig(args)
			saved, err := r.svc.SaveAgent(ctx, "root", agents.Agent{Name: "Auto", Model: "m", Builtins: grants})
			Expect(err).NotTo(HaveOccurred())
			rec, err := r.svc.CreateAs(ctx, "root", studio.CreateInput{Model: "m", AgentID: saved.ID}, studio.Origin{Kind: session.OriginJob, ID: "x"})
			Expect(err).NotTo(HaveOccurred())
			r.turnNames("root", rec.ID)
			res := r.h.ResultsFor(localaitools.ToolVoiceProfile)
			Expect(res).To(HaveLen(1))
			return r, res[0].Out
		}
		r, out := unattendedRun(`{"action":"delete","id":"p1"}`, builtins.GrantActSpend)
		Expect(out).To(ContainSubstring("Not deleted"))
		Expect(r.client.voiceCalls()).To(BeEmpty())

		r, out = unattendedRun(`{"action":"list"}`)
		Expect(out).NotTo(ContainSubstring("I did not run"))
		Expect(r.client.voiceCalls()).To(Equal([]string{"plain-list"}))
	})

	It("refuses the create action in a messaging chat, where no one can confirm in Studio", func() {
		r := newRig(createVoiceArgs)
		saved, err := r.svc.SaveAgent(ctx, "root", agents.Agent{Name: "Bot", Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		rec, err := r.svc.CreateAs(ctx, "root", studio.CreateInput{Model: "m", AgentID: saved.ID}, studio.Origin{Kind: session.OriginConnector, ID: "x"})
		Expect(err).NotTo(HaveOccurred())
		r.turnNames("root", rec.ID)
		res := r.h.ResultsFor(localaitools.ToolVoiceProfile)
		Expect(res).To(HaveLen(1))
		Expect(res[0].Out).To(ContainSubstring("not in a messaging chat"))
		Expect(r.client.voiceCalls()).To(BeEmpty())
		Expect(pending(r, "root")).To(BeZero())
	})

	It("lets list and delete through unchanged", func() {
		for _, args := range []string{`{"action":"list"}`, `{"action":"delete","id":"p1"}`} {
			r := newRig(args)
			id := create(r.svc, bob)
			sent(r.svc, bob, id, "go")
			idle(r.svc, bob, id)
			Expect(r.client.voiceCalls()).To(HaveLen(1))
			Expect(r.client.voiceCalls()[0]).To(HavePrefix("plain-"))
		}
	})

	It("is not a kind that propose_change offers or accepts", func() {
		r := newLocalAIRig(func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: "propose_change", Args: proposeArgs("voice_profile", "user", "", `{"name":"x","audio":"a.wav","transcript":"t"}`, "clone")}, {Say: "ok"}}
		}, func(d *studio.Deps) { d.Files = newFiles() })
		id := create(r.svc, bob)
		sent(r.svc, bob, id, "go")
		idle(r.svc, bob, id)
		Expect(pending(r, bob)).To(BeZero())
		Expect(r.client.voiceCalls()).To(BeEmpty())
	})

	It("does not create the profile, and says why, when the owner cannot be found at confirmation", func() {
		var lost atomic.Bool
		r := newLocalAIRig(func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: localaitools.ToolVoiceProfile, Args: createVoiceArgs}, {Say: "done"}}
		}, func(d *studio.Deps) {
			d.Files = newFiles()
			d.Users = func(ctx context.Context, id string) (*auth.User, error) {
				if lost.Load() {
					return nil, errors.New("user database is down")
				}
				return assistantUsers(ctx, id)
			}
		})
		id := create(r.svc, bob)
		sent(r.svc, bob, id, "clone me")
		idle(r.svc, bob, id)
		list, err := r.svc.Changes().List(ctx, bob, contract.ProposalPending)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))

		lost.Store(true)
		done, err := r.svc.Changes().Confirm(ctx, bob, list[0].ID)
		if err == nil {
			Expect(done.Status).NotTo(Equal(contract.ProposalApplied))
		} else {
			Expect(err.Error()).To(ContainSubstring("not available"))
		}
		Expect(r.client.voiceCalls()).To(Equal([]string{"check"}), "no profile may be created without an acting user")
		for _, who := range r.client.users {
			Expect(who).NotTo(BeEmpty(), "every call to the client tools carries the acting user")
		}
	})

	It("offers no LocalAI tool, and sends no request, when the owner cannot be found for a turn", func() {
		var lost atomic.Bool
		r := newLocalAIRig(sayOK, func(d *studio.Deps) {
			d.Users = func(ctx context.Context, id string) (*auth.User, error) {
				if lost.Load() {
					return nil, errors.New("user database is down")
				}
				return assistantUsers(ctx, id)
			}
		})
		id := create(r.svc, "root")
		r.adminOnFor("root")(id)
		lost.Store(true)
		names := r.turnNames("root", id)
		for _, n := range append(clientNames(), "install_model") {
			Expect(names).NotTo(ContainElement(n))
		}
		Expect(r.client.callCount()).To(BeZero())
	})
})
