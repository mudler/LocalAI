package studio_test

import (
	"context"
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// hostTool is a tool of the host with the given name.
func hostTool(name string, readOnly bool) session.Tool {
	return session.Tool{
		Name: name, Description: name, Schema: json.RawMessage(`{"type":"object"}`), ReadOnly: readOnly,
		Run: func(context.Context, json.RawMessage) (string, error) { return "done", nil },
	}
}

var _ = Describe("the intent line of a tool call", func() {
	ctx := context.Background()

	// raw reads the transcript as the store has it, with the server-only lines.
	raw := func(st store.Store, id string) []session.Entry {
		GinkgoHelper()
		es, err := st.Entries(ctx, id, 0, 0)
		Expect(err).NotTo(HaveOccurred())
		return es
	}

	It("comes before the result of a tool that may change something, and the client never sees it", func() {
		var st store.Store
		svc, _ := newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "go" {
				return []studiotest.ToolStep{{Tool: "deploy", Args: `{"env":"prod"}`}, {Tool: "lookup", Args: `{}`}, {Say: "done"}}
			}
			return nil
		}, func(d *studio.Deps) {
			st = d.Store
			d.HostTools = func(context.Context, workspace.Actor) ([]session.Tool, error) {
				return []session.Tool{hostTool("deploy", false), hostTool("lookup", true)}, nil
			}
		})
		id := create(svc, alice)
		sent(svc, alice, id, "go")
		idle(svc, alice, id)

		var roles []string
		for _, e := range raw(st, id) {
			if e.Role != session.RoleCheckpoint && e.Role != session.RoleUser {
				roles = append(roles, e.Role+":"+strings.SplitN(e.Text, ":", 2)[0])
			}
		}
		Expect(roles[:3]).To(Equal([]string{"tool_intent:deploy", "tool:deploy", "tool:lookup"}),
			"deploy leaves an intent line before its result, lookup (read only) leaves none: %v", roles)
		Expect(raw(st, id)[1].Note).To(Equal(`{"env":"prod"}`))
		for _, e := range transcript(svc, alice, id) {
			Expect(e.Role).NotTo(Equal(session.RoleToolIntent))
		}
		page, err := svc.Messages(ctx, alice, id, 0, 2)
		Expect(err).NotTo(HaveOccurred())
		Expect(page).To(HaveLen(2), "a page is full although a line was left out")
		Expect(page[1].Seq).To(BeNumerically(">", 2), "the page skips the intent line")
	})

	It("tells the model that the outcome of a cut call is not known, and the plain note otherwise", func() {
		r := newResumeRig(true)
		// A call to deploy started and the pod died: intent, no result.
		cut := func(withResult bool) string {
			id := create(r.svc, alice)
			_, err := r.st.AppendEntry(ctx, id, session.Entry{Turn: 1, Role: session.RoleUser, Text: "ship it"})
			Expect(err).NotTo(HaveOccurred())
			_, err = r.st.AppendEntry(ctx, id, session.Entry{Turn: 1, Role: session.RoleToolIntent, Text: "deploy", Note: `{"env":"prod"}`})
			Expect(err).NotTo(HaveOccurred())
			if withResult {
				_, err = r.st.AppendEntry(ctx, id, session.Entry{Turn: 1, Role: session.RoleTool, Text: "deploy: ok\ndone"})
				Expect(err).NotTo(HaveOccurred())
			}
			Expect(r.st.ClaimTurn(ctx, alice, id, "dead")).To(Succeed())
			Expect(r.st.MarkInterrupted(ctx, alice, id, "dead", 0)).To(Succeed())
			started, err := r.svc.SweepResumes(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(started).To(Equal(1))
			idle(r.svc, alice, id)
			return (*r.calls)[len(*r.calls)-1]
		}
		unknown := cut(false)
		Expect(unknown).To(HavePrefix("(System note:"))
		Expect(unknown).To(ContainSubstring("not known whether they took effect: deploy " + `{"env":"prod"}`))
		Expect(unknown).To(ContainSubstring("Check the current state first"))
		Expect(unknown).NotTo(ContainSubstring("do that step again"), "an effect must not be repeated blindly")
		Expect(cut(true)).To(Equal(session.ResumePrompt))
	})
})
