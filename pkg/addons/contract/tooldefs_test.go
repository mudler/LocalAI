// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	"reflect"
	"time"

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

	It("accepts an action tool that names no resource, and refuses one that offers it without requiring it", func() {
		Expect(ValidateToolSchema(json.RawMessage(withoutResource), ToolKindAction)).To(Succeed())
		Expect(ValidateToolSchema(json.RawMessage(resourceNotRequired), ToolKindAction)).NotTo(Succeed())
	})
	It("keeps the argument names of identity and approval out of an action tool", func() {
		for _, name := range []string{"userId", "approved", "scope", "force"} {
			schema := `{"type":"object","properties":{"` + name + `":{"type":"string"}},"additionalProperties":false}`
			Expect(ValidateToolSchema(json.RawMessage(schema), ToolKindAction)).NotTo(Succeed(), name)
		}
	})
})

var _ = Describe("Action tools in a manifest", func() {
	const schema = `{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`
	manifestWith := func(actions bool, kind string) Manifest {
		m, e := ParseManifest(fixture())
		Expect(e).NotTo(HaveOccurred())
		m.Capabilities = Capabilities{
			Tools:           []string{"run"},
			Actions:         actions,
			ToolDefinitions: []ToolDefinition{{Name: "run", Description: "Run a command.", Kind: kind, InputSchema: json.RawMessage(schema)}},
		}
		return m
	}
	It("is valid when the add-on says it acts", func() {
		h := compatible()
		h.SDK = 0
		Expect(ValidateManifest(manifestWith(true, ToolKindAction), h)).To(Succeed())
	})
	It("is refused when the add-on does not say it acts", func() {
		h := compatible()
		h.SDK = 0
		Expect(ValidateManifest(manifestWith(false, ToolKindAction), h)).NotTo(Succeed())
	})
	It("leaves the other kinds alone", func() {
		h := compatible()
		h.SDK = 0
		read := `{"type":"object","properties":{"command":{"type":"string"}},"additionalProperties":false}`
		m := manifestWith(false, ToolKindRead)
		m.Capabilities.ToolDefinitions[0].InputSchema = json.RawMessage(read)
		Expect(ValidateManifest(m, h)).To(Succeed())
	})
})

var _ = Describe("The act scope", func() {
	env := func(scope, action string) Envelope {
		return Envelope{
			Request: Request{AddonID: "sandbox", OperationID: "op1", Action: action, Generation: 1, Arguments: json.RawMessage(`{}`)},
			Authority: Authority{Actor: Actor{UserID: "u", CredentialID: "c", Mode: "named-user"}, Scope: scope,
				ExpiresAt: time.Now().Add(time.Minute)},
		}
	}
	It("is a valid scope for an action request", func() {
		Expect(env("act", "act").Validate(time.Now(), DefaultLimits())).To(Succeed())
	})
	It("is refused for any other request, and an action request refuses any other scope", func() {
		for _, a := range []string{"read", "write", "schema", "create", "output"} {
			Expect(env("act", a).Validate(time.Now(), DefaultLimits())).NotTo(Succeed(), a)
		}
		for _, sc := range []string{"read", "preview", "apply", "output"} {
			Expect(env(sc, "act").Validate(time.Now(), DefaultLimits())).NotTo(Succeed(), sc)
		}
	})
	It("has a long timeout of its own that is not part of the limits an add-on receives", func() {
		Expect(ActTimeout).To(BeNumerically(">=", 10*time.Minute))
		_, has := reflect.TypeOf(DefaultLimits()).FieldByName("ActTimeout")
		Expect(has).To(BeFalse(), "the limits are sent to every add-on at start; a new field would break older add-ons")
	})
})

var _ = Describe("Tools for the add-on's own page", func() {
	const noResource = `{"type":"object","properties":{"backend":{"type":"string"}},"additionalProperties":false}`
	const withResource = `{"type":"object","properties":{"resourceId":{"type":"string"}},"required":["resourceId"],"additionalProperties":false}`
	def := func(kind, schema string) []ToolDefinition {
		return []ToolDefinition{{Name: "set-backend", Description: "Choose the backend.", Kind: kind, InputSchema: json.RawMessage(schema), UIOnly: true}}
	}
	It("accepts a read tool without a resource", func() {
		Expect(validateToolDefinitions(def(ToolKindRead, noResource), []string{"set-backend"})).To(Succeed())
	})
	It("refuses one that works on a resource or is not a read: the page has no resource to name", func() {
		Expect(validateToolDefinitions(def(ToolKindRead, withResource), []string{"set-backend"})).NotTo(Succeed())
		Expect(validateToolDefinitions(def(ToolKindWrite, withResource), []string{"set-backend"})).NotTo(Succeed())
		Expect(validateToolDefinitions(def(ToolKindAction, noResource), []string{"set-backend"})).NotTo(Succeed())
	})
	It("is resourceless like any read without a resource", func() {
		Expect(IsResourceless(def(ToolKindRead, noResource)[0])).To(BeTrue())
	})
})

var _ = Describe("A tool that shadows a built-in chat tool", func() {
	const schema = `{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`
	def := func(name, shadows string, uiOnly bool) ToolDefinition {
		kind := ToolKindAction
		if uiOnly {
			kind = ToolKindRead
		}
		return ToolDefinition{Name: name, Description: "Run a script.", Kind: kind, InputSchema: json.RawMessage(schema), UIOnly: uiOnly, Shadows: shadows}
	}
	read := func(name, shadows, schema string) ToolDefinition {
		return ToolDefinition{Name: name, Description: "Read the web.", Kind: ToolKindRead, InputSchema: json.RawMessage(schema), Shadows: shadows}
	}
	DescribeTable("is verified at install",
		func(defs []ToolDefinition, ok bool) {
			names := []string{}
			for _, d := range defs {
				names = append(names, d.Name)
			}
			err := validateToolDefinitions(defs, names)
			if ok {
				Expect(err).To(Succeed())
			} else {
				Expect(err).To(HaveOccurred())
			}
		},
		Entry("bash is replaceable", []ToolDefinition{def("bash", "bash", false)}, true),
		Entry("web_search and web_fetch are replaceable", []ToolDefinition{
			read("search", "web_search", `{"type":"object","properties":{"query":{"type":"string"},"maxResults":{"type":"integer"}},"required":["query"],"additionalProperties":false}`),
			read("fetch", "web_fetch", `{"type":"object","properties":{"url":{"type":"string"}},"required":["url"],"additionalProperties":false}`),
		}, true),
		Entry("a file tool is not", []ToolDefinition{def("write", "write_file", false)}, false),
		Entry("an unknown name is not", []ToolDefinition{def("x", "spawn_agent", false)}, false),
		Entry("a page-only tool cannot shadow", []ToolDefinition{def("bash", "bash", true)}, false),
		Entry("two tools cannot shadow one name", []ToolDefinition{def("a", "bash", false), def("b", "bash", false)}, false),
		Entry("a shadow of bash must be an action tool", []ToolDefinition{read("bash", "bash", schema)}, false),
		Entry("a shadow of web_fetch must be a read tool", []ToolDefinition{
			{Name: "fetch", Description: "Fetch.", Kind: ToolKindAction, Shadows: "web_fetch", InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"],"additionalProperties":false}`)},
		}, false),
		Entry("a shadow of bash must declare command", []ToolDefinition{
			{Name: "bash", Description: "Run.", Kind: ToolKindAction, Shadows: "bash", InputSchema: json.RawMessage(`{"type":"object","properties":{"script":{"type":"string"}},"required":["script"],"additionalProperties":false}`)},
		}, false),
		Entry("a shadow keeps the type of a required property", []ToolDefinition{
			read("search", "web_search", `{"type":"object","properties":{"query":{"type":"integer"}},"required":["query"],"additionalProperties":false}`),
		}, false),
		Entry("a shadow requires no property the built-in does not", []ToolDefinition{
			read("fetch", "web_fetch", `{"type":"object","properties":{"url":{"type":"string"},"format":{"type":"string"}},"required":["url","format"],"additionalProperties":false}`),
		}, false),
		Entry("a shadow names no resource", []ToolDefinition{
			read("fetch", "web_fetch", `{"type":"object","properties":{"url":{"type":"string"},"resourceId":{"type":"string"}},"required":["url","resourceId"],"additionalProperties":false}`),
		}, false),
		Entry("extra optional properties are allowed", []ToolDefinition{
			{Name: "bash", Description: "Run.", Kind: ToolKindAction, Shadows: "bash", InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"},"timeoutSeconds":{"type":"integer"}},"required":["command"],"additionalProperties":false}`)},
		}, true),
	)
	It("lists only the shadows that pass the check", func() {
		bad := ToolDefinition{Name: "bash", Description: "Run.", Kind: ToolKindRead, Shadows: "bash", InputSchema: json.RawMessage(schema)}
		Expect(ShadowedTools([]ToolDefinition{bad})).To(BeEmpty())
		Expect(ShadowedTools([]ToolDefinition{def("bash", "bash", false)})).To(Equal([]string{"bash"}))
	})
	It("keeps the file tools off the replaceable list", func() {
		for _, n := range []string{"read_file", "write_file", "edit_file", "list_files", "search_files", "move_file", "delete_file"} {
			Expect(Replaceable(n)).To(BeFalse(), n)
		}
		Expect(Replaceable("bash")).To(BeTrue())
	})
})
