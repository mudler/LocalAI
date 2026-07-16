package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/router"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func classifierTestConfig(threshold float64, fallback *types.ClassifierFallback) *types.ClassifierConfig {
	return &types.ClassifierConfig{
		Threshold: threshold,
		Fallback:  fallback,
		Options: []types.ClassifierOption{
			{
				ID:          "up",
				Description: "the user asks the drone to fly up",
				Reply:       "Going up.",
				Tool:        &types.ClassifierTool{Name: "move", Arguments: json.RawMessage(`{"direction":"up"}`)},
			},
			{ID: "greeting", Description: "the user greets the assistant", Reply: "Hello."},
		},
	}
}

func classifierTestSession(m *fakeModel) *Session {
	return &Session{
		ModelInterface:   m,
		OutputModalities: []types.Modality{types.ModalityText},
		ModelConfig:      &config.ModelConfig{},
	}
}

var classifierTestHistory = schema.Messages{
	{Role: "system", StringContent: "instructions", Content: "instructions"},
	{Role: "user", StringContent: "please go up", Content: "please go up"},
}

func classifierResultEvents(t *fakeTransport) []types.ClassifierResultEvent {
	var out []types.ClassifierResultEvent
	for _, e := range t.events {
		if ev, ok := e.(types.ClassifierResultEvent); ok {
			out = append(out, ev)
		}
	}
	return out
}

// replyTexts collects the assistant reply text of every completed output
// item — what a classifier response actually "spoke".
func replyTexts(t *fakeTransport) []string {
	var out []string
	for _, e := range t.events {
		if ev, ok := e.(types.ResponseOutputTextDoneEvent); ok {
			out = append(out, ev.Text)
		}
	}
	return out
}

var _ = Describe("classifierConfigFromPipeline", func() {
	It("returns nil for an absent block", func() {
		cc, err := classifierConfigFromPipeline(nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(cc).To(BeNil())
	})

	It("converts options and tool argument maps to wire form", func() {
		cc, err := classifierConfigFromPipeline(&config.PipelineClassifier{
			Enabled:   true,
			Threshold: 0.4,
			Fallback:  &config.PipelineClassifierFallback{Mode: "reply", Reply: "Say again?"},
			Options: []config.PipelineClassifierOption{
				{
					ID:          "up",
					Description: "fly up",
					Reply:       "Going up.",
					Tool:        &config.PipelineClassifierTool{Name: "move", Arguments: map[string]any{"direction": "up"}},
				},
			},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(cc.Active()).To(BeTrue())
		Expect(cc.Threshold).To(Equal(0.4))
		Expect(cc.Options).To(HaveLen(1))
		Expect(string(cc.Options[0].Tool.Arguments)).To(MatchJSON(`{"direction":"up"}`))
		Expect(cc.Fallback.Mode).To(Equal(types.ClassifierFallbackReply))
	})

	It("rejects invalid blocks via the shared validation", func() {
		_, err := classifierConfigFromPipeline(&config.PipelineClassifier{
			Enabled: true,
			Options: []config.PipelineClassifierOption{
				{ID: "a", Description: "one"},
				{ID: "a", Description: "two"},
			},
		})
		Expect(err).To(MatchError(ContainSubstring("duplicate option id")))
	})
})

var _ = Describe("validateClassifierActivation", func() {
	It("accepts a combined inference and score model", func() {
		usecases := config.FLAG_CHAT | config.FLAG_SCORE
		m := &wrappedModel{LLMConfig: &config.ModelConfig{KnownUsecases: &usecases}}
		Expect(validateClassifierActivation(m, classifierTestConfig(0.4, nil))).To(Succeed())
	})

	It("rejects an active classifier when the model does not declare score", func() {
		usecases := config.FLAG_CHAT
		m := &wrappedModel{LLMConfig: &config.ModelConfig{KnownUsecases: &usecases}}
		Expect(validateClassifierActivation(m, classifierTestConfig(0.4, nil))).To(MatchError(ContainSubstring("known_usecases")))
	})

	It("rejects a router config as the concrete scoring model", func() {
		usecases := config.FLAG_SCORE
		m := &wrappedModel{LLMConfig: &config.ModelConfig{
			KnownUsecases: &usecases,
			Router:        config.RouterConfig{Candidates: []config.RouterCandidate{{Model: "target"}}},
		}}
		Expect(validateClassifierActivation(m, classifierTestConfig(0.4, nil))).To(MatchError(ContainSubstring("concrete")))
	})

	It("allows disabling classification without score support", func() {
		disabled := false
		m := &wrappedModel{LLMConfig: &config.ModelConfig{}}
		Expect(validateClassifierActivation(m, &types.ClassifierConfig{Enabled: &disabled})).To(Succeed())
	})
})

var _ = Describe("resolveClassifier", func() {
	It("uses the session config when no override is present", func() {
		sess := classifierTestConfig(0, nil)
		Expect(resolveClassifier(sess, nil)).To(BeIdenticalTo(sess))
		Expect(resolveClassifier(sess, &types.ResponseCreateParams{})).To(BeIdenticalTo(sess))
	})

	It("replaces the whole config when the response overrides it", func() {
		sess := classifierTestConfig(0, nil)
		disabled := false
		over := &types.ClassifierConfig{Enabled: &disabled}
		got := resolveClassifier(sess, &types.ResponseCreateParams{LocalAIClassifier: over})
		Expect(got).To(BeIdenticalTo(over))
		Expect(got.Active()).To(BeFalse())
	})
})

var _ = Describe("trimClassifierHistory", func() {
	history := schema.Messages{
		{Role: "system", StringContent: "sys"},
		{Role: "user", StringContent: "one"},
		{Role: "assistant", StringContent: "two"},
		{Role: "user", StringContent: "three"},
		{Role: "assistant", StringContent: "four"},
		{Role: "user", StringContent: "five"},
	}

	It("keeps only the latest user message by default", func() {
		// Earlier turns echo option names (canned replies) and empirically
		// dominate small scoring models, so the default is user-turn-only.
		got := trimClassifierHistory(history, 0)
		Expect(got).To(HaveLen(1))
		Expect(got[0].StringContent).To(Equal("five"))
	})

	It("keeps only the latest user message for -1", func() {
		got := trimClassifierHistory(history, -1)
		Expect(got).To(HaveLen(1))
		Expect(got[0].StringContent).To(Equal("five"))
	})

	It("honors an explicit cap", func() {
		got := trimClassifierHistory(history, 2)
		Expect(got).To(HaveLen(2))
		Expect(got[0].StringContent).To(Equal("four"))
	})
})

var _ = Describe("mentionsAnyName", func() {
	It("matches case-insensitive whole words in any position", func() {
		Expect(mentionsAnyName("Drone, go up", []string{"drone"})).To(BeTrue())
		Expect(mentionsAnyName("go up drone", []string{"drone"})).To(BeTrue())
		Expect(mentionsAnyName("go up", []string{"drone"})).To(BeFalse())
		// Whole-word: no substring matches.
		Expect(mentionsAnyName("I like drones", []string{"drone"})).To(BeFalse())
		// Multiple aliases and multi-word names.
		Expect(mentionsAnyName("hey quadcopter rise", []string{"drone", "quadcopter"})).To(BeTrue())
		Expect(mentionsAnyName("okay drone go", []string{"okay drone"})).To(BeTrue())
	})
})

var _ = Describe("classifierProbe", func() {
	It("renders a single user message verbatim", func() {
		probe := classifierProbe(schema.Messages{{Role: "user", Content: "fly forward"}})
		Expect(probe.Prompt).To(Equal("fly forward\n"))
		Expect(probe.Messages).To(Equal([]string{"fly forward"}))
	})

	It("role-labels multi-message histories and skips text-less items", func() {
		probe := classifierProbe(schema.Messages{
			{Role: "user", Content: "go up"},
			{Role: "assistant", Content: "Going up."},
			{Role: "assistant"}, // tool-call item: no text
			{Role: "tool", Content: "ok: moved"},
			{Role: "user", Content: "fly forward"},
		})
		Expect(probe.Messages).To(Equal([]string{
			"User: go up",
			"Assistant: Going up.",
			"Tool: ok: moved",
			"User: fly forward",
		}))
	})
})

var _ = Describe("classifierRespond", func() {
	It("emits the winning option's canned reply and tool call", func() {
		m := &fakeModel{classifyScores: []router.LabelScore{
			{Label: "up", Score: 0.9},
			{Label: "greeting", Score: 0.1},
		}}
		session := classifierTestSession(m)
		conv := &Conversation{}
		t := &fakeTransport{}
		r := &liveResponse{id: "resp1"}

		handled := classifierRespond(context.Background(), session, conv, t, r, classifierTestConfig(0.35, nil), classifierTestHistory, nil, 0)

		Expect(handled).To(BeTrue())
		Expect(m.classifyCalls).To(Equal(1))
		// System instructions stay out of the scoring prompt.
		for _, msg := range m.lastMessages {
			Expect(msg.Role).ToNot(Equal("system"))
		}

		results := classifierResultEvents(t)
		Expect(results).To(HaveLen(1))
		Expect(results[0].ChosenID).To(Equal("up"))
		Expect(results[0].Fallback).To(BeEmpty())
		Expect(results[0].Scores).To(HaveLen(2))
		Expect(results[0].Scores[0].Score).To(BeNumerically("~", 0.9))

		// Canned reply as text (text-only modality), canned tool call after it.
		Expect(t.countEvents(types.ServerEventTypeResponseOutputTextDone)).To(Equal(1))
		Expect(t.countEvents(types.ServerEventTypeResponseFunctionCallArgumentsDone)).To(Equal(1))
		var fcArgs string
		for _, e := range t.events {
			if done, ok := e.(types.ResponseFunctionCallArgumentsDoneEvent); ok {
				fcArgs = done.Arguments
			}
		}
		Expect(fcArgs).To(MatchJSON(`{"direction":"up"}`))
		// Assistant reply + function_call item recorded in the conversation.
		Expect(conv.Items).To(HaveLen(2))
		Expect(conv.Items[0].Assistant).ToNot(BeNil())
		Expect(conv.Items[1].FunctionCall).ToNot(BeNil())
		Expect(conv.Items[1].FunctionCall.Name).To(Equal("move"))
	})

	It("drops unaddressed turns without scoring when the address gate is on", func() {
		m := &fakeModel{classifyScores: []router.LabelScore{{Label: "up", Score: 0.99}}}
		session := classifierTestSession(m)
		conv := &Conversation{}
		t := &fakeTransport{}
		r := &liveResponse{id: "resp-unaddressed"}
		cc := classifierTestConfig(0.35, nil)
		cc.Address = &types.ClassifierAddress{Names: []string{"drone"}}
		history := schema.Messages{
			{Role: "user", StringContent: "go up", Content: "go up"},
		}

		handled := classifierRespond(context.Background(), session, conv, t, r, cc, history, nil, 0)

		Expect(handled).To(BeTrue())
		Expect(m.classifyCalls).To(BeZero(), "unaddressed turns must not be scored")
		results := classifierResultEvents(t)
		Expect(results).To(HaveLen(1))
		Expect(results[0].Scores).To(BeEmpty())
		Expect(results[0].Fallback).To(Equal(types.ClassifierNotAddressed))
		Expect(t.countEvents(types.ServerEventTypeResponseOutputTextDone)).To(BeZero(), "ignore mode must stay silent")
	})

	It("scores turns that address the assistant by name", func() {
		m := &fakeModel{classifyScores: []router.LabelScore{
			{Label: "up", Score: 0.9},
			{Label: "greeting", Score: 0.1},
		}}
		session := classifierTestSession(m)
		conv := &Conversation{}
		t := &fakeTransport{}
		r := &liveResponse{id: "resp-addressed"}
		cc := classifierTestConfig(0.35, nil)
		cc.Address = &types.ClassifierAddress{Names: []string{"drone"}}
		history := schema.Messages{
			{Role: "user", StringContent: "Drone, go up", Content: "Drone, go up"},
		}

		handled := classifierRespond(context.Background(), session, conv, t, r, cc, history, nil, 0)

		Expect(handled).To(BeTrue())
		Expect(m.classifyCalls).To(Equal(1))
		results := classifierResultEvents(t)
		Expect(results).To(HaveLen(1))
		Expect(results[0].ChosenID).To(Equal("up"))
	})

	It("speaks the address reply for unaddressed turns in reply mode", func() {
		m := &fakeModel{}
		session := classifierTestSession(m)
		conv := &Conversation{}
		t := &fakeTransport{}
		r := &liveResponse{id: "resp-unaddressed-reply"}
		cc := classifierTestConfig(0.35, nil)
		cc.Address = &types.ClassifierAddress{Names: []string{"drone"}, Mode: types.ClassifierAddressReply, Reply: "Call me Drone."}
		history := schema.Messages{
			{Role: "user", StringContent: "go up", Content: "go up"},
		}

		handled := classifierRespond(context.Background(), session, conv, t, r, cc, history, nil, 0)

		Expect(handled).To(BeTrue())
		Expect(m.classifyCalls).To(BeZero())
		Expect(t.countEvents(types.ServerEventTypeResponseOutputTextDone)).To(Equal(1))
	})

	It("applies the fallback without scoring when the turn has no words", func() {
		// A VAD-committed turn whose transcript is empty must not be
		// scored: an empty prompt yields a confidently arbitrary winner.
		m := &fakeModel{classifyScores: []router.LabelScore{{Label: "up", Score: 0.99}}}
		session := classifierTestSession(m)
		conv := &Conversation{}
		t := &fakeTransport{}
		r := &liveResponse{id: "resp-empty"}
		history := schema.Messages{
			{Role: "system", StringContent: "instructions", Content: "instructions"},
			{Role: "user", StringContent: "", Content: ""},
		}
		cc := classifierTestConfig(0.35, &types.ClassifierFallback{Mode: types.ClassifierFallbackReply, Reply: "Say again?"})

		handled := classifierRespond(context.Background(), session, conv, t, r, cc, history, nil, 0)

		Expect(handled).To(BeTrue())
		Expect(m.classifyCalls).To(BeZero(), "an empty turn must not be scored")
		results := classifierResultEvents(t)
		Expect(results).To(HaveLen(1))
		Expect(results[0].Scores).To(BeEmpty())
		Expect(results[0].ChosenID).To(BeEmpty())
		Expect(results[0].Fallback).To(Equal(types.ClassifierFallbackReply))
		Expect(t.countEvents(types.ServerEventTypeResponseOutputTextDone)).To(Equal(1))
		Expect(t.countEvents(types.ServerEventTypeResponseFunctionCallArgumentsDone)).To(BeZero())
	})

	It("falls through to generation for a word-less turn when the fallback is generate", func() {
		m := &fakeModel{classifyScores: []router.LabelScore{{Label: "up", Score: 0.99}}}
		session := classifierTestSession(m)
		conv := &Conversation{}
		t := &fakeTransport{}
		r := &liveResponse{id: "resp-empty-gen"}
		history := schema.Messages{
			{Role: "user", StringContent: "", Content: ""},
		}
		cc := classifierTestConfig(0.35, &types.ClassifierFallback{Mode: types.ClassifierFallbackGenerate})

		handled := classifierRespond(context.Background(), session, conv, t, r, cc, history, nil, 0)

		Expect(handled).To(BeFalse())
		Expect(m.classifyCalls).To(BeZero())
		Expect(classifierResultEvents(t)).To(BeEmpty())
	})

	It("speaks the fallback reply when no option clears the threshold", func() {
		m := &fakeModel{classifyScores: []router.LabelScore{
			{Label: "up", Score: 0.3},
			{Label: "greeting", Score: 0.3},
		}}
		session := classifierTestSession(m)
		conv := &Conversation{}
		t := &fakeTransport{}
		r := &liveResponse{id: "resp1"}
		cc := classifierTestConfig(0.6, &types.ClassifierFallback{Mode: types.ClassifierFallbackReply, Reply: "Say again?"})

		handled := classifierRespond(context.Background(), session, conv, t, r, cc, classifierTestHistory, nil, 0)

		Expect(handled).To(BeTrue())
		results := classifierResultEvents(t)
		Expect(results).To(HaveLen(1))
		Expect(results[0].ChosenID).To(BeEmpty())
		Expect(results[0].Fallback).To(Equal(types.ClassifierFallbackReply))
		Expect(t.countEvents(types.ServerEventTypeResponseOutputTextDone)).To(Equal(1))
		Expect(t.countEvents(types.ServerEventTypeResponseFunctionCallArgumentsDone)).To(BeZero())
	})

	It("completes with no output for the none fallback", func() {
		m := &fakeModel{classifyScores: []router.LabelScore{
			{Label: "up", Score: 0.3},
			{Label: "greeting", Score: 0.3},
		}}
		session := classifierTestSession(m)
		conv := &Conversation{}
		t := &fakeTransport{}
		r := &liveResponse{id: "resp1"}

		handled := classifierRespond(context.Background(), session, conv, t, r, classifierTestConfig(0.6, nil), classifierTestHistory, nil, 0)

		Expect(handled).To(BeTrue())
		Expect(r.outcome).ToNot(Equal(outcomeFailed))
		Expect(conv.Items).To(BeEmpty())
		Expect(t.countEvents(types.ServerEventTypeResponseOutputTextDone)).To(BeZero())
		results := classifierResultEvents(t)
		Expect(results).To(HaveLen(1))
		Expect(results[0].Fallback).To(Equal(types.ClassifierFallbackNone))
	})

	It("falls through to generation for the generate fallback", func() {
		m := &fakeModel{classifyScores: []router.LabelScore{
			{Label: "up", Score: 0.3},
			{Label: "greeting", Score: 0.3},
		}}
		session := classifierTestSession(m)
		conv := &Conversation{}
		t := &fakeTransport{}
		r := &liveResponse{id: "resp1"}
		cc := classifierTestConfig(0.6, &types.ClassifierFallback{Mode: types.ClassifierFallbackGenerate})

		handled := classifierRespond(context.Background(), session, conv, t, r, cc, classifierTestHistory, nil, 0)

		Expect(handled).To(BeFalse())
		// The distribution is still reported before falling through.
		Expect(classifierResultEvents(t)).To(HaveLen(1))
	})

	It("fails the response when scoring errors without a generate fallback", func() {
		m := &fakeModel{classifyErr: fmt.Errorf("backend exploded")}
		session := classifierTestSession(m)
		conv := &Conversation{}
		t := &fakeTransport{}
		r := &liveResponse{id: "resp1"}

		handled := classifierRespond(context.Background(), session, conv, t, r, classifierTestConfig(0.35, nil), classifierTestHistory, nil, 0)

		Expect(handled).To(BeTrue())
		Expect(r.outcome).To(Equal(outcomeFailed))
		Expect(t.countEvents(types.ServerEventTypeError)).To(Equal(1))
	})

	It("falls through to generation when scoring errors and fallback is generate", func() {
		m := &fakeModel{classifyErr: fmt.Errorf("backend exploded")}
		session := classifierTestSession(m)
		conv := &Conversation{}
		t := &fakeTransport{}
		r := &liveResponse{id: "resp1"}
		cc := classifierTestConfig(0.35, &types.ClassifierFallback{Mode: types.ClassifierFallbackGenerate})

		handled := classifierRespond(context.Background(), session, conv, t, r, cc, classifierTestHistory, nil, 0)

		Expect(handled).To(BeFalse())
		Expect(r.outcome).ToNot(Equal(outcomeFailed))
	})

	It("records a cancelled outcome when barge-in fires during scoring", func() {
		m := &fakeModel{classifyScores: []router.LabelScore{
			{Label: "up", Score: 0.9},
			{Label: "greeting", Score: 0.1},
		}}
		session := classifierTestSession(m)
		conv := &Conversation{}
		t := &fakeTransport{}
		r := &liveResponse{id: "resp1"}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		handled := classifierRespond(ctx, session, conv, t, r, classifierTestConfig(0.35, nil), classifierTestHistory, nil, 0)

		Expect(handled).To(BeTrue())
		Expect(r.outcome).To(Equal(outcomeCancelled))
		Expect(conv.Items).To(BeEmpty())
	})

	It("skips to generation when there is nothing scorable", func() {
		m := &fakeModel{}
		session := classifierTestSession(m)
		conv := &Conversation{}
		t := &fakeTransport{}
		r := &liveResponse{id: "resp1"}
		systemOnly := schema.Messages{{Role: "system", StringContent: "instructions"}}

		handled := classifierRespond(context.Background(), session, conv, t, r, classifierTestConfig(0.35, nil), systemOnly, nil, 0)

		Expect(handled).To(BeFalse())
		Expect(m.classifyCalls).To(BeZero())
	})
})

// slottedTestConfig is classifierTestConfig with the winning option's tool
// carrying argument slots (the hybrid classify-then-complete path).
func slottedTestConfig(threshold float64, fallback *types.ClassifierFallback, defaults bool) *types.ClassifierConfig {
	slots := []types.ClassifierSlot{
		{Name: "distance", Type: types.ClassifierSlotNumber},
		{Name: "units", Type: types.ClassifierSlotEnum, Values: []string{"m", "meters", "ft", "feet"}, Hint: "assume m when the user gives no units"},
	}
	if defaults {
		slots[0].Default = "1"
		slots[1].Default = "m"
	}
	return &types.ClassifierConfig{
		Threshold: threshold,
		Fallback:  fallback,
		Options: []types.ClassifierOption{
			{
				ID:          "up",
				Description: "the user asks the drone to fly up",
				Reply:       "Going up {{distance}} {{units}}.",
				Tool: &types.ClassifierTool{
					Name:      "move",
					Arguments: json.RawMessage(`{"direction":"up","distance":"{{distance}}","units":"{{units}}"}`),
					Slots:     slots,
				},
			},
			{ID: "greeting", Description: "the user greets the assistant", Reply: "Hello."},
		},
	}
}

var _ = Describe("slotFillGrammar", func() {
	It("pins the field skeleton and frees only the slot values", func() {
		g := slotFillGrammar([]types.ClassifierSlot{
			{Name: "distance", Type: types.ClassifierSlotNumber},
			{Name: "units", Type: types.ClassifierSlotEnum, Values: []string{"m", "ft"}},
		})
		Expect(g).To(ContainSubstring(`root ::= slot0 ", \"units\": " slot1 "}"`))
		Expect(g).To(ContainSubstring("slot0 ::= num"))
		Expect(g).To(ContainSubstring(`slot1 ::= "\"m\"" | "\"ft\""`))
		Expect(g).To(ContainSubstring("num ::="))
	})

	It("JSON-encodes enum values before embedding them in the grammar", func() {
		g := slotFillGrammar([]types.ClassifierSlot{
			{Name: "units", Type: types.ClassifierSlotEnum, Values: []string{"quoted\"value", "line\nbreak", `back\slash`}},
		})
		Expect(g).To(ContainSubstring(gbnfLiteral(`"quoted\"value"`)))
		Expect(g).To(ContainSubstring(gbnfLiteral(`"line\nbreak"`)))
		Expect(g).To(ContainSubstring(gbnfLiteral(`"back\\slash"`)))
	})

	It("budgets forced enum and field text by encoded length", func() {
		short := []types.ClassifierSlot{{Name: "value", Type: types.ClassifierSlotEnum, Values: []string{"m"}}}
		long := []types.ClassifierSlot{
			{Name: "value", Type: types.ClassifierSlotEnum, Values: []string{strings.Repeat("long-value-", 20)}},
			{Name: strings.Repeat("field", 20), Type: types.ClassifierSlotNumber},
		}
		Expect(slotFillMaxTokens(long)).To(BeNumerically(">", slotFillMaxTokens(short)+200))
	})

	It("emits a string rule only when needed", func() {
		g := slotFillGrammar([]types.ClassifierSlot{{Name: "what", Type: types.ClassifierSlotString}})
		Expect(g).To(ContainSubstring("slot0 ::= str"))
		Expect(g).To(ContainSubstring("str ::="))
		Expect(g).ToNot(ContainSubstring("num ::="))
	})
})

var _ = Describe("parseSlotValues", func() {
	slots := []types.ClassifierSlot{
		{Name: "distance", Type: types.ClassifierSlotNumber},
		{Name: "units", Type: types.ClassifierSlotEnum, Values: []string{"m", "ft"}},
	}

	It("extracts values from a grammar-shaped completion", func() {
		values, err := parseSlotValues("up", "distance", `3.5, "units": "m"}`, slots)
		Expect(err).ToNot(HaveOccurred())
		Expect(values).To(Equal(map[string]string{"distance": "3.5", "units": "m"}))
	})

	It("tolerates a completion missing the closing brace", func() {
		values, err := parseSlotValues("up", "distance", `2, "units": "ft"`, slots)
		Expect(err).ToNot(HaveOccurred())
		Expect(values["distance"]).To(Equal("2"))
	})

	It("rejects completions missing a slot", func() {
		_, err := parseSlotValues("up", "distance", `3}`, slots)
		Expect(err).To(MatchError(ContainSubstring(`missing "units"`)))
	})
})

var _ = Describe("classifierPolicyDescription", func() {
	It("passes plain options through", func() {
		o := &types.ClassifierOption{Description: "plain"}
		Expect(classifierPolicyDescription(o)).To(Equal("plain"))
	})

	It("appends slot declarations and hints", func() {
		cc := slottedTestConfig(0, nil, false)
		d := classifierPolicyDescription(&cc.Options[0])
		Expect(d).To(ContainSubstring("route parameters:"))
		Expect(d).To(ContainSubstring("distance (number)"))
		Expect(d).To(ContainSubstring("units (one of: m, meters, ft, feet)"))
		Expect(d).To(ContainSubstring("assume m when the user gives no units"))
	})
})

var _ = Describe("classifierRespond slot filling", func() {
	It("emits the filled tool arguments and reports them in the result event", func() {
		m := &fakeModel{
			classifyScores: []router.LabelScore{{Label: "up", Score: 0.9}, {Label: "greeting", Score: 0.1}},
			fillArgs:       `{"direction":"up","distance":3,"units":"meters"}`,
			fillValues:     map[string]string{"distance": "3", "units": "meters"},
		}
		session := classifierTestSession(m)
		conv := &Conversation{}
		t := &fakeTransport{}
		r := &liveResponse{id: "resp-slots"}

		handled := classifierRespond(context.Background(), session, conv, t, r, slottedTestConfig(0.35, nil, false), classifierTestHistory, nil, 0)

		Expect(handled).To(BeTrue())
		Expect(m.fillCalls).To(Equal(1))
		Expect(m.lastFillChosen.ID).To(Equal("up"))

		results := classifierResultEvents(t)
		Expect(results).To(HaveLen(1))
		Expect(results[0].ChosenID).To(Equal("up"))
		Expect(results[0].Arguments).To(MatchJSON(`{"direction":"up","distance":3,"units":"meters"}`))

		var fcArgs string
		for _, e := range t.events {
			if done, ok := e.(types.ResponseFunctionCallArgumentsDoneEvent); ok {
				fcArgs = done.Arguments
			}
		}
		Expect(fcArgs).To(MatchJSON(`{"direction":"up","distance":3,"units":"meters"}`))
	})

	It("splices the filled values into a templated reply", func() {
		m := &fakeModel{
			classifyScores: []router.LabelScore{{Label: "up", Score: 0.9}, {Label: "greeting", Score: 0.1}},
			fillArgs:       `{"direction":"up","distance":3,"units":"meters"}`,
			fillValues:     map[string]string{"distance": "3", "units": "meters"},
		}
		session := classifierTestSession(m)
		conv := &Conversation{}
		t := &fakeTransport{}
		r := &liveResponse{id: "resp-slot-reply"}

		handled := classifierRespond(context.Background(), session, conv, t, r, slottedTestConfig(0.35, nil, false), classifierTestHistory, nil, 0)

		Expect(handled).To(BeTrue())
		Expect(replyTexts(t)).To(ConsistOf("Going up 3 meters."))
	})

	It("recovers with slot defaults when filling fails", func() {
		m := &fakeModel{
			classifyScores: []router.LabelScore{{Label: "up", Score: 0.9}, {Label: "greeting", Score: 0.1}},
			fillErr:        fmt.Errorf("backend unavailable"),
		}
		session := classifierTestSession(m)
		conv := &Conversation{}
		t := &fakeTransport{}
		r := &liveResponse{id: "resp-slot-defaults"}

		handled := classifierRespond(context.Background(), session, conv, t, r, slottedTestConfig(0.35, nil, true), classifierTestHistory, nil, 0)

		Expect(handled).To(BeTrue())
		var fcArgs string
		for _, e := range t.events {
			if done, ok := e.(types.ResponseFunctionCallArgumentsDoneEvent); ok {
				fcArgs = done.Arguments
			}
		}
		Expect(fcArgs).To(MatchJSON(`{"direction":"up","distance":1,"units":"m"}`))
		Expect(replyTexts(t)).To(ConsistOf("Going up 1 m."), "the default-recovery reply confirms the defaults")
	})

	It("fails the response when filling fails and a slot has no default", func() {
		m := &fakeModel{
			classifyScores: []router.LabelScore{{Label: "up", Score: 0.9}, {Label: "greeting", Score: 0.1}},
			fillErr:        fmt.Errorf("backend unavailable"),
		}
		session := classifierTestSession(m)
		conv := &Conversation{}
		t := &fakeTransport{}
		r := &liveResponse{id: "resp-slot-fail"}

		handled := classifierRespond(context.Background(), session, conv, t, r, slottedTestConfig(0.35, nil, false), classifierTestHistory, nil, 0)

		Expect(handled).To(BeTrue())
		Expect(r.outcome).To(Equal(outcomeFailed))
		Expect(classifierResultEvents(t)).To(BeEmpty(), "no result event for a failed fill")
	})

	It("falls back to generation on fill failure in generate mode", func() {
		m := &fakeModel{
			classifyScores: []router.LabelScore{{Label: "up", Score: 0.9}, {Label: "greeting", Score: 0.1}},
			fillErr:        fmt.Errorf("backend unavailable"),
		}
		session := classifierTestSession(m)
		conv := &Conversation{}
		t := &fakeTransport{}
		r := &liveResponse{id: "resp-slot-genfb"}
		cc := slottedTestConfig(0.35, &types.ClassifierFallback{Mode: types.ClassifierFallbackGenerate}, false)

		handled := classifierRespond(context.Background(), session, conv, t, r, cc, classifierTestHistory, nil, 0)

		Expect(handled).To(BeFalse(), "generate fallback lets the caller run generation")
	})
})
