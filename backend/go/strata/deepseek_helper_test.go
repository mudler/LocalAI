package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// dsSpecials are the DeepSeek V4.1 control tokens the chat template writes, as
// (text, GGUF token type): 3 is control, 4 is user-defined.
var dsSpecials = []struct {
	text string
	typ  int
}{
	{"<｜begin▁of▁sentence｜>", 3}, {"<｜end▁of▁sentence｜>", 3}, {"<｜User｜>", 3}, {"<｜Assistant｜>", 3},
	{"<｜System｜>", 3}, {"<｜latest_reminder｜>", 3}, {"<think>", 4}, {"</think>", 4}, {"｜DSML｜", 4},
	{"<｜deepseek_image｜>", 3},
}

// byteChars is the GPT-2 byte to character table the pack's vocabulary uses.
func byteChars() [256]string {
	var t [256]string
	extra := 0
	for b := 0; b < 256; b++ {
		if (b >= 33 && b <= 126) || (b >= 161 && b <= 172) || (b >= 174 && b <= 255) {
			t[b] = string(rune(b))
		} else {
			t[b] = string(rune(256 + extra))
			extra++
		}
	}
	return t
}

// writeDSPack writes the tokenizer folder of a small DeepSeek-style pack: the
// control tokens, the 256 byte tokens and one merge, with the pre-tokenizer
// name and end id the real pack states and the chat template of the Strata tree.
func writeDSPack(root string) string {
	pack := GinkgoT().TempDir()
	tdir := filepath.Join(pack, "tokenizer")
	Expect(os.MkdirAll(tdir, 0o755)).To(Succeed())
	bc := byteChars()
	var tokens []string
	var types []int
	for _, s := range dsSpecials {
		tokens, types = append(tokens, s.text), append(types, s.typ)
	}
	for b := 0; b < 256; b++ {
		tokens, types = append(tokens, bc[b]), append(types, 1)
	}
	tokens, types = append(tokens, "hi"), append(types, 1)
	vocab := map[string]int{}
	for i, t := range tokens {
		vocab[t] = i
	}
	write := func(name string, v any) {
		b, err := json.Marshal(v)
		Expect(err).ToNot(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(tdir, name), b, 0o644)).To(Succeed())
	}
	write("vocab.json", vocab)
	write("token_type.json", types)
	write("tokenizer.json", map[string]any{"pre": "deepseek3", "special_ids": map[string]int{"bos": 0, "eos": 1, "pad": 2}})
	Expect(os.WriteFile(filepath.Join(tdir, "merges.txt"), []byte("h i"), 0o644)).To(Succeed())
	tpl, err := os.ReadFile(filepath.Join(root, "serve", "fixtures", "deepseek41", "chat_template.jinja"))
	Expect(err).ToNot(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(tdir, "chat_template.jinja"), tpl, 0o644)).To(Succeed())
	return pack
}

func decode(tok Tokenizer, ids []int) string {
	var b []byte
	for _, id := range ids {
		t, ok := tok.TokenBytes(id)
		Expect(ok).To(BeTrue(), "token %d", id)
		b = append(b, t...)
	}
	return string(b)
}

// These specs drive the real Python helper and Strata's own DeepSeek code.
// They need a Strata source checkout at the pinned commit (STRATA_ROOT) and a
// Python with regex and jinja2 (STRATA_TEST_PYTHON, default python3); they are
// skipped otherwise. The golden file is Strata's: each case is a request with
// the exact prompt the model's template renders for it.
var _ = Describe("deepseek41 helper", func() {
	var root, python string
	BeforeEach(func() {
		root, python = os.Getenv("STRATA_ROOT"), os.Getenv("STRATA_TEST_PYTHON")
		if root == "" {
			Skip("set STRATA_ROOT (a Strata checkout) to run")
		}
	})

	It("renders the V4.1 template to the bytes of the golden cases", func() {
		tok, err := newHelperTokenizer(python, root, writeDSPack(root), familyDeepSeek41, time.Minute)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = tok.Close() }()
		eos, ok := tok.EOSID()
		Expect(ok).To(BeTrue())
		Expect(eos).To(Equal(1))

		raw, err := os.ReadFile(filepath.Join(root, "serve", "fixtures", "deepseek41", "golden.json"))
		Expect(err).ToNot(HaveOccurred())
		var cases []struct {
			ID      string `json:"id"`
			Mode    string `json:"mode"`
			Request struct {
				Messages           []map[string]any `json:"messages"`
				Tools              json.RawMessage  `json:"tools"`
				ChatTemplateKwargs map[string]any   `json:"chat_template_kwargs"`
			} `json:"request"`
			Thinking bool   `json:"thinking"`
			Text     string `json:"text"`
		}
		Expect(json.Unmarshal(raw, &cases)).To(Succeed())
		Expect(len(cases)).To(BeNumerically(">=", 6))
		for _, c := range cases {
			// Tools go as the client wrote them: the template prints their keys in order.
			req := promptRequest{Messages: c.Request.Messages, ChatTemplateKwargs: c.Request.ChatTemplateKwargs}
			if len(c.Request.Tools) > 0 {
				req.Tools = c.Request.Tools
			}
			pr, err := tok.Prompt(req)
			Expect(err).ToNot(HaveOccurred(), c.ID)
			Expect(decode(tok, pr.IDs)).To(Equal(c.Text), "%s (%s)", c.ID, c.Mode)
			Expect(pr.Thinking).To(Equal(c.Thinking), c.ID)
		}
	})

	It("takes the reasoning effort words and numbers of V4.1", func() {
		tok, err := newHelperTokenizer(python, root, writeDSPack(root), familyDeepSeek41, time.Minute)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = tok.Close() }()
		msgs := []map[string]any{{"role": "user", "content": "x"}}
		pr, err := tok.Prompt(promptRequest{Messages: msgs, ReasoningEffort: "max"})
		Expect(err).ToNot(HaveOccurred())
		Expect(decode(tok, pr.IDs)).To(ContainSubstring("Reasoning Effort: 100"))
		pr, err = tok.Prompt(promptRequest{Messages: msgs, ReasoningEffort: "none"})
		Expect(err).ToNot(HaveOccurred())
		Expect(pr.Thinking).To(BeFalse())
		Expect(decode(tok, pr.IDs)).To(HaveSuffix("<｜Assistant｜></think>"))
		_, err = tok.Prompt(promptRequest{Messages: msgs, ReasoningEffort: "sideways"})
		Expect(err).To(HaveOccurred())
	})

	It("writes a forced tool call in DSML", func() {
		tok, err := newHelperTokenizer(python, root, writeDSPack(root), familyDeepSeek41, time.Minute)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = tok.Close() }()
		tools := []any{map[string]any{"type": "function", "function": map[string]any{"name": "lookup", "description": "d",
			"parameters": map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}}}}}
		pr, err := tok.Prompt(promptRequest{Messages: []map[string]any{{"role": "user", "content": "x"}}, Tools: tools, ToolChoice: "required"})
		Expect(err).ToNot(HaveOccurred())
		Expect(pr.Thinking).To(BeFalse())
		Expect(pr.Force).To(Equal("<｜DSML｜ calls>\n<｜DSML｜ invoke name=\"lookup\">\n"))
		Expect(decode(tok, pr.IDs)).To(HaveSuffix(pr.Force))
	})

	It("splits reasoning, answer and DSML tool calls", func() {
		tok, err := newHelperTokenizer(python, root, writeDSPack(root), familyDeepSeek41, time.Minute)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = tok.Close() }()
		tools := []any{map[string]any{"type": "function", "function": map[string]any{"name": "get_weather", "description": "d",
			"parameters": map[string]any{"type": "object", "properties": map[string]any{
				"city": map[string]any{"type": "string"}, "days": map[string]any{"type": "integer"}, "code": map[string]any{"type": "string"}}}}}}
		pr, err := tok.Prompt(promptRequest{Messages: []map[string]any{{"role": "user", "content": "x"}}, Tools: tools})
		Expect(err).ToNot(HaveOccurred())

		text := "Need the weather.</think>\n\nChecking.\n\n<｜DSML｜ calls>\n" +
			"<｜DSML｜ invoke name=\"get_weather\">\n" +
			"<｜DSML｜ parameter name=\"city\" string=\"true\">Zagreb</｜DSML｜ parameter>\n" +
			"<｜DSML｜ parameter name=\"days\" string=\"false\">3</｜DSML｜ parameter>\n" +
			"<｜DSML｜ parameter name=\"code\" string=\"true\">123</｜DSML｜ parameter>\n" +
			"</｜DSML｜ invoke>\n</｜DSML｜ calls>"
		pa, _, err := tok.NewParser(pr.Thinking, pr.Tools, "")
		Expect(err).ToNot(HaveOccurred())
		defer pa.Close()
		var evs []parseEvent
		runes := []rune(text)
		for i := 0; i < len(runes); i += 7 { // small pieces: tags arrive cut in the middle
			e, err := pa.Feed(string(runes[i:min(i+7, len(runes))]))
			Expect(err).ToNot(HaveOccurred())
			evs = append(evs, e...)
		}
		e, err := pa.Finish("stop")
		Expect(err).ToNot(HaveOccurred())
		evs = append(evs, e...)
		var reasoning, content string
		var calls []*parseCall
		for _, ev := range evs {
			switch ev.Kind {
			case "reasoning":
				reasoning += ev.Text
			case "content":
				content += ev.Text
			case "tool_call":
				calls = append(calls, ev.Call)
			}
		}
		Expect(reasoning).To(ContainSubstring("Need the weather."))
		Expect(content).To(ContainSubstring("Checking."))
		Expect(content).ToNot(ContainSubstring("DSML"))
		Expect(calls).To(HaveLen(1))
		Expect(calls[0].Name).To(Equal("get_weather"))
		// string="true" keeps "123" a string; string="false" is JSON.
		var args map[string]any
		Expect(json.Unmarshal(calls[0].Arguments, &args)).To(Succeed())
		Expect(args).To(Equal(map[string]any{"city": "Zagreb", "days": float64(3), "code": "123"}))
	})

	It("loads through the backend with the family and the tokenizer's end id", func() {
		pack := writeDSPack(root)
		exe, _ := os.Executable()
		s := NewStrata()
		s.startEngine = func(c engineConfig) (*engine, error) {
			c.Env = append(c.Env, "STRATA_STUB_MODE=ok", "STRATA_STUB_TOKENS=104,105,1")
			return startEngine(c)
		}
		DeferCleanup(func() { _ = s.Free() })
		Expect(s.Load(&pb.ModelOptions{ModelFile: pack, ContextSize: 2048, Options: []string{
			"engine_path:" + exe, "strata_root:" + root, "python:" + python, "family:deepseek41", "ready_timeout:1m"}})).To(Succeed())
		Expect(s.eos).To(Equal(map[int]bool{1: true}))
		_, tok, _, err := s.state()
		Expect(err).ToNot(HaveOccurred())
		ht := tok.(*helperTokenizer)
		Expect(strings.Contains(helperScript, "deepseek41")).To(BeTrue())
		Expect(ht.hasEOS).To(BeTrue())
	})

	// With a real pack (STRATA_TEST_DS_PACK: a folder with tokenizer/ made by pack_gguf.py from the
	// GGUF files of a published set; the tokenizer part is a few MB and the tool reads only headers),
	// the token ids must be those llama.cpp produced for the same prompts (golden "ids").
	It("gives the golden token ids with the real vocabulary", func() {
		pack := os.Getenv("STRATA_TEST_DS_PACK")
		if pack == "" {
			Skip("set STRATA_TEST_DS_PACK to a DeepSeek pack to run")
		}
		tok, err := newHelperTokenizer(python, root, pack, familyDeepSeek41, 5*time.Minute)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = tok.Close() }()
		raw, err := os.ReadFile(filepath.Join(root, "serve", "fixtures", "deepseek41", "golden.json"))
		Expect(err).ToNot(HaveOccurred())
		var cases []struct {
			ID      string `json:"id"`
			Mode    string `json:"mode"`
			Request struct {
				Messages           []map[string]any `json:"messages"`
				Tools              json.RawMessage  `json:"tools"`
				ChatTemplateKwargs map[string]any   `json:"chat_template_kwargs"`
			} `json:"request"`
			IDs  []int  `json:"ids"`
			Text string `json:"text"`
		}
		Expect(json.Unmarshal(raw, &cases)).To(Succeed())
		for _, c := range cases {
			req := promptRequest{Messages: c.Request.Messages, ChatTemplateKwargs: c.Request.ChatTemplateKwargs}
			if len(c.Request.Tools) > 0 {
				req.Tools = c.Request.Tools
			}
			pr, err := tok.Prompt(req)
			Expect(err).ToNot(HaveOccurred(), c.ID)
			Expect(decode(tok, pr.IDs)).To(Equal(c.Text), c.ID)
			Expect(pr.IDs).To(Equal(c.IDs), "%s (%s)", c.ID, c.Mode)
		}
	})
})
