// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Deferred tools", func() {
	const plain = `{"type":"object","properties":{"limit":{"type":"integer"}},"additionalProperties":false}`
	const withResource = `{"type":"object","properties":{"resourceId":{"type":"string"}},"required":["resourceId"],"additionalProperties":false}`
	const fetchSchema = `{"type":"object","properties":{"url":{"type":"string"}},"required":["url"],"additionalProperties":false}`

	schemaFor := func(kind string) string {
		switch kind {
		case ToolKindWrite, ToolKindSchema:
			return withResource
		}
		return plain
	}
	def := func(kind string, mod func(*ToolDefinition)) []ToolDefinition {
		d := ToolDefinition{Name: "t", Description: "Do a thing.", Kind: kind, InputSchema: json.RawMessage(schemaFor(kind)), Deferred: true}
		if mod != nil {
			mod(&d)
		}
		return []ToolDefinition{d}
	}

	It("is accepted on every kind", func() {
		for _, kind := range []string{ToolKindRead, ToolKindWrite, ToolKindSchema, ToolKindCreate, ToolKindAction} {
			Expect(validateToolDefinitions(def(kind, nil), []string{"t"})).To(Succeed(), kind)
		}
	})

	It("keeps an approval ceiling and a title on a deferred write tool", func() {
		defs := def(ToolKindWrite, func(d *ToolDefinition) { d.Approval = ApprovalAlways; d.Title = "Do it" })
		Expect(validateToolDefinitions(defs, []string{"t"})).To(Succeed())
	})

	It("is refused on a page-only tool", func() {
		err := validateToolDefinitions(def(ToolKindRead, func(d *ToolDefinition) { d.UIOnly = true }), []string{"t"})
		Expect(err).To(MatchError(ContainSubstring("page-only")))
		Expect(err).To(MatchError(ContainSubstring("deferred")))
	})

	It("is refused on a tool that shadows a built-in tool", func() {
		defs := def(ToolKindRead, func(d *ToolDefinition) { d.Shadows = "web_fetch"; d.InputSchema = json.RawMessage(fetchSchema) })
		Expect(validateToolDefinitions(defs, []string{"t"})).To(MatchError(ContainSubstring("cannot be deferred")))
		// The same tool without the flag is valid, so the flag is the cause.
		defs[0].Deferred = false
		Expect(validateToolDefinitions(defs, []string{"t"})).To(Succeed())
	})

	It("is false when omitted and is not written when false", func() {
		var d ToolDefinition
		Expect(json.Unmarshal([]byte(`{"name":"t","description":"x","kind":"read","inputSchema":{}}`), &d)).To(Succeed())
		Expect(d.Deferred).To(BeFalse())
		raw, err := json.Marshal(d)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring("deferred"))
	})

	It("survives a JSON round trip", func() {
		raw, err := json.Marshal(def(ToolKindRead, nil)[0])
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(`"deferred":true`))
		var back ToolDefinition
		Expect(json.Unmarshal(raw, &back)).To(Succeed())
		Expect(back.Deferred).To(BeTrue())
	})

	It("is not counted against the limits of the definitions", func() {
		var many []ToolDefinition
		var names []string
		for i := 0; i < MaxToolDefinitions; i++ {
			name := "t" + string(rune('a'+i/10)) + string(rune('a'+i%10))
			d := def(ToolKindRead, func(d *ToolDefinition) { d.Name = name })[0]
			many, names = append(many, d), append(names, name)
		}
		Expect(validateToolDefinitions(many, names)).To(Succeed())
		with := append([]ToolDefinition(nil), many...)
		for i := range with {
			with[i].Deferred = false
		}
		Expect(validateToolDefinitions(with, names)).To(Succeed())
	})
})
