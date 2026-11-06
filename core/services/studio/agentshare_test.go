// SPDX-License-Identifier: MIT
package studio_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connsets"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const carol = "carol"

// shareRig is a chat service whose saved agents are shared through a real
// sharing policy, the one the add-on owner runs.
type shareRig struct {
	svc    *studio.Service
	h      *studiotest.ToolHarness
	agents agents.Store
	pol    *policy.Policy
	colls  *fakeCollections
	// off holds the accounts whose agents feature is off.
	mu  sync.Mutex
	off map[string]bool
}

var shareUsers = map[string]string{alice: "Alice", bob: "Bob", carol: "Carol"}

func newShareRig(script func(workspace.Actor, string) []studiotest.ToolStep, opts ...func(*studio.Deps)) *shareRig {
	r := &shareRig{off: map[string]bool{}}
	st, err := policy.OpenStore(GinkgoT().TempDir(), policy.ModeNamedUser)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = st.Close() })
	r.pol = policy.New(st, policy.Options{
		DrainTimeout: 5 * time.Second,
		Revalidate:   func(context.Context, contract.Actor) error { return nil },
		Account: func(_ context.Context, a contract.Actor, feature string) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			if _, ok := shareUsers[a.UserID]; !ok || (feature == auth.FeatureAgents && r.off[a.UserID]) {
				return policy.ErrForbidden
			}
			return nil
		},
	})
	as, err := agents.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	r.agents = as
	r.colls = &fakeCollections{mine: map[string]map[string][]studio.CollectionHit{
		alice: {"docs": {{Content: "alice private"}}, "secret": nil},
		bob:   {"docs": {{Content: "bob own"}}},
	}}
	fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: {addonTool("office", "read-doc")}, bob: {addonTool("office", "read-doc")}}}
	r.svc, r.h = newToolService(script, func(d *studio.Deps) {
		d.Files, d.AddonTools, d.Collections, d.Agents = newFiles(), fa, r.colls, as
		d.Users = func(_ context.Context, id string) (*auth.User, error) {
			name, ok := shareUsers[id]
			if !ok {
				return nil, errors.New("no such user")
			}
			return &auth.User{ID: id, Name: name, Status: auth.StatusActive}, nil
		}
		d.AgentShares = studio.PolicyAgentShares{Policy: func() *policy.Policy { return r.pol }}
		for _, o := range opts {
			o(d)
		}
	})
	Expect(policy.RegisterKinds(r.pol, r.svc)).To(Succeed())
	return r
}

func ownerActor(user string) contract.Actor {
	return contract.Actor{UserID: user, CredentialID: auth.AddonChatsCredential, Mode: auth.AddonModeNamedUser}
}

// share saves an agent of alice and shares it with principal under role.
func (r *shareRig) share(a agents.Agent, principal string, role policy.Role) string {
	a.Principal, a.Created = alice, time.Now().UTC()
	Expect(r.agents.Put(context.Background(), a)).To(Succeed())
	ref, err := r.svc.AgentShareRef(context.Background(), alice, a.ID)
	Expect(err).NotTo(HaveOccurred())
	if principal != "" {
		r.grant(ref, principal, role)
	}
	return ref
}

func (r *shareRig) grant(ref, principal string, role policy.Role) {
	Expect(r.pol.SetGrantRef(context.Background(), ownerActor(alice), policy.Ref{Kind: studio.AgentShareKind, ResourceID: ref}, principal, role)).To(Succeed())
}

func (r *shareRig) revoke(ref, principal string) error {
	return r.pol.RevokeRef(context.Background(), ownerActor(alice), policy.Ref{Kind: studio.AgentShareKind, ResourceID: ref}, principal)
}

var _ = Describe("shared saved agents", func() {
	ctx := context.Background()

	It("lists an agent shared with the grantee with the owner's name and the role, and the owner's with the count", func() {
		r := newShareRig(sayOK)
		r.share(agents.Agent{ID: "ag1", Name: "Reviewer", Description: "checks changes"}, bob, policy.RoleUse)
		list, err := r.svc.AgentsWithShared(ctx, bob)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].Agent.ID).To(Equal("ag1"))
		Expect(list[0].Access.Role).To(Equal(studio.AgentRoleUse))
		Expect(list[0].Access.OwnerName).To(Equal("Alice"))
		Expect(list[0].Access.Owner()).To(BeFalse())
		own, err := r.svc.AgentsWithShared(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		Expect(own).To(HaveLen(1))
		Expect(own[0].Access.Owner()).To(BeTrue())
		Expect(own[0].Access.SharedWith).To(Equal(1))
		none, err := r.svc.AgentsWithShared(ctx, carol)
		Expect(err).NotTo(HaveOccurred())
		Expect(none).To(BeEmpty(), "an agent that is not shared with carol is not listed for her")
	})

	It("runs a shared agent as the grantee: the owner's instructions, the grantee's own collections", func() {
		r := newShareRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			// The message carries a note of what the turn lacks first.
			switch {
			case strings.HasSuffix(text, "docs"):
				return []studiotest.ToolStep{{Tool: "search_collection", Args: `{"collection":"docs","query":"x"}`}, {Say: "ok"}}
			case strings.HasSuffix(text, "secret"):
				return []studiotest.ToolStep{{Tool: "search_collection", Args: `{"collection":"secret","query":"x"}`}, {Say: "ok"}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		})
		r.share(agents.Agent{ID: "ag1", Name: "Reviewer", Instructions: "Always answer in French.", Addons: []string{"office"}, Collections: []string{"docs", "secret"}}, bob, policy.RoleUse)
		rec, err := r.svc.Create(ctx, bob, studio.CreateInput{Model: "m", AgentID: "ag1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Principal).To(Equal(bob), "the chat is the grantee's own")
		sent(r.svc, bob, rec.ID, "docs")
		idle(r.svc, bob, rec.ID)
		Expect(r.h.TurnPrompt(rec.ID)).To(ContainSubstring("Always answer in French."))
		Expect(r.h.ToolNames(rec.ID)).To(ContainElement("addon__office__read-doc"))
		Expect(r.colls.asked).To(ContainElement("bob|docs|x"), "the collection resolves in the grantee's namespace: tools %v, results %+v", r.h.ToolNames(rec.ID), r.h.ResultsFor("search_collection"))
		Expect(strings.Join(r.colls.asked, ",")).NotTo(ContainSubstring("alice|"), "the owner's collections are never searched")
		sent(r.svc, bob, rec.ID, "secret")
		idle(r.svc, bob, rec.ID)
		res := r.h.ResultsFor("search_collection")
		Expect(res).To(HaveLen(2))
		Expect(res[1].Err).To(HaveOccurred(), "the owner's collection that bob does not have is not reachable")
		Expect(strings.Join(r.colls.asked, ",")).NotTo(ContainSubstring("alice|"))
	})

	It("never uses the owner's connection or folder for the grantee", func() {
		r := newShareRig(sayOK)
		r.share(agents.Agent{ID: "ag1", Name: "Mailer", Addons: []string{"office"},
			Connections: connsets.Sets{"office": {"binding-of-alice"}}}, bob, policy.RoleViewer)
		needs, err := r.svc.AgentNeeds(ctx, bob, "ag1")
		Expect(err).NotTo(HaveOccurred())
		var conn *studio.AgentNeed
		for i := range needs {
			if needs[i].Kind == "connection" {
				conn = &needs[i]
			}
		}
		Expect(conn).NotTo(BeNil())
		Expect(conn.State).To(Equal(studio.ReferenceMissing))
		Expect(conn.Label).NotTo(ContainSubstring("binding-of-alice"), "the grantee never learns the owner's binding ID")
		// An agent working in a folder of the owner's home does not start for
		// the grantee: the folder is not theirs.
		folder := agents.Agent{ID: "ag2", Name: "Folder", Folders: []agents.AgentFolder{{Node: 9999, Mode: agents.FolderRead}}}
		r.share(folder, bob, policy.RoleUse)
		_, err = r.svc.Create(ctx, bob, studio.CreateInput{Model: "m", AgentID: "ag2"})
		Expect(err).To(MatchError(studio.ErrAgentFolderMissing))
	})

	It("says what a shared agent needs and which of it is not shared with the grantee", func() {
		r := newShareRig(sayOK)
		r.share(agents.Agent{ID: "ag1", Name: "Reviewer", Addons: []string{"office", "absent"}, Collections: []string{"docs", "secret"}}, bob, policy.RoleUse)
		needs, err := r.svc.AgentNeeds(ctx, bob, "ag1")
		Expect(err).NotTo(HaveOccurred())
		states := map[string]string{}
		for _, n := range needs {
			states[n.Kind+":"+n.Label] = n.State
		}
		Expect(states).To(HaveKeyWithValue("addon:office", studio.ReferenceOK))
		Expect(states).To(HaveKeyWithValue("addon:absent", studio.ReferenceUnavailable))
		Expect(states).To(HaveKeyWithValue("collection:docs", studio.ReferenceOK))
		Expect(states).To(HaveKeyWithValue("collection:secret", studio.ReferenceMissing))
		_, err = r.svc.AgentNeeds(ctx, carol, "ag1")
		Expect(err).To(MatchError(agents.ErrNotFound), "a person the agent is not shared with learns nothing")
	})

	It("lets only the owner edit, delete, export, share, read the references or start a trigger of the agent", func() {
		r := newShareRig(sayOK)
		r.share(agents.Agent{ID: "ag1", Name: "Reviewer"}, bob, policy.RoleViewer)
		_, err := r.svc.SaveAgent(ctx, bob, agents.Agent{ID: "ag1", Name: "Taken over"})
		Expect(err).To(MatchError(studio.ErrAgentNotOwner))
		Expect(r.svc.DeleteAgent(ctx, bob, "ag1")).To(MatchError(studio.ErrAgentNotOwner))
		_, err = r.svc.DeleteAgentWithNotes(ctx, bob, "ag1", "")
		Expect(err).To(MatchError(studio.ErrAgentNotOwner))
		_, err = r.svc.OwnAgent(ctx, bob, "ag1")
		Expect(err).To(MatchError(studio.ErrAgentNotOwner), "export")
		_, err = r.svc.AgentShareRef(ctx, bob, "ag1")
		Expect(err).To(MatchError(studio.ErrAgentNotOwner), "share")
		_, err = r.svc.AgentReferences(ctx, bob, "ag1")
		Expect(err).To(MatchError(studio.ErrAgentNotOwner), "references")
		_, err = r.svc.CreateAs(ctx, bob, studio.CreateInput{Model: "m", AgentID: "ag1"}, studio.Origin{Kind: session.OriginTrigger, ID: "t1"})
		Expect(err).To(MatchError(studio.ErrAgentNotOwner), "a trigger run of a shared agent")
		got, _ := r.agents.Get(ctx, alice, "ag1")
		Expect(got.Name).To(Equal("Reviewer"))
		// The owner's own unattended runs go on as before.
		own, err := r.svc.CreateAs(ctx, alice, studio.CreateInput{Model: "m", AgentID: "ag1"}, studio.Origin{Kind: session.OriginTrigger, ID: "t1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(own.Principal).To(Equal(alice))
		refs, err := r.svc.AgentReferences(ctx, alice, "ag1")
		Expect(err).NotTo(HaveOccurred())
		Expect(refs).To(BeEmpty())
	})

	It("gives an unknown agent and an agent that is not shared the same answer", func() {
		r := newShareRig(sayOK)
		r.share(agents.Agent{ID: "ag1", Name: "Private"}, "", "")
		_, _, errUnknown := r.svc.SharedAgent(ctx, bob, "nope")
		_, _, errPrivate := r.svc.SharedAgent(ctx, bob, "ag1")
		Expect(errUnknown).To(MatchError(agents.ErrNotFound))
		Expect(errPrivate).To(MatchError(agents.ErrNotFound))
		Expect(errPrivate.Error()).To(Equal(errUnknown.Error()))
		_, err := r.svc.Create(ctx, bob, studio.CreateInput{Model: "m", AgentID: "ag1"})
		Expect(err).To(MatchError(agents.ErrNotFound))
		_, err = r.svc.AgentReferences(ctx, bob, "ag1")
		Expect(err).To(MatchError(agents.ErrNotFound))
		_, err = r.svc.AgentRuns(ctx, bob, "ag1", 10)
		Expect(err).To(MatchError(agents.ErrNotFound))
	})

	It("reaches every account through a grant to everyone", func() {
		r := newShareRig(sayOK)
		r.share(agents.Agent{ID: "ag1", Name: "Helper"}, policy.Everyone, policy.RoleUse)
		for _, u := range []string{bob, carol} {
			ag, acc, err := r.svc.SharedAgent(ctx, u, "ag1")
			Expect(err).NotTo(HaveOccurred(), u)
			Expect(ag.Name).To(Equal("Helper"))
			Expect(acc.Everyone).To(BeTrue())
			Expect(acc.OwnerName).To(Equal("Alice"))
		}
	})

	It("follows the agents feature of the grantee", func() {
		r := newShareRig(sayOK)
		r.share(agents.Agent{ID: "ag1", Name: "Helper"}, bob, policy.RoleUse)
		r.mu.Lock()
		r.off[bob] = true
		r.mu.Unlock()
		_, _, err := r.svc.SharedAgent(ctx, bob, "ag1")
		Expect(err).To(MatchError(agents.ErrNotFound))
	})

	stopsRun := func(change func(r *shareRig, ref string)) {
		release := make(chan struct{})
		r := newShareRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "long" {
				return []studiotest.ToolStep{{Block: release}, {Say: "late"}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		})
		defer close(release)
		ref := r.share(agents.Agent{ID: "ag1", Name: "Worker"}, bob, policy.RoleViewer)
		rec, err := r.svc.Create(ctx, bob, studio.CreateInput{Model: "m", AgentID: "ag1"})
		Expect(err).NotTo(HaveOccurred())
		sent(r.svc, bob, rec.ID, "long")
		until("the turn to run", func() bool { return turn(r.svc, bob, rec.ID).Running })
		change(r, ref)
		// The change answers only once the turn stopped.
		Expect(turn(r.svc, bob, rec.ID).Running).To(BeFalse())
		Expect(turn(r.svc, bob, rec.ID).Last).To(Equal(store.TurnStopped))
	}

	It("stops the grantee's run when the owner revokes, and refuses the next turn", func() {
		var r2 *shareRig
		var chat string
		stopsRun(func(r *shareRig, ref string) {
			Expect(r.revoke(ref, bob)).To(Succeed())
			r2 = r
			list, _ := r.svc.List(ctx, bob, 10)
			chat = list[0].ID
		})
		_, err := r2.svc.Send(ctx, bob, chat, "again")
		Expect(err).To(MatchError(studio.ErrAgentNotShared))
		entries, err := r2.svc.Messages(ctx, bob, chat, 0, 100)
		Expect(err).NotTo(HaveOccurred(), "the past chat stays readable")
		Expect(entries).NotTo(BeEmpty())
	})

	It("keeps the grantee's run when the owner lowers the role from viewer to use", func() {
		release := make(chan struct{})
		r := newShareRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "long" {
				return []studiotest.ToolStep{{Block: release}, {Say: "late"}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		})
		ref := r.share(agents.Agent{ID: "ag1", Name: "Worker"}, bob, policy.RoleViewer)
		rec, err := r.svc.Create(ctx, bob, studio.CreateInput{Model: "m", AgentID: "ag1"})
		Expect(err).NotTo(HaveOccurred())
		sent(r.svc, bob, rec.ID, "long")
		until("the turn to run", func() bool { return turn(r.svc, bob, rec.ID).Running })
		// use still chats with the agent: the lowering takes away only the
		// read of the definition, so the drain leaves the turn.
		r.grant(ref, bob, policy.RoleUse)
		Expect(turn(r.svc, bob, rec.ID).Running).To(BeTrue())
		close(release)
		until("the turn to finish", func() bool { return !turn(r.svc, bob, rec.ID).Running })
		Expect(turn(r.svc, bob, rec.ID).Last).To(Equal(store.TurnDone))
		_, acc, err := r.svc.SharedAgent(ctx, bob, "ag1")
		Expect(err).NotTo(HaveOccurred())
		Expect(acc.Role).To(Equal(studio.AgentRoleUse))
	})

	It("stops the grantee's run when the owner lowers the role from viewer to nothing", func() {
		var r2 *shareRig
		stopsRun(func(r *shareRig, ref string) {
			Expect(r.revoke(ref, bob)).To(Succeed())
			r2 = r
		})
		_, _, err := r2.svc.SharedAgent(ctx, bob, "ag1")
		Expect(err).To(MatchError(agents.ErrNotFound))
	})

	It("drops the record and its grants when the owner deletes the agent", func() {
		r := newShareRig(sayOK)
		ref := r.share(agents.Agent{ID: "ag1", Name: "Gone"}, bob, policy.RoleUse)
		Expect(r.svc.DeleteAgent(ctx, alice, "ag1")).To(Succeed())
		_, ok := r.pol.OwnerOfRef(policy.Ref{Kind: studio.AgentShareKind, ResourceID: ref})
		Expect(ok).To(BeFalse(), "the record of a deleted agent is gone")
		_, err := r.pol.GrantsRef(ctx, ownerActor(alice), policy.Ref{Kind: studio.AgentShareKind, ResourceID: ref})
		Expect(err).To(MatchError(policy.ErrForbidden), "no grant of the record is left to list")
		list, _ := r.svc.AgentsWithShared(ctx, bob)
		Expect(list).To(BeEmpty())
		_, _, err = r.svc.SharedAgent(ctx, bob, "ag1")
		Expect(err).To(MatchError(agents.ErrNotFound))
	})

	It("keeps the share record across an edit by the owner", func() {
		r := newShareRig(sayOK)
		ref := r.share(agents.Agent{ID: "ag1", Name: "First"}, bob, policy.RoleUse)
		_, err := r.svc.SaveAgent(ctx, alice, agents.Agent{ID: "ag1", Name: "Second"})
		Expect(err).NotTo(HaveOccurred())
		again, err := r.svc.AgentShareRef(ctx, alice, "ag1")
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(ref))
		ag, _, err := r.svc.SharedAgent(ctx, bob, "ag1")
		Expect(err).NotTo(HaveOccurred())
		Expect(ag.Name).To(Equal("Second"))
	})

	It("makes one record when the first shares of an agent race", func() {
		r := newShareRig(sayOK)
		a := agents.Agent{ID: "ag1", Name: "Racer", Principal: alice, Created: time.Now().UTC()}
		Expect(r.agents.Put(ctx, a)).To(Succeed())
		refs := make([]string, 16)
		var wg sync.WaitGroup
		for i := range refs {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				ref, err := r.svc.AgentShareRef(ctx, alice, "ag1")
				Expect(err).NotTo(HaveOccurred())
				refs[i] = ref
			}()
		}
		wg.Wait()
		for _, ref := range refs {
			Expect(ref).To(Equal(refs[0]))
		}
		views, err := r.pol.ListingKind(ctx, ownerActor(alice), studio.AgentShareKind)
		Expect(err).NotTo(HaveOccurred())
		Expect(views).To(HaveLen(1), "the first share wins; no unused record is left")
	})

	It("shares nothing without the policy", func() {
		r := newAgentRig(sayOK)
		r.save(agents.Agent{ID: "ag1", Name: "Mine"})
		_, err := r.svc.AgentShareRef(ctx, alice, "ag1")
		Expect(err).To(MatchError(studio.ErrAgentSharingOff))
		Expect(r.svc.AgentSharingAvailable()).To(BeFalse())
	})
})
