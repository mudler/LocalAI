package studio_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	addontools "github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/resourcesets"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// resourceHost stands in for the add-on host: it applies the limit the chats
// service put in the context the way the adapter does, and answers "ok:<id>"
// for a call it lets through.
type resourceHost struct {
	mu      sync.Mutex
	scoped  bool
	reached []string
}

func (h *resourceHost) Tools(context.Context, string) ([]studio.AddonTool, error) {
	read := addonTool("tables", "rows-query")
	read.Tool, read.ResourceScoped = "rows-query", h.scoped
	write := addonTool("tables", "rows-insert")
	write.Tool, write.ResourceScoped, write.Action = "rows-insert", h.scoped, true
	create := addonTool("tables", "databases-create")
	create.Tool, create.ResourceScoped, create.Action = "databases-create", h.scoped, true
	return []studio.AddonTool{read, write, create}, nil
}

func (h *resourceHost) Call(ctx context.Context, _, _, name string, args json.RawMessage) (string, error) {
	var in struct {
		ResourceID string `json:"resourceId"`
	}
	_ = json.Unmarshal(args, &in)
	if l, ok := addontools.ResourceLimitsFrom(ctx); ok && in.ResourceID != "" {
		if !l.Allows("tables", in.ResourceID, name != "addon__tables__rows-query") {
			return "refused:" + in.ResourceID, nil
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reached = append(h.reached, name+"|"+in.ResourceID)
	return "ok:" + in.ResourceID, nil
}

var _ = Describe("the resource list of a saved agent, at the chats service", func() {
	ctx := context.Background()
	type rig struct {
		svc    *studio.Service
		h      *studiotest.ToolHarness
		host   *resourceHost
		agents agents.Store
		call   func(tool, args string)
	}
	var steps []studiotest.ToolStep
	setup := func(scoped bool) *rig {
		as, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		host := &resourceHost{scoped: scoped}
		svc, h := newToolService(func(workspace.Actor, string) []studiotest.ToolStep { return steps }, func(d *studio.Deps) {
			d.AddonTools, d.Agents = host, as
		})
		return &rig{svc: svc, h: h, host: host, agents: as}
	}
	save := func(r *rig, id string, res resourcesets.Sets, addons ...string) {
		Expect(r.agents.Put(ctx, agents.Agent{ID: id, Principal: alice, Name: id, Addons: addons, Resources: res, Created: time.Now().UTC(),
			Builtins: []string{builtins.GrantSavedAgents}, BuiltinsOn: []string{builtins.GroupSavedAgents}})).To(Succeed())
	}
	// run sends one turn that makes the calls and returns what each answered.
	run := func(r *rig, id string, calls ...[2]string) []string {
		steps = nil
		for _, c := range calls {
			steps = append(steps, studiotest.ToolStep{Tool: c[0], Args: c[1]})
		}
		steps = append(steps, studiotest.ToolStep{Say: "done"})
		before := map[string]int{}
		for _, c := range calls {
			before[c[0]] = len(r.h.ResultsFor(c[0]))
		}
		sent(r.svc, alice, id, "go")
		idle(r.svc, alice, id)
		var out []string
		seen := map[string]int{}
		for _, c := range calls {
			rs := r.h.ResultsFor(c[0])
			at := before[c[0]] + seen[c[0]]
			Expect(len(rs)).To(BeNumerically(">", at), c[0])
			out = append(out, rs[at].Out)
			seen[c[0]]++
		}
		return out
	}
	chat := func(r *rig, agent string) string {
		rec, err := r.svc.Create(ctx, alice, studio.CreateInput{Model: "m", AgentID: agent})
		Expect(err).NotTo(HaveOccurred())
		return rec.ID
	}
	read := func(id string) [2]string {
		return [2]string{"addon__tables__rows-query", `{"resourceId":"` + id + `"}`}
	}
	write := func(id string) [2]string {
		return [2]string{"addon__tables__rows-insert", `{"resourceId":"` + id + `"}`}
	}
	scopedList := resourcesets.Sets{"tables": {{ID: "db-read", Mode: "read"}, {ID: "db-write", Mode: "write"}}}

	It("does not limit a chat that has no agent", func() {
		r := setup(true)
		id := create(r.svc, alice)
		Expect(run(r, id, read("db-any"), write("db-any"))).To(Equal([]string{"ok:db-any", "ok:db-any"}))
	})
	It("gives an agent with an empty list no resource", func() {
		r := setup(true)
		save(r, "ag", nil, "tables")
		Expect(run(r, chat(r, "ag"), read("db-read"), write("db-read"))).To(Equal([]string{"refused:db-read", "refused:db-read"}))
	})
	It("lets a listed read resource be read and not changed", func() {
		r := setup(true)
		save(r, "ag", scopedList, "tables")
		Expect(run(r, chat(r, "ag"), read("db-read"), write("db-read"))).To(Equal([]string{"ok:db-read", "refused:db-read"}))
	})
	It("lets a listed write resource be read and changed", func() {
		r := setup(true)
		save(r, "ag", scopedList, "tables")
		Expect(run(r, chat(r, "ag"), read("db-write"), write("db-write"))).To(Equal([]string{"ok:db-write", "ok:db-write"}))
	})
	It("refuses a resource that is not on the list", func() {
		r := setup(true)
		save(r, "ag", scopedList, "tables")
		Expect(run(r, chat(r, "ag"), read("db-other"), write("db-other"))).To(Equal([]string{"refused:db-other", "refused:db-other"}))
	})
	It("has no effect on an add-on that does not opt in", func() {
		r := setup(false)
		save(r, "ag", nil, "tables")
		Expect(run(r, chat(r, "ag"), read("db-any"), write("db-any"))).To(Equal([]string{"ok:db-any", "ok:db-any"}))
	})
	It("leaves a tool that names no resource alone", func() {
		r := setup(true)
		save(r, "ag", nil, "tables")
		out := run(r, chat(r, "ag"), [2]string{"addon__tables__databases-create", `{"name":"New"}`})
		Expect(out).To(Equal([]string{"ok:"}))
	})
	It("holds in the chat of a trigger, which runs as the agent", func() {
		r := setup(true)
		save(r, "ag", scopedList, "tables")
		rec, err := r.svc.CreateAs(ctx, alice, studio.CreateInput{AgentID: "ag", Title: "Nightly", Model: "m"}, studio.Origin{Kind: session.OriginTrigger, ID: "trg1"})
		Expect(err).NotTo(HaveOccurred())
		out := run(r, rec.ID, read("db-write"), read("db-read"), read("db-other"), write("db-write"))
		Expect(out[:3]).To(Equal([]string{"ok:db-write", "ok:db-read", "refused:db-other"}))
		// An unattended run changes nothing, whatever the list says: that rule
		// stands before this one and is not loosened by it.
		Expect(out[3]).NotTo(HavePrefix("ok:"))
	})
	It("reads the list from the stored agent at each call", func() {
		r := setup(true)
		save(r, "ag", scopedList, "tables")
		id := chat(r, "ag")
		Expect(run(r, id, write("db-write"))).To(Equal([]string{"ok:db-write"}))
		save(r, "ag", resourcesets.Sets{"tables": {{ID: "db-write", Mode: "read"}}}, "tables")
		Expect(run(r, id, write("db-write"), read("db-write"))).To(Equal([]string{"refused:db-write", "ok:db-write"}))
		save(r, "ag", nil, "tables")
		Expect(run(r, id, read("db-write"))).To(Equal([]string{"refused:db-write"}))
	})
	It("fails closed when the agent of the chat is gone", func() {
		r := setup(true)
		save(r, "ag", scopedList, "tables")
		id := chat(r, "ag")
		Expect(r.agents.Delete(ctx, alice, "ag")).To(Succeed())
		sent(r.svc, alice, id, "go")
		idle(r.svc, alice, id)
		Expect(r.host.reached).To(BeEmpty())
	})
	It("keeps a sub-agent inside the list of the agent that started it", func() {
		r := setup(true)
		save(r, "parent", scopedList, "tables")
		save(r, "child", resourcesets.Sets{"tables": {{ID: "db-write", Mode: "write"}, {ID: "db-other", Mode: "write"}}}, "tables")
		id := chat(r, "parent")
		steps = []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"child task","agent":"child","wait":true}`}, {Say: "parent done"}}
		r.h.Script = func(a workspace.Actor, text string) []studiotest.ToolStep {
			if text == "child task" {
				return []studiotest.ToolStep{
					{Tool: "addon__tables__rows-insert", Args: `{"resourceId":"db-write"}`},
					{Tool: "addon__tables__rows-insert", Args: `{"resourceId":"db-other"}`},
					{Say: "child done"},
				}
			}
			return steps
		}
		sent(r.svc, alice, id, "go")
		idle(r.svc, alice, id)
		var outs []string
		for _, res := range r.h.ResultsFor("addon__tables__rows-insert") {
			outs = append(outs, res.Out)
		}
		Expect(outs).To(Equal([]string{"ok:db-write", "refused:db-other"}), "the child's own list adds nothing the parent lacks; spawn: %+v", r.h.ResultsFor("spawn_agent"))
	})

	Describe("the selection of a chat", func() {
		It("accepts a listed resource only, for a chat of an agent", func() {
			r := setup(true)
			save(r, "ag", scopedList, "tables")
			id := chat(r, "ag")
			for resource, want := range map[string]bool{"db-read": true, "db-write": true, "db-other": false} {
				got, err := r.svc.ResourceSelectable(ctx, alice, id, "tables", resource)
				Expect(err).NotTo(HaveOccurred())
				Expect(got).To(Equal(want), resource)
			}
		})
		It("accepts anything for a chat with no agent and for an add-on that does not opt in", func() {
			r := setup(true)
			got, err := r.svc.ResourceSelectable(ctx, alice, create(r.svc, alice), "tables", "db-any")
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(BeTrue())
			r = setup(false)
			save(r, "ag", nil, "tables")
			got, err = r.svc.ResourceSelectable(ctx, alice, chat(r, "ag"), "tables", "db-any")
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(BeTrue())
		})
	})

	It("lists the tools of an add-on with their kind and the word for its resources", func() {
		r := setup(true)
		id := create(r.svc, alice)
		list, err := r.svc.Addons(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		var names []string
		for _, t := range list[0].ToolList {
			names = append(names, t.Name+map[bool]string{true: ":write", false: ":read"}[t.Write])
		}
		Expect(strings.Join(names, ",")).To(Equal("databases-create:write,rows-insert:write,rows-query:read"))
	})
})

// chooserHost adds the listing of an add-on and of its resources.
type chooserHost struct {
	*resourceHost
	items []studio.ResourceChoice
	err   error
}

func (c chooserHost) AddonListings(context.Context, string) ([]studio.AddonListing, error) {
	return []studio.AddonListing{{AddonID: "tables", Title: "Tables", Resources: &studio.ResourceLabel{Noun: "database", NounPlural: "databases"}}}, nil
}

func (c chooserHost) ResourceChoices(_ context.Context, _, addon string) ([]studio.ResourceChoice, bool, error) {
	if addon != "tables" {
		return nil, false, studio.ErrNotScoped
	}
	return c.items, false, c.err
}

var _ = Describe("the resources a person can give an agent", func() {
	ctx := context.Background()
	newSvc := func(h studio.AddonTools) *studio.Service {
		svc, _ := newToolService(func(workspace.Actor, string) []studiotest.ToolStep { return nil }, func(d *studio.Deps) { d.AddonTools = h })
		return svc
	}
	It("lists what the add-on's listing gives, with the word for them", func() {
		svc := newSvc(chooserHost{resourceHost: &resourceHost{scoped: true}, items: []studio.ResourceChoice{{ID: "db-a", Name: "A", Role: "owner"}}})
		got, err := svc.AgentResourceChoices(ctx, alice, "tables")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Items).To(Equal([]studio.ResourceChoice{{ID: "db-a", Name: "A", Role: "owner"}}))
		Expect(got.Label).NotTo(BeNil())
		Expect(got.Label.NounPlural).To(Equal("databases"))
	})
	It("is not found for an add-on that does not opt in, and when the broker cannot list", func() {
		svc := newSvc(chooserHost{resourceHost: &resourceHost{scoped: true}})
		_, err := svc.AgentResourceChoices(ctx, alice, "office")
		Expect(err).To(MatchError(studio.ErrNotScoped))
		_, err = newSvc(&resourceHost{}).AgentResourceChoices(ctx, alice, "tables")
		Expect(err).To(MatchError(studio.ErrNotScoped))
	})
})
