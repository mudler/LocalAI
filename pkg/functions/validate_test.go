package functions_test

import (
	. "github.com/mudler/LocalAI/pkg/functions"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ValidateFuncCall", func() {
	bash := Function{
		Name: "bash",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"script":  map[string]any{"type": "string"},
				"timeout": map[string]any{"type": "integer"},
			},
			"required": []any{"script"},
		},
	}
	declared := Functions{bash}

	It("accepts a call that fits the schema", func() {
		Expect(ValidateFuncCall(FuncCallResults{Name: "bash", Arguments: `{"script":"ls","timeout":5}`}, declared)).To(Succeed())
	})

	It("refuses an argument the schema does not list", func() {
		err := ValidateFuncCall(FuncCallResults{Name: "bash", Arguments: `{"command":"ls"}`}, declared)
		Expect(err).To(HaveOccurred())
		m := err.(ToolCallMismatch)
		Expect(m.Unknown).To(Equal([]string{"command"}))
		Expect(m.Missing).To(Equal([]string{"script"}))
	})

	It("refuses a call that leaves out a required argument", func() {
		err := ValidateFuncCall(FuncCallResults{Name: "bash", Arguments: `{"timeout":5}`}, declared)
		Expect(err).To(MatchError(ContainSubstring("missing required arguments script")))
	})

	It("refuses a tool the request did not declare", func() {
		Expect(ValidateFuncCall(FuncCallResults{Name: "shell", Arguments: `{"script":"ls"}`}, declared)).
			To(MatchError(ContainSubstring("no such tool")))
	})

	It("refuses arguments that are not a JSON object", func() {
		Expect(ValidateFuncCall(FuncCallResults{Name: "bash", Arguments: `"ls"`}, declared)).
			To(MatchError(ContainSubstring("not a JSON object")))
	})

	DescribeTable("treats additionalProperties the way llama.cpp's grammar does",
		func(ap any, set bool, wantOK bool) {
			params := map[string]any{"properties": map[string]any{"a": map[string]any{"type": "string"}}}
			if set {
				params["additionalProperties"] = ap
			}
			err := ValidateFuncCall(FuncCallResults{Name: "t", Arguments: `{"a":"x","b":"y"}`}, Functions{{Name: "t", Parameters: params}})
			if wantOK {
				Expect(err).ToNot(HaveOccurred())
			} else {
				Expect(err).To(HaveOccurred())
			}
		},
		Entry("absent: closed", nil, false, false),
		Entry("false: closed", false, true, false),
		Entry("true: open", true, true, true),
		Entry("a schema: open", map[string]any{"type": "string"}, true, true),
	)

	It("does not check a schema without properties", func() {
		Expect(ValidateFuncCall(FuncCallResults{Name: "t", Arguments: `{"anything":1}`}, Functions{{Name: "t"}})).To(Succeed())
	})

	It("checks only top-level names, not nested objects", func() {
		f := Function{Name: "t", Parameters: map[string]any{
			"properties": map[string]any{"opts": map[string]any{"type": "object", "properties": map[string]any{}}},
		}}
		Expect(ValidateFuncCall(FuncCallResults{Name: "t", Arguments: `{"opts":{"extra":true}}`}, Functions{f})).To(Succeed())
	})

	It("reads required as []string too", func() {
		f := Function{Name: "t", Parameters: map[string]any{"properties": map[string]any{"a": map[string]any{}}, "required": []string{"a"}}}
		Expect(ValidateFuncCall(FuncCallResults{Name: "t", Arguments: `{}`}, Functions{f})).To(HaveOccurred())
	})
})

var _ = Describe("FilterValidFuncCalls", func() {
	declared := DeclaredFunctions(nil, Tools{{Type: "function", Function: Function{
		Name:       "bash",
		Parameters: map[string]any{"properties": map[string]any{"script": map[string]any{}}, "required": []any{"script"}},
	}}})

	It("drops only the calls that do not fit", func() {
		got := FilterValidFuncCalls([]FuncCallResults{
			{Name: "bash", Arguments: `{"command":"ls"}`},
			{Name: "bash", Arguments: `{"script":"pwd"}`},
		}, declared, "answer")
		Expect(got).To(HaveLen(1))
		Expect(got[0].Arguments).To(ContainSubstring("pwd"))
	})

	It("keeps the no-action sentinel", func() {
		got := FilterValidFuncCalls([]FuncCallResults{{Name: "answer", Arguments: `{"message":"hi"}`}}, declared, "answer")
		Expect(got).To(HaveLen(1))
	})

	It("keeps everything when the request declared no functions", func() {
		calls := []FuncCallResults{{Name: "anything", Arguments: `{"x":1}`}}
		Expect(FilterValidFuncCalls(calls, nil, "answer")).To(Equal(calls))
	})
})

var _ = Describe("ValidatesToolCalls", func() {
	declared := Functions{{Name: "bash"}}

	It("validates when LocalAI sent no grammar", func() {
		Expect(ValidatesToolCalls("", FunctionsConfig{}, declared)).To(BeTrue())
	})
	It("does not validate under LocalAI's own grammar", func() {
		Expect(ValidatesToolCalls("root ::= x", FunctionsConfig{}, declared)).To(BeFalse())
	})
	It("does not validate when turned off", func() {
		Expect(ValidatesToolCalls("", FunctionsConfig{DisableToolCallValidation: true}, declared)).To(BeFalse())
	})
	It("does not validate without declared tools", func() {
		Expect(ValidatesToolCalls("", FunctionsConfig{}, nil)).To(BeFalse())
	})
})

var _ = Describe("SplitFuncCalls and AnswerText", func() {
	declared := Functions{{
		Name:       "bash",
		Parameters: map[string]any{"properties": map[string]any{"script": map[string]any{}}},
	}}

	It("returns the dropped calls", func() {
		valid, dropped := SplitFuncCalls([]FuncCallResults{
			{Name: "bash", Arguments: `{"command":"ls"}`},
			{Name: "bash", Arguments: `{"script":"pwd"}`},
		}, declared, "answer")
		Expect(valid).To(HaveLen(1))
		Expect(dropped).To(HaveLen(1))
		Expect(dropped[0].Arguments).To(ContainSubstring("command"))
	})

	It("renders autoparser calls after the content", func() {
		dropped := []FuncCallResults{{Name: "bash", Arguments: `{"command":"ls"}`}}
		Expect(AnswerText("", dropped, true)).To(Equal(`{"name":"bash","arguments":{"command":"ls"}}`))
		Expect(AnswerText("running it\n", dropped, true)).To(Equal("running it\n" + `{"name":"bash","arguments":{"command":"ls"}}`))
	})

	It("leaves text-parsed content as it is: it already holds the calls", func() {
		Expect(AnswerText("raw text", []FuncCallResults{{Name: "bash"}}, false)).To(Equal("raw text"))
	})

	It("keeps arguments that are not JSON as a string", func() {
		Expect(DroppedCallsText([]FuncCallResults{{Name: "bash", Arguments: "ls -la"}})).To(Equal(`{"name":"bash","arguments":"ls -la"}`))
	})
})
