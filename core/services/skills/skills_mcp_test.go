package skills_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	agiSkills "github.com/mudler/LocalAGI/services/skills"
	localskills "github.com/mudler/LocalAI/core/services/skills"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSkillsMCP(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Skills MCP test")
}

// listSkillsResult mirrors the output struct of skillserver's list_skills tool.
type listSkillsResult struct {
	Skills []struct {
		ID          string `json:"id"`
		Description string `json:"description,omitempty"`
	} `json:"skills"`
}

// Exercises the same wire the agent uses at runtime: open an in-process
// MCP session via LocalAGI's skills.Service, create a skill through the
// LocalAI FilesystemManager, then list_skills on the still-open session.
// Guards against regressions in the manager <-> MCP session lifecycle
// (e.g. cached manager not picking up newly-created skills).
var _ = Describe("Skills exposed to agent via MCP", func() {
	var (
		stateDir string
		svc      *agiSkills.Service
		ctx      context.Context
		cancel   context.CancelFunc
	)

	BeforeEach(func() {
		var err error
		stateDir, err = os.MkdirTemp("", "skills-mcp-test")
		Expect(err).NotTo(HaveOccurred())

		// Create the LocalAGI skills service (this is what AgentPoolService wires
		// into LocalAGI's state.NewAgentPool for MCP session exposure).
		svc, err = agiSkills.NewService(stateDir)
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	})

	AfterEach(func() {
		cancel()
		Expect(os.RemoveAll(stateDir)).To(Succeed())
	})

	It("returns a skill created after the MCP session was established", func() {
		// Open the MCP session first — this is what the agent does at startup
		// with EnableSkills=true, before any skill might exist.
		session, err := svc.GetMCPSession(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(session).NotTo(BeNil())

		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_skills"})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.IsError).To(BeFalse())
		var initial listSkillsResult
		Expect(decodeMCPText(res, &initial)).To(Succeed())
		Expect(initial.Skills).To(BeEmpty(), "no skills should exist initially")

		// Create a skill via the LocalAI FilesystemManager — same code path the
		// /api/agents/skills POST endpoint takes.
		mgr := localskills.NewFilesystemManager(svc)
		_, err = mgr.Create("talk-like-pirate", "Talk like a pirate", "Speak in pirate-style.", "", "", "", nil)
		Expect(err).NotTo(HaveOccurred())

		// Re-list via the SAME already-open session: the manager is shared,
		// so a freshly-created skill must be visible without re-attaching.
		res, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "list_skills"})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.IsError).To(BeFalse())

		var got listSkillsResult
		Expect(decodeMCPText(res, &got)).To(Succeed())

		ids := make([]string, 0, len(got.Skills))
		for _, s := range got.Skills {
			ids = append(ids, s.ID)
		}
		Expect(ids).To(ContainElement("talk-like-pirate"))
	})
})

func mcpText(res *mcp.CallToolResult) string {
	text := ""
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	return text
}

func decodeMCPText(res *mcp.CallToolResult, out any) error {
	text := mcpText(res)
	if text == "" {
		return nil
	}
	return json.Unmarshal([]byte(text), out)
}
