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
