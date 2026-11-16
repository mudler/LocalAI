package localaitools

import (
	"encoding/json"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
)

// clientToolNames lists the names in ClientTools.
func clientToolNames() []string {
	var out []string
	for _, s := range ClientTools {
		if !slices.Contains(out, s.Tool) {
			out = append(out, s.Tool)
		}
	}
	return out
}

var _ = Describe("Client tool table", func() {
	It("has an entry with a route, a feature and a model flag for every client tool", func() {
		want := []string{
			ToolGenerateImage, ToolSpeak, ToolTranscribeAudio, ToolDescribeImage, ToolEmbed,
			ToolDetect, ToolRerank, ToolGenerateVideo, ToolGenerateSound, ToolUpscaleImage, ToolTransformAudio,
			ToolVoice, ToolFace, ToolVoiceProfile, ToolListModels, ToolProjects,
		}
		Expect(clientToolNames()).To(ConsistOf(want))
		for _, s := range ClientTools {
			Expect(s.Route).To(HavePrefix("/"), s.Tool)
			Expect(s.Method).To(BeElementOf("GET", "POST", "DELETE", "PATCH", "PUT"), s.Tool)
			if s.Tool != ToolVoiceProfile && s.Tool != ToolListModels && s.Tool != ToolProjects {
				// voice_profile, list_models and projects manage or read data and need no model.
				Expect(s.Method).To(Equal("POST"), s.Tool)
				Expect(s.Flag).ToNot(BeZero(), s.Tool)
			}
			Expect(s.Capability).ToNot(BeEmpty(), s.Tool)
		}
	})

	It("names a feature that exists, and the API route checks that same feature", func() {
		for _, s := range ClientTools {
			if s.Feature == NoFeature {
				// list_models: the route checks no feature (the model
				// allow-list filters its answer).
				Expect(s.Tool).To(Equal(ToolListModels))
				continue
			}
			Expect(auth.AllFeatures).To(ContainElement(s.Feature), "%s: unknown feature %q", s.Tool, s.Feature)
			found := false
			for _, r := range auth.RouteFeatureRegistry {
				// A client row (client_inference) is another way in for a
				// client key; the user feature is the one the tool names.
				if r.Method == s.Method && r.Pattern == s.Route && !auth.IsClientFeature(r.Feature) {
					found = true
					Expect(r.Feature).To(Equal(s.Feature), "%s: %s %s checks another feature", s.Tool, s.Method, s.Route)
				}
			}
			Expect(found).To(BeTrue(), "%s: %s %s is not in auth.RouteFeatureRegistry", s.Tool, s.Method, s.Route)
		}
	})

	It("carries the projects routes as rows of the projects tool, on the chats feature (the lists of agents, connections and triggers on their own), with no model", func() {
		// Decision (see .agents/localai-assistant-mcp.md): the client group may
		// carry per-user routes that are not inference, as voice_profile does.
		// The projects routes are of the chats feature, which clientCoveredFeatures
		// does not list (that is the feature of the chat itself, not of the studio),
		// so no row of this table is forced by the coverage test; the rows are here
		// because the tool acts as the calling user.
		want := map[string]string{
			ActionList: "GET /api/chats/projects", ActionGet: "GET /api/chats/projects/:id",
			ActionCreate: "POST /api/chats/projects", ActionUpdate: "PATCH /api/chats/projects/:id",
			ActionArchive: "POST /api/chats/projects/:id/archive", ActionRestore: "POST /api/chats/projects/:id/restore",
			ActionDelete: "DELETE /api/chats/projects/:id", ActionSetChatProject: "PUT /api/chats/:id/project",
			ActionPull: "PUT /api/chats/:id/projects", ActionUnpull: "PUT /api/chats/:id/projects",
			ActionAgents:      "GET /api/chats/projects/:id/agents",
			ActionConnections: "GET /api/chats/projects/:id/connections", ActionTriggers: "GET /api/chats/projects/:id/triggers",
			ActionLoops: "GET /api/chats/projects/:id/loops", ActionShared: "GET /api/chats/projects/shared",
			ActionSharedGet: "GET /api/chats/projects/shared/:ref", actionSharedMemory: "GET /api/chats/projects/shared/:ref/memory",
			ActionMemory: "GET /api/chats/projects/:id/memory", ActionWriteMemory: "PUT /api/chats/projects/:id/memory/:file",
			ActionChats: "GET /api/chats/projects/:id/chats",
		}
		// The three lists show objects of another feature: the registry gates
		// them by that feature, and the row says the same.
		feature := map[string]string{ActionAgents: auth.FeatureAgents, ActionConnections: auth.FeatureConnectors, ActionTriggers: auth.FeatureTriggers}
		for action, route := range want {
			s := clientSpecAction(ToolProjects, action)
			Expect(s.Tool).To(Equal(ToolProjects), action)
			Expect(s.Method+" "+s.Route).To(Equal(route), action)
			if f, ok := feature[action]; ok {
				Expect(s.Feature).To(Equal(f), action)
			} else {
				Expect(s.Feature).To(Equal(auth.FeatureChats), action)
			}
			Expect(s.Flag).To(BeZero(), action)
		}
		Expect(ToolGroupOf(ToolProjects)).To(Equal(GroupClient))
	})

	It("tells client tools from manage tools", func() {
		for _, name := range clientToolNames() {
			Expect(ToolGroupOf(name)).To(Equal(GroupClient), name)
			Expect(ToolGroups()[name]).To(Equal(GroupClient), name)
		}
		for _, name := range mutatingToolNames {
			Expect(ToolGroupOf(name)).To(Equal(GroupManage), name)
		}
		Expect(ToolGroupOf(ToolListNodes)).To(Equal(GroupManage))
	})

	It("registers every client tool by default", func() {
		ctx, sess, done := connectInMemory(&fakeClient{}, Options{})
		defer done()
		names := listToolNames(ctx, sess)
		for _, n := range clientToolNames() {
			Expect(slices.Contains(names, n)).To(BeTrue(), "%s missing", n)
		}
	})

	It("registers no client tool that writes files or makes content in the read-only flavour", func() {
		ctx, sess, done := connectInMemory(&fakeClient{}, Options{DisableMutating: true})
		defer done()
		names := listToolNames(ctx, sess)
		for _, n := range []string{ToolGenerateImage, ToolSpeak, ToolEmbed, ToolGenerateVideo, ToolGenerateSound, ToolUpscaleImage, ToolTransformAudio} {
			Expect(names).NotTo(ContainElement(n))
		}
		Expect(names).To(ContainElements(ToolTranscribeAudio, ToolDescribeImage, ToolRerank, ToolVoiceProfile, ToolListModels, ToolProjects))
		// voice_profile and projects keep only the actions that read.
		res, err := sess.ListTools(ctx, nil)
		Expect(err).NotTo(HaveOccurred())
		for _, t := range res.Tools {
			if t.Name == ToolProjects {
				schema, _ := json.Marshal(t.InputSchema)
				Expect(string(schema)).To(ContainSubstring(`"list"`))
				Expect(string(schema)).To(ContainSubstring(`"get"`))
				for _, w := range []string{"create", "update", "archive", "restore", "delete", "set_chat_project", "pull", "unpull"} {
					Expect(string(schema)).NotTo(ContainSubstring(`"` + w + `"`))
				}
			}
			if t.Name == ToolVoiceProfile {
				schema, _ := json.Marshal(t.InputSchema)
				Expect(string(schema)).To(ContainSubstring(`"list"`))
				Expect(string(schema)).NotTo(ContainSubstring(`"create"`))
				Expect(string(schema)).NotTo(ContainSubstring(`"delete"`))
			}
		}
	})

	It("registers no client tool that needs files when there is no files folder", func() {
		ctx, sess, done := connectInMemory(&fakeClient{}, Options{DisableClientFiles: true})
		defer done()
		names := listToolNames(ctx, sess)
		for n := range clientToolFiles {
			Expect(names).NotTo(ContainElement(n))
		}
		Expect(names).To(ContainElements(ToolRerank, ToolVoiceProfile, ToolListModels, ToolProjects))
	})

	It("keeps list_models in the client group, and list_installed_models in the manage group", func() {
		Expect(ToolGroupOf(ToolListModels)).To(Equal(GroupClient))
		Expect(ToolGroupOf(ToolListInstalledModels)).To(Equal(GroupManage))
	})
})
