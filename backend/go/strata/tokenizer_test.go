package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("helper tokenizer", func() {
	// TestHelperTokenizer drives the real Python helper. It needs a Strata
	// source checkout (STRATA_ROOT) and a pack directory whose tokenizer/
	// folder exists (STRATA_TEST_PACK); it is skipped otherwise.
	It("helper tokenizer", func() {
		root, pack := os.Getenv("STRATA_ROOT"), os.Getenv("STRATA_TEST_PACK")
		if root == "" || pack == "" {
			Skip("set STRATA_ROOT and STRATA_TEST_PACK to run")
		}
		tok, err := newHelperTokenizer("", root, pack, "", time.Minute)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = tok.Close() }()
		ids, err := tok.Encode("hi", true)
		if err != nil || len(ids) == 0 {
			Fail(fmt.Sprintf("encode: %v %v", ids, err))
		}
		var back []byte
		for _, id := range ids {
			b, ok := tok.TokenBytes(id)
			if !ok {
				Fail(fmt.Sprintf("no bytes for id %d", id))
			}
			back = append(back, b...)
		}
		if string(back) != "hi" {
			Fail(fmt.Sprintf("round trip gave %q", back))
		}
		tools := []any{map[string]any{"type": "function", "function": map[string]any{
			"name": "get_weather", "description": "weather",
			"parameters": map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}},
		}}}
		pr, err := tok.Prompt(promptRequest{Messages: []map[string]any{{"role": "user", "content": "hi"}}, Tools: tools})
		if err != nil || len(pr.IDs) == 0 || !pr.Thinking || pr.Force != "" {
			Fail(fmt.Sprintf("prompt: %+v %v", pr, err))
		}
		// A forced tool call turns thinking off and ends the prompt with the call opening.
		pr, err = tok.Prompt(promptRequest{Messages: []map[string]any{{"role": "user", "content": "hi"}}, Tools: tools, ToolChoice: "required"})
		if err != nil || pr.Thinking || pr.Force == "" {
			Fail(fmt.Sprintf("forced prompt: %+v %v", pr, err))
		}
		// The output parser splits reasoning, content and a streamed tool call.
		pa, _, err := tok.NewParser(true, pr.Tools, "")
		Expect(err).ToNot(HaveOccurred())
		defer pa.Close()
		text := "plan\n</think>\n\nOk.\n<tool_call>\n<function=get_weather>\n<parameter=city>\nRome\n</parameter>\n</function>\n</tool_call>"
		var evs []parseEvent
		for i := 0; i < len(text); i += 9 {
			e, err := pa.Feed(text[i:min(i+9, len(text))])
			Expect(err).ToNot(HaveOccurred())
			evs = append(evs, e...)
		}
		e, err := pa.Finish("stop")
		Expect(err).ToNot(HaveOccurred())
		evs = append(evs, e...)
		var reasoning, content, args string
		var final *parseCall
		for _, ev := range evs {
			switch ev.Kind {
			case "reasoning":
				reasoning += ev.Text
			case "content":
				content += ev.Text
			case "tool_args":
				args += ev.Text
			case "tool_call":
				final = ev.Call
			}
		}
		if !strings.Contains(reasoning, "plan") || !strings.Contains(content, "Ok.") || args != `{"city":"Rome"}` ||
			final == nil || final.Name != "get_weather" {
			Fail(fmt.Sprintf("reasoning %q content %q args %q final %+v", reasoning, content, args, final))
		}
		if _, err := newHelperTokenizer("", root, GinkgoT().TempDir(), "", time.Minute); err == nil {
			Fail("a directory with no tokenizer must fail to load")
		}
	})

})
