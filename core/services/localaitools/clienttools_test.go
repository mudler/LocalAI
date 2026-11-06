package localaitools

import (
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
			ToolDetect, ToolRerank, ToolGenerateVideo, ToolGenerateSound,
			ToolVoice, ToolFace, ToolVoiceProfile,
		}
		Expect(clientToolNames()).To(ConsistOf(want))
		for _, s := range ClientTools {
			Expect(s.Route).To(HavePrefix("/"), s.Tool)
			Expect(s.Method).To(BeElementOf("GET", "POST", "DELETE"), s.Tool)
			if s.Tool != ToolVoiceProfile {
				// voice_profile manages data and needs no model.
				Expect(s.Method).To(Equal("POST"), s.Tool)
				Expect(s.Flag).ToNot(BeZero(), s.Tool)
			}
			Expect(s.Capability).ToNot(BeEmpty(), s.Tool)
		}
	})

	It("names a feature that exists, and the API route checks that same feature", func() {
		for _, s := range ClientTools {
			Expect(auth.AllFeatures).To(ContainElement(s.Feature), "%s: unknown feature %q", s.Tool, s.Feature)
			found := false
			for _, r := range auth.RouteFeatureRegistry {
				if r.Method == s.Method && r.Pattern == s.Route {
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

	It("registers every client tool, even in the read-only flavour", func() {
		for _, opts := range []Options{{}, {DisableMutating: true}} {
			ctx, sess, done := connectInMemory(&fakeClient{}, opts)
			names := listToolNames(ctx, sess)
			done()
			for _, n := range clientToolNames() {
				Expect(slices.Contains(names, n)).To(BeTrue(), "%s missing with %+v", n, opts)
			}
		}
	})
})
