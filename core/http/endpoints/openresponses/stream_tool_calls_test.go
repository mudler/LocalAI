package openresponses

import (
	"github.com/mudler/LocalAI/pkg/functions"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Streaming JSON tool calls", func() {
	It("waits for the arguments before completing a split call", func() {
		Expect(parseStreamingJSONToolCalls(`{"name":"Bash",`)).To(BeEmpty())
		Expect(parseStreamingJSONToolCalls(`{"name":"Bash","arguments":{"command":"ls`)).To(BeEmpty())
		Expect(parseStreamingJSONToolCalls(`{"name":"Bash","arguments":{"command":"ls -la"}}`)).To(Equal([]functions.FuncCallResults{
			{Name: "Bash", Arguments: `{"command":"ls -la"}`},
		}))
	})

	It("does not complete a call at any intermediate token boundary", func() {
		text := `{"name":"Bash","arguments":{"command":"printf \"hello\"","options":[1,2]}}`
		for end := 1; end < len(text); end++ {
			Expect(parseStreamingJSONToolCalls(text[:end])).To(BeEmpty(), "prefix: %s", text[:end])
		}
		Expect(parseStreamingJSONToolCalls(text)).To(HaveLen(1))
	})

	It("keeps completed calls while the next call is incomplete", func() {
		Expect(parseStreamingJSONToolCalls(`{"name":"Bash","arguments":{"command":"ls -la"}} {"name":"Read",`)).To(Equal([]functions.FuncCallResults{
			{Name: "Bash", Arguments: `{"command":"ls -la"}`},
		}))
	})

	It("preserves string arguments and calls that take no arguments", func() {
		Expect(parseStreamingJSONToolCalls(`[{"name":"Bash","arguments":"{\"command\":\"ls -la\"}"},{"name":"status"}]`)).To(Equal([]functions.FuncCallResults{
			{Name: "Bash", Arguments: `{"command":"ls -la"}`},
			{Name: "status", Arguments: `{}`},
		}))
	})

	It("does not count unrelated JSON objects as emitted calls", func() {
		Expect(parseStreamingJSONToolCalls(`{"message":"checking"} {"name":"status","arguments":{}}`)).To(Equal([]functions.FuncCallResults{
			{Name: "status", Arguments: `{}`},
		}))
	})
})
