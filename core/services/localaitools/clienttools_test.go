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
			ToolVoice, ToolFace, ToolVoiceProfile, ToolListModels,
		}
		Expect(clientToolNames()).To(ConsistOf(want))
		for _, s := range ClientTools {
			Expect(s.Route).To(HavePrefix("/"), s.Tool)
			Expect(s.Method).To(BeElementOf("GET", "POST", "DELETE"), s.Tool)
			if s.Tool != ToolVoiceProfile && s.Tool != ToolListModels {
				// voice_profile and list_models manage or read data and need no model.
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
		Expect(names).To(ContainElements(ToolTranscribeAudio, ToolDescribeImage, ToolRerank, ToolVoiceProfile, ToolListModels))
		// voice_profile keeps only list.
		res, err := sess.ListTools(ctx, nil)
		Expect(err).NotTo(HaveOccurred())
		for _, t := range res.Tools {
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
		Expect(names).To(ContainElements(ToolRerank, ToolVoiceProfile, ToolListModels))
	})

	It("keeps list_models in the client group, and list_installed_models in the manage group", func() {
		Expect(ToolGroupOf(ToolListModels)).To(Equal(GroupClient))
		Expect(ToolGroupOf(ToolListInstalledModels)).To(Equal(GroupManage))
	})
})
