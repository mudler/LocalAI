// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("what a tool shows of a call that waits in a chat", func() {
	const schema = `{"type":"object","properties":{"resourceId":{"type":"string"}},"required":["resourceId"],"additionalProperties":false}`
	def := func(kind string, mod func(*ToolDefinition)) []ToolDefinition {
		d := ToolDefinition{Name: "t", Description: "Does a thing.", Kind: kind, InputSchema: json.RawMessage(schema)}
		mod(&d)
		return []ToolDefinition{d}
	}

	It("accepts a title and summary fields on a write tool", func() {
		defs := def(ToolKindWrite, func(d *ToolDefinition) { d.Title = "Open an issue"; d.SummaryFields = []string{"repo", "title"} })
		Expect(validateToolDefinitions(defs, []string{"t"})).To(Succeed())
	})
	It("refuses them on a read tool", func() {
		defs := def(ToolKindRead, func(d *ToolDefinition) { d.Title = "Look" })
		Expect(validateToolDefinitions(defs, []string{"t"})).NotTo(Succeed())
	})
	It("refuses a bad title, a bad key, a repeated key and too many keys", func() {
		for _, mod := range []func(*ToolDefinition){
			func(d *ToolDefinition) { d.Title = "<b>x</b>" },
			func(d *ToolDefinition) { d.Title = strings.Repeat("x", MaxToolTitleLen+1) },
			func(d *ToolDefinition) { d.SummaryFields = []string{"a b"} },
			func(d *ToolDefinition) { d.SummaryFields = []string{"a", "a"} },
			func(d *ToolDefinition) { d.SummaryFields = []string{"a", "b", "c", "d", "e", "f", "g"} },
		} {
			Expect(validateToolDefinitions(def(ToolKindWrite, mod), []string{"t"})).NotTo(Succeed())
		}
	})

	It("shows only the listed scalar keys, in order, as short plain lines", func() {
		d := ToolDefinition{SummaryFields: []string{"title", "labels", "n", "obj", "missing", "secret"}}
		sample := json.RawMessage(`{"title":"Fix <b>it</b>\nnow","labels":["bug","ui"],"n":7,"obj":{"a":1},"token":"ghp_x","secret":null}`)
		Expect(DisplayFacts(d, sample)).To(Equal([]Fact{
			{Key: "title", Value: "Fix b it /b now"},
			{Key: "labels", Value: "bug, ui"},
			{Key: "n", Value: "7"},
		}))
	})
	It("cuts a long value and shows nothing without keys", func() {
		d := ToolDefinition{SummaryFields: []string{"body"}}
		long, _ := json.Marshal(map[string]string{"body": strings.Repeat("é", 500)})
		f := DisplayFacts(d, long)
		Expect(f).To(HaveLen(1))
		Expect([]rune(f[0].Value)).To(HaveLen(MaxFactRunes + 1))
		Expect(DisplayFacts(ToolDefinition{}, long)).To(BeEmpty())
	})
})
