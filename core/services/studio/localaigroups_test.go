package studio_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	mcpendpoint "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	localaitools "github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/functions"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeClientTools stands in for the client (inference) tools of the LocalAI
// Assistant server: every tool of localaitools.ClientTools, with the models
// the spec installs.
type fakeClientTools struct {
	mu     sync.Mutex
	models map[config.ModelConfigUsecase]string
	users  []string // the acting user of each call
	calls  []string
	// voice records the voice_profile calls: "check", "consent" or "plain".
	voice []string
}

func newFakeClientTools() *fakeClientTools {
	f := &fakeClientTools{models: map[config.ModelConfigUsecase]string{}}
	for _, s := range localaitools.ClientTools {
		f.models[s.Flag] = "model-" + s.Capability
	}
	return f
}

func (f *fakeClientTools) ClientExecutor() mcpendpoint.ToolExecutor { return f }
func (f *fakeClientTools) FirstModel(_ context.Context, use config.ModelConfigUsecase) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.models[use], nil
}
func (f *fakeClientTools) uninstall(use config.ModelConfigUsecase) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.models, use)
}
func (f *fakeClientTools) DiscoverTools(context.Context) ([]functions.Function, error) {
	var out []functions.Function
	for _, s := range localaitools.ClientTools {
		out = append(out, functions.Function{Name: s.Tool, Description: s.Tool, Parameters: map[string]any{"type": "object"}})
	}
	return out, nil
}
func (f *fakeClientTools) IsTool(n string) bool {
	return localaitools.ToolGroupOf(n) == localaitools.GroupClient
}
func (f *fakeClientTools) HasTools() bool { return true }
func (f *fakeClientTools) ExecuteTool(ctx context.Context, name, args string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	who := ""
	if u := localaitools.UserFromContext(ctx); u != nil {
		who = u.ID
	}
	f.users = append(f.users, who)
	f.calls = append(f.calls, name)
	if name == localaitools.ToolVoiceProfile {
		// Act as the real tool does with the grants of the host.
		var in localaitools.VoiceProfileToolInput
		_ = json.Unmarshal([]byte(args), &in)
		consent, check := localaitools.VoiceGrants(ctx)
		switch {
		case in.Action != localaitools.ActionCreate:
			f.voice = append(f.voice, "plain-"+in.Action)
			return "[]", nil
		case consent:
			f.voice = append(f.voice, "consent")
			if sha, size := localaitools.VoiceClipPin(ctx); sha == "" || size <= 0 {
				return localaitools.ErrConsentNeeded.Error(), nil
			}
			return `{"id":"p1","name":"` + in.Name + `","voice":"localai://voice-profiles/p1"}`, nil
		case check:
			f.voice = append(f.voice, "check")
			if strings.HasSuffix(in.Audio, ".mp3") {
				return localaitools.ErrNotWAV.Error(), nil
			}
			return `{"checked":true,"sha256":"` + strings.Repeat("ab", 32) + `","bytes":64044,"duration_ms":2000}`, nil
		}
		f.voice = append(f.voice, "refused")
		return localaitools.ErrConsentNeeded.Error(), nil
	}
	return "made", nil
}
func (f *fakeClientTools) voiceCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.voice)
}
func (f *fakeClientTools) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// localaiRig is a service with both LocalAI groups: the admin tools of
// fakeAssistant and the client tools of fakeClientTools.
type localaiRig struct {
	svc    *studio.Service
	h      *studiotest.ToolHarness
	client *fakeClientTools
	admin  *fakeAssistant
	agents agents.Store

	mu          sync.Mutex
	clientOn    bool
	adminOn     bool
	deniedFeats map[string][]string // user -> features the account lacks
}

func newLocalAIRig(script func(workspace.Actor, string) []studiotest.ToolStep, opts ...func(*studio.Deps)) *localaiRig {
	r := &localaiRig{client: newFakeClientTools(), admin: &fakeAssistant{enabled: true}, clientOn: true, adminOn: true, deniedFeats: map[string][]string{}}
	as, err := agents.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	r.agents = as
	r.svc, r.h = newToolService(script, func(d *studio.Deps) {
		d.Assistant, d.ClientTools, d.Users, d.Agents = r.admin, r.client, assistantUsers, as
		d.LocalAIClientOn = func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.clientOn }
		d.LocalAIAdminOn = func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.adminOn }
		d.Allowed = func(u *auth.User, feature string) bool {
			r.mu.Lock()
			defer r.mu.Unlock()
			return !slices.Contains(r.deniedFeats[u.ID], feature)
		}
		for _, o := range opts {
			o(d)
		}
	})
	return r
}

func (r *localaiRig) set(f func()) { r.mu.Lock(); defer r.mu.Unlock(); f() }

// names runs one turn of a new chat and returns the tool names the model had.
func (r *localaiRig) names(user string, prep ...func(id string)) []string {
	GinkgoHelper()
	id := create(r.svc, user)
	for _, p := range prep {
		p(id)
	}
	return r.turnNames(user, id)
}

func (r *localaiRig) turnNames(user, id string) []string {
	GinkgoHelper()
	sent(r.svc, user, id, "hi")
	idle(r.svc, user, id)
	return r.h.ToolNames(id)
}

func (r *localaiRig) adminOnFor(user string) func(string) {
	return func(id string) {
		Expect(r.svc.SetBuiltinsOn(context.Background(), user, id, []string{builtins.GroupLocalAIAdmin})).To(Succeed())
	}
}

func (r *localaiRig) group(user, id, group string) studio.BuiltinGroup {
	GinkgoHelper()
	p, err := r.svc.Tools(context.Background(), user, id)
	Expect(err).NotTo(HaveOccurred())
	for _, g := range p.Builtins {
		if g.Group == group {
			return g
		}
	}
	Fail("no " + group + " group in the tools panel")
	return studio.BuiltinGroup{}
}

func clientNames() []string {
	var out []string
	for _, s := range localaitools.ClientTools {
		if s.Tool != "describe_image" && s.Tool != "transcribe_audio" && !slices.Contains(out, s.Tool) {
			out = append(out, s.Tool)
		}
	}
	return out
}

var _ = Describe("the LocalAI client and admin tool groups", func() {
	ctx := context.Background()

	It("shows an administrator both groups", func() {
		r := newLocalAIRig(sayOK)
		names := r.names("root", r.adminOnFor("root"))
		Expect(names).To(ContainElements("generate_image", "speak", "install_model"))
		Expect(names).To(ContainElements(clientNames()))
	})

	It("shows a user only the client group, and never an admin tool", func() {
		r := newLocalAIRig(sayOK)
		// The group is opt-in; the user may even switch it on.
		id := create(r.svc, bob)
		r.adminOnFor(bob)(id)
		names := r.turnNames(bob, id)
		Expect(names).To(ContainElements(clientNames()))
		Expect(names).NotTo(ContainElement("install_model"))
		Expect(r.h.TurnPrompt(id)).NotTo(ContainSubstring("manage this LocalAI"))
	})

	It("offers an administrator's chat the client group alone until the admin group is switched on", func() {
		r := newLocalAIRig(sayOK)
		names := r.names("root")
		Expect(names).To(ContainElement("generate_image"))
		Expect(names).NotTo(ContainElement("install_model"))
	})

	It("leaves out the tool of a feature the account lacks", func() {
		r := newLocalAIRig(sayOK)
		r.set(func() { r.deniedFeats[bob] = []string{auth.FeatureImages} })
		names := r.names(bob)
		Expect(names).NotTo(ContainElement("generate_image"))
		Expect(names).To(ContainElement("speak"))
		// Another user keeps them.
		Expect(r.names("root")).To(ContainElement("generate_image"))
	})

	It("offers no client tool to an account with none of the features", func() {
		r := newLocalAIRig(sayOK)
		var all []string
		for _, s := range localaitools.ClientTools {
			all = append(all, s.Feature)
		}
		r.set(func() { r.deniedFeats[bob] = all })
		id := create(r.svc, bob)
		names := r.turnNames(bob, id)
		for _, n := range clientNames() {
			Expect(names).NotTo(ContainElement(n))
		}
		g := r.group(bob, id, builtins.GroupLocalAIClient)
		Expect(g.Usable).To(BeFalse())
		Expect(g.Reason).NotTo(BeEmpty())
	})

	It("leaves out a tool no installed model can serve", func() {
		r := newLocalAIRig(sayOK)
		r.client.uninstall(config.FLAG_TTS)
		names := r.names(bob)
		Expect(names).NotTo(ContainElement("speak"))
		Expect(names).To(ContainElement("generate_image"))
	})

	It("is hidden from everyone when the instance setting is off", func() {
		r := newLocalAIRig(sayOK)
		r.set(func() { r.clientOn, r.adminOn = false, false })
		for _, u := range []string{"root", bob} {
			names := r.names(u, r.adminOnFor(u))
			Expect(names).NotTo(ContainElement("generate_image"), u)
			Expect(names).NotTo(ContainElement("install_model"), u)
		}
		// Each group has its own setting.
		r.set(func() { r.clientOn, r.adminOn = true, false })
		names := r.names("root", r.adminOnFor("root"))
		Expect(names).To(ContainElement("generate_image"))
		Expect(names).NotTo(ContainElement("install_model"))
		r.set(func() { r.clientOn, r.adminOn = false, true })
		names = r.names("root", r.adminOnFor("root"))
		Expect(names).NotTo(ContainElement("generate_image"))
		Expect(names).To(ContainElement("install_model"))
	})

	It("is hidden when the chat switches the group off", func() {
		r := newLocalAIRig(sayOK)
		names := r.names("root", func(id string) {
			Expect(r.svc.SetBuiltinsOff(ctx, "root", id, []string{builtins.GroupLocalAIClient})).To(Succeed())
			r.adminOnFor("root")(id)
			Expect(r.svc.SetBuiltinsOn(ctx, "root", id, []string{builtins.GroupLocalAIAdmin})).To(Succeed())
		})
		Expect(names).NotTo(ContainElement("generate_image"))
		Expect(names).To(ContainElement("install_model"))
		id := create(r.svc, "root")
		g := r.group("root", id, builtins.GroupLocalAIClient)
		Expect(g.Off).To(BeFalse())
		Expect(g.Tools).To(ContainElement("generate_image"))
		Expect(r.group("root", id, builtins.GroupLocalAIAdmin).OptIn).To(BeTrue())
	})

	It("lists both groups in the tool panel of the chat and refuses the admin group to a user as usable", func() {
		r := newLocalAIRig(sayOK)
		rootChat, bobChat := create(r.svc, "root"), create(r.svc, bob)
		Expect(r.group("root", rootChat, builtins.GroupLocalAIAdmin).Usable).To(BeTrue())
		Expect(r.group("root", rootChat, builtins.GroupLocalAIAdmin).Tools).To(ContainElement("install_model"))
		g := r.group(bob, bobChat, builtins.GroupLocalAIAdmin)
		Expect(g.Usable).To(BeFalse())
		Expect(g.Tools).NotTo(ContainElement("install_model"))
		Expect(r.group(bob, bobChat, builtins.GroupLocalAIClient).Usable).To(BeTrue())
	})

	It("lists the groups as built-in tools with their own settings", func() {
		r := newLocalAIRig(sayOK)
		list, err := r.svc.AdminBuiltinTools(ctx, "root")
		Expect(err).NotTo(HaveOccurred())
		settings := map[string]string{}
		for _, t := range list {
			settings[t.Group] = t.Setting
		}
		Expect(settings).To(HaveKeyWithValue(builtins.GroupLocalAIClient, "chats_localai_client"))
		Expect(settings).To(HaveKeyWithValue(builtins.GroupLocalAIAdmin, "chats_localai_admin"))
		for _, t := range list {
			Expect(t.Replaceable).To(BeFalse(), t.ID+" must not be shadowed by an add-on")
		}
	})

	Describe("a saved agent", func() {
		save := func(r *localaiRig, owner string, a agents.Agent) agents.Agent {
			GinkgoHelper()
			a.Model = "m"
			saved, err := r.svc.SaveAgent(ctx, owner, a)
			Expect(err).NotTo(HaveOccurred())
			return saved
		}
		chatOf := func(r *localaiRig, owner, agent string) string {
			rec, err := r.svc.Create(ctx, owner, studio.CreateInput{Model: "m", AgentID: agent})
			Expect(err).NotTo(HaveOccurred())
			return rec.ID
		}

		It("has the client group on, and builtins_off removes it", func() {
			r := newLocalAIRig(sayOK)
			plain := save(r, "root", agents.Agent{Name: "Plain"})
			Expect(r.turnNames("root", chatOf(r, "root", plain.ID))).To(ContainElement("generate_image"))
			narrowed := save(r, "root", agents.Agent{Name: "Narrow", BuiltinsOff: []string{builtins.GroupLocalAIClient}})
			Expect(narrowed.BuiltinsOff).To(ContainElement(builtins.GroupLocalAIClient))
			id := chatOf(r, "root", narrowed.ID)
			Expect(r.turnNames("root", id)).NotTo(ContainElement("generate_image"))
			g := r.group("root", id, builtins.GroupLocalAIClient)
			Expect(g.Usable).To(BeFalse(), "the ceiling: the chat cannot switch it on")
		})

		It("has the admin group off until builtins_on, and then only for an administrator", func() {
			r := newLocalAIRig(sayOK)
			off := save(r, "root", agents.Agent{Name: "Off"})
			Expect(r.turnNames("root", chatOf(r, "root", off.ID))).NotTo(ContainElement("install_model"))

			on := save(r, "root", agents.Agent{Name: "On", BuiltinsOn: []string{builtins.GroupLocalAIAdmin}})
			Expect(on.BuiltinsOn).To(ContainElement(builtins.GroupLocalAIAdmin))
			Expect(r.turnNames("root", chatOf(r, "root", on.ID))).To(ContainElement("install_model"))

			// A user who owns the same kind of agent still gets no admin tool.
			bobs := save(r, bob, agents.Agent{Name: "On", BuiltinsOn: []string{builtins.GroupLocalAIAdmin}})
			names := r.turnNames(bob, chatOf(r, bob, bobs.ID))
			Expect(names).NotTo(ContainElement("install_model"))
			Expect(names).To(ContainElement("generate_image"))
		})
	})

	Describe("sub-agents", func() {
		It("never get the admin group, whatever the parent switched on", func() {
			r := newLocalAIRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
				if text == "go" {
					return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"look","wait":true}`}, {Say: "done"}}
				}
				return []studiotest.ToolStep{{Say: "seen"}}
			})
			rec, err := r.svc.Create(ctx, "root", studio.CreateInput{Model: "m", Assistant: true})
			Expect(err).NotTo(HaveOccurred())
			sent(r.svc, "root", rec.ID, "go")
			idle(r.svc, "root", rec.ID)
			Expect(r.h.ToolNames(rec.ID)).To(ContainElement("install_model"))
			kids := childrenOf(r.svc, "root", rec.ID)
			Expect(kids).NotTo(BeEmpty())
			for _, k := range kids {
				Expect(r.h.ToolNames(k.ID)).NotTo(ContainElement("install_model"))
			}
		})

		It("started from a saved agent never get the admin group either, but follow the client switches like any group", func() {
			r := newLocalAIRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
				if text == "go" {
					return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"look","agent":"worker"}`}, {Say: "done"}}
				}
				return []studiotest.ToolStep{{Say: "seen"}}
			})
			_, err := r.svc.SaveAgent(ctx, "root", agents.Agent{Name: "worker", Model: "m", BuiltinsOn: []string{builtins.GroupLocalAIAdmin}})
			Expect(err).NotTo(HaveOccurred())
			rec, err := r.svc.Create(ctx, "root", studio.CreateInput{Model: "m", Assistant: true})
			Expect(err).NotTo(HaveOccurred())
			Expect(r.svc.SetBuiltinsOn(ctx, "root", rec.ID, []string{builtins.GroupLocalAIAdmin, builtins.GroupSavedAgents})).To(Succeed())
			sent(r.svc, "root", rec.ID, "go")
			idle(r.svc, "root", rec.ID)
			kids := childrenOf(r.svc, "root", rec.ID)
			Expect(kids).NotTo(BeEmpty())
			for _, k := range kids {
				Expect(r.h.ToolNames(k.ID)).NotTo(ContainElement("install_model"))
			}
		})
	})

	Describe("a run that nobody watches", func() {
		run := func(r *localaiRig, user string, origin session.ChatRecord, ag agents.Agent) []string {
			GinkgoHelper()
			saved, err := r.svc.SaveAgent(ctx, user, ag)
			Expect(err).NotTo(HaveOccurred())
			rec, err := r.svc.CreateAs(ctx, user, studio.CreateInput{Model: "m", AgentID: saved.ID}, studio.Origin{Kind: origin.Origin, ID: "x"})
			Expect(err).NotTo(HaveOccurred())
			return r.turnNames(user, rec.ID)
		}

		It("is not offered the admin group, even when the agent switched it on", func() {
			for _, origin := range []string{session.OriginTrigger, session.OriginJob} {
				r := newLocalAIRig(sayOK)
				names := run(r, "root", session.ChatRecord{Origin: origin}, agents.Agent{Name: "Auto", Model: "m", BuiltinsOn: []string{builtins.GroupLocalAIAdmin}})
				Expect(names).NotTo(ContainElement("install_model"), origin)
				Expect(names).To(ContainElement("generate_image"), origin)
			}
		})

		It("gates the client tools by act.spend", func() {
			script := func(_ workspace.Actor, text string) []studiotest.ToolStep {
				return []studiotest.ToolStep{{Tool: "generate_image", Args: `{"prompt":"a cat"}`}, {Say: "finished"}}
			}
			// Without the grant the call answers with a refusal and runs nothing.
			r := newLocalAIRig(script)
			run(r, "root", session.ChatRecord{Origin: session.OriginJob}, agents.Agent{Name: "Poor", Model: "m"})
			res := r.h.ResultsFor("generate_image")
			Expect(res).To(HaveLen(1))
			Expect(res[0].Err).NotTo(HaveOccurred(), "a refusal is an answer, not an error")
			Expect(res[0].Out).To(ContainSubstring("act.spend"))
			Expect(r.client.callCount()).To(Equal(0), "the tool must not run")

			// With the grant it runs, as the owner of the run.
			r = newLocalAIRig(script)
			run(r, "root", session.ChatRecord{Origin: session.OriginJob}, agents.Agent{Name: "Rich", Model: "m", Builtins: []string{builtins.GrantActSpend}})
			res = r.h.ResultsFor("generate_image")
			Expect(res).To(HaveLen(1))
			Expect(res[0].Out).To(Equal("made"))
			Expect(r.client.users).To(Equal([]string{"root"}))
		})
	})

	It("never gives a chat the same tool name twice", func() {
		r := newLocalAIRig(sayOK, func(d *studio.Deps) {
			// A tool of another source with the name of a client tool, and
			// the helper tools the media group serves.
			d.HostTools = func(context.Context, workspace.Actor) ([]session.Tool, error) {
				return []session.Tool{searchTool(), {Name: "generate_image", Description: "from elsewhere", Schema: []byte(`{"type":"object"}`),
					Run: func(context.Context, json.RawMessage) (string, error) { return "", nil }}}, nil
			}
		})
		names := r.names("root", r.adminOnFor("root"))
		seen := map[string]int{}
		for _, n := range names {
			seen[n]++
		}
		for n, c := range seen {
			Expect(c).To(Equal(1), "the tool %q is offered %d times in %v", n, c, names)
		}
		// The client group leaves describe_image and transcribe_audio to the
		// media group, which this server does not have: nobody serves them.
		Expect(names).NotTo(ContainElements("describe_image"))
		Expect(names).NotTo(ContainElement("transcribe_audio"))
	})

	It("runs the client tools as the chat's user, and not as a fallback", func() {
		r := newLocalAIRig(func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: "speak", Args: `{"text":"hello"}`}, {Say: "done"}}
		})
		id := create(r.svc, bob)
		sent(r.svc, bob, id, "go")
		idle(r.svc, bob, id)
		res := r.h.ResultsFor("speak")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Out).To(Equal("made"))
		Expect(r.client.users).To(Equal([]string{bob}))
	})

	Describe("assistant mode", func() {
		It("still gives an administrator the admin tools and the prompt, and switches the group on", func() {
			r := newLocalAIRig(sayOK)
			rec, err := r.svc.Create(ctx, "root", studio.CreateInput{Model: "m", Assistant: true})
			Expect(err).NotTo(HaveOccurred())
			names := r.turnNames("root", rec.ID)
			Expect(names).To(ContainElements("install_model", "generate_image"))
			Expect(r.h.TurnPrompt(rec.ID)).To(ContainSubstring("manage this LocalAI instance"))
			g := r.group("root", rec.ID, builtins.GroupLocalAIAdmin)
			Expect(g.Off).To(BeFalse())
		})

		It("refuses to create an assistant chat while the admin group is switched off for the instance", func() {
			r := newLocalAIRig(sayOK)
			r.set(func() { r.adminOn = false })
			_, err := r.svc.Create(ctx, "root", studio.CreateInput{Model: "m", Assistant: true})
			Expect(err).To(MatchError(studio.ErrAssistantOff))
			// An administrator can still chat without assistant mode.
			Expect(r.names("root")).NotTo(ContainElement("install_model"))
			// Switching it on again works at once.
			r.set(func() { r.adminOn = true })
			_, err = r.svc.Create(ctx, "root", studio.CreateInput{Model: "m", Assistant: true})
			Expect(err).NotTo(HaveOccurred())
		})

		It("still refuses a user", func() {
			r := newLocalAIRig(sayOK)
			_, err := r.svc.Create(ctx, bob, studio.CreateInput{Model: "m", Assistant: true})
			Expect(err).To(MatchError(studio.ErrAssistantForbidden))
		})

		It("gives the admin tools their prompt without assistant mode, when the group is on", func() {
			r := newLocalAIRig(sayOK)
			id := create(r.svc, "root")
			r.adminOnFor("root")(id)
			Expect(r.turnNames("root", id)).To(ContainElement("install_model"))
			Expect(r.h.TurnPrompt(id)).To(ContainSubstring("manage this LocalAI instance"))
		})
	})
})
