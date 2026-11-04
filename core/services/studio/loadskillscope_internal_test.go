package studio

import (
	"context"
	"encoding/json"
	"testing/fstest"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/session"
	model "github.com/mudler/LocalAI/pkg/addons/model/executionstatus"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// observedBroker is a Sandbox whose isolation comes from the typed execution
// status, read through the request scope as the real gateway reads it.
type observedBroker struct {
	*documentBroker
	*observationProvider
}

func (b *observedBroker) Tools(ctx context.Context, u string) ([]AddonTool, error) {
	return b.documentBroker.Tools(ctx, u)
}
func (b *observedBroker) Unavailable(ctx context.Context, u, a string, _ bool) string {
	if v, e := ExecutionObservation(ctx, u, a); v.Readiness == model.Unavailable {
		return e
	}
	return ""
}
func (b *observedBroker) Isolation(ctx context.Context, u, a string) string {
	v, _ := ExecutionObservation(ctx, u, a)
	if v.Readiness != model.Available || v.Isolation == model.UnknownIsolation {
		return ""
	}
	return string(v.Isolation)
}

var _ = Describe("load_skill as the harness calls it", func() {
	It("loads a document skill when the tool runs outside the turn's context values", func() {
		ctx := context.Background()
		b := &observedBroker{
			documentBroker:      &documentBroker{skillBroker: &skillBroker{}, defs: []AddonTool{{AddonID: "sandbox", Name: "addon__sandbox__exec", Shadows: "bash", Execution: sandboxExecution}}},
			observationProvider: &observationProvider{addon: "sandbox"},
		}
		svc, f := newSkillService(b.skillBroker)
		svc.d.AddonTools = b
		agentsStore, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc.d.Agents = agentsStore
		Expect(f.SyncAddonSkills(ctx, "sandbox", fstest.MapFS{"pptx/SKILL.md": {Data: skillMD("pptx", "Sandbox slides.", "Slides body.", "requires: [exec]")}})).To(Succeed())
		b.set(AddonSkillSet{AddonID: "sandbox", Skills: []string{"pptx"}, Shadows: []string{"pptx"}, Isolated: []string{"pptx"}, Running: true})
		agent, err := svc.SaveAgent(ctx, "alice", agents.Agent{Name: "Slides", Model: "m", Addons: []string{"sandbox"}, Skills: []string{"pptx"}})
		Expect(err).NotTo(HaveOccurred())
		parent, err := svc.Create(ctx, "alice", CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		rec := session.ChatRecord{ID: "slides-child", Principal: "alice", Model: "m", Kind: session.KindSubAgent, ParentID: parent.ID, AgentID: agent.ID}
		Expect(svc.d.Store.CreateChat(ctx, rec)).To(Succeed())
		x, err := svc.ChatAccess(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())

		// The turn builds its tools under the product scope, as the session does.
		turnCtx, closeScope := svc.productScope(ctx, x)
		defer closeScope()
		turn, err := svc.chatTools(turnCtx, x)
		Expect(err).NotTo(HaveOccurred())
		var loader session.Tool
		for _, tool := range turn.Tools {
			if tool.Name == "load_skill" {
				loader = tool
			}
		}
		Expect(loader.Run).NotTo(BeNil(), "the tool is offered at the start of the turn")

		// The harness calls the tool with a context of its own.
		body, err := loader.Run(context.Background(), json.RawMessage(`{"name":"pptx"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(body).To(ContainSubstring("Slides body."))
	})
})
