// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Tool schemas and the resource argument", func() {
	const withoutResource = `{"type":"object","properties":{"limit":{"type":"integer"}},"additionalProperties":false}`
	const withResource = `{"type":"object","properties":{"resourceId":{"type":"string"}},"required":["resourceId"],"additionalProperties":false}`
	const resourceNotRequired = `{"type":"object","properties":{"resourceId":{"type":"string"}},"additionalProperties":false}`

	It("accepts a read tool that names no resource", func() {
		Expect(ValidateToolSchema(json.RawMessage(withoutResource), ToolKindRead)).To(Succeed())
	})
	It("still accepts a read tool that requires its resource", func() {
		Expect(ValidateToolSchema(json.RawMessage(withResource), ToolKindRead)).To(Succeed())
	})
	It("refuses a read tool that offers the resource without requiring it", func() {
		Expect(ValidateToolSchema(json.RawMessage(resourceNotRequired), ToolKindRead)).NotTo(Succeed())
	})
	It("refuses a write or schema tool without a resource", func() {
		Expect(ValidateToolSchema(json.RawMessage(withoutResource), ToolKindWrite)).NotTo(Succeed())
		Expect(ValidateToolSchema(json.RawMessage(withoutResource), ToolKindSchema)).NotTo(Succeed())
	})
	It("still accepts a write or schema tool that requires its resource", func() {
		Expect(ValidateToolSchema(json.RawMessage(withResource), ToolKindWrite)).To(Succeed())
		Expect(ValidateToolSchema(json.RawMessage(withResource), ToolKindSchema)).To(Succeed())
	})
	It("keeps a create tool free of the resource argument", func() {
		Expect(ValidateToolSchema(json.RawMessage(withoutResource), ToolKindCreate)).To(Succeed())
		Expect(ValidateToolSchema(json.RawMessage(withResource), ToolKindCreate)).NotTo(Succeed())
	})
})
