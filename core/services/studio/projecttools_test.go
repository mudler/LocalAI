package studio_test

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

var _ = Describe("the list_projects chat tool", func() {
	ctx := context.Background()
	const bob = "bob"

	type rig struct {
		svc  *studio.Service
		proj *projects.Service
		h    *studiotest.ToolHarness
		st   store.Store
	}
	newRig := func(withProjects bool) rig {
		f := newFiles()
		ps, err := projects.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		proj := projects.NewService(projects.Options{Store: ps, Files: f})
		r := rig{proj: proj}
		r.svc, r.h = newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "go" {
				return []studiotest.ToolStep{{Tool: "list_projects", Args: `{}`}, {Say: "ok"}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		}, func(d *studio.Deps) {
			d.Files = f
			if withProjects {
				d.Projects = proj
			}
			r.st = d.Store
		})
		return r
	}
	mk := func(r rig, owner, name, desc string) projects.Project {
		p, err := r.proj.Create(ctx, owner, projects.CreateInput{Name: name, Description: desc})
		Expect(err).NotTo(HaveOccurred())
		return p
	}
	call := func(r rig, user string) studiotest.ToolResult {
		GinkgoHelper()
		id := create(r.svc, user)
		before := len(r.h.ResultsFor("list_projects"))
		sent(r.svc, user, id, "go")
		idle(r.svc, user, id)
		res := r.h.ResultsFor("list_projects")
		Expect(len(res)).To(BeNumerically(">", before))
		return res[len(res)-1]
	}

	It("returns only the caller's active projects, with id, name, slug and description", func() {
		r := newRig(true)
		mine := mk(r, alice, "Acme", "the Acme work")
		gone := mk(r, alice, "Old", "")
		_, err := r.proj.Archive(ctx, alice, gone.ID)
		Expect(err).NotTo(HaveOccurred())
		theirs := mk(r, bob, "Theirs", "bob only")

		res := call(r, alice)
		Expect(res.Err).NotTo(HaveOccurred())
		var out struct {
			Projects []map[string]any `json:"projects"`
		}
		Expect(json.Unmarshal([]byte(res.Out), &out)).To(Succeed(), res.Out)
		Expect(out.Projects).To(HaveLen(1))
		Expect(out.Projects[0]).To(Equal(map[string]any{"id": mine.ID, "name": "Acme", "slug": mine.Slug, "description": "the Acme work"}))
		Expect(res.Out).NotTo(ContainSubstring(theirs.ID))
		Expect(res.Out).NotTo(ContainSubstring("bob only"))
		Expect(res.Out).NotTo(ContainSubstring(gone.ID))

		other := call(r, bob)
		Expect(other.Out).To(ContainSubstring(theirs.ID))
		Expect(other.Out).NotTo(ContainSubstring(mine.ID))
	})

	It("shows a chat that works inside a fence its own project only", func() {
		f := newFiles()
		ps, err := projects.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		proj := projects.NewService(projects.Options{Store: ps, Files: f})
		ag, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc, h := newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "go" {
				return []studiotest.ToolStep{{Tool: "list_projects", Args: `{}`}, {Say: "ok"}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		}, func(d *studio.Deps) { d.Files, d.Projects, d.Agents = f, proj, ag })
		own, err := proj.Create(ctx, alice, projects.CreateInput{Name: "Kitchen", Description: "own text"})
		Expect(err).NotTo(HaveOccurred())
		other, err := proj.Create(ctx, alice, projects.CreateInput{Name: "Taxes", Description: "private text"})
		Expect(err).NotTo(HaveOccurred())
		a, err := svc.SaveAgent(ctx, alice, agents.Agent{Name: "Helper", Model: "m", ProjectID: own.ID})
		Expect(err).NotTo(HaveOccurred())

		fenced, err := svc.Create(ctx, alice, studio.CreateInput{Model: "m", AgentID: a.ID})
		Expect(err).NotTo(HaveOccurred())
		sent(svc, alice, fenced.ID, "go")
		idle(svc, alice, fenced.ID)
		res := h.ResultsFor("list_projects")
		Expect(res).NotTo(BeEmpty())
		Expect(res[len(res)-1].Out).To(And(ContainSubstring(own.ID), Not(ContainSubstring(other.ID)), Not(ContainSubstring("Taxes")), Not(ContainSubstring("private text"))))

		// A chat of the same person with no agent sees both.
		open := create(svc, alice)
		sent(svc, alice, open, "go")
		idle(svc, alice, open)
		res = h.ResultsFor("list_projects")
		Expect(res[len(res)-1].Out).To(And(ContainSubstring(own.ID), ContainSubstring(other.ID)))
	})

	It("says there are no projects, instead of failing", func() {
		r := newRig(true)
		res := call(r, alice)
		Expect(res.Err).NotTo(HaveOccurred())
		Expect(res.Out).To(ContainSubstring(`"projects":[]`))
	})

	It("is a group of its own, on by default like the collections group, with a short description", func() {
		r := newRig(true)
		id := create(r.svc, alice)
		p, err := r.svc.Tools(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		var found *studio.BuiltinGroup
		for i := range p.Builtins {
			if p.Builtins[i].Group == builtins.GroupProjects {
				found = &p.Builtins[i]
			}
		}
		Expect(found).NotTo(BeNil())
		Expect(found.OptIn).To(BeFalse())
		Expect(found.Off).To(BeFalse())
		Expect(found.Tools).To(ConsistOf("list_projects"))
		sent(r.svc, alice, id, "hi")
		idle(r.svc, alice, id)
		Expect(r.h.ToolNames(id)).To(ContainElement("list_projects"))
		// Always sent: one short sentence, an empty schema, and no pull_project
		// (no approval mechanism fits a model-initiated pull yet).
		Expect(len(r.h.ToolDescription(id, "list_projects"))).To(BeNumerically("<=", 200))
		Expect(r.h.ToolNames(id)).NotTo(ContainElement("pull_project"))
	})

	It("is off for a chat that switched the group off", func() {
		r := newRig(true)
		id := create(r.svc, alice)
		Expect(r.svc.SetBuiltinsOff(ctx, alice, id, []string{builtins.GroupProjects})).To(Succeed())
		sent(r.svc, alice, id, "hi")
		idle(r.svc, alice, id)
		Expect(r.h.ToolNames(id)).NotTo(ContainElement("list_projects"))
	})

	It("is not offered when the server has no projects", func() {
		r := newRig(false)
		id := create(r.svc, alice)
		sent(r.svc, alice, id, "hi")
		idle(r.svc, alice, id)
		Expect(r.h.ToolNames(id)).NotTo(ContainElement("list_projects"))
	})

	DescribeTable("is built only for a chat a person watches",
		func(c builtins.Chat, offered bool) {
			r := newRig(true)
			c.Actor = workspace.Actor{Principal: alice}
			for _, b := range r.svc.Builtins().Build(ctx, c) {
				if b.ID == "list_projects" {
					Expect(b.OK).To(Equal(offered), b.Reason)
					return
				}
			}
			Fail("list_projects is not in the registry")
		},
		Entry("a person's own chat", builtins.Chat{}, true),
		Entry("a sub-agent", builtins.Chat{SubAgent: true}, false),
		Entry("a messaging chat", builtins.Chat{Origin: session.OriginConnector}, false),
		Entry("a trigger run", builtins.Chat{Origin: session.OriginTrigger}, false),
		Entry("a job run", builtins.Chat{Origin: session.OriginJob}, false),
	)

	It("is absent from a messaging chat's turn", func() {
		r := newRig(true)
		rec, err := r.svc.CreateAs(ctx, alice, studio.CreateInput{Model: "m"}, studio.Origin{Kind: session.OriginConnector, ID: "binding"})
		Expect(err).NotTo(HaveOccurred())
		sent(r.svc, alice, rec.ID, "hi")
		idle(r.svc, alice, rec.ID)
		Expect(r.h.ToolNames(rec.ID)).NotTo(ContainElement("list_projects"))
	})

})

// The model in an interactive chat may not mount a project, move a chat or
// delete a project: those are for the person (the Projects page) and for
// API and MCP callers. The gate sits on the chat side only.
var _ = Describe("the projects client tool in an interactive chat", func() {
	call := func(args string) (studiotest.ToolResult, int) {
		GinkgoHelper()
		r := newLocalAIRig(func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: localaitools.ToolProjects, Args: args}, {Say: "done"}}
		})
		r.turnNames("root", create(r.svc, "root"))
		res := r.h.ResultsFor(localaitools.ToolProjects)
		Expect(res).To(HaveLen(1))
		return res[0], r.client.callCount()
	}

	DescribeTable("refuses pull, unpull, set_chat_project and delete, and nothing reaches the routes",
		func(args string) {
			res, calls := call(args)
			Expect(res.Err).To(MatchError(ContainSubstring("Projects page")), args)
			Expect(res.Err).To(MatchError(ContainSubstring("ask the person")), args)
			Expect(calls).To(BeZero(), "no route was called, so no chat or project changed")
		},
		Entry("pull", `{"action":"pull","chat_id":"c1","project":"p1"}`),
		Entry("unpull", `{"action":"unpull","chat_id":"c1","project":"p1"}`),
		Entry("set_chat_project", `{"action":"set_chat_project","chat_id":"c1","project":"p1"}`),
		Entry("delete", `{"action":"delete","project":"p1"}`),
	)

	DescribeTable("refuses to attach a connector, an add-on or an MCP server and calls no route",
		func(args string) {
			res, calls := call(args)
			Expect(res.Err).To(MatchError(ContainSubstring("credentials")), args)
			Expect(res.Err).To(MatchError(ContainSubstring("Ask the person")), args)
			Expect(calls).To(BeZero())
		},
		Entry("connector on update", `{"action":"update","project":"p1","attached":{"connectors":["mail"]}}`),
		Entry("add-on on update", `{"action":"update","project":"p1","attached":{"skills":["s"],"addons":["a"]}}`),
		Entry("MCP server on create", `{"action":"create","name":"x","attached":{"mcp_servers":["srv"]}}`),
	)

	It("lets the model attach collections on create", func() {
		res, calls := call(`{"action":"create","name":"x","attached":{"collections":["docs"]}}`)
		Expect(res.Err).NotTo(HaveOccurred())
		Expect(calls).To(Equal(1))
	})

	// The tool replaces the block; from a chat the lists are added to it, so a
	// model that names one skill detaches nothing the person attached.
	It("adds the skills of an update to what the project attaches and removes nothing", func() {
		var sent []string
		r := newLocalAIRig(func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: localaitools.ToolProjects, Args: `{"action":"update","project":"p1","attached":{"skills":["s"]}}`}, {Say: "done"}}
		})
		r.turnNames("root", create(r.svc, "root"))
		res := r.h.ResultsFor(localaitools.ToolProjects)
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).NotTo(HaveOccurred())
		sent = r.client.projectCalls()
		Expect(sent).To(HaveLen(2), "a read of the project, then the update")
		var upd localaitools.ProjectsToolInput
		Expect(json.Unmarshal([]byte(sent[1]), &upd)).To(Succeed())
		Expect(upd.Action).To(Equal(localaitools.ActionUpdate))
		Expect(upd.Attached["skills"]).To(Equal([]string{"old", "s"}))
		Expect(upd.Attached["connectors"]).To(Equal([]string{"mail"}), "the connector the person attached stays")
	})

	DescribeTable("refuses arguments it cannot read instead of passing them on",
		func(args string) {
			res, calls := call(args)
			Expect(res.Err).To(MatchError(ContainSubstring("ask the person")), args)
			Expect(calls).To(BeZero())
		},
		Entry("not json", `not json`),
		Entry("a truncated object", `{"action":"delete","project":`),
		Entry("an action of the wrong type", `{"action":["delete"],"project":"p1"}`),
	)

	DescribeTable("still runs the reversible and read actions",
		func(args string) {
			res, calls := call(args)
			Expect(res.Err).NotTo(HaveOccurred(), args)
			Expect(calls).To(Equal(1), args)
		},
		Entry("list", `{"action":"list"}`),
		Entry("get", `{"action":"get","project":"p1"}`),
		Entry("create", `{"action":"create","name":"x"}`),
		Entry("update", `{"action":"update","project":"p1","name":"y"}`),
		Entry("archive", `{"action":"archive","project":"p1"}`),
		Entry("restore", `{"action":"restore","project":"p1"}`),
		Entry("agents", `{"action":"agents","project":"p1"}`),
		Entry("connections", `{"action":"connections","project":"p1"}`),
		Entry("triggers", `{"action":"triggers","project":"p1"}`),
		Entry("loops", `{"action":"loops","project":"p1"}`),
		Entry("shared", `{"action":"shared"}`),
	)
})
