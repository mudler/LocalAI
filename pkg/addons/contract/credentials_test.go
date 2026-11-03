// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The credential slot of an add-on", func() {
	const schema = `{"type":"object","properties":{"repo":{"type":"string"}},"required":["repo"],"additionalProperties":false}`
	slot := CredentialSlot{ID: "token", Title: "Access token", Kind: CredentialKindToken, ResourceArgument: "repo"}
	manifest := func(mod func(*Manifest)) Manifest {
		m, err := ParseManifest(fixture())
		Expect(err).NotTo(HaveOccurred())
		m.API = VersionRange{Min: 2, Max: 2}
		m.Capabilities = Capabilities{
			Tools:           []string{"read-file"},
			ToolDefinitions: []ToolDefinition{{Name: "read-file", Description: "Read a file.", Kind: ToolKindRead, InputSchema: json.RawMessage(schema)}},
			Credentials:     []CredentialSlot{slot},
			Connector:       &ConnectorCapability{Kinds: []ConnectorKind{{ID: "sample", Title: "Sample", Delivery: DeliveryPull}}},
		}
		if mod != nil {
			mod(&m)
		}
		return m
	}
	host := func() HostCompatibility {
		h := compatible()
		h.SDK = 0
		return h
	}

	It("is valid with a connector kind and read tools", func() {
		Expect(ValidateManifest(manifest(nil), host())).To(Succeed())
	})
	It("needs a connector kind to hold the binding", func() {
		m := manifest(func(m *Manifest) { m.Capabilities.Connector = nil })
		Expect(ValidateManifest(m, host())).NotTo(Succeed())
	})
	It("is refused with a bad kind, a repeated or extra slot or a bad argument name", func() {
		for _, mod := range []func(*Manifest){
			func(m *Manifest) { m.Capabilities.Credentials[0].Kind = "app" },
			func(m *Manifest) { m.Capabilities.Credentials[0].ID = "Token" },
			func(m *Manifest) { m.Capabilities.Credentials[0].Title = "" },
			func(m *Manifest) { m.Capabilities.Credentials[0].ResourceArgument = "re po" },
			func(m *Manifest) {
				m.Capabilities.Credentials = append(m.Capabilities.Credentials, CredentialSlot{ID: "other", Title: "Other", Kind: CredentialKindToken})
			},
		} {
			Expect(ValidateManifest(manifest(mod), host())).NotTo(Succeed())
		}
	})
	Describe("write tools", func() {
		const writeSchema = `{"type":"object","properties":{"repo":{"type":"string"},"body":{"type":"string"}},"required":["repo","body"],"additionalProperties":false}`
		withWrite := func(schema, approval string) Manifest {
			return manifest(func(m *Manifest) {
				m.Capabilities.Tools = append(m.Capabilities.Tools, "comment")
				m.Capabilities.ToolDefinitions = append(m.Capabilities.ToolDefinitions, ToolDefinition{Name: "comment", Description: "Comment.", Kind: ToolKindWrite,
					InputSchema: json.RawMessage(schema), Approval: approval})
			})
		}
		It("are allowed when they name the resource with the slot argument", func() {
			Expect(ValidateManifest(withWrite(writeSchema, ""), host())).To(Succeed())
			Expect(ValidateManifest(withWrite(writeSchema, ApprovalAlways), host())).To(Succeed())
		})
		It("never take the host resource argument", func() {
			s := `{"type":"object","properties":{"repo":{"type":"string"},"resourceId":{"type":"string"}},"required":["repo","resourceId"],"additionalProperties":false}`
			Expect(ValidateManifest(withWrite(s, ""), host())).NotTo(Succeed())
		})
		It("must require the slot argument", func() {
			s := `{"type":"object","properties":{"body":{"type":"string"}},"required":["body"],"additionalProperties":false}`
			Expect(ValidateManifest(withWrite(s, ""), host())).NotTo(Succeed())
		})
		It("refuse an approval value the host does not know", func() {
			Expect(ValidateManifest(withWrite(writeSchema, "sometimes"), host())).NotTo(Succeed())
		})
		It("are the only change a credential allows: schema, create and action tools are refused", func() {
			for _, kind := range []string{ToolKindSchema, ToolKindCreate, ToolKindAction} {
				m := manifest(func(m *Manifest) {
					m.Capabilities.Actions = true
					m.Capabilities.Tools = append(m.Capabilities.Tools, "x")
					m.Capabilities.ToolDefinitions = append(m.Capabilities.ToolDefinitions, ToolDefinition{Name: "x", Description: "X.", Kind: kind, InputSchema: json.RawMessage(writeSchema)})
				})
				Expect(ValidateManifest(m, host())).NotTo(Succeed(), kind)
			}
		})
		It("are refused in an add-on without a credential when they lack resourceId", func() {
			m := manifest(func(m *Manifest) {
				m.Capabilities.Credentials = nil
				m.Capabilities.Tools = append(m.Capabilities.Tools, "comment")
				m.Capabilities.ToolDefinitions = append(m.Capabilities.ToolDefinitions, ToolDefinition{Name: "comment", Description: "Comment.", Kind: ToolKindWrite, InputSchema: json.RawMessage(writeSchema)})
			})
			Expect(ValidateManifest(m, host())).NotTo(Succeed())
		})
		It("accept the approval field on a write tool only", func() {
			m := manifest(func(m *Manifest) { m.Capabilities.ToolDefinitions[0].Approval = ApprovalAlways })
			Expect(ValidateManifest(m, host())).NotTo(Succeed())
		})
		It("may be checked on their own with ValidateCredentialedToolSchema", func() {
			Expect(ValidateCredentialedToolSchema(json.RawMessage(writeSchema), ToolKindWrite)).To(Succeed())
			Expect(ValidateToolSchema(json.RawMessage(writeSchema), ToolKindWrite)).NotTo(Succeed())
		})
	})
	It("makes a tool require the resource argument as a string", func() {
		for _, s := range []string{
			`{"type":"object","properties":{"repo":{"type":"string"}},"additionalProperties":false}`,
			`{"type":"object","properties":{"repo":{"type":"integer"}},"required":["repo"],"additionalProperties":false}`,
			`{"type":"object","properties":{"repo":{"type":"string"},"resourceId":{"type":"string"}},"required":["repo","resourceId"],"additionalProperties":false}`,
		} {
			m := manifest(func(m *Manifest) { m.Capabilities.ToolDefinitions[0].InputSchema = json.RawMessage(s) })
			Expect(ValidateManifest(m, host())).NotTo(Succeed(), s)
		}
	})
	It("round trips through the manifest and rejects a spelling of another case", func() {
		m := manifest(nil)
		b, err := json.Marshal(m.Capabilities)
		Expect(err).NotTo(HaveOccurred())
		var back Capabilities
		Expect(json.Unmarshal(b, &back)).To(Succeed())
		Expect(back.Credentials).To(Equal(m.Capabilities.Credentials))
	})

	Describe("CredentialResource", func() {
		def := ToolDefinition{Name: "read-file", Kind: ToolKindRead, InputSchema: json.RawMessage(schema)}
		It("returns the key of the argument", func() {
			k, found, err := slot.CredentialResource(def, json.RawMessage(`{"repo":"acme/widgets"}`))
			Expect(err).NotTo(HaveOccurred())
			Expect([]any{k, found}).To(Equal([]any{"acme/widgets", true}))
		})
		It("refuses a missing or non-string key", func() {
			for _, a := range []string{`{}`, `{"repo":""}`, `{"repo":3}`, `[]`} {
				_, found, err := slot.CredentialResource(def, json.RawMessage(a))
				Expect(found).To(BeTrue())
				Expect(err).To(HaveOccurred(), a)
			}
		})
		It("finds nothing in a tool that does not declare the argument", func() {
			other := ToolDefinition{Name: "x", Kind: ToolKindRead, InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)}
			_, found, err := slot.CredentialResource(other, json.RawMessage(`{}`))
			Expect([]any{found, err}).To(Equal([]any{false, nil}))
		})
	})

	Describe("the request", func() {
		now := time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC)
		It("carries a resource key and a credential", func() {
			v := envelope()
			v.Request.ResourceKey, v.Request.Credential = "acme/widgets", "secret"
			Expect(v.Validate(now, DefaultLimits())).To(Succeed())
			b, err := json.Marshal(v)
			Expect(err).NotTo(HaveOccurred())
			var back Envelope
			Expect(json.Unmarshal(b, &back)).To(Succeed())
			Expect(back.Request.Credential).To(Equal("secret"))
		})
		It("refuses a resource key with a control character or too long, and a huge credential", func() {
			for _, change := range []func(*Envelope){
				func(v *Envelope) { v.Request.ResourceKey = "a\nb" },
				func(v *Envelope) { v.Request.ResourceKey = strings.Repeat("a", MaxResourceKeyBytes+1) },
				func(v *Envelope) { v.Request.Credential = strings.Repeat("a", MaxCredentialBytes+1) },
			} {
				v := envelope()
				change(&v)
				Expect(v.Validate(now, DefaultLimits())).NotTo(Succeed())
			}
		})
	})

	Describe("ErrRateLimited", func() {
		It("is found through wrapping and keeps its wait in bounds", func() {
			err := fmt.Errorf("call: %w", &ErrRateLimited{RetryAfter: 42})
			var rl *ErrRateLimited
			Expect(errors.As(err, &rl)).To(BeTrue())
			Expect(rl.Seconds()).To(Equal(42))
			Expect((&ErrRateLimited{}).Seconds()).To(Equal(1))
			Expect((&ErrRateLimited{RetryAfter: 1 << 30}).Seconds()).To(Equal(MaxRetryAfter))
		})
	})
})
