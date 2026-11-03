// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func extensionFixture() []byte {
	return []byte(strings.Replace(string(fixture()), `"service":true`, `"service":true,"actions":true,"tools":["run","health"],"toolDefinitions":[{"name":"run","description":"Run commands","kind":"action","shadows":"bash","inputSchema":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}},{"name":"health","description":"Report readiness","kind":"read","uiOnly":true,"inputSchema":{"type":"object","properties":{},"additionalProperties":false}}],"executionProvider":{"tools":["run"],"statusTool":"health"},"hostOperations":{"required":["workspace.materialize"],"optional":["egress.check"]},"ui":{"entry":"ui/main.js","sdk":{"min":2,"max":2},"pages":["work","history"],"mainPage":"work","icon":"ui/icon.png","homeCommands":[{"id":"open","alias":"work","label":{"en":"Open work","it":"Apri"}},{"id":"history","page":"history","icon":"clock"}]}`, 1))
}

var _ = Describe("Optional extension contracts", func() {
	DescribeTable("keeps execution declaration predicates aligned with manifest validation", func(field, value, message string) {
		m, err := ParseManifest(extensionFixture())
		Expect(err).NotTo(HaveOccurred())
		c := &m.Capabilities
		switch field {
		case "schema":
			c.ToolDefinitions[1].InputSchema = json.RawMessage(value)
		case "admin":
			c.AdminTools = []string{"health"}
		case "status-ui":
			c.ToolDefinitions[1].UIOnly = false
		case "status-kind":
			c.ToolDefinitions[1].Kind = value
		case "action-ui":
			c.ToolDefinitions[0].UIOnly = true
		case "action-kind":
			c.ToolDefinitions[0].Kind = value
		case "shadow":
			c.ToolDefinitions[0].Shadows = value
		}
		action := IsExecutionAction(*c, c.ToolDefinitions[0])
		status := IsExecutionStatusTool(*c, c.ToolDefinitions[1]) && ExecutionStatusAcceptsEmptyArguments(c.ToolDefinitions[1])
		Expect(action && status).To(BeFalse())
		Expect(validateExtensions(*c)).To(MatchError(message))
		raw, err := json.Marshal(m)
		Expect(err).NotTo(HaveOccurred())
		_, err = ParseManifest(raw)
		Expect(err).To(HaveOccurred())
	},
		Entry("wrong object", "schema", `{"type":"string","additionalProperties":false}`, "execution status must accept empty arguments"),
		Entry("additional properties true", "schema", `{"type":"object","additionalProperties":true}`, "execution status must accept empty arguments"),
		Entry("additional properties absent", "schema", `{"type":"object"}`, "execution status must accept empty arguments"),
		Entry("required", "schema", `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`, "execution status must accept empty arguments"),
		Entry("enum", "schema", `{"type":"object","enum":["x"],"additionalProperties":false}`, "execution status must accept empty arguments"),
		Entry("unknown schema keyword", "schema", `{"type":"object","minProperties":1,"additionalProperties":false}`, "execution status must accept empty arguments"),
		Entry("malformed required", "schema", `{"type":"object","required":null,"additionalProperties":false}`, "execution status must accept empty arguments"),
		Entry("resource", "schema", `{"type":"object","properties":{"resourceId":{"type":"string"}},"required":["resourceId"],"additionalProperties":false}`, "execution status must be a separate page-only resourceless read tool"),
		Entry("admin", "admin", "", "execution status must be a separate page-only resourceless read tool"),
		Entry("status uiOnly", "status-ui", "", "execution status must be a separate page-only resourceless read tool"),
		Entry("status kind", "status-kind", "action", "execution status must be a separate page-only resourceless read tool"),
		Entry("action uiOnly", "action-ui", "", "execution provider must bind declared action tools shadowing bash"),
		Entry("action kind", "action-kind", "read", "execution provider must bind declared action tools shadowing bash"),
		Entry("shadow", "shadow", "other", "execution provider must bind declared action tools shadowing bash"),
	)
	It("accepts empty arguments with optional property constraints", func() {
		m, err := ParseManifest(extensionFixture())
		Expect(err).NotTo(HaveOccurred())
		d := &m.Capabilities.ToolDefinitions[1]
		d.InputSchema = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","enum":["ok"]}},"required":[],"additionalProperties":false}`)
		Expect(IsExecutionStatusTool(m.Capabilities, *d)).To(BeTrue())
		Expect(ExecutionStatusAcceptsEmptyArguments(*d)).To(BeTrue())
		raw, err := json.Marshal(m)
		Expect(err).NotTo(HaveOccurred())
		_, err = ParseManifest(raw)
		Expect(err).NotTo(HaveOccurred())
	})

	It("preserves all three declarations through manifest and capability echo", func() {
		m, err := ParseManifest(extensionFixture())
		Expect(err).NotTo(HaveOccurred())
		b, err := json.Marshal(m)
		Expect(err).NotTo(HaveOccurred())
		again, err := ParseManifest(b)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(m))
		b, err = json.Marshal(m.Capabilities)
		Expect(err).NotTo(HaveOccurred())
		var echo Capabilities
		Expect(json.Unmarshal(b, &echo)).To(Succeed())
		Expect(echo).To(Equal(m.Capabilities))
		Expect(SchemaVersion).To(Equal(1))
		Expect(HostSDKVersion).To(Equal(uint64(2)))
	})
	It("keeps old manifests free of extension keys", func() {
		m, err := ParseManifest(fixture())
		Expect(err).NotTo(HaveOccurred())
		b, err := json.Marshal(m)
		Expect(err).NotTo(HaveOccurred())
		for _, key := range []string{"homeCommands", "executionProvider", "hostOperations"} {
			Expect(string(b)).NotTo(ContainSubstring(key))
		}
	})
	It("bounds commands and operation requests at their declared limits", func() {
		m, err := ParseManifest(extensionFixture())
		Expect(err).NotTo(HaveOccurred())
		c := m.Capabilities
		c.UI.HomeCommands = nil
		for i := 0; i < MaxHomeCommands; i++ {
			c.UI.HomeCommands = append(c.UI.HomeCommands, HomeCommand{ID: fmt.Sprintf("open-%d", i)})
		}
		Expect(validateExtensions(c)).To(Succeed())
		c.UI.HomeCommands = append(c.UI.HomeCommands, HomeCommand{ID: "overflow"})
		Expect(validateExtensions(c)).NotTo(Succeed())
		c.UI.HomeCommands = nil
		c.HostOperations = &HostOperations{}
		for i := 0; i < MaxRequestedHostOperations; i++ {
			c.HostOperations.Required = append(c.HostOperations.Required, "future.op"+string(rune('a'+i/26))+string(rune('a'+i%26)))
		}
		Expect(validateExtensions(c)).To(Succeed())
		c.HostOperations.Optional = []string{"future.overflow"}
		Expect(validateExtensions(c)).NotTo(Succeed())
		c.HostOperations = nil
		c.ExecutionProvider.Tools = make([]string, MaxExecutionTools+1)
		Expect(validateExtensions(c)).NotTo(Succeed())
	})
	It("accepts localized string labels, packaged icons and explicit non-navigation pages", func() {
		b := strings.Replace(string(extensionFixture()), `"label":{"en":"Open work","it":"Apri"}`, `"label":"`+strings.Repeat("界", MaxLabelChars)+`"`, 1)
		b = strings.Replace(b, `"icon":"clock"`, `"icon":"ui/icon.png"`, 1)
		_, err := ParseManifest([]byte(b))
		Expect(err).NotTo(HaveOccurred())
	})
	DescribeTable("requires status schemas to accept the empty object", func(schema string, valid bool) {
		b := strings.Replace(string(extensionFixture()), `"type":"object","properties":{},"additionalProperties":false`, schema, 1)
		Expect(b).NotTo(Equal(string(extensionFixture())))
		_, err := ParseManifest([]byte(b))
		if valid {
			Expect(err).NotTo(HaveOccurred())
		} else {
			Expect(err).To(HaveOccurred())
		}
	},
		Entry("root enum excludes objects", `"type":"object","properties":{},"additionalProperties":false,"enum":["never-an-object"]`, false),
		Entry("root required property excludes empty objects", `"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false`, false),
		Entry("root type excludes objects", `"type":"string","properties":{},"additionalProperties":false`, false),
		Entry("optional constrained properties accept empty objects", `"type":"object","properties":{"query":{"type":"string","enum":["ready"],"minLength":1},"options":{"type":"object","properties":{"detail":{"type":"boolean"}},"required":["detail"],"additionalProperties":false}},"additionalProperties":false`, true),
		Entry("keywords for other types do not constrain objects", `"type":"object","properties":{},"additionalProperties":false,"minLength":1,"maxLength":0,"minItems":1,"maxItems":0,"minimum":1,"maximum":0`, true),
	)
	DescribeTable("rejects malformed extension declarations", func(old, replacement string) {
		b := strings.Replace(string(extensionFixture()), old, replacement, 1)
		Expect(b).NotTo(Equal(string(extensionFixture())))
		_, err := ParseManifest([]byte(b))
		Expect(err).To(HaveOccurred())
	},
		Entry("non bash execution tool", `"shadows":"bash",`, ``),
		Entry("empty optional alias", `"alias":"work"`, `"alias":""`),
		Entry("null optional alias", `"alias":"work"`, `"alias":null`),
		Entry("empty optional page", `"page":"history"`, `"page":""`),
		Entry("null optional label", `"label":{"en":"Open work","it":"Apri"}`, `"label":null`),
		Entry("empty optional icon", `"icon":"clock"`, `"icon":""`),
		Entry("unknown command key", `"id":"open"`, `"id":"open","target":"https://example.org"`),
		Entry("duplicate command key", `"id":"open"`, `"id":"open","id":"again"`),
		Entry("invalid alias", `"alias":"work"`, `"alias":"/Work"`),
		Entry("missing id", `"id":"open",`, ``),
		Entry("duplicate id", `"id":"history"`, `"id":"open"`),
		Entry("undeclared page", `"page":"history"`, `"page":"outside"`),
		Entry("no default destination", `"mainPage":"work",`, ``),
		Entry("foreign icon", `"icon":"clock"`, `"icon":"ui/other.png"`),
		Entry("long label", `"en":"Open work"`, `"en":"`+strings.Repeat("界", 61)+`"`),
		Entry("bad locale", `"it":"Apri"`, `"bad_tag":"Apri"`),
		Entry("markup label", `"en":"Open work"`, `"en":"<b>Open</b>"`),
		Entry("duplicate locale", `"en":"Open work"`, `"en":"Open work","en":"Again"`),
		Entry("status schema open", `"properties":{},"additionalProperties":false`, `"properties":{},"additionalProperties":true`),
		Entry("status wrong kind", `"name":"health","description":"Report readiness","kind":"read"`, `"name":"health","description":"Report readiness","kind":"action"`),
		Entry("empty provider", `"tools":["run"],"statusTool":"health"`, ``),
		Entry("unknown execution tool", `"tools":["run"],"statusTool"`, `"tools":["missing"],"statusTool"`),
		Entry("duplicate execution binding", `"tools":["run"],"statusTool"`, `"tools":["run","run"],"statusTool"`),
		Entry("same status binding", `"statusTool":"health"`, `"statusTool":"run"`),
		Entry("non UI status", `"uiOnly":true`, `"uiOnly":false`),
		Entry("status required input", `"properties":{},"additionalProperties"`, `"properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties"`),
		Entry("status resource", `"properties":{},"additionalProperties"`, `"properties":{"resourceId":{"type":"string"}},"additionalProperties"`),
		Entry("empty requests", `"required":["workspace.materialize"],"optional":["egress.check"]`, ``),
		Entry("duplicate requests", `"optional":["egress.check"]`, `"optional":["workspace.materialize"]`),
		Entry("wildcard request", `"egress.check"`, `"egress.*"`),
		Entry("unknown request key", `"optional":["egress.check"]`, `"optional":["egress.check"],"grants":true`),
	)
})
