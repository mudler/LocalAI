package studio

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// searchable builds a tool with a schema from property names.
func searchable(name, desc string, params ...string) session.Tool {
	props := map[string]any{}
	for _, p := range params {
		props[p] = map[string]any{"type": "string"}
	}
	raw, _ := json.Marshal(map[string]any{"type": "object", "properties": props})
	return session.Tool{Name: name, Description: desc, Schema: raw,
		Run: func(_ context.Context, args json.RawMessage) (string, error) { return name + " " + string(args), nil }}
}

func fixtureTools() []session.Tool {
	return []session.Tool{
		searchable("install_model", "Install a model from the gallery by id.", "id", "overrides"),
		searchable("delete_model", "Delete an installed model.", "name"),
		searchable("gallery_search", "Search the model gallery for models by name or tag.", "query", "tag"),
		searchable("list_backends", "List the installed backends.", "kind"),
		searchable("install_backend", "Install a backend by name.", "name"),
		searchable("generate_image", "Make images from a prompt. Saves files.", "prompt", "size", "image", "mask"),
		searchable("speak", "Turn text into speech. Saves an audio file.", "text", "voice", "format"),
		searchable("transcribe_audio", "Turn a recording into text.", "audio", "language"),
		searchable("embed", "Turn texts into embedding vectors.", "input"),
		searchable("get_usage_stats", "Read the token usage of users.", "since"),
	}
}

var _ = Describe("the decision to defer tools behind the tool search", func() {
	tools := fixtureTools()
	tokens := schemaTokens(tools)

	It("estimates tokens as the bytes of the tool JSON over four", func() {
		Expect(tokens).To(BeNumerically(">", 100))
		one := []session.Tool{searchable("a_tool", "does a thing", "x")}
		raw, _ := json.Marshal(map[string]any{"name": "a_tool", "description": "does a thing", "parameters": json.RawMessage(one[0].Schema)})
		Expect(schemaTokens(one)).To(Equal((len(raw) + 3) / 4))
	})

	It("in auto mode defers at a tenth of the window and not below it", func() {
		// The schemas take exactly a tenth of a window of 10*tokens.
		Expect(shouldDefer(config.ToolSearchAuto, tools, tokens*10)).To(BeTrue(), "at the threshold")
		Expect(shouldDefer(config.ToolSearchAuto, tools, tokens*10-1)).To(BeTrue(), "above it")
		Expect(shouldDefer(config.ToolSearchAuto, tools, tokens*10+1)).To(BeFalse(), "below it")
		Expect(shouldDefer(config.ToolSearchAuto, tools, 4096)).To(BeTrue(), "a small window")
		Expect(shouldDefer(config.ToolSearchAuto, tools, 1<<20)).To(BeFalse(), "a large window")
	})

	It("does not defer in auto mode when the window is unknown", func() {
		Expect(shouldDefer(config.ToolSearchAuto, tools, 0)).To(BeFalse())
		Expect(shouldDefer(config.ToolSearchAuto, tools, -1)).To(BeFalse())
	})

	It("honours on and off whatever the window", func() {
		for _, w := range []int{0, 512, 1 << 20} {
			Expect(shouldDefer(config.ToolSearchOn, tools, w)).To(BeTrue(), "on, window %d", w)
			Expect(shouldDefer(config.ToolSearchOff, tools, w)).To(BeFalse(), "off, window %d", w)
		}
	})

	It("defers nothing when there is nothing to defer", func() {
		for _, m := range []string{config.ToolSearchAuto, config.ToolSearchOn, config.ToolSearchOff} {
			Expect(shouldDefer(m, nil, 4096)).To(BeFalse(), m)
		}
	})

	It("gives the same answer for the same inputs, however often it is asked", func() {
		for _, w := range []int{0, 300, tokens * 10, tokens*10 + 1, 4096, 1 << 20} {
			first := shouldDefer(config.ToolSearchAuto, tools, w)
			for i := 0; i < 50; i++ {
				// A rebuilt copy of the same tools, in the same order, is the
				// same input.
				Expect(shouldDefer(config.ToolSearchAuto, fixtureTools(), w)).To(Equal(first), "window %d", w)
			}
		}
	})
})

var _ = Describe("the tool search index", func() {
	ix := newToolIndex(fixtureTools())
	top := func(q string) string {
		GinkgoHelper()
		hits := ix.search(q, 5)
		Expect(hits).NotTo(BeEmpty(), q)
		return hits[0].Name
	}

	It("ranks the tool a query is about first", func() {
		Expect(top("install a model from the gallery")).To(Equal("install_model"))
		Expect(top("delete model")).To(Equal("delete_model"))
		Expect(top("search gallery")).To(Equal("gallery_search"))
		Expect(top("backends")).To(Equal("list_backends"))
		Expect(top("make an image")).To(Equal("generate_image"))
		Expect(top("text to speech")).To(Equal("speak"))
		Expect(top("transcribe a recording")).To(Equal("transcribe_audio"))
		Expect(top("usage stats")).To(Equal("get_usage_stats"))
	})

	It("finds a tool by the name of a parameter", func() {
		Expect(top("mask")).To(Equal("generate_image"))
		Expect(top("overrides")).To(Equal("install_model"))
	})

	It("returns only tools with a matching word, and nothing for noise", func() {
		Expect(ix.search("zzzz qqqq", 5)).To(BeEmpty())
		Expect(ix.search("the a of", 5)).To(BeEmpty())
		for _, h := range ix.search("model", 10) {
			Expect(strings.Contains(h.Name+h.Description, "model") || strings.Contains(strings.ToLower(h.Description), "model")).To(BeTrue(), h.Name)
		}
	})

	It("orders by score and then by name, and honours the limit", func() {
		a := ix.search("install", 10)
		b := ix.search("install", 10)
		Expect(a).To(Equal(b))
		for i := 1; i < len(a); i++ {
			Expect(a[i-1].Score >= a[i].Score).To(BeTrue())
			if a[i-1].Score == a[i].Score {
				Expect(a[i-1].Name < a[i].Name).To(BeTrue())
			}
		}
		Expect(ix.search("install", 1)).To(HaveLen(1))
	})

	It("breaks a tie by name", func() {
		tie := newToolIndex([]session.Tool{searchable("b_tool", "same words"), searchable("a_tool", "same words"), searchable("c_tool", "same words")})
		hits := tie.search("same words", 5)
		Expect([]string{hits[0].Name, hits[1].Name, hits[2].Name}).To(Equal([]string{"a_tool", "b_tool", "c_tool"}))
	})
})

var _ = Describe("the tool search bridge", func() {
	call := func(t session.Tool, args string) (string, error) {
		return t.Run(context.Background(), json.RawMessage(args))
	}
	bridge := func(tools []session.Tool) map[string]session.Tool {
		out := map[string]session.Tool{}
		for _, t := range toolSearchTools(tools, []string{builtins.GroupLocalAIClient, builtins.GroupLocalAIAdmin}) {
			out[t.Name] = t
		}
		return out
	}

	It("is absent when nothing is deferred", func() {
		Expect(toolSearchTools(nil, nil)).To(BeEmpty())
	})

	It("is three short tools", func() {
		b := toolSearchTools(fixtureTools(), []string{builtins.GroupLocalAIClient, builtins.GroupLocalAIAdmin})
		Expect(b).To(HaveLen(3))
		Expect(schemaTokens(b)).To(BeNumerically("<", 400))
	})

	It("searches only the tools it holds", func() {
		b := bridge(fixtureTools()[:2]) // install_model and delete_model only
		out, err := call(b["tool_search"], `{"query":"install model backend image"}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("install_model"))
		Expect(out).NotTo(ContainSubstring("install_backend"))
		Expect(out).NotTo(ContainSubstring("generate_image"))
	})

	It("shows a one-line description per result and says when nothing matches", func() {
		b := bridge(fixtureTools())
		out, err := call(b["tool_search"], `{"query":"install","limit":2}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Split(out, "\n")).To(HaveLen(2))
		out, err = call(b["tool_search"], `{"query":"qqqq"}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("No tool matches"))
		_, err = call(b["tool_search"], `{"query":"  "}`)
		Expect(err).To(HaveOccurred())
	})

	It("describes a tool with its full schema", func() {
		b := bridge(fixtureTools())
		out, err := call(b["tool_describe"], `{"name":"generate_image"}`)
		Expect(err).NotTo(HaveOccurred())
		var d struct {
			Name        string
			Description string
			Parameters  struct {
				Properties map[string]any `json:"properties"`
			}
		}
		Expect(json.Unmarshal([]byte(out), &d)).To(Succeed())
		Expect(d.Name).To(Equal("generate_image"))
		Expect(d.Description).To(ContainSubstring("Make images"))
		Expect(d.Parameters.Properties).To(HaveKey("mask"))
	})

	It("answers the same for a tool it does not hold and a tool that does not exist", func() {
		b := bridge(fixtureTools()[:1]) // install_model only; speak exists elsewhere
		_, errHeldBack := call(b["tool_call"], `{"name":"speak","arguments":{"text":"x"}}`)
		_, errUnknown := call(b["tool_call"], `{"name":"no_such_thing"}`)
		Expect(errHeldBack).To(HaveOccurred())
		Expect(errUnknown).To(HaveOccurred())
		Expect(strings.ReplaceAll(errHeldBack.Error(), "speak", "X")).To(Equal(strings.ReplaceAll(errUnknown.Error(), "no_such_thing", "X")))
		Expect(errHeldBack.Error()).To(ContainSubstring("no such tool"))
		_, errDescribe := call(b["tool_describe"], `{"name":"speak"}`)
		Expect(errDescribe).To(MatchError(ContainSubstring("no such tool")))
	})

	It("runs the held tool with its arguments, as an object, a string or nothing", func() {
		b := bridge(fixtureTools())
		out, err := call(b["tool_call"], `{"name":"speak","arguments":{"text":"hi"}}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal(`speak {"text":"hi"}`))
		out, err = call(b["tool_call"], `{"name":"speak","arguments":"{\"text\":\"hi\"}"}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal(`speak {"text":"hi"}`))
		out, err = call(b["tool_call"], `{"name":"list_backends"}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal(`list_backends {}`))
		_, err = call(b["tool_call"], `{"name":"speak","arguments":[1]}`)
		Expect(err).To(MatchError(ContainSubstring("arguments must be an object")))
	})

	It("passes the error of the tool through, and cannot call a bridge tool", func() {
		boom := searchable("boom", "fails")
		boom.Run = func(context.Context, json.RawMessage) (string, error) { return "", errors.New("it broke") }
		b := bridge([]session.Tool{boom})
		_, err := call(b["tool_call"], `{"name":"boom"}`)
		Expect(err).To(MatchError("it broke"))
		_, err = call(b["tool_call"], `{"name":"tool_call"}`)
		Expect(err).To(MatchError(ContainSubstring("no such tool")))
	})
})
