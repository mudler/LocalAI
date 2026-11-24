package studio_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/sandboxpolicy"
)

// profileAddons is a Sandbox add-on with profiles: it records the options of
// each bash call and answers the status tool.
type profileAddons struct {
	isolatedAddons
	mu     sync.Mutex
	opts   []contract.ExecutionCall
	status string
}

func (p *profileAddons) Call(ctx context.Context, user, conv, name string, args json.RawMessage) (string, error) {
	if strings.HasSuffix(name, "__status") {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.status, nil
	}
	if c, ok := contract.ExecutionCallFrom(ctx); ok {
		p.mu.Lock()
		p.opts = append(p.opts, c)
		p.mu.Unlock()
	}
	return p.isolatedAddons.Call(ctx, user, conv, name, args)
}

func (p *profileAddons) lastOpts() contract.ExecutionCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.opts) == 0 {
		return contract.ExecutionCall{}
	}
	return p.opts[len(p.opts)-1]
}

func tool(name, detail string) contract.ProfileTool {
	return contract.ProfileTool{Name: name, Detail: detail}
}

var testProfiles = []contract.ExecutionProfile{
	{ID: "base", Title: "Base", Description: "The usual tools.", MemoryMiB: 2048, Tools: []contract.ProfileTool{tool("python3", "pandas"), tool("jq", "")}},
	{ID: "media", Title: "Media", Description: "Base plus ffmpeg.", MemoryMiB: 4096, Tools: []contract.ProfileTool{tool("python3", "pandas"), tool("jq", ""), tool("ffmpeg", "")}},
	{ID: "heavy", Title: "Heavy", Description: "Media plus a compiler.", MemoryMiB: 8192, Tools: []contract.ProfileTool{tool("python3", "pandas"), tool("jq", ""), tool("ffmpeg", ""), tool("gcc", "")}},
}

func newProfileAddons(users ...string) *profileAddons {
	bash := addonTool("sandbox", "bash")
	bash.Action, bash.Shadows = true, "bash"
	bash.Execution = &contract.ExecutionDescriptor{Class: contract.ExecutionClassCommands, Title: "Sandbox", Profiles: testProfiles}
	status := addonTool("sandbox", "status")
	tools := map[string][]studio.AddonTool{}
	for _, u := range users {
		tools[u] = []studio.AddonTool{bash, status}
	}
	return &profileAddons{isolatedAddons: isolatedAddons{fakeAddons: &fakeAddons{tools: tools}, mode: "isolated"},
		status: `{"ok":true,"profiles":[{"id":"base","phase":"ready"},{"id":"media","phase":"pulling","percent":40,"hasPrevious":true},{"id":"heavy","phase":"failed","error":"registry unreachable"}],"chat":{"profile":"base","newerImage":true}}`}
}

type profileRig struct {
	svc  *studio.Service
	h    *studiotest.ToolHarness
	fa   *profileAddons
	pol  *sandboxpolicy.Policy
	f    *files.Service
	mu   sync.Mutex
	admn map[string]bool
}

func newProfileRig(script func(workspace.Actor, string) []studiotest.ToolStep) *profileRig {
	r := &profileRig{fa: newProfileAddons(alice, "bob", "root"), pol: &sandboxpolicy.Policy{}, admn: map[string]bool{"root": true}}
	f := newFiles()
	r.f = f
	if script == nil {
		script = func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: "bash", Args: `{"command":"ffmpeg -version"}`}, {Say: "ok"}}
		}
	}
	r.svc, r.h = newToolService(script, func(d *studio.Deps) {
		d.Files = f
		d.AddonTools = r.fa
		ag, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		d.Agents = ag
		d.IsAdmin = func(_ context.Context, u string) bool {
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.admn[u]
		}
		d.SandboxPolicy = func(context.Context) (sandboxpolicy.Policy, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			return *r.pol, nil
		}
	})
	return r
}

func (r *profileRig) setPolicy(p sandboxpolicy.Policy) {
	r.mu.Lock()
	*r.pol = p
	r.mu.Unlock()
}

func (r *profileRig) run(user, id string) {
	sent(r.svc, user, id, "go")
	idle(r.svc, user, id)
}

// withoutWorkDir checks that the call carries the chat's work folder (its name
// has the date of the day) and returns the options without it.
func withoutWorkDir(c contract.ExecutionCall) contract.ExecutionCall {
	GinkgoHelper()
	Expect(c.WorkDir).To(MatchRegexp(`^/workspace/home/Chats/\d{4}-\d{2}-\d{2}-[a-z0-9-]+/work$`))
	c.WorkDir = ""
	return c
}

var _ = Describe("sandbox profiles", func() {
	It("tells the model the profile of the chat, its programs and the others, and puts the profile in the tool description", func() {
		r := newProfileRig(nil)
		id := create(r.svc, alice)
		prompt := turnPrompt(r.svc, r.h, id)
		Expect(prompt).To(ContainSubstring("The sandbox profile is media. It has: python3 (pandas), jq, ffmpeg."))
		Expect(prompt).To(ContainSubstring("heavy (adds gcc)"))
		Expect(r.h.LastToolDescription("bash")).To(ContainSubstring("Sandbox profile media: python3 (pandas), jq, ffmpeg."))
		Expect(withoutWorkDir(r.fa.lastOpts())).To(Equal(contract.ExecutionCall{Profile: "media"}))
	})

	It("lets the person switch the profile, and the next call and the prompt follow", func() {
		r := newProfileRig(nil)
		id := create(r.svc, alice)
		Expect(r.svc.SetSandboxProfile(context.Background(), alice, id, "base")).To(Succeed())
		prompt := turnPrompt(r.svc, r.h, id)
		Expect(prompt).To(ContainSubstring("The sandbox profile is base. It has: python3 (pandas), jq."))
		Expect(r.fa.lastOpts().Profile).To(Equal("base"))
		v, err := r.svc.SandboxProfiles(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.Selected).To(Equal("base"))
		Expect(v.Source).To(Equal(studio.ProfileFromChat))
		Expect(r.svc.SetSandboxProfile(context.Background(), alice, id, "")).To(Succeed())
		v, _ = r.svc.SandboxProfiles(context.Background(), alice, id)
		Expect(v.Profile).To(Equal("media"))
		Expect(v.Source).To(Equal(studio.ProfileFromDefault))
	})

	It("refuses an unknown profile and a chat of another user", func() {
		r := newProfileRig(nil)
		id := create(r.svc, alice)
		Expect(r.svc.SetSandboxProfile(context.Background(), alice, id, "nope")).To(MatchError(studio.ErrSandboxProfileUnknown))
		Expect(r.svc.SetSandboxProfile(context.Background(), "bob", id, "media")).NotTo(Succeed())
	})

	It("is open to everyone by default and follows the administrator's policy when there is one", func() {
		r := newProfileRig(nil)
		id := create(r.svc, alice)
		Expect(r.svc.SetSandboxProfile(context.Background(), alice, id, "heavy")).To(Succeed(), "no policy: everyone may use every profile")
		r.setPolicy(sandboxpolicy.Policy{Default: "media", Access: map[string]sandboxpolicy.Access{"heavy": {Roles: []string{"admin"}}}, MemoryMiB: 1024, DiskMiB: 5000, PinPrevious: []string{"media"}})
		// The profile alice picked is closed now: the default applies.
		Expect(turnPrompt(r.svc, r.h, id)).To(ContainSubstring("The sandbox profile is media."))
		Expect(withoutWorkDir(r.fa.lastOpts())).To(Equal(contract.ExecutionCall{Profile: "media", Pin: true, MemoryMiB: 1024, DiskMiB: 5000}))
		Expect(r.svc.SetSandboxProfile(context.Background(), alice, id, "heavy")).To(MatchError(studio.ErrSandboxProfileNotAllowed))
		root := create(r.svc, "root")
		Expect(r.svc.SetSandboxProfile(context.Background(), "root", root, "heavy")).To(Succeed(), "an administrator is named by the role")
		v, err := r.svc.SandboxProfiles(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		allowed := map[string]bool{}
		for _, p := range v.Profiles {
			allowed[p.ID] = p.Allowed
		}
		Expect(allowed).To(Equal(map[string]bool{"base": true, "media": true, "heavy": false}))
	})

	It("starts a chat in media when the administrator sets no default, and base when the policy says so", func() {
		r := newProfileRig(nil)
		id := create(r.svc, alice)
		v, err := r.svc.SandboxProfiles(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.Profile).To(Equal("media"))
		Expect(v.Default).To(Equal("media"))
		Expect(v.Source).To(Equal(studio.ProfileFromDefault))
		r.setPolicy(sandboxpolicy.Policy{Default: "base"})
		Expect(turnPrompt(r.svc, r.h, id)).To(ContainSubstring("The sandbox profile is base."))
		v, _ = r.svc.SandboxProfiles(context.Background(), alice, id)
		Expect(v.Default).To(Equal("base"))
		_, _, def, err := r.svc.SandboxProfileChoices(context.Background(), alice)
		Expect(err).NotTo(HaveOccurred())
		Expect(def).To(Equal("base"))
	})

	It("falls back to base when media is not allowed to the user and no default is set", func() {
		r := newProfileRig(nil)
		r.setPolicy(sandboxpolicy.Policy{Access: map[string]sandboxpolicy.Access{"media": {Users: []string{"carol"}}}})
		id := create(r.svc, alice)
		Expect(turnPrompt(r.svc, r.h, id)).To(ContainSubstring("The sandbox profile is base."))
	})

	It("falls back to base when the default is not allowed or not there", func() {
		r := newProfileRig(nil)
		r.setPolicy(sandboxpolicy.Policy{Default: "media", Access: map[string]sandboxpolicy.Access{"media": {Users: []string{"carol"}}}})
		id := create(r.svc, alice)
		Expect(turnPrompt(r.svc, r.h, id)).To(ContainSubstring("The sandbox profile is base."))
		r.setPolicy(sandboxpolicy.Policy{Default: "gone"})
		Expect(turnPrompt(r.svc, r.h, id)).To(ContainSubstring("The sandbox profile is base."))
	})

	It("uses the profile of the saved agent when the chat has no choice of its own", func() {
		r := newProfileRig(nil)
		ag, err := r.svc.SaveAgent(context.Background(), alice, agents.Agent{Name: "Editor", Model: "m", Addons: []string{"sandbox"}, SandboxProfile: "media"})
		Expect(err).NotTo(HaveOccurred())
		rec, err := r.svc.Create(context.Background(), alice, studio.CreateInput{Model: "m", AgentID: ag.ID})
		Expect(err).NotTo(HaveOccurred())
		id := rec.ID
		Expect(turnPrompt(r.svc, r.h, id)).To(ContainSubstring("The sandbox profile is media."))
		v, _ := r.svc.SandboxProfiles(context.Background(), alice, id)
		Expect(v.Source).To(Equal(studio.ProfileFromAgent))
		Expect(r.svc.SetSandboxProfile(context.Background(), alice, id, "base")).To(Succeed())
		v, _ = r.svc.SandboxProfiles(context.Background(), alice, id)
		Expect(v.Profile).To(Equal("base"), "the person's own pick wins over the agent's")
	})

	It("shows the state of the images, the fallback and a newer image to the picker", func() {
		r := newProfileRig(nil)
		id := create(r.svc, alice)
		Expect(r.svc.SetSandboxProfile(context.Background(), alice, id, "base")).To(Succeed())
		v, err := r.svc.SandboxProfiles(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.Applies).To(BeTrue())
		Expect(v.NewerImage).To(BeTrue())
		Expect(v.Fallback).To(BeFalse())
		states := map[string]studio.ProfileImageState{}
		for _, p := range v.Profiles {
			states[p.ID] = p.State
		}
		Expect(states["media"]).To(Equal(studio.ProfileImageState{Phase: "pulling", Percent: 40}))
		Expect(r.svc.SetSandboxProfile(context.Background(), alice, id, "heavy")).To(Succeed())
		v, _ = r.svc.SandboxProfiles(context.Background(), alice, id)
		Expect(v.Fallback).To(BeTrue())
		Expect(v.FallbackReason).To(Equal("registry unreachable"))
		Expect(v.NewerImage).To(BeFalse(), "the running sandbox is of another profile")
	})

	It("does not apply to a chat whose commands are not isolated", func() {
		r := newProfileRig(nil)
		r.fa.mode = "host"
		id := create(r.svc, alice)
		v, err := r.svc.SandboxProfiles(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.Applies).To(BeFalse())
		Expect(v.Profiles).To(BeEmpty())
	})

	It("lists the profiles for the agent editor and for the administrator", func() {
		r := newProfileRig(nil)
		addon, list, def, err := r.svc.SandboxProfileChoices(context.Background(), alice)
		Expect(err).NotTo(HaveOccurred())
		Expect(addon).To(Equal("sandbox"))
		Expect(def).To(Equal("media"))
		Expect(list).To(HaveLen(3))
		addon, list, err = r.svc.AdminSandboxProfiles(context.Background(), "root")
		Expect(err).NotTo(HaveOccurred())
		Expect(addon).To(Equal("sandbox"))
		Expect(list[1].MemoryMiB).To(Equal(4096))
		Expect(list[1].HasPrevious).To(BeTrue())
	})

	Describe("the model tools", func() {
		script := func(steps ...studiotest.ToolStep) func(workspace.Actor, string) []studiotest.ToolStep {
			return func(workspace.Actor, string) []studiotest.ToolStep {
				return append(steps, studiotest.ToolStep{Say: "done"})
			}
		}
		It("offers sandbox_profile with the commands, and not without a sandbox that has profiles", func() {
			r := newProfileRig(script())
			id := create(r.svc, alice)
			sent(r.svc, alice, id, "go")
			idle(r.svc, alice, id)
			Expect(r.h.ToolNames(id)).To(ContainElement("sandbox_profile"))
			plain := &fakeAddons{tools: map[string][]studio.AddonTool{alice: runAddon()}}
			svc, h, _ := newCapsChat(isolatedAddons{fakeAddons: plain, mode: "isolated"}, nil)
			id = create(svc, alice)
			sent(svc, alice, id, "go")
			idle(svc, alice, id)
			Expect(h.ToolNames(id)).NotTo(ContainElement("sandbox_profile"), "an add-on without profiles has nothing to pick")
		})

		It("lists the profiles with their programs, the policy and the state of the images", func() {
			r := newProfileRig(script(studiotest.ToolStep{Tool: "sandbox_profile", Args: `{"action":"list"}`}))
			r.setPolicy(sandboxpolicy.Policy{Access: map[string]sandboxpolicy.Access{"heavy": {Roles: []string{"admin"}}}})
			id := create(r.svc, alice)
			r.run(alice, id)
			out := r.h.ResultsFor("sandbox_profile")
			Expect(out).To(HaveLen(1))
			Expect(out[0].Out).To(ContainSubstring("This chat runs the media profile."))
			Expect(out[0].Out).To(ContainSubstring("- media: Base plus ffmpeg. Programs: python3 (pandas), jq, ffmpeg. Its image is being prepared (40%)"))
			Expect(out[0].Out).To(ContainSubstring("- heavy: Media plus a compiler."))
			Expect(out[0].Out).To(ContainSubstring("Not allowed for this user."))
		})

		It("switches the profile of the chat, with the same rules as the picker", func() {
			r := newProfileRig(script(studiotest.ToolStep{Tool: "sandbox_profile", Args: `{"action":"use","profile":"media"}`}, studiotest.ToolStep{Tool: "sandbox_profile", Args: `{"action":"use","profile":"nope"}`}))
			id := create(r.svc, alice)
			r.run(alice, id)
			res := r.h.ResultsFor("sandbox_profile")
			Expect(res).To(HaveLen(2))
			Expect(res[0].Out).To(ContainSubstring("The sandbox profile is media now."))
			Expect(res[0].Out).To(ContainSubstring("files stay"))
			Expect(res[0].Out).To(ContainSubstring("ffmpeg"))
			Expect(res[1].Out).To(ContainSubstring("There is no such profile."))
			v, _ := r.svc.SandboxProfiles(context.Background(), alice, id)
			Expect(v.Selected).To(Equal("media"))
		})

		It("refuses a profile the policy closed, and says so to the model", func() {
			r := newProfileRig(script(studiotest.ToolStep{Tool: "sandbox_profile", Args: `{"action":"use","profile":"heavy"}`}))
			r.setPolicy(sandboxpolicy.Policy{Access: map[string]sandboxpolicy.Access{"heavy": {Roles: []string{"admin"}}}})
			id := create(r.svc, alice)
			r.run(alice, id)
			Expect(r.h.ResultsFor("sandbox_profile")[0].Out).To(ContainSubstring("Not permitted"))
			v, _ := r.svc.SandboxProfiles(context.Background(), alice, id)
			Expect(v.Selected).To(BeEmpty())
		})

		It("is hidden with the group of the commands", func() {
			r := newProfileRig(script())
			id := create(r.svc, alice)
			Expect(r.svc.SetBuiltinsOff(context.Background(), alice, id, []string{"shell"})).To(Succeed())
			sent(r.svc, alice, id, "go")
			idle(r.svc, alice, id)
			Expect(r.h.ToolNames(id)).NotTo(ContainElement("sandbox_profile"))
		})
	})

	Describe("the sandbox hint of a skill", func() {
		writeSkill := func(f *files.Service, name, profile string) {
			Expect(f.EnsureShared(context.Background())).To(Succeed())
			body := "---\nname: " + name + "\ndescription: Cut video.\nsandbox: " + profile + "\n---\nRun ffmpeg.\n"
			_, err := f.Store().Put(context.Background(), workspace.Actor{Principal: files.AdminPrincipal}, "/skills/"+name+"/SKILL.md",
				strings.NewReader(body), workspace.PutOptions{Overwrite: true})
			Expect(err).NotTo(HaveOccurred())
		}
		load := func(r *profileRig, id string) string {
			r.run(alice, id)
			res := r.h.ResultsFor("load_skill")
			Expect(res).NotTo(BeEmpty())
			return res[len(res)-1].Out
		}
		It("says the skill wants another profile, offers the switch and does not switch", func() {
			r := newProfileRig(func(workspace.Actor, string) []studiotest.ToolStep {
				return []studiotest.ToolStep{{Tool: "load_skill", Args: `{"name":"cut"}`}, {Say: "ok"}}
			})
			writeSkill(r.f, "cut", "heavy")
			id := create(r.svc, alice)
			out := load(r, id)
			Expect(out).To(ContainSubstring("This skill wants the sandbox profile heavy; this chat runs media."))
			Expect(out).To(ContainSubstring("Run ffmpeg."))
			v, _ := r.svc.SandboxProfiles(context.Background(), alice, id)
			Expect(v.Profile).To(Equal("media"), "a hint never switches the profile")
			Expect(v.Selected).To(BeEmpty())
		})
		It("adds nothing when the chat runs the profile the skill wants", func() {
			r := newProfileRig(func(workspace.Actor, string) []studiotest.ToolStep {
				return []studiotest.ToolStep{{Tool: "load_skill", Args: `{"name":"cut"}`}, {Say: "ok"}}
			})
			writeSkill(r.f, "cut", "media")
			id := create(r.svc, alice)
			Expect(r.svc.SetSandboxProfile(context.Background(), alice, id, "media")).To(Succeed())
			Expect(load(r, id)).NotTo(ContainSubstring("This skill wants"))
		})
		It("ignores a profile the add-on does not have", func() {
			r := newProfileRig(func(workspace.Actor, string) []studiotest.ToolStep {
				return []studiotest.ToolStep{{Tool: "load_skill", Args: `{"name":"cut"}`}, {Say: "ok"}}
			})
			writeSkill(r.f, "cut", "gpu")
			id := create(r.svc, alice)
			Expect(load(r, id)).NotTo(ContainSubstring("This skill wants"))
		})
	})
})
