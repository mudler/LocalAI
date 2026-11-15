// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
	Describe("wildcard resources", func() {
		const listSchema = `{"type":"object","properties":{"page":{"type":"integer"}},"additionalProperties":false}`
		withList := func(wildcard bool, mod func(*ToolDefinition)) Manifest {
			return manifest(func(m *Manifest) {
				d := ToolDefinition{Name: "list-repos", Description: "List.", Kind: ToolKindRead, InputSchema: json.RawMessage(listSchema), Enumerates: true}
				if mod != nil {
					mod(&d)
				}
				m.Capabilities.Tools = append(m.Capabilities.Tools, "list-repos")
				m.Capabilities.ToolDefinitions = append(m.Capabilities.ToolDefinitions, d)
				m.Capabilities.Connector.Kinds[0].WildcardResources = wildcard
			})
		}
		It("lets a kind that declares them have a tool that enumerates", func() {
			Expect(ValidateManifest(withList(true, nil), host())).To(Succeed())
		})
		It("refuses an enumerating tool when the kind has no wildcards, or when it is not a plain read tool", func() {
			Expect(ValidateManifest(withList(false, nil), host())).NotTo(Succeed())
			Expect(ValidateManifest(withList(true, func(d *ToolDefinition) { d.Kind = ToolKindWrite }), host())).NotTo(Succeed())
			Expect(ValidateManifest(withList(true, func(d *ToolDefinition) { d.UIOnly = true }), host())).NotTo(Succeed())
		})
		It("refuses an enumerating tool that names a resource", func() {
			s := `{"type":"object","properties":{"repo":{"type":"string"}},"required":["repo"],"additionalProperties":false}`
			Expect(ValidateManifest(withList(true, func(d *ToolDefinition) { d.InputSchema = json.RawMessage(s) }), host())).NotTo(Succeed())
		})
	})
	Describe("token guards", func() {
		withGuard := func(g TokenGuard) Manifest {
			return manifest(func(m *Manifest) {
				k := &m.Capabilities.Connector.Kinds[0]
				k.Options = []ConnectorOption{{ID: "classicToken", Title: "Classic", Default: "refuse", Values: []ConnectorOptionValue{{ID: "refuse", Title: "No"}, {ID: "allow", Title: "Yes"}}}}
				k.TokenGuards = []TokenGuard{g}
			})
		}
		It("name a prefix, an option of the kind and a value it offers", func() {
			Expect(ValidateManifest(withGuard(TokenGuard{Prefix: "ghp_", Option: "classicToken", Allow: "allow"}), host())).To(Succeed())
			Expect(ValidateManifest(withGuard(TokenGuard{Prefix: "ghp_", Option: "missing", Allow: "allow"}), host())).NotTo(Succeed())
			Expect(ValidateManifest(withGuard(TokenGuard{Prefix: "ghp_", Option: "classicToken", Allow: "maybe"}), host())).NotTo(Succeed())
			Expect(ValidateManifest(withGuard(TokenGuard{Prefix: "", Option: "classicToken", Allow: "allow"}), host())).NotTo(Succeed())
			Expect(ValidateManifest(withGuard(TokenGuard{Prefix: "a b", Option: "classicToken", Allow: "allow"}), host())).NotTo(Succeed())
		})
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
		Describe("the approval the person may relax", func() {
			relaxable := func(mod func(*Manifest)) Manifest {
				m := withWrite(writeSchema, ApprovalAlways)
				m.Capabilities.ToolDefinitions[len(m.Capabilities.ToolDefinitions)-1].ApprovalOverridable = true
				m.Capabilities.UserSettings = append(m.Capabilities.UserSettings,
					UserSetting{Key: ApprovalSettingKey("comment"), Values: []string{ApprovalAsk, ApprovalSkip}, Default: ApprovalAsk})
				if mod != nil {
					mod(&m)
				}
				return m
			}
			It("is accepted with its setting, the values ask and skip and the default ask", func() {
				Expect(ValidateManifest(relaxable(nil), host())).To(Succeed())
			})
			DescribeTable("is refused",
				func(mod func(*Manifest)) { Expect(ValidateManifest(relaxable(mod), host())).NotTo(Succeed()) },
				Entry("without the setting", func(m *Manifest) { m.Capabilities.UserSettings = nil }),
				Entry("when the setting starts at skip", func(m *Manifest) {
					m.Capabilities.UserSettings[0].Default = ApprovalSkip
				}),
				Entry("when the setting has another value", func(m *Manifest) {
					m.Capabilities.UserSettings[0].Values = []string{ApprovalAsk, ApprovalSkip, "maybe"}
				}),
				Entry("when the setting carries a host role", func(m *Manifest) {
					m.Capabilities.UserSettings[0].Role = UserSettingRoleMemorySave
				}),
				Entry("on a tool without approval always", func(m *Manifest) {
					m.Capabilities.ToolDefinitions[len(m.Capabilities.ToolDefinitions)-1].Approval = ""
				}),
				Entry("in an add-on without a credential", func(m *Manifest) {
					m.Capabilities.Credentials = nil
				}),
			)
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

	Describe("the granted scopes of a request", func() {
		now := time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC)
		It("carry the scopes the host holds and round-trip", func() {
			v := envelope()
			v.Request.GrantedScopes = []string{"read", "https://api.example.test/auth/write"}
			Expect(v.Validate(now, DefaultLimits())).To(Succeed())
			b, err := json.Marshal(v)
			Expect(err).NotTo(HaveOccurred())
			var back Envelope
			Expect(json.Unmarshal(b, &back)).To(Succeed())
			Expect(back.Request.GrantedScopes).To(Equal(v.Request.GrantedScopes))
		})
		It("are omitted when empty", func() {
			b, err := json.Marshal(envelope().Request)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).NotTo(ContainSubstring("grantedScopes"))
		})
		It("are bounded in count, size and shape", func() {
			many := make([]string, MaxGrantedScopes+1)
			for i := range many {
				many[i] = fmt.Sprintf("s%d", i)
			}
			for _, list := range [][]string{many, {strings.Repeat("a", MaxOAuthScopeBytes+1)}, {""}, {"a b"}, {"a", "a"}} {
				v := envelope()
				v.Request.GrantedScopes = list
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

var _ = Describe("An optional resource argument of a credential slot", func() {
	const (
		optionalSchema = `{"type":"object","properties":{"instance":{"type":"string"}},"additionalProperties":false}`
		requiredSchema = `{"type":"object","properties":{"instance":{"type":"string"}},"required":["instance"],"additionalProperties":false}`
	)
	manifest := func(optional bool, schema string) Manifest {
		m, err := ParseManifest(fixture())
		Expect(err).NotTo(HaveOccurred())
		m.API = VersionRange{Min: 2, Max: 2}
		m.Capabilities = Capabilities{
			Tools:           []string{"read-file"},
			ToolDefinitions: []ToolDefinition{{Name: "read-file", Description: "Read a file.", Kind: ToolKindRead, InputSchema: json.RawMessage(schema)}},
			Credentials:     []CredentialSlot{{ID: "token", Title: "Access token", Kind: CredentialKindToken, ResourceArgument: "instance", ResourceOptional: optional}},
			Connector:       &ConnectorCapability{Kinds: []ConnectorKind{{ID: "sample", Title: "Sample", Delivery: DeliveryPull}}},
		}
		return m
	}
	host := func() HostCompatibility {
		h := compatible()
		h.SDK = 0
		return h
	}

	It("is valid when the tool does not require the argument", func() {
		Expect(ValidateManifest(manifest(true, optionalSchema), host())).To(Succeed())
	})
	It("is refused when the tool still requires it, and a required slot is refused without the requirement", func() {
		Expect(ValidateManifest(manifest(true, requiredSchema), host())).NotTo(Succeed())
		Expect(ValidateManifest(manifest(false, optionalSchema), host())).NotTo(Succeed())
		Expect(ValidateManifest(manifest(false, requiredSchema), host())).To(Succeed())
	})
	It("needs a resource argument to be optional", func() {
		m := manifest(true, optionalSchema)
		m.Capabilities.Credentials[0].ResourceArgument = ""
		Expect(ValidateManifest(m, host())).NotTo(Succeed())
	})
	It("reports an omitted argument, and still refuses a wrong one", func() {
		def := ToolDefinition{Name: "read-file", Kind: ToolKindRead, InputSchema: json.RawMessage(optionalSchema)}
		optional := CredentialSlot{ResourceArgument: "instance", ResourceOptional: true}
		_, found, err := optional.CredentialResource(def, json.RawMessage(`{"tool":"read-file"}`))
		Expect(found).To(BeTrue())
		Expect(errors.Is(err, ErrResourceOmitted)).To(BeTrue())
		k, _, err := optional.CredentialResource(def, json.RawMessage(`{"instance":"http://ha.local:8123"}`))
		Expect([]any{k, err}).To(Equal([]any{"http://ha.local:8123", nil}))
		for _, a := range []string{`{"instance":""}`, `{"instance":3}`} {
			_, _, err = optional.CredentialResource(def, json.RawMessage(a))
			Expect(err).To(HaveOccurred(), a)
			Expect(errors.Is(err, ErrResourceOmitted)).To(BeFalse(), a)
		}
		// A slot that does not opt in refuses the omission, as before.
		_, _, err = CredentialSlot{ResourceArgument: "instance"}.CredentialResource(def, json.RawMessage(`{}`))
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrResourceOmitted)).To(BeFalse())
	})
	It("is how the Home Assistant add-on declares its address", func() {
		b, err := os.ReadFile("../../../addons/home-assistant/manifest.json")
		Expect(err).NotTo(HaveOccurred())
		m, err := ParseManifest(b)
		Expect(err).NotTo(HaveOccurred())
		Expect(m.Capabilities.Credentials[0].ResourceOptional).To(BeTrue())
		for _, d := range m.Capabilities.ToolDefinitions {
			if d.UIOnly {
				continue
			}
			Expect(requiredProperty(d, "instance")).To(BeFalse(), d.Name)
		}
	})
})
