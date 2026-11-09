package studio_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/coordinator"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// pageRig is a coordinator that owns the page pages/status/. PAGE writes the
// page, ESCAPE tries to write next to it, HELPER starts a helper that has no
// file tool, HELPERW one that is given write_file, and HELPERD one with the
// default tools.
func newPageRig() *coordRig {
	script := func(_ workspace.Actor, text string) []studiotest.ToolStep {
		switch text {
		case "PAGE":
			return []studiotest.ToolStep{
				{Tool: "write_file", Args: `{"path":"/index.html","content":"<html><body>status: green</body></html>"}`},
				{Say: "page written"},
			}
		case "ESCAPE":
			return []studiotest.ToolStep{
				{Tool: "write_file", Args: `{"path":"../other/up.txt","content":"x"}`},
				{Tool: "write_file", Args: `{"path":"/pages/other/abs.txt","content":"x"}`},
				{Say: "tried"},
			}
		case "HELPER":
			return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"HW","tools":["search"],"wait":true}`}, {Say: "done"}}
		case "HELPERW":
			return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"HW","tools":["write_file"],"wait":true}`}, {Say: "done"}}
		case "HELPERD":
			return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"HW","wait":true}`}, {Say: "done"}}
		case "HW":
			return []studiotest.ToolStep{
				{Tool: "write_file", Args: `{"path":"/helper.txt","content":"from a helper"}`},
				{Say: "tried"},
			}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}
	r := newShared().rig(script, studio.TriggerOptions{})
	host, st := newCoordHost(r.svc, nil)
	return &coordRig{trigRig: r, host: host, store: st}
}

var _ = Describe("coordinator page", func() {
	ctx := context.Background()
	const refused = "I did not run write_file"

	create := func(r *coordRig, grants ...string) (agents.Agent, string) {
		in := studio.CoordinatorInput{
			Agent:  agents.Agent{Name: "Desk", Model: "m", Builtins: grants},
			Config: coordinator.Config{Page: "status"},
		}
		v, err := r.host.Create(ctx, alice, in)
		Expect(err).NotTo(HaveOccurred())
		return v.Agent, v.Config.ChatID
	}
	run := func(r *coordRig, chat, text string) {
		sent(r.svc, alice, chat, text)
		idle(r.svc, alice, chat)
	}
	proposals := func(r *coordRig) int {
		if r.svc.Changes() == nil {
			return 0
		}
		all, err := r.svc.Changes().List(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		return len(all)
	}
	home := func(r *coordRig, path string) (string, bool) {
		text, ok, err := r.sh.files.ReadHome(ctx, alice, path, 1<<20)
		Expect(err).NotTo(HaveOccurred())
		return text, ok
	}

	It("a coordinator writes pages/status/index.html with write_file and no proposal is stored", func() {
		r := newPageRig()
		_, chat := create(r)
		before, ok := home(r, "/pages/status/index.html")
		Expect(ok).To(BeTrue(), "the page starts from the starter file")
		Expect(before).NotTo(ContainSubstring("status: green"))

		run(r, chat, "PAGE")
		res := r.h.ResultsFor("write_file")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).NotTo(HaveOccurred())
		Expect(res[0].Out).NotTo(ContainSubstring(refused))
		text, ok := home(r, "/pages/status/index.html")
		Expect(ok).To(BeTrue())
		Expect(text).To(ContainSubstring("status: green"))
		Expect(proposals(r)).To(BeZero(), "the write is direct: nothing waits for the owner")
		Expect(r.h.ResultsFor("propose_change")).To(BeEmpty())
	})

	It("without act.files the write is refused with the unattended sentence", func() {
		r := newPageRig()
		// The creator's choice: a coordinator whose grants leave out act.files.
		a, chat := create(r)
		a.Builtins = []string{"memory.write"}
		_, err := r.svc.SaveAgentWithFolders(ctx, alice, a, nil)
		Expect(err).NotTo(HaveOccurred())
		before, _ := home(r, "/pages/status/index.html")

		run(r, chat, "PAGE")
		res := r.h.ResultsFor("write_file")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).NotTo(HaveOccurred(), "a refusal is an answer, not an error")
		Expect(res[0].Out).To(ContainSubstring(refused))
		Expect(res[0].Out).To(ContainSubstring("act.files"))
		after, _ := home(r, "/pages/status/index.html")
		Expect(after).To(Equal(before), "the page is unchanged")
		Expect(proposals(r)).To(BeZero(), "a refused write does not turn into a proposal")
	})

	It("propose_change is not offered to a coordinator chat", func() {
		r := newPageRig()
		_, chat := create(r)
		run(r, chat, "PAGE")
		names := r.h.ToolNames(chat)
		Expect(names).To(ContainElement("write_file"))
		Expect(names).NotTo(ContainElement("propose_change"))
		Expect(names).NotTo(ContainElement("revert_change"))
	})

	It("the agent cannot write outside its page folder", func() {
		r := newPageRig()
		_, chat := create(r)
		run(r, chat, "ESCAPE")
		for _, p := range []string{"/pages/other/up.txt", "/pages/other/abs.txt", "/up.txt", "/other/up.txt", "/pages/up.txt"} {
			_, ok := home(r, p)
			Expect(ok).To(BeFalse(), "%s must not exist", p)
		}
		// The fence turns an absolute path into one inside the folder; a path that
		// climbs out of it is refused or lands inside. The text of the result is
		// not the proof: where the file is, is.
		var inside []string
		for _, p := range []string{"/pages/status/abs.txt", "/pages/status/pages/other/abs.txt", "/pages/status/up.txt", "/pages/status/other/up.txt"} {
			if _, ok := home(r, p); ok {
				inside = append(inside, p)
			}
		}
		Expect(inside).NotTo(BeEmpty(), "the writes that were allowed landed inside the page folder")
		Expect(r.h.ResultsFor("write_file")).To(HaveLen(2))
	})

	It("a helper cannot write the page unless the coordinator gave it write_file", func() {
		r := newPageRig()
		_, chat := create(r)

		run(r, chat, "HELPER")
		kids := childrenOf(r.svc, alice, chat)
		Expect(kids).To(HaveLen(1))
		Expect(r.h.ToolNames(kids[0].ID)).NotTo(ContainElement("write_file"))
		_, ok := home(r, "/pages/status/helper.txt")
		Expect(ok).To(BeFalse(), "a helper with a list of tools without write_file wrote nothing")
	})

	It("a helper given write_file writes only inside the page, as the coordinator would", func() {
		r := newPageRig()
		_, chat := create(r)
		run(r, chat, "HELPERW")
		kids := childrenOf(r.svc, alice, chat)
		Expect(kids).To(HaveLen(1))
		Expect(r.h.ToolNames(kids[0].ID)).To(ContainElement("write_file"))
		text, ok := home(r, "/pages/status/helper.txt")
		Expect(ok).To(BeTrue(), "the helper wrote into the page folder")
		Expect(text).To(Equal("from a helper"))
		_, ok = home(r, "/helper.txt")
		Expect(ok).To(BeFalse())
		Expect(proposals(r)).To(BeZero())
	})

	It("a helper with the default tools has the rights of its coordinator, and no more", func() {
		r := newPageRig()
		a, chat := create(r)
		a.Builtins = []string{"memory.write"} // no act.files
		_, err := r.svc.SaveAgentWithFolders(ctx, alice, a, nil)
		Expect(err).NotTo(HaveOccurred())
		run(r, chat, "HELPERD")
		kids := childrenOf(r.svc, alice, chat)
		Expect(kids).To(HaveLen(1))
		var out string
		for _, res := range r.h.ResultsFor("write_file") {
			if res.Chat == kids[0].ID {
				out = res.Out
			}
		}
		Expect(out).To(ContainSubstring(refused), "the helper writes under the rights of the root, which has no act.files")
		_, ok := home(r, "/pages/status/helper.txt")
		Expect(ok).To(BeFalse())
	})
})
