package types_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
)

func validClassifier() *types.ClassifierConfig {
	return &types.ClassifierConfig{
		Threshold: 0.35,
		Options: []types.ClassifierOption{
			{
				ID:          "up",
				Description: "the user asks the drone to fly up",
				Reply:       "Going up.",
				Tool:        &types.ClassifierTool{Name: "move", Arguments: json.RawMessage(`{"direction":"up"}`)},
			},
			{ID: "greeting", Description: "the user greets the assistant", Reply: "Hello."},
		},
		Fallback: &types.ClassifierFallback{Mode: types.ClassifierFallbackReply, Reply: "Say again?"},
	}
}

var _ = Describe("ClassifierConfig", func() {
	Describe("JSON round-trip", func() {
		It("survives marshal/unmarshal with all fields", func() {
			in := validClassifier()
			enabled := true
			in.Enabled = &enabled
			in.Normalization = "mean"
			in.HistoryItems = -1

			data, err := json.Marshal(in)
			Expect(err).ToNot(HaveOccurred())

			var out types.ClassifierConfig
			Expect(json.Unmarshal(data, &out)).To(Succeed())
			Expect(out.Enabled).ToNot(BeNil())
			Expect(*out.Enabled).To(BeTrue())
			Expect(out.Threshold).To(Equal(0.35))
			Expect(out.Normalization).To(Equal("mean"))
			Expect(out.HistoryItems).To(Equal(-1))
			Expect(out.Options).To(HaveLen(2))
			Expect(out.Options[0].Tool.Name).To(Equal("move"))
			Expect(string(out.Options[0].Tool.Arguments)).To(MatchJSON(`{"direction":"up"}`))
			Expect(out.Fallback.Mode).To(Equal("reply"))
		})

		It("is carried by RealtimeSession under localai_classifier", func() {
			s := types.RealtimeSession{LocalAIClassifier: validClassifier()}
			data, err := json.Marshal(s)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(data)).To(ContainSubstring(`"localai_classifier"`))

			var back types.RealtimeSession
			Expect(json.Unmarshal(data, &back)).To(Succeed())
			Expect(back.LocalAIClassifier).ToNot(BeNil())
			Expect(back.LocalAIClassifier.Options).To(HaveLen(2))
		})

		It("is carried by ResponseCreateParams under localai_classifier", func() {
			var params types.ResponseCreateParams
			Expect(json.Unmarshal([]byte(`{"localai_classifier":{"enabled":false}}`), &params)).To(Succeed())
			Expect(params.LocalAIClassifier).ToNot(BeNil())
			Expect(params.LocalAIClassifier.Enabled).ToNot(BeNil())
			Expect(*params.LocalAIClassifier.Enabled).To(BeFalse())
		})
	})

	Describe("Active", func() {
		It("is inactive when nil", func() {
			var c *types.ClassifierConfig
			Expect(c.Active()).To(BeFalse())
		})

		It("defaults to active when options exist", func() {
			Expect(validClassifier().Active()).To(BeTrue())
		})

		It("is inactive without options even when enabled", func() {
			enabled := true
			c := &types.ClassifierConfig{Enabled: &enabled}
			Expect(c.Active()).To(BeFalse())
		})

		It("honors an explicit enabled=false override", func() {
			c := validClassifier()
			disabled := false
			c.Enabled = &disabled
			Expect(c.Active()).To(BeFalse())
		})
	})

	Describe("Validate", func() {
		It("accepts a valid config and a nil config", func() {
			Expect(validClassifier().Validate()).To(Succeed())
			var c *types.ClassifierConfig
			Expect(c.Validate()).To(Succeed())
		})

		It("rejects out-of-range thresholds", func() {
			c := validClassifier()
			c.Threshold = 1.0
			Expect(c.Validate()).To(MatchError(ContainSubstring("threshold")))
			c.Threshold = -0.1
			Expect(c.Validate()).To(MatchError(ContainSubstring("threshold")))
		})

		It("rejects unknown normalization", func() {
			c := validClassifier()
			c.Normalization = "zscore"
			Expect(c.Validate()).To(MatchError(ContainSubstring("normalization")))
		})

		It("rejects history_items below -1", func() {
			c := validClassifier()
			c.HistoryItems = -2
			Expect(c.Validate()).To(MatchError(ContainSubstring("history_items")))
		})

		It("rejects unknown fallback modes", func() {
			c := validClassifier()
			c.Fallback = &types.ClassifierFallback{Mode: "retry"}
			Expect(c.Validate()).To(MatchError(ContainSubstring("fallback mode")))
		})

		It("rejects a reply fallback without a reply", func() {
			c := validClassifier()
			c.Fallback = &types.ClassifierFallback{Mode: types.ClassifierFallbackReply}
			Expect(c.Validate()).To(MatchError(ContainSubstring("fallback reply")))
		})

		It("rejects empty and duplicate option ids", func() {
			c := validClassifier()
			c.Options[1].ID = ""
			Expect(c.Validate()).To(MatchError(ContainSubstring("empty id")))
			c.Options[1].ID = "up"
			Expect(c.Validate()).To(MatchError(ContainSubstring("duplicate option id")))
		})

		It("rejects an option without a description", func() {
			c := validClassifier()
			c.Options[0].Description = ""
			Expect(c.Validate()).To(MatchError(ContainSubstring("empty description")))
		})

		It("rejects tools with no name or non-object arguments", func() {
			c := validClassifier()
			c.Options[0].Tool = &types.ClassifierTool{}
			Expect(c.Validate()).To(MatchError(ContainSubstring("empty name")))
			c.Options[0].Tool = &types.ClassifierTool{Name: "move", Arguments: json.RawMessage(`["up"]`)}
			Expect(c.Validate()).To(MatchError(ContainSubstring("JSON object")))
		})
	})

	Describe("FallbackMode", func() {
		It("defaults to none", func() {
			Expect((&types.ClassifierConfig{}).FallbackMode()).To(Equal(types.ClassifierFallbackNone))
			var c *types.ClassifierConfig
			Expect(c.FallbackMode()).To(Equal(types.ClassifierFallbackNone))
		})
	})

	Describe("ClassifierResultEvent", func() {
		It("marshals with the localai.classifier.result type tag", func() {
			ev := types.ClassifierResultEvent{
				ResponseID: "resp_1",
				Scores:     []types.ClassifierScore{{ID: "up", Score: 0.9}, {ID: "down", Score: 0.1}},
				ChosenID:   "up",
				Threshold:  0.35,
				LatencyMs:  12,
			}
			data, err := json.Marshal(ev)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(data)).To(ContainSubstring(`"type":"localai.classifier.result"`))
			Expect(string(data)).To(ContainSubstring(`"chosen_id":"up"`))
			Expect(string(data)).To(ContainSubstring(`"threshold":0.35`))
		})
	})
})

var _ = Describe("ClassifierTool slots", func() {
	tool := func(slots ...types.ClassifierSlot) *types.ClassifierTool {
		return &types.ClassifierTool{
			Name:      "move",
			Arguments: json.RawMessage(`{"direction":"up","distance":"{{distance}}","units":"{{units}}"}`),
			Slots:     slots,
		}
	}
	numberSlot := types.ClassifierSlot{Name: "distance", Type: types.ClassifierSlotNumber, Default: "1"}
	enumSlot := types.ClassifierSlot{Name: "units", Type: types.ClassifierSlotEnum, Values: []string{"m", "ft"}, Default: "m"}

	cfgWith := func(t *types.ClassifierTool) *types.ClassifierConfig {
		return &types.ClassifierConfig{Options: []types.ClassifierOption{{ID: "up", Description: "d", Tool: t}}}
	}

	Describe("Validate", func() {
		It("accepts a well-formed slotted tool", func() {
			Expect(cfgWith(tool(numberSlot, enumSlot)).Validate()).To(Succeed())
		})

		It("rejects unknown slot types", func() {
			bad := numberSlot
			bad.Type = "float"
			Expect(cfgWith(tool(bad, enumSlot)).Validate()).To(MatchError(ContainSubstring("number|enum|string")))
		})

		It("rejects enum slots without values", func() {
			bad := enumSlot
			bad.Values = nil
			bad.Default = ""
			Expect(cfgWith(tool(numberSlot, bad)).Validate()).To(MatchError(ContainSubstring("need values")))
		})

		It("rejects enum defaults outside the value set", func() {
			bad := enumSlot
			bad.Default = "yards"
			Expect(cfgWith(tool(numberSlot, bad)).Validate()).To(MatchError(ContainSubstring("not one of")))
		})

		It("rejects number defaults that do not parse", func() {
			bad := numberSlot
			bad.Default = "three"
			Expect(cfgWith(tool(bad, enumSlot)).Validate()).To(MatchError(ContainSubstring("does not parse")))
		})

		It("rejects empty enum values that cannot be spliced", func() {
			bad := enumSlot
			bad.Values = []string{"m", ""}
			Expect(cfgWith(tool(numberSlot, bad)).Validate()).To(MatchError(ContainSubstring("must be non-empty")))
		})

		It("rejects slots the template never references", func() {
			t := tool(numberSlot, enumSlot, types.ClassifierSlot{Name: "speed", Type: types.ClassifierSlotNumber})
			Expect(cfgWith(t).Validate()).To(MatchError(ContainSubstring("{{speed}}")))
		})

		It("rejects invalid slot names", func() {
			bad := numberSlot
			bad.Name = "dis tance"
			Expect(cfgWith(tool(bad, enumSlot)).Validate()).To(MatchError(ContainSubstring("invalid name")))
		})
	})

	Describe("SpliceArguments", func() {
		It("substitutes numbers unquoted and strings escaped", func() {
			args, err := tool(numberSlot, enumSlot).SpliceArguments(map[string]string{"distance": "3.5", "units": `m"eters`})
			Expect(err).ToNot(HaveOccurred())
			Expect(args).To(MatchJSON(`{"direction":"up","distance":3.5,"units":"m\"eters"}`))
		})

		It("fails on missing values", func() {
			_, err := tool(numberSlot, enumSlot).SpliceArguments(map[string]string{"distance": "3.5"})
			Expect(err).To(MatchError(ContainSubstring(`no value for slot "units"`)))
		})
	})

	Describe("SlotDefaults", func() {
		It("returns every default", func() {
			values, err := tool(numberSlot, enumSlot).SlotDefaults()
			Expect(err).ToNot(HaveOccurred())
			Expect(values).To(Equal(map[string]string{"distance": "1", "units": "m"}))
		})

		It("names the slot lacking a default", func() {
			bare := numberSlot
			bare.Default = ""
			_, err := tool(bare, enumSlot).SlotDefaults()
			Expect(err).To(MatchError(ContainSubstring(`"distance"`)))
		})
	})

	Describe("SpliceReply", func() {
		option := func(reply string, t *types.ClassifierTool) *types.ClassifierOption {
			return &types.ClassifierOption{ID: "up", Description: "d", Reply: reply, Tool: t}
		}

		It("substitutes slot values as plain text", func() {
			o := option("Going up {{distance}} {{units}}.", tool(numberSlot, enumSlot))
			Expect(o.SpliceReply(map[string]string{"distance": "3.5", "units": "m"})).To(Equal("Going up 3.5 m."))
		})

		It("leaves placeholders without a value literal", func() {
			o := option("Going up {{distance}} {{units}}.", tool(numberSlot, enumSlot))
			Expect(o.SpliceReply(map[string]string{"distance": "3"})).To(Equal("Going up 3 {{units}}."))
		})

		It("returns the reply verbatim without slots or values", func() {
			o := option("Going up {{distance}}.", nil)
			Expect(o.SpliceReply(map[string]string{"distance": "3"})).To(Equal("Going up {{distance}}."))
			slotted := option("Going up {{distance}}.", tool(numberSlot))
			Expect(slotted.SpliceReply(nil)).To(Equal("Going up {{distance}}."))
		})
	})
})
