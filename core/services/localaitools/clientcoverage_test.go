package localaitools

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
)

// clientCoveredFeatures are the features whose routes run inference. Every
// route of these features, in auth.RouteFeatureRegistry, must be wrapped by a
// client tool (ClientTools) or listed in clientToolExemptions with a reason.
//
// A new inference endpoint is therefore not finished until the LocalAI client
// can use it, or someone writes down why it cannot. See
// .agents/localai-assistant-mcp.md, "Keeping the client in step with the API".
var clientCoveredFeatures = []string{
	auth.FeatureChat,
	auth.FeatureImages,
	auth.FeatureAudioSpeech,
	auth.FeatureAudioTranscription,
	auth.FeatureAudioDiarization,
	auth.FeatureAudioClassification,
	auth.FeatureVAD,
	auth.FeatureDetection,
	auth.FeatureForecast,
	auth.FeatureDecisions,
	auth.FeatureFaceRecognition,
	auth.FeatureVoiceRecognition,
	auth.FeatureAudioTransform,
	auth.FeatureVideo,
	auth.Feature3D,
	auth.FeatureSound,
	auth.FeatureEmbeddings,
	auth.FeatureRerank,
	auth.FeatureModeration,
	auth.FeatureTokenize,
	auth.FeatureStores,
}

// clientToolExemptions lists the inference routes that deliberately have no
// client tool, as "METHOD /pattern" -> reason. "not built yet" marks the
// backlog: a tool is wanted but does not exist. Any other reason is a
// decision. A route listed here must not be wrapped by a tool too.
var clientToolExemptions = map[string]string{
	"POST /api/chats/files/extract": "a person's action on the Files page for large archives; the model unpacks small ones in its sandbox, and a tool would cost tokens on every turn",
	"POST /chat/completions":              "chat is the chat itself: the agent already runs on it, a tool would only recurse",
	"POST /v1/completions":                "chat is the chat itself: the agent already runs on it, a tool would only recurse",
	"POST /completions":                   "chat is the chat itself: the agent already runs on it, a tool would only recurse",
	"POST /v1/engines/:model/completions": "chat is the chat itself: the agent already runs on it, a tool would only recurse",
	"POST /v1/edits":                      "chat is the chat itself: the agent already runs on it, a tool would only recurse",
	"POST /edits":                         "chat is the chat itself: the agent already runs on it, a tool would only recurse",
	"POST /v1/messages":                   "chat is the chat itself: the agent already runs on it, a tool would only recurse",
	"POST /messages":                      "chat is the chat itself: the agent already runs on it, a tool would only recurse",
	"POST /v1/responses":                  "chat is the chat itself: the agent already runs on it, a tool would only recurse",
	"POST /responses":                     "chat is the chat itself: the agent already runs on it, a tool would only recurse",
	"GET /v1/responses":                   "chat is the chat itself: the agent already runs on it, a tool would only recurse",
	"GET /responses":                      "chat is the chat itself: the agent already runs on it, a tool would only recurse",
	"POST /embeddings":                    "no-prefix alias of the covered route POST /v1/embeddings: same handler",
	"POST /v1/engines/:model/embeddings":  "legacy alias of the covered route POST /v1/embeddings: same handler",
	"POST /images/generations":            "no-prefix alias of the covered route POST /v1/images/generations: same handler",
	"POST /images/inpainting":             "no-prefix alias of the covered route POST /v1/images/inpainting: same handler",
	"POST /v1/images/upscale":             "not built yet: image upscaling has no tool",
	"POST /images/upscale":                "not built yet: image upscaling has no tool (alias of POST /v1/images/upscale)",
	"POST /audio/transcriptions":          "no-prefix alias of the covered route POST /v1/audio/transcriptions: same handler",
	"POST /v1/audio/diarization":          "not built yet: speaker diarization has no tool",
	"POST /audio/diarization":             "not built yet: speaker diarization has no tool",
	"POST /v1/audio/classification":       "not built yet: audio classification has no tool",
	"POST /audio/classification":          "not built yet: audio classification has no tool",
	"POST /audio/speech":                  "no-prefix alias of the covered route POST /v1/audio/speech: same handler",
	"POST /tts":                           "legacy alias of the covered route POST /v1/audio/speech: same handler",
	"GET /v1/audio/voices":                "not built yet: listing the voices of a TTS model has no tool; speak takes the voice name",
	"GET /audio/voices":                   "not built yet: alias of GET /v1/audio/voices",
	"POST /v1/text-to-speech/:voice-id":   "ElevenLabs-compatible alias of POST /v1/audio/speech, kept for third-party clients",
	"GET /api/voice-profiles/:id/audio":   "streams the stored sample as binary audio: an agent cannot use it, the voice_profile tool lists and creates profiles instead",
	"POST /vad":                           "not built yet: voice activity detection has no tool",
	"POST /v1/vad":                        "not built yet: voice activity detection has no tool",
	"POST /v1/forecast":                   "not built yet: time series forecasting has no tool",
	"POST /v1/decisions":                  "not built yet: the decisions family has no tool",
	"POST /v1/systemone":                  "not built yet: the decisions family has no tool",
	"POST /v1/systemone/permute":          "not built yet: the decisions family has no tool",
	"POST /v1/systemone/separate":         "not built yet: the decisions family has no tool",
	"POST /v1/face/analyze":               "returns raw face attributes or embeddings: not built yet, verify and identify cover the agent use cases",
	"POST /v1/voice/analyze":              "returns raw voice attributes or embeddings: not built yet, verify and identify cover the agent use cases",
	"POST /v1/face/embed":                 "returns raw face attributes or embeddings: not built yet, verify and identify cover the agent use cases",
	"POST /v1/voice/embed":                "returns raw voice attributes or embeddings: not built yet, verify and identify cover the agent use cases",
	"POST /v1/face/register":              "writes or returns raw biometric data in the face registry: an agent should not do it; the face tool only verifies and identifies",
	"POST /v1/voice/register":             "writes or returns raw biometric data in the voice registry: an agent should not do it; the voice tool only verifies and identifies",
	"POST /v1/face/forget":                "writes or returns raw biometric data in the face registry: an agent should not do it; the face tool only verifies and identifies",
	"POST /v1/voice/forget":               "writes or returns raw biometric data in the voice registry: an agent should not do it; the voice tool only verifies and identifies",
	"POST /audio/transformations":         "not built yet: audio transformation has no tool",
	"POST /audio/transform":               "not built yet: audio transformation has no tool",
	"GET /audio/transformations/stream":   "not built yet: audio transformation has no tool (and it is a stream)",
	"POST /3d/generations":                "not built yet: 3D generation has no tool",
	"POST /3d/remesh":                     "not built yet: 3D generation has no tool",
	"POST /3d/animate":                    "not built yet: 3D generation has no tool",
	"POST /v1/tokenize":                   "not useful as an agent tool yet: a model counts its own tokens",
	"POST /v1/detokenize":                 "not useful as an agent tool yet: a model counts its own tokens",
	"POST /v1/moderations":                "not useful as an agent tool yet: moderation is applied by the server, not requested by an agent",
	"POST /moderations":                   "no-prefix alias of the covered route POST /v1/moderations: same handler (not a tool yet)",
	"POST /stores/set":                    "not useful as an agent tool yet: raw vector store access, agents use collections",
	"POST /stores/delete":                 "not useful as an agent tool yet: raw vector store access, agents use collections",
	"POST /stores/get":                    "not useful as an agent tool yet: raw vector store access, agents use collections",
	"POST /stores/find":                   "not useful as an agent tool yet: raw vector store access, agents use collections",
}

// clientCoverageProblems compares the routes of the covered features with the
// client tools and the exemptions, and returns one message per problem. It
// takes its inputs as arguments so a test can feed it fake registries.
func clientCoverageProblems(
	registry []auth.RouteFeature,
	tools []ClientToolSpec,
	exemptions map[string]string,
	features []string,
) []string {
	var problems []string
	inRegistry := map[string]bool{}
	for _, r := range registry {
		key := r.Method + " " + r.Pattern
		inRegistry[key] = true
		if !slices.Contains(features, r.Feature) {
			continue
		}
		_, exempt := exemptions[key]
		wrapped := false
		for _, t := range tools {
			if t.Method == r.Method && t.Route == r.Pattern {
				wrapped = true
			}
		}
		if !wrapped && !exempt {
			problems = append(problems, fmt.Sprintf(
				"%s (feature %q) has no client tool and no exemption. Either add a client tool for it "+
					"(a row in ClientTools in clienttools.go, plus the LocalAIClient method, both implementations, "+
					"the tool schema and the localai skill), or add %q to clientToolExemptions in "+
					"clientcoverage_test.go with the reason it is not a tool.", key, r.Feature, key))
		}
	}
	for _, t := range tools {
		key := t.Method + " " + t.Route
		if !inRegistry[key] {
			problems = append(problems, fmt.Sprintf(
				"client tool %q wraps %s, which is not in auth.RouteFeatureRegistry. The route was removed or renamed: update the tool row.", t.Tool, key))
		}
		if why, ok := exemptions[key]; ok {
			problems = append(problems, fmt.Sprintf(
				"%s is wrapped by client tool %q and also exempt (%q). Keep one: remove the exemption.", key, t.Tool, why))
		}
	}
	for key, why := range exemptions {
		if !inRegistry[key] {
			problems = append(problems, fmt.Sprintf(
				"exemption %q is stale: the route is not in auth.RouteFeatureRegistry. Remove it from clientToolExemptions.", key))
		}
		if strings.TrimSpace(why) == "" {
			problems = append(problems, fmt.Sprintf("exemption %q has no reason. Write why the route is not a client tool.", key))
		}
	}
	sort.Strings(problems)
	return problems
}

var _ = Describe("Client tool coverage of the inference routes", func() {
	It("wraps or exempts every inference route in the API route table", func() {
		Expect(clientCoverageProblems(auth.RouteFeatureRegistry, ClientTools, clientToolExemptions, clientCoveredFeatures)).
			To(BeEmpty())
	})

	Describe("the checker", func() {
		registry := []auth.RouteFeature{
			{Method: "POST", Pattern: "/v1/images/generations", Feature: auth.FeatureImages},
			{Method: "POST", Pattern: "/v1/images/new", Feature: auth.FeatureImages},
			{Method: "POST", Pattern: "/api/settings", Feature: auth.FeatureAddons},
		}
		tools := []ClientToolSpec{{Tool: "t", Method: "POST", Route: "/v1/images/generations"}}
		features := []string{auth.FeatureImages}

		It("passes when every route is wrapped or exempt", func() {
			ex := map[string]string{"POST /v1/images/new": "not built yet"}
			Expect(clientCoverageProblems(registry, tools, ex, features)).To(BeEmpty())
		})
		It("names a route with neither a tool nor an exemption, and the fix", func() {
			p := clientCoverageProblems(registry, tools, nil, features)
			Expect(p).To(HaveLen(1))
			Expect(p[0]).To(ContainSubstring("POST /v1/images/new"))
			Expect(p[0]).To(ContainSubstring("add a client tool"))
			Expect(p[0]).To(ContainSubstring("clientToolExemptions"))
		})
		It("ignores routes of other features", func() {
			Expect(clientCoverageProblems(registry, tools, map[string]string{"POST /v1/images/new": "x"}, features)).To(BeEmpty())
		})
		It("flags a stale exemption", func() {
			ex := map[string]string{"POST /v1/images/new": "x", "POST /gone": "x"}
			p := clientCoverageProblems(registry, tools, ex, features)
			Expect(p).To(HaveLen(1))
			Expect(p[0]).To(ContainSubstring("exemption \"POST /gone\" is stale"))
		})
		It("flags a tool route that left the registry", func() {
			gone := append([]ClientToolSpec{}, tools...)
			gone = append(gone, ClientToolSpec{Tool: "old", Method: "POST", Route: "/v1/removed"})
			ex := map[string]string{"POST /v1/images/new": "x"}
			p := clientCoverageProblems(registry, gone, ex, features)
			Expect(p).To(HaveLen(1))
			Expect(p[0]).To(ContainSubstring("not in auth.RouteFeatureRegistry"))
		})
		It("flags a route that is both wrapped and exempt", func() {
			ex := map[string]string{"POST /v1/images/new": "x", "POST /v1/images/generations": "x"}
			p := clientCoverageProblems(registry, tools, ex, features)
			Expect(p).To(HaveLen(1))
			Expect(p[0]).To(ContainSubstring("also exempt"))
		})
		It("flags an exemption without a reason", func() {
			p := clientCoverageProblems(registry, tools, map[string]string{"POST /v1/images/new": " "}, features)
			Expect(p).To(HaveLen(1))
			Expect(p[0]).To(ContainSubstring("has no reason"))
		})
	})
})
